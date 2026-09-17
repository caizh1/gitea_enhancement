// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/require"
)

func TestApprovalSettingsPreservePRRulesAcrossOverrideChanges(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := db.GetEngine(ctx).ID(3).Cols("is_closed").Update(&issues_model.Issue{IsClosed: false})
	require.NoError(t, err)
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	input := governance_model.ApprovalRule{Name: "项目默认", Required: 1, UserIDs: []int64{2}, BranchMode: "all", Enabled: true}
	rule, err := SaveRepositoryApprovalRule(ctx, actor, 1, 0, 0, input, false)
	require.NoError(t, err)
	saved, has, err := governance_model.LatestPullRuleVersion(ctx, 2)
	require.NoError(t, err)
	require.True(t, has)
	require.Empty(t, saved.Rules, "默认允许覆盖，项目新建规则不能意外修改已有 PR")
	settings := ApprovalSettingsOption{PreventAuthor: true, PreventOverrides: true, ResetOnChange: true}
	local, err := SaveRepositoryApprovalSettings(ctx, actor, 1, settings)
	require.NoError(t, err)
	saved, has, err = governance_model.LatestPullRuleVersion(ctx, 2)
	require.NoError(t, err)
	require.True(t, has)
	require.Len(t, saved.Rules, 1)
	require.Equal(t, 1, saved.Rules[0].Required)
	input.Required = 2
	rule, err = SaveRepositoryApprovalRule(ctx, actor, 1, rule.ID, rule.Revision, input, false)
	require.NoError(t, err)
	settings.Revision, settings.PreventOverrides = local.Revision, false
	local, err = SaveRepositoryApprovalSettings(ctx, actor, 1, settings)
	require.NoError(t, err)
	input.Required = 3
	_, err = SaveRepositoryApprovalRule(ctx, actor, 1, rule.ID, rule.Revision, input, false)
	require.NoError(t, err)
	saved, has, err = governance_model.LatestPullRuleVersion(ctx, 2)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, 2, saved.Rules[0].Required, "重新允许覆盖后，已有 PR 保留此前生效规则")
	_, err = SaveRepositoryApprovalSettings(ctx, actor, 1, settings)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	settings.Revision = local.Revision
	settings.RequireReauthentication = true
	defer test.MockVariableValue(&setting.Service.EnablePasswordSignInForm, true)()
	local, err = SaveRepositoryApprovalSettings(ctx, actor, 1, settings)
	require.NoError(t, err, "当前账号已有原生密码认证方式")
	settings.Revision = local.Revision
	require.NoError(t, db.Insert(ctx, &governance_model.ApprovalSettings{ScopeType: "instance", PreventAuthor: true, ResetOnChange: true, Revision: 1}))
	settings.RequireReauthentication, settings.PreventAuthor = false, false
	_, err = SaveRepositoryApprovalSettings(ctx, actor, 1, settings)
	require.ErrorIs(t, err, governance_model.ErrForbidden)
}
