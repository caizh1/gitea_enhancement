// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/require"
)

func TestApprovalReauthenticationNativePasswordAndLimit(t *testing.T) {
	unittest.PrepareTestEnv(t)
	defer test.MockVariableValue(&setting.Service.EnablePasswordSignInForm, true)()
	ctx := t.Context()
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	actor := RequestActor(user, "127.0.0.1:1234", "api")
	supported, err := CanReauthenticateApproval(ctx, user.ID)
	require.NoError(t, err)
	require.True(t, supported)
	require.ErrorIs(t, ReauthenticateApproval(ctx, user.ID, ReviewAuthentication{Actor: actor}), governance_model.ErrForbidden)
	require.ErrorIs(t, ReauthenticateApproval(ctx, 1, ReviewAuthentication{Actor: actor, Password: "password"}), governance_model.ErrForbidden)
	require.ErrorIs(t, ReauthenticateApproval(ctx, user.ID, ReviewAuthentication{Actor: actor, Password: "错误验收口令"}), governance_model.ErrForbidden)
	require.NoError(t, ReauthenticateApproval(ctx, user.ID, ReviewAuthentication{Actor: actor, Password: "password"}))
	for range 3 {
		require.ErrorIs(t, ReauthenticateApproval(ctx, user.ID, ReviewAuthentication{Actor: actor, Password: "错误验收口令"}), governance_model.ErrForbidden)
	}
	require.ErrorIs(t, ReauthenticateApproval(ctx, user.ID, ReviewAuthentication{Actor: actor, Password: "password"}), governance_model.ErrConflict)
	var events []*governance_model.AuditEvent
	require.NoError(t, db.GetEngine(ctx).Where("actor_id = ?", user.ID).Find(&events))
	require.Len(t, events, 10)
	for _, event := range events {
		require.NotContains(t, string(event.Details), "口令")
		require.NotContains(t, string(event.Details), "password")
		require.Equal(t, "127.0.0.1", event.Actor.IP)
	}
	_, err = db.GetEngine(ctx).ID(user.ID).Cols("passwd").Update(&user_model.User{Passwd: ""})
	require.NoError(t, err)
	supported, err = CanReauthenticateApproval(ctx, user.ID)
	require.NoError(t, err)
	require.False(t, supported)
	setting.Service.EnablePasswordSignInForm = false
	defer test.MockVariableValue(&setting.Service.EnableBasicAuth, false)()
	supported, err = CanReauthenticateApproval(ctx, 1)
	require.NoError(t, err)
	require.False(t, supported, "全站关闭密码认证时不能另开再次认证入口")
}
