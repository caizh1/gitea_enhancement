// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"net/http"
	"sync"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/optional"
	"gitea.dev/modules/setting"
	user_service "gitea.dev/services/user"
	"gitea.dev/tests"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/require"
)

func TestGovernanceMFARecoveryOnce(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "web"})
	tf := &auth_model.TwoFactor{UID: 4}
	require.NoError(t, tf.SetSecret("JBSWY3DPEHPK3PXP"))
	code, err := tf.GenerateScratchToken()
	require.NoError(t, err)
	require.NoError(t, auth_model.NewTwoFactor(ctx, tf))
	var wg sync.WaitGroup
	start := make(chan struct{})
	results, errs := make([]bool, 2), make([]error, 2)
	for i := range 2 {
		wg.Go(func() {
			factorCopy := *tf
			<-start
			results[i], errs[i] = factorCopy.ConsumeScratchToken(ctx, code)
		})
	}
	close(start)
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.NotEqual(t, results[0], results[1], "同一恢复码并发使用只能成功一次")
	event := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.recovery_code_consumed", ScopeID: 4})
	require.EqualValues(t, 4, event.Actor.ID)
	require.JSONEq(t, "null", string(event.Details), "恢复码事件不保存凭据详情")
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "credential.recovery_code_consumed", ScopeID: 4}, 1)
}

func TestGovernanceMFANativeRecoveryLogin(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	tf := &auth_model.TwoFactor{UID: 2}
	require.NoError(t, tf.SetSecret("JBSWY3DPEHPK3PXP"))
	code, err := tf.GenerateScratchToken()
	require.NoError(t, err)
	require.NoError(t, auth_model.NewTwoFactor(t.Context(), tf))
	first, second := emptyTestSession(t), emptyTestSession(t)
	for _, session := range []*TestSession{first, second} {
		session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user/login", map[string]string{"user_name": "user2", "password": "password"}), http.StatusSeeOther)
	}
	first.MakeRequest(t, NewRequestWithValues(t, "POST", "/user/two_factor/scratch", map[string]string{"token": code}), http.StatusSeeOther)
	first.MakeRequest(t, NewRequest(t, "GET", "/user/settings"), http.StatusOK)
	second.MakeRequest(t, NewRequestWithValues(t, "POST", "/user/two_factor/scratch", map[string]string{"token": code}), http.StatusOK)
	second.MakeRequest(t, NewRequest(t, "GET", "/user/settings"), http.StatusSeeOther)
	event := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.recovery_code_consumed", ScopeID: 2})
	require.EqualValues(t, 2, event.Actor.ID)
	require.Equal(t, "web", event.Actor.Transport)
}

func TestGovernanceMFARollback(t *testing.T) {
	for _, eventType := range []string{"credential.webauthn_revoked", "credential.password_changed"} {
		t.Run(eventType, func(t *testing.T) {
			defer tests.PrepareTestEnv(t)()
			ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 1, Name: "user1", Kind: "user", Transport: "web"})
			tf := &auth_model.TwoFactor{UID: 4}
			require.NoError(t, tf.SetSecret("JBSWY3DPEHPK3PXP"))
			code, err := tf.GenerateScratchToken()
			require.NoError(t, err)
			require.NoError(t, auth_model.NewTwoFactor(ctx, tf))
			credential, err := auth_model.CreateCredential(ctx, 4, "验收通行密钥", &webauthn.Credential{ID: []byte("隔离凭据标识")})
			require.NoError(t, err)
			create := "CREATE TRIGGER governance_mfa_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = '" + eventType + "' BEGIN SELECT RAISE(ABORT, '双因素审计故障'); END"
			drop := "DROP TRIGGER IF EXISTS governance_mfa_fail"
			if setting.Database.Type.IsPostgreSQL() {
				_, err := db.GetEngine(ctx).Exec("CREATE FUNCTION governance_mfa_fail_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type = '" + eventType + "' THEN RAISE EXCEPTION '双因素审计故障'; END IF; RETURN NEW; END $$")
				require.NoError(t, err)
				defer func() {
					_, err := db.GetEngine(context.WithoutCancel(ctx)).Exec("DROP FUNCTION IF EXISTS governance_mfa_fail_fn() CASCADE")
					require.NoError(t, err)
				}()
				create = "CREATE TRIGGER governance_mfa_fail BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION governance_mfa_fail_fn()"
				drop += " ON governance_audit_event"
			}
			_, err = db.GetEngine(ctx).Exec(create)
			require.NoError(t, err)
			defer func() { _, err := db.GetEngine(context.WithoutCancel(ctx)).Exec(drop); require.NoError(t, err) }()
			before := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
			if eventType == "credential.webauthn_revoked" {
				session := loginUser(t, "user1")
				session.MakeRequest(t, NewRequestWithValues(t, "POST", "/-/admin/users/4/edit", map[string]string{
					"user_name": before.Name, "login_type": "0-0", "email": before.Email, "active": "on", "password": "MFA-test-Password!42", "reset_2fa": "on",
				}), http.StatusInternalServerError)
			} else {
				actor := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "web"}
				err := user_service.UpdateAuth(governance_model.WithAuditActor(ctx, actor), before, &user_service.UpdateAuthOptions{Password: optional.Some("MFA-test-Password!42"), RecoveryCode: optional.Some(code)})
				require.Error(t, err)
			}
			after := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
			require.Equal(t, before.Passwd, after.Passwd)
			restored, err := auth_model.GetTwoFactorByUID(ctx, 4)
			require.NoError(t, err)
			require.True(t, restored.VerifyScratchToken(code))
			unittest.AssertExistsAndLoadBean(t, &auth_model.WebAuthnCredential{ID: credential.ID})
			for _, kind := range []string{"credential.totp_disabled", "credential.webauthn_revoked", "credential.password_changed", "credential.recovery_code_consumed"} {
				unittest.AssertCount(t, &governance_model.AuditEvent{Type: kind, ScopeID: 4}, 0)
			}
		})
	}
}
