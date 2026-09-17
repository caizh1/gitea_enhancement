// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"testing"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	gm "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"github.com/stretchr/testify/require"
)

func TestUnifiedBranchApprovalAtomicityAndConflict(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	actor := gm.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	ctx = gm.WithAuditActor(ctx, actor)
	repo, err := repo_model.GetRepositoryByID(ctx, 1)
	require.NoError(t, err)
	branch := &git_model.ProtectedBranch{RepoID: 1, RuleName: "unified-test", RequiredApprovals: 3}
	require.NoError(t, git_model.UpdateProtectBranch(ctx, repo, branch, git_model.WhitelistOptions{}))
	state, err := ReadBranchApprovals(ctx, 2, 1, branch.ID)
	require.NoError(t, err)
	var native *gm.ApprovalRule
	for _, rule := range state.Rules {
		if rule.NativeProtectionID == branch.ID {
			native = rule
		}
	}
	require.NotNil(t, native)
	require.Equal(t, 3, native.Required)
	next := *native
	next.Required = 2
	invalid := gm.ApprovalRule{Name: "", Required: 0, Enabled: true}
	err = SaveBranchApprovals(ctx, actor, 1, BranchApprovalUpdate{Version: state.Version, ProtectionID: branch.ID, Rules: []ApprovalRuleChange{{Rule: next, Revision: native.Revision}, {Rule: invalid}}}, func(tx context.Context) (int64, error) {
		changed := *branch
		changed.CanPush = true
		return changed.ID, git_model.UpdateProtectBranch(tx, repo, &changed, git_model.WhitelistOptions{})
	})
	require.Error(t, err)
	saved, err := git_model.GetProtectedBranchRuleByID(ctx, 1, branch.ID)
	require.NoError(t, err)
	require.False(t, saved.CanPush, "任何审批失败必须回滚同次保护配置")
	require.EqualValues(t, 3, saved.RequiredApprovals)
	require.NoError(t, SaveBranchApprovals(ctx, actor, 1, BranchApprovalUpdate{Version: state.Version, ProtectionID: branch.ID, Rules: []ApprovalRuleChange{{Rule: next, Revision: native.Revision}}}, nil))
	saved, err = git_model.GetProtectedBranchRuleByID(ctx, 1, branch.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, saved.RequiredApprovals)
	require.ErrorIs(t, SaveBranchApprovals(ctx, actor, 1, BranchApprovalUpdate{Version: state.Version, ProtectionID: branch.ID}, nil), gm.ErrConflict, "旧草稿不能覆盖新配置")
}

func TestUnifiedBranchBindingsAndPRProtection(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	actor := gm.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	ctx = gm.WithAuditActor(ctx, actor)
	repo, err := repo_model.GetRepositoryByID(ctx, 1)
	require.NoError(t, err)
	branch := &git_model.ProtectedBranch{RepoID: 1, RuleName: "stable-old"}
	require.NoError(t, git_model.UpdateProtectBranch(ctx, repo, branch, git_model.WhitelistOptions{}))
	rule, err := SaveRepositoryApprovalRule(ctx, actor, 1, 0, 0, gm.ApprovalRule{Name: "稳定关联", Required: 0, Enabled: true, BranchMode: "protection_rules", Branches: []string{"stable-old"}, AllEligible: true}, false)
	require.NoError(t, err)
	require.Equal(t, []int64{branch.ID}, rule.ProtectionIDs)
	branch.RuleName = "stable-new"
	require.NoError(t, git_model.UpdateProtectBranch(ctx, repo, branch, git_model.WhitelistOptions{}))
	applies, err := ApprovalRuleApplies(ctx, 1, "stable-new", rule)
	require.NoError(t, err)
	require.True(t, applies)
	require.NoError(t, git_model.DeleteProtectedBranch(ctx, repo, branch.ID))
	stored, has, err := db.GetByID[gm.ApprovalRule](ctx, rule.ID)
	require.NoError(t, err)
	require.True(t, has)
	require.False(t, stored.Enabled)
	applies, err = ApprovalRuleApplies(ctx, 1, "any-branch", stored)
	require.NoError(t, err)
	require.False(t, applies, "删除最后一个关联不能扩大到所有分支")
	_, err = SavePullApprovalRule(ctx, actor, 1, 0, 0, gm.ApprovalRule{NativeProtectionID: 99}, false)
	require.Error(t, err, "不能伪造不可覆盖的原生规则身份")
}

func TestUnifiedApprovalManagerCannotSaveNativeProtection(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	role := &gm.CustomRole{RootID: 2, Name: "仅管理审批", BaseRole: gm.Reporter, Abilities: []string{gm.ManageApprovals}}
	require.NoError(t, db.Insert(ctx, role))
	require.NoError(t, db.Insert(ctx, &gm.Membership{ScopeType: "repository", ScopeID: 1, UserID: 4, Role: gm.Reporter, CustomRoleID: role.ID}))
	actor := gm.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}
	state, err := ReadBranchApprovals(ctx, 4, 1, 0)
	require.NoError(t, err)
	require.True(t, state.CanManage)
	change := BranchApprovalUpdate{Version: state.Version, Rules: []ApprovalRuleChange{{Rule: gm.ApprovalRule{Name: "可选审批", Required: 0, Enabled: true, AllEligible: true, BranchMode: "all"}}}}
	called := false
	require.ErrorIs(t, SaveBranchApprovals(ctx, actor, 1, change, func(context.Context) (int64, error) { called = true; return 0, nil }), gm.ErrForbidden)
	require.False(t, called, "权限不足时不得进入原生保护写回调")
	require.NoError(t, SaveBranchApprovals(ctx, actor, 1, change, nil), "审批专用入口仍可保存审批内容")
}

func TestUnifiedBranchApprovalConfigurationDiagnostics(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	repo, err := repo_model.GetRepositoryByID(ctx, 1)
	require.NoError(t, err)
	branch := &git_model.ProtectedBranch{RepoID: 1, RuleName: "diagnostic-main", RequiredApprovals: 99}
	require.NoError(t, git_model.UpdateProtectBranch(ctx, repo, branch, git_model.WhitelistOptions{}))
	state, err := ReadBranchApprovals(ctx, 2, 1, branch.ID)
	require.NoError(t, err)
	found := false
	for _, rule := range state.Rules {
		if rule.NativeProtectionID == branch.ID {
			require.Contains(t, state.Presentation[rule.ID].Notice, "候选人不足")
			found = true
		}
	}
	require.True(t, found)
	branch.RuleName = "release/**"
	require.NoError(t, git_model.UpdateProtectBranch(ctx, repo, branch, git_model.WhitelistOptions{}))
	state, err = ReadBranchApprovals(ctx, 2, 1, branch.ID)
	require.NoError(t, err)
	for _, rule := range state.Rules {
		if rule.NativeProtectionID == branch.ID {
			require.Contains(t, state.Presentation[rule.ID].Scope, "release/**")
			require.Empty(t, state.Presentation[rule.ID].Notice, "通配配置不能冒充具体 PR 候选人数诊断")
		}
	}
}
