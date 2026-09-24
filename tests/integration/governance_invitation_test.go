// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/secret"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	governance_service "gitea.dev/services/governance"
	user_service "gitea.dev/services/user"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceInvitationEmailRemovalOrdering(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "dummy"})()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	recipient := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "web"}
	group, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "invite-email-order", Visibility: 2})
	require.NoError(t, err)
	email := "email-order@example.invalid"
	require.NoError(t, db.Insert(ctx, &user_model.EmailAddress{UID: 4, Email: email, IsActivated: true}))
	invitation, err := governance_service.CreateInvitation(ctx, actor, "group", group.ID, governance_service.InvitationOption{Email: email, Role: governance_model.Developer, Revision: group.Revision})
	require.NoError(t, err)
	token, err := secret.DecryptSecret(setting.SecretKey, invitation.TokenEncrypted)
	require.NoError(t, err)
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	done := make(chan error, 1)
	var removedBeforeCommit bool
	require.NoError(t, db.WithTx(ctx, func(txCtx context.Context) error {
		if err := governance_service.AcceptInvitation(txCtx, recipient, invitation.ID, token); err != nil {
			return err
		}
		go func() { done <- user_service.DeleteEmailAddresses(ctx, user, []string{email}) }()
		select {
		case err := <-done:
			require.NoError(t, err)
			removedBeforeCommit = true
		case <-time.After(250 * time.Millisecond):
		}
		return nil
	}))
	if !removedBeforeCommit {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("邀请提交后邮箱删除未恢复")
		}
	}
	require.False(t, removedBeforeCommit, "邮箱删除不能在尚未提交的邀请接受事务之前报告完成")
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 1)
}

func TestGovernanceInvitationHTTP(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "dummy"})()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "invite-http", Visibility: 2})
	require.NoError(t, err)
	email := "invite-http@example.invalid"
	require.NoError(t, db.Insert(ctx, &user_model.EmailAddress{UID: 4, Email: email, IsActivated: true}))
	manager := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteGovernance)
	recipient := getUserToken(t, "user4", auth_model.AccessTokenScopeWriteGovernance)
	reader := getUserToken(t, "user4", auth_model.AccessTokenScopeReadGovernance)
	endpoint := fmt.Sprintf("/api/v1/governance/groups/%d/invitations", group.ID)
	option := governance_service.InvitationOption{Email: email, Role: governance_model.Reporter, Revision: group.Revision}
	MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, option).AddTokenAuth(reader), http.StatusForbidden)
	response := MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, option).AddTokenAuth(manager), http.StatusCreated)
	invitation := DecodeJSON(t, response, &governance_model.Invitation{})
	require.Empty(t, invitation.TokenHash)
	stored := unittest.AssertExistsAndLoadBean(t, &governance_model.Invitation{ID: invitation.ID})
	token, err := secret.DecryptSecret(setting.SecretKey, stored.TokenEncrypted)
	require.NoError(t, err)
	require.NotContains(t, response.Body.String(), token)
	MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(recipient), http.StatusNotFound)
	action := fmt.Sprintf("/api/v1/governance/invitations/%d", invitation.ID)
	body := governance_service.InvitationTokenOption{Token: token}
	MakeRequest(t, NewRequestWithJSON(t, "POST", action+"/preview", body).AddTokenAuth(manager), http.StatusNotFound)
	MakeRequest(t, NewRequestWithJSON(t, "POST", action+"/preview", body).AddTokenAuth(recipient), http.StatusOK)
	landing := fmt.Sprintf("/governance/invitations/%d", invitation.ID)
	response = MakeRequest(t, NewRequest(t, "GET", landing), http.StatusOK)
	require.NotContains(t, response.Body.String(), email)
	require.NotContains(t, response.Body.String(), group.FullPath)
	require.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
	session := loginUser(t, "user4")
	session.MakeRequest(t, NewRequestWithValues(t, "POST", landing+"?token="+token, map[string]string{"action": "preview"}), http.StatusNotFound)
	response = session.MakeRequest(t, NewRequestWithValues(t, "POST", landing, map[string]string{"action": "preview", "token": token}), http.StatusOK)
	require.Contains(t, response.Body.String(), email)
	MakeRequest(t, NewRequestWithJSON(t, "POST", action+"/accept", body).AddTokenAuth(recipient), http.StatusNoContent)
	MakeRequest(t, NewRequestWithJSON(t, "POST", action+"/accept", body).AddTokenAuth(recipient), http.StatusNotFound)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4, Role: governance_model.Reporter}, 1)
	audit := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "invitation.accepted", ObjectID: 4})
	require.EqualValues(t, 4, audit.Actor.ID)
	require.NotContains(t, string(audit.Details), token)
}

