// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"net/http"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/optional"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	user_service "gitea.dev/services/user"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceAccountSecurityAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	admin := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
	restricted := true
	request := api.EditUserOption{Restricted: &restricted, Password: "Security-test-Password!42"}
	require.NoError(t, auth_model.InsertAuthToken(ctx, &auth_model.AuthToken{ID: "account-security", UserID: 4, TokenHash: "验收占位摘要"}))
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user4", request).AddTokenAuth(admin), http.StatusOK)
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	require.True(t, user.IsRestricted)
	require.True(t, user.ValidatePassword(request.Password))
	unittest.AssertCount(t, &auth_model.AuthToken{ID: "account-security"}, 0)
	security := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "user.security_changed", ObjectID: 4})
	require.JSONEq(t, `{"before":{"is_restricted":false},"after":{"is_restricted":true}}`, string(security.Details))
	credential := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.password_changed", ObjectID: 4})
	for _, event := range []*governance_model.AuditEvent{security, credential} {
		require.EqualValues(t, 1, event.Actor.ID)
		require.Equal(t, "api", event.Actor.Transport)
		require.NotContains(t, string(event.Details), request.Password)
		require.NotContains(t, string(event.Details), user.Passwd)
		require.NotContains(t, string(event.Details), user.Salt)
	}
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user4", api.EditUserOption{Restricted: &restricted}).AddTokenAuth(admin), http.StatusOK)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "user.security_changed", ObjectID: 4}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "credential.password_changed", ObjectID: 4}, 1)
	stale := *user
	require.NoError(t, user_service.UpdateAuth(ctx, user, &user_service.UpdateAuthOptions{Password: optional.Some("Newer-test-Password!43")}))
	require.ErrorIs(t, user_service.UpdateAuth(ctx, &stale, &user_service.UpdateAuthOptions{Password: optional.Some("Stale-test-Password!44")}), governance_model.ErrConflict)
	require.True(t, unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4}).ValidatePassword("Newer-test-Password!43"))
}

func TestGovernanceAccountRenameIsSeparate(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	before := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	session := loginUser(t, "user1")
	page := session.MakeRequest(t, NewRequest(t, "GET", "/-/admin/users/4/edit"), http.StatusOK)
	require.Contains(t, page.Body.String(), "只保存账号名称")
	require.Contains(t, page.Body.String(), `action="./rename"`)
	session.MakeRequest(t, NewRequestWithValues(t, "POST", "/-/admin/users/4/edit", map[string]string{
		"user_name": "renamed-account", "login_type": "0-0", "email": "new-account@example.invalid", "password": "Combined-test-Password!42",
	}), http.StatusBadRequest)
	after := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	require.Equal(t, before.Name, after.Name)
	require.Equal(t, before.Email, after.Email)
	require.Equal(t, before.Passwd, after.Passwd)
	session.MakeRequest(t, NewRequestWithValues(t, "POST", "/-/admin/users/4/rename", map[string]string{"user_name": "renamed-account"}), http.StatusSeeOther)
	after = unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	require.Equal(t, "renamed-account", after.Name)
	require.Equal(t, before.Email, after.Email)
	require.Equal(t, before.Passwd, after.Passwd)
}

func TestGovernanceAccountSecurityRollback(t *testing.T) {
	for _, failure := range []string{"user.security_changed", "credential.password_changed", "撤销登录凭据"} {
		t.Run(failure, func(t *testing.T) {
			defer tests.PrepareTestEnv(t)()
			ctx := t.Context()
			admin := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
			require.NoError(t, auth_model.InsertAuthToken(ctx, &auth_model.AuthToken{ID: "account-rollback", UserID: 4, TokenHash: "验收占位摘要"}))
			table, operation, condition := "governance_audit_event", "INSERT", "NEW.type = '"+failure+"'"
			if failure == "撤销登录凭据" {
				table, operation, condition = "auth_token", "DELETE", "OLD.user_id = 4"
			}
			create := "CREATE TRIGGER governance_account_fail BEFORE " + operation + " ON " + table + " WHEN " + condition + " BEGIN SELECT RAISE(ABORT, '账号安全保存故障'); END"
			drop := "DROP TRIGGER IF EXISTS governance_account_fail"
			if setting.Database.Type.IsPostgreSQL() {
				_, err := db.GetEngine(ctx).Exec("CREATE FUNCTION governance_account_fail_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF " + condition + " THEN RAISE EXCEPTION '账号安全保存故障'; END IF; IF TG_OP = 'DELETE' THEN RETURN OLD; END IF; RETURN NEW; END $$")
				require.NoError(t, err)
				defer func() {
					_, err := db.GetEngine(context.WithoutCancel(ctx)).Exec("DROP FUNCTION IF EXISTS governance_account_fail_fn() CASCADE")
					require.NoError(t, err)
				}()
				create = "CREATE TRIGGER governance_account_fail BEFORE " + operation + " ON " + table + " FOR EACH ROW EXECUTE FUNCTION governance_account_fail_fn()"
				drop += " ON " + table
			}
			_, err := db.GetEngine(ctx).Exec(create)
			require.NoError(t, err)
			defer func() { _, err := db.GetEngine(context.WithoutCancel(ctx)).Exec(drop); require.NoError(t, err) }()
			before := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
			restricted := true
			MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user4", api.EditUserOption{Password: "Rollback-test-Password!42", Restricted: &restricted}).AddTokenAuth(admin), http.StatusInternalServerError)
			after := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
			require.Equal(t, before.Passwd, after.Passwd)
			require.Equal(t, before.IsRestricted, after.IsRestricted)
			unittest.AssertExistsAndLoadBean(t, &auth_model.AuthToken{ID: "account-rollback"})
			unittest.AssertCount(t, &governance_model.AuditEvent{Type: "credential.password_changed", ObjectID: 4}, 0)
			unittest.AssertCount(t, &governance_model.AuditEvent{Type: "user.security_changed", ObjectID: 4}, 0)
		})
	}
}
