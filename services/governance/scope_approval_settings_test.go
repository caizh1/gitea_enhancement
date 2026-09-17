// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestScopeApprovalSettingsPreserveHistoryAndLocks(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	issue := &issues_model.Issue{RepoID: 3, Index: 99, PosterID: 2, Title: "设置继承验收", IsPull: true}
	require.NoError(t, db.Insert(ctx, issue))
	pr := &issues_model.PullRequest{IssueID: issue.ID, BaseRepoID: 3, HeadRepoID: 3, BaseBranch: "master", HeadBranch: "feature", Index: 99}
	require.NoError(t, db.Insert(ctx, pr))
	input := governance_model.ApprovalRule{Name: "项目默认", Required: 1, UserIDs: []int64{2}, BranchMode: "all", Enabled: true}
	rule, err := SaveRepositoryApprovalRule(ctx, owner, 3, 0, 0, input, false)
	require.NoError(t, err)
	option := ScopeApprovalSettingsOption{ApprovalSettingsOption: ApprovalSettingsOption{PreventAuthor: true, PreventCommitter: true, PreventOverrides: true, ResetOnChange: true}}
	settings, err := SaveScopeApprovalSettings(ctx, owner, "group", 3, option, false)
	require.NoError(t, err)
	version, has, err := governance_model.LatestPullRuleVersion(ctx, pr.ID)
	require.NoError(t, err)
	require.True(t, has)
	require.Len(t, version.Rules, 1)
	require.Equal(t, rule.ID, version.Rules[0].ID)
	_, err = SaveRepositoryApprovalSettings(ctx, owner, 3, ApprovalSettingsOption{PreventAuthor: true, ResetOnChange: true, PreventOverrides: true})
	require.ErrorIs(t, err, governance_model.ErrForbidden, "项目不能关闭群组的提交者限制")
	input.Required = 2
	rule, err = SaveRepositoryApprovalRule(ctx, owner, 3, rule.ID, rule.Revision, input, false)
	require.NoError(t, err)
	option.Revision, option.PreventOverrides = settings.Revision, false
	settings, err = SaveScopeApprovalSettings(ctx, owner, "group", 3, option, false)
	require.NoError(t, err)
	input.Required = 3
	_, err = SaveRepositoryApprovalRule(ctx, owner, 3, rule.ID, rule.Revision, input, false)
	require.NoError(t, err)
	version, _, err = governance_model.LatestPullRuleVersion(ctx, pr.ID)
	require.NoError(t, err)
	require.Equal(t, 2, version.Rules[0].Required, "解除锁定后保留之前实际生效规则")
	_, err = SaveScopeApprovalSettings(ctx, owner, "group", 3, option, false)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	operation := &governance_model.MergeAuthorization{RepoID: 3, PullID: pr.ID, Branch: "master", Head: "提交", OldTarget: "旧", NewTarget: "新", Actor: owner}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, []string{governance_model.Resource("group", 3)}, func(context.Context) error { return nil }))
	option.Revision = settings.Revision
	_, err = SaveScopeApprovalSettings(ctx, owner, "group", 3, option, true)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = governance_model.ReconcileMerge(ctx, operation.ID, "旧")
	require.NoError(t, err)
	removed, err := SaveScopeApprovalSettings(ctx, owner, "group", 3, option, true)
	require.NoError(t, err)
	require.True(t, removed.Inherit)
	_, err = SaveScopeApprovalSettings(ctx, owner, "group", 3, ScopeApprovalSettingsOption{}, false)
	require.ErrorIs(t, err, governance_model.ErrConflict, "恢复继承不能重置修订历史")
	option.Revision = removed.Revision
	settings, err = SaveScopeApprovalSettings(ctx, owner, "group", 3, option, false)
	require.NoError(t, err)
	require.Greater(t, settings.Revision, removed.Revision)
	option.Revision = settings.Revision
	_, err = SaveScopeApprovalSettings(ctx, governance_model.Actor{ID: 1, Name: "user1", Kind: "user", Transport: "api"}, "instance", 0, ScopeApprovalSettingsOption{ApprovalSettingsOption: ApprovalSettingsOption{PreventAuthor: true, ResetOnChange: true}}, false)
	require.NoError(t, err)
	_, err = SaveScopeApprovalSettings(ctx, owner, "group", 3, option, false)
	require.ErrorIs(t, err, governance_model.ErrForbidden, "实例字段同样约束群组")
}