func TestGovernanceInvitationWebForm(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "dummy"})()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	group, err := governance_service.CreateGroup(ctx, owner, governance_service.GroupOption{Path: "invite-web-form", Visibility: 2})
	require.NoError(t, err)
	email := "invite-web-form@example.invalid"
	require.NoError(t, db.Insert(ctx, &user_model.EmailAddress{UID: 4, Email: email, IsActivated: true}))
	invitation, err := governance_service.CreateInvitation(ctx, owner, "group", group.ID, governance_service.InvitationOption{Email: email, Role: governance_model.Reporter, Revision: group.Revision})
	require.NoError(t, err)
	token, err := secret.DecryptSecret(setting.SecretKey, invitation.TokenEncrypted)
	require.NoError(t, err)
	landing := fmt.Sprintf("/governance/invitations/%d", invitation.ID)
	wrong := loginUser(t, "user5")
	response := wrong.MakeRequest(t, NewRequest(t, "GET", landing), http.StatusOK)
	require.Contains(t, response.Body.String(), `data-invitation-preview`)
	require.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
	if strings.Contains(response.Body.String(), token) {
		t.Fatal("邀请落地页意外包含令牌")
	}
	wrong.MakeRequest(t, NewRequestWithValues(t, "POST", landing, map[string]string{"action": "preview", "token": token}), http.StatusNotFound)
	wrong.MakeRequest(t, NewRequestWithValues(t, "POST", landing, map[string]string{"action": "accept", "token": token}), http.StatusNotFound)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 5}, 0)

	recipient := loginUser(t, "user4")
	response = recipient.MakeRequest(t, NewRequest(t, "GET", landing), http.StatusOK)
	require.Contains(t, response.Body.String(), `data-invitation-preview`)
	response = recipient.MakeRequest(t, NewRequestWithValues(t, "POST", landing, map[string]string{"action": "preview", "token": token}), http.StatusOK)
	require.Contains(t, response.Body.String(), email)
	require.Contains(t, response.Body.String(), `value="accept"`)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
	response = recipient.MakeRequest(t, NewRequestWithValues(t, "POST", landing, map[string]string{"action": "accept", "token": token}), http.StatusOK)
	require.Contains(t, response.Body.String(), "邀请已接受")
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4, Role: governance_model.Reporter}, 1)
	recipient.MakeRequest(t, NewRequestWithValues(t, "POST", landing, map[string]string{"action": "accept", "token": token}), http.StatusNotFound)
}

