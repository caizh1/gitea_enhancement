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

func TestRepositoryApprovalRuleManagementScopesAndRevisions(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	input := governance_model.ApprovalRule{Name: "指定人员必须批准", Required: 1, UserIDs: []int64{2, 2}, BranchMode: "all", Enabled: true}
	rule, err := SaveRepositoryApprovalRule(ctx, actor, 1, 0, 0, input, false)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, rule.UserIDs)
	require.EqualValues(t, 1, rule.Revision)
	state, err := ListRepositoryApprovalRules(ctx, 2, 1)
	require.NoError(t, err)
	require.True(t, state.CanManage)
	require.False(t, state.EnforcementReady, "兼容字段表示所有引用写入均已验证，当前仍应为 false")
	require.False(t, state.MergeEnforcementReady, "项目规则接口没有具体 PR 版本，不能声明当前合并已就绪")
	require.True(t, state.ReferenceEnforcementReady, "保存必需规则会先安装并回读目标项目引用事务 Hook")
	require.Len(t, state.Rules, 1)
	_, err = SaveRepositoryApprovalRule(ctx, actor, 1, 0, 0, input, false)
	require.ErrorIs(t, err, governance_model.ErrConflict, "不能重复创建同名项目规则")
	input.Required = 2
	next, err := SaveRepositoryApprovalRule(ctx, actor, 1, rule.ID, rule.Revision, input, false)
	require.NoError(t, err)
	require.EqualValues(t, 2, next.Revision)
	_, err = SaveRepositoryApprovalRule(ctx, actor, 1, rule.ID, rule.Revision, input, true)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	policy := &governance_model.ApprovalRule{ScopeType: "instance", ScopeID: 0, Name: "实例强制策略", Required: 1, BranchMode: "all", Enabled: true, Locked: true, Revision: 1}
	require.NoError(t, db.Insert(ctx, policy))
	_, err = SaveRepositoryApprovalRule(ctx, actor, 1, policy.ID, 1, input, true)
	require.ErrorIs(t, err, governance_model.ErrNotFound, "项目接口不能删除祖先或实例策略")
	operation := &governance_model.MergeAuthorization{PullID: 1, RepoID: 1, Head: "甲", OldTarget: "旧", NewTarget: "新", Branch: "master", Actor: actor}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, nil, func(context.Context) error { return nil }))
	_, err = SaveRepositoryApprovalRule(ctx, actor, 1, rule.ID, next.Revision, input, true)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = governance_model.ReconcileMerge(ctx, operation.ID, "旧")
	require.NoError(t, err)
	_, err = SaveRepositoryApprovalRule(ctx, actor, 1, rule.ID, next.Revision, input, true)
	require.NoError(t, err)
	state, err = ListRepositoryApprovalRules(ctx, 2, 1)
	require.NoError(t, err)
	require.Len(t, state.Rules, 1)
	require.Equal(t, policy.ID, state.Rules[0].ID)
	count, err := db.GetEngine(ctx).Where("type = ?", "approval.rule_changed").Count(new(governance_model.AuditEvent))
	require.NoError(t, err)
	require.EqualValues(t, 3, count, "只有成功治理变更提交审计，拒绝不伪造成功事件")
	require.NoError(t, db.Insert(ctx, &governance_model.ApprovalSettings{ScopeType: "instance", PreventOverrides: true, Revision: 1}))
	_, err = SaveRepositoryApprovalRule(ctx, actor, 1, 0, 0, input, false)
	require.ErrorIs(t, err, governance_model.ErrForbidden, "实例禁止覆盖时也禁止项目编辑规则列表")
	state, err = ListRepositoryApprovalRules(ctx, 2, 1)
	require.NoError(t, err)
	require.False(t, state.CanManage)
}

func TestAuthorizePullMergeBlocksRequiredRulesWhenHooksMissing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		required   int
		branchMode string
		branches   []string
		mustBlock  bool
	}{{"必需规则", 1, "all", nil, true}, {"零票可选规则", 0, "all", nil, false}, {"其他分支规则", 1, "branches", []string{"release"}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			unittest.PrepareTestEnv(t)
			ctx := t.Context()
			require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
			actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
			rule := &governance_model.ApprovalRule{ScopeType: "instance", ScopeID: 0, Name: tc.name, Required: tc.required, UserIDs: []int64{2}, BranchMode: tc.branchMode, Branches: tc.branches, Enabled: true, Locked: true, Revision: 1}
			require.NoError(t, db.Insert(ctx, rule))
			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: 2})
			require.NoError(t, ensurePullReferenceHooks(ctx, actor, pr))
			snapshot, err := CaptureInitialPullApprovalSnapshot(ctx, pr)
			require.NoError(t, err)
			require.NotNil(t, snapshot)
			authorization, err := AuthorizePullMerge(ctx, actor, pr, snapshot.Version.Head, snapshot.BaseHead, "new", func(context.Context, *issues_model.PullRequest) ([]string, error) { return nil, nil })
			if tc.mustBlock {
				require.ErrorIs(t, err, governance_model.ErrForbidden)
				require.Nil(t, authorization, "拒绝时不得返回或持久化最终合并授权")
			} else {
				require.NoError(t, err)
				require.NotNil(t, authorization, "即使没有必需规则，原生合并也持久化授权以关闭并发新增规则窗口")
			}
		})
	}
}

func TestAnonymousCanReadOnlyPublicRepositoryApprovalRules(t *testing.T) {
	unittest.PrepareTestEnv(t)
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	publicState, err := ListRepositoryApprovalRules(t.Context(), 0, 1)
	require.NoError(t, err)
	require.False(t, publicState.CanManage)

	_, err = ListRepositoryApprovalRules(t.Context(), 0, 2)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
}
