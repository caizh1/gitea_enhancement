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

func TestPullRuleOverridesPreserveProjectAndHistory(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	input := governance_model.ApprovalRule{Name: "项目默认", Required: 1, UserIDs: []int64{2}, BranchMode: "all", Enabled: true}
	project, err := SaveRepositoryApprovalRule(ctx, actor, 1, 0, 0, input, false)
	require.NoError(t, err)
	saved, err := governance_model.SnapshotProjectPullRules(ctx, 2, 1, true)
	require.NoError(t, err)
	input.Required = 2
	changed, err := SavePullApprovalRule(ctx, actor, 2, project.ID, saved.Revision, input, false)
	require.NoError(t, err)
	require.Equal(t, 2, changed.Rules[0].Required)
	unchanged, _, err := db.GetByID[governance_model.ApprovalRule](ctx, project.ID)
	require.NoError(t, err)
	require.Equal(t, 1, unchanged.Required)
	history, _, err := db.GetByID[governance_model.PullRuleVersion](ctx, saved.ID)
	require.NoError(t, err)
	require.Equal(t, 1, history.Rules[0].Required)
	_, err = SavePullApprovalRule(ctx, actor, 2, project.ID, saved.Revision, input, true)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	policy := &governance_model.ApprovalRule{ScopeType: "instance", Name: "强制规则", Required: 1, BranchMode: "all", Enabled: true, Locked: true}
	require.NoError(t, db.Insert(ctx, policy))
	_, err = SavePullApprovalRule(ctx, actor, 2, policy.ID, changed.Revision, input, true)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	operation := &governance_model.MergeAuthorization{PullID: 2, RepoID: 1, Head: "提交", OldTarget: "旧", NewTarget: "新", Branch: "master", Actor: actor}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, nil, func(context.Context) error { return nil }))
	_, err = SavePullApprovalRule(ctx, actor, 2, project.ID, changed.Revision, input, true)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = governance_model.ReconcileMerge(ctx, operation.ID, "旧")
	require.NoError(t, err)
	deleted, err := SavePullApprovalRule(ctx, actor, 2, project.ID, changed.Revision, input, true)
	require.NoError(t, err)
	require.Empty(t, deleted.Rules)
	created, err := SavePullApprovalRule(ctx, actor, 2, 0, deleted.Revision, input, false)
	require.NoError(t, err)
	require.NotEqual(t, project.ID, created.Rules[0].ID)
	_, err = SaveRepositoryApprovalSettings(ctx, actor, 1, ApprovalSettingsOption{PreventAuthor: true, ResetOnChange: true, PreventOverrides: true})
	require.NoError(t, err)
	state, err := ListPullApprovalRules(ctx, 2, 2)
	require.NoError(t, err)
	require.False(t, state.CanManage)
	require.Equal(t, project.ID, state.Version.Rules[0].ID)
	require.Len(t, state.Policies, 1)
	_, err = SavePullApprovalRule(ctx, actor, 2, project.ID, state.Version.Revision, input, true)
	require.ErrorIs(t, err, governance_model.ErrForbidden)
}

func TestPullRulesOnlyAuthorDeveloperOrMaintainerMayEdit(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := db.GetEngine(ctx).ID(3).Cols("poster_id").Update(&issues_model.Issue{PosterID: 4})
	require.NoError(t, err)
	state, err := ListPullApprovalRules(ctx, 4, 2)
	require.NoError(t, err)
	require.False(t, state.CanManage, "只有作者身份但没有目标项目代码写权限不能覆盖规则")
	state, err = ListPullApprovalRules(ctx, 2, 2)
	require.NoError(t, err)
	require.True(t, state.CanManage, "项目维护者不需要是作者")
	_, err = db.GetEngine(ctx).ID(3).Cols("is_closed").Update(&issues_model.Issue{IsClosed: true})
	require.NoError(t, err)
	state, err = ListPullApprovalRules(ctx, 2, 2)
	require.NoError(t, err)
	require.False(t, state.CanManage)
}