func TestGovernanceInvitationRevokedWebRendersHTML404(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "dummy"})()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	group, err := governance_service.CreateGroup(ctx, owner, governance_service.GroupOption{Path: "invite-revoked-web", Visibility: 2})
	require.NoError(t, err)
	email := "invite-revoked-web@example.invalid"
	require.NoError(t, db.Insert(ctx, &user_model.EmailAddress{UID: 4, Email: email, IsActivated: true}))
	invitation, err := governance_service.CreateInvitation(ctx, owner, "group", group.ID, governance_service.InvitationOption{Email: email, Role: governance_model.Reporter, Revision: group.Revision})
	require.NoError(t, err)
	token, err := secret.DecryptSecret(setting.SecretKey, invitation.TokenEncrypted)
	require.NoError(t, err)
	require.NoError(t, governance_service.RevokeInvitation(ctx, owner, "group", group.ID, invitation.ID))

	landing := fmt.Sprintf("/governance/invitations/%d", invitation.ID)
	recipient := loginUser(t, "user4")
	for _, action := range []string{"preview", "accept"} {
		t.Run(action, func(t *testing.T) {
			response := recipient.MakeRequest(t, NewRequestWithValues(t, "POST", landing, map[string]string{"action": action, "token": token}).SetHeader("Accept", "text/html"), http.StatusNotFound)
			require.Contains(t, response.Header().Get("Content-Type"), "text/html")
			require.True(t, test.IsNormalPageCompleted(response.Body.String()))
			require.NotContains(t, response.Body.String(), token)
			require.NotContains(t, response.Body.String(), email)
		})
	}
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 0)
}

func TestGovernanceInvitationAuditRollback(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "dummy"})()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	recipient := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "invite-rollback", Visibility: 0})
	require.NoError(t, err)
	email := "invite-rollback@example.invalid"
	require.NoError(t, db.Insert(ctx, &user_model.EmailAddress{UID: 4, Email: email, IsActivated: true}))
	pending, err := governance_service.RequestAccess(ctx, recipient, "group", group.ID)
	require.NoError(t, err)
	invitation, err := governance_service.CreateInvitation(ctx, actor, "group", group.ID, governance_service.InvitationOption{Email: email, Role: governance_model.Developer, Revision: group.Revision})
	require.NoError(t, err)
	token, err := secret.DecryptSecret(setting.SecretKey, invitation.TokenEncrypted)
	require.NoError(t, err)
	create := "CREATE TRIGGER governance_invitation_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'invitation.accepted' BEGIN SELECT RAISE(ABORT, '邀请审计故障'); END"
	drop := "DROP TRIGGER IF EXISTS governance_invitation_fail"
	if setting.Database.Type.IsPostgreSQL() {
		_, err := db.GetEngine(ctx).Exec("CREATE FUNCTION governance_invitation_fail_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type = 'invitation.accepted' THEN RAISE EXCEPTION '邀请审计故障'; END IF; RETURN NEW; END $$")
		require.NoError(t, err)
		defer func() {
			_, err := db.GetEngine(context.WithoutCancel(ctx)).Exec("DROP FUNCTION IF EXISTS governance_invitation_fail_fn() CASCADE")
			require.NoError(t, err)
		}()
		create = "CREATE TRIGGER governance_invitation_fail BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION governance_invitation_fail_fn()"
		drop += " ON governance_audit_event"
	}
	_, err = db.GetEngine(ctx).Exec(create)
	require.NoError(t, err)
	defer func() { _, err := db.GetEngine(context.WithoutCancel(ctx)).Exec(drop); require.NoError(t, err) }()
	require.Error(t, governance_service.AcceptInvitation(ctx, recipient, invitation.ID, token))
	unittest.AssertCount(t, &governance_model.Invitation{ID: invitation.ID}, 1)
	unittest.AssertCount(t, &governance_model.AccessRequest{ID: pending.ID}, 1)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "access_request.fulfilled"}, 0)
	current, err := governance_model.GetNamespace(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, group.Revision, current.Revision)
	_, err = db.GetEngine(ctx).Exec(drop)
	require.NoError(t, err)
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	require.NoError(t, user_service.DeleteEmailAddresses(ctx, user, []string{email}))
	require.ErrorIs(t, governance_service.AcceptInvitation(ctx, recipient, invitation.ID, token), governance_model.ErrNotFound)
	unittest.AssertCount(t, &governance_model.Invitation{ID: invitation.ID}, 1)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 0)
}
