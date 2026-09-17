// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/secret"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/require"
)

func TestInvitationIdentityAndAtomicAcceptance(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "dummy"})()
	defer test.MockVariableValue(&setting.SecretKey, "邀请验收专用密钥")()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	recipient := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "web"}
	group, err := CreateGroup(ctx, owner, GroupOption{Path: "invitation-test", Visibility: 0})
	require.NoError(t, err)
	_, err = RequestAccess(ctx, recipient, "group", group.ID)
	require.NoError(t, err)
	email := "invite-user@example.invalid"
	require.NoError(t, db.Insert(ctx, &user_model.EmailAddress{UID: 4, Email: email, IsActivated: false}))
	invitation, err := CreateInvitation(ctx, owner, "group", group.ID, InvitationOption{Email: email, Role: governance_model.Developer, Revision: group.Revision})
	require.NoError(t, err)
	token, err := secret.DecryptSecret(setting.SecretKey, invitation.TokenEncrypted)
	require.NoError(t, err)
	require.Len(t, token, 64)
	raw, err := json.Marshal(invitation)
	require.NoError(t, err)
	require.NotContains(t, string(raw), token)
	require.NotContains(t, string(raw), invitation.TokenEncrypted)
	require.NotContains(t, string(raw), invitation.TokenHash)
	_, err = CreateInvitation(ctx, owner, "group", group.ID, InvitationOption{Email: strings.ToUpper(email), Role: governance_model.Developer, Revision: group.Revision})
	require.ErrorIs(t, err, governance_model.ErrConflict)
	require.ErrorIs(t, AcceptInvitation(ctx, recipient, invitation.ID, token), governance_model.ErrNotFound, "未验证的同名邮箱不能接受")
	_, err = db.GetEngine(ctx).Where("lower_email = ?", email).Cols("is_activated").Update(&user_model.EmailAddress{IsActivated: true})
	require.NoError(t, err)
	stranger := governance_model.Actor{ID: 5, Name: "user5", Kind: "user", Transport: "web"}
	require.ErrorIs(t, AcceptInvitation(ctx, stranger, invitation.ID, token), governance_model.ErrNotFound, "持有邮件链接不能替代邮箱归属")
	require.ErrorIs(t, AcceptInvitation(ctx, recipient, invitation.ID, strings.Repeat("x", 64)), governance_model.ErrNotFound)
	delegated := governance_model.Actor{ID: 1, Name: "user1", ActingAsID: 4, Kind: "user", Transport: "api"}
	require.ErrorIs(t, AcceptInvitation(ctx, delegated, invitation.ID, token), governance_model.ErrNotFound)
	// 接受的最后审计失败，前面成员新增和邀请删除必须整体回滚。
	_, err = db.GetEngine(ctx).Exec("CREATE TRIGGER invitation_accept_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'invitation.accepted' BEGIN SELECT RAISE(ABORT, '邀请接受审计故障'); END")
	require.NoError(t, err)
	require.Error(t, AcceptInvitation(ctx, recipient, invitation.ID, token))
	unittest.AssertExistsAndLoadBean(t, &governance_model.Invitation{ID: invitation.ID})
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 0)
	unittest.AssertCount(t, &governance_model.AccessRequest{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 1)
	_, err = db.GetEngine(ctx).Exec("DROP TRIGGER invitation_accept_fail")
	require.NoError(t, err)
	require.NoError(t, AcceptInvitation(ctx, recipient, invitation.ID, token))
	require.ErrorIs(t, AcceptInvitation(ctx, recipient, invitation.ID, token), governance_model.ErrNotFound, "已接受的邀请不能重放")
	unittest.AssertCount(t, &governance_model.Invitation{ID: invitation.ID}, 0)
	unittest.AssertExistsAndLoadBean(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4, Role: governance_model.Developer})
	unittest.AssertCount(t, &governance_model.AccessRequest{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "invitation.accepted", ActorID: 4}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "access_request.fulfilled", ActorID: 4}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "member.added", ScopeType: "group", ScopeID: group.ID, ActorID: 4}, 1)
}

func TestInvitationRechecksInviterAndExpiry(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "dummy"})()
	defer test.MockVariableValue(&setting.SecretKey, "邀请权限验收密钥")()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	recipient := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "web"}
	group, err := CreateGroup(ctx, owner, GroupOption{Path: "invitation-authority", Visibility: 2})
	require.NoError(t, err)
	email := "invite-authority@example.invalid"
	require.NoError(t, db.Insert(ctx, &user_model.EmailAddress{UID: 4, Email: email, IsActivated: true}))
	invitation, err := CreateInvitation(ctx, owner, "group", group.ID, InvitationOption{Email: email, Role: governance_model.Developer, Revision: group.Revision})
	require.NoError(t, err)
	token, err := secret.DecryptSecret(setting.SecretKey, invitation.TokenEncrypted)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(2).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: true})
	require.NoError(t, err)
	require.ErrorIs(t, AcceptInvitation(ctx, recipient, invitation.ID, token), governance_model.ErrNotFound)
	_, err = db.GetEngine(ctx).ID(2).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: false})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(invitation.ID).Cols("expires_unix").Update(&governance_model.Invitation{ExpiresUnix: time.Now().Add(-time.Minute).Unix()})
	require.NoError(t, err)
	require.ErrorIs(t, AcceptInvitation(ctx, recipient, invitation.ID, token), governance_model.ErrNotFound)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 0)
}

func TestInvitationRepositoryDeclineAndRevoke(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "dummy"})()
	defer test.MockVariableValue(&setting.SecretKey, "项目邀请验收密钥")()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	recipient := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "web"}
	email := "repo-invitation@example.invalid"
	require.NoError(t, db.Insert(ctx, &user_model.EmailAddress{UID: 4, Email: email, IsActivated: true}))
	state, err := ListInvitations(ctx, actor.ID, "repository", 2, 0)
	require.NoError(t, err)
	option := InvitationOption{Email: email, Role: governance_model.Reporter, Revision: state.Revision}
	for _, action := range []string{"decline", "revoke", "accept"} {
		invitation, err := CreateInvitation(ctx, actor, "repository", 2, option)
		require.NoError(t, err)
		token, err := secret.DecryptSecret(setting.SecretKey, invitation.TokenEncrypted)
		require.NoError(t, err)
		switch action {
		case "decline":
			require.ErrorIs(t, DeclineInvitation(ctx, actor, invitation.ID, token), governance_model.ErrNotFound)
			require.NoError(t, DeclineInvitation(ctx, recipient, invitation.ID, token))
		case "revoke":
			require.ErrorIs(t, RevokeInvitation(ctx, actor, "repository", 1, invitation.ID), governance_model.ErrNotFound)
			require.NoError(t, RevokeInvitation(ctx, actor, "repository", 2, invitation.ID))
		case "accept":
			require.NoError(t, AcceptInvitation(ctx, recipient, invitation.ID, token))
		}
		require.ErrorIs(t, AcceptInvitation(ctx, recipient, invitation.ID, token), governance_model.ErrNotFound)
		unittest.AssertCount(t, &governance_model.Invitation{ID: invitation.ID}, 0)
	}
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "repository", ScopeID: 2, UserID: 4, Role: governance_model.Reporter}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "invitation.declined", ActorID: 4}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "invitation.revoked", ActorID: 2}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "invitation.accepted", ActorID: 4}, 1)
}
