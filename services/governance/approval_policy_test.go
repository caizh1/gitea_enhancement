// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestApprovalPolicyInheritanceAndAuthority(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	input := governance_model.ApprovalRule{Name: "顶级组必批", Required: 1, UserIDs: []int64{2}, BranchMode: "all", Enabled: true}
	_, err := SaveApprovalPolicy(ctx, governance_model.Actor{ID: 4, Kind: "user", Name: "user4", Transport: "api"}, "group", 3, 0, 0, input, false)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	policy, err := SaveApprovalPolicy(ctx, owner, "group", 3, 0, 0, input, false)
	require.NoError(t, err)
	require.True(t, policy.Locked)
	child, err := CreateGroup(ctx, owner, GroupOption{Name: "继承策略子组", Path: "policy-child", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	_, err = SaveApprovalPolicy(ctx, owner, "group", child.ID, 0, 0, input, false)
	require.NoError(t, err, "子群组可以追加本级强制规则")
	_, err = SaveApprovalPolicy(ctx, owner, "group", child.ID, policy.ID, policy.Revision, input, false)
	require.ErrorIs(t, err, governance_model.ErrNotFound, "不能从子群组替换上级规则")
	repo := &repo_model.Repository{OwnerID: child.ID, OwnerName: child.InternalName, OwnerNamespace: child.FullPath, Name: "policy-project", LowerName: "policy-project", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypePullRequests}))
	configuration, err := ListRepositoryApprovalRules(ctx, 2, repo.ID)
	require.NoError(t, err)
	require.Len(t, configuration.Rules, 2)
	require.Equal(t, policy.ID, configuration.Rules[0].ID)
	_, err = SaveRepositoryApprovalRule(ctx, owner, repo.ID, policy.ID, policy.Revision, input, true)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	input.Required = 2
	policy, err = SaveApprovalPolicy(ctx, owner, "group", 3, policy.ID, policy.Revision, input, false)
	require.NoError(t, err)
	configuration, err = ListRepositoryApprovalRules(ctx, 2, repo.ID)
	require.NoError(t, err)
	require.Equal(t, 2, configuration.Rules[0].Required, "已有后代项目立即读取新策略")
	_, err = SaveApprovalPolicy(ctx, owner, "group", 3, policy.ID, policy.Revision-1, input, false)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	operation := &governance_model.MergeAuthorization{RepoID: repo.ID, PullID: 900, Branch: "main", Head: "提交", OldTarget: "旧", NewTarget: "新", Actor: owner}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, []string{governance_model.Resource("group", 3)}, func(context.Context) error { return nil }))
	_, err = SaveApprovalPolicy(ctx, owner, "group", 3, policy.ID, policy.Revision, input, true)
	require.ErrorIs(t, err, governance_model.ErrConflict, "策略修改不能越过后代项目已取得的最终授权")
	_, err = governance_model.ReconcileMerge(ctx, operation.ID, "旧")
	require.NoError(t, err)
	_, err = SaveApprovalPolicy(ctx, owner, "instance", 0, 0, 0, input, false)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	_, err = SaveApprovalPolicy(ctx, governance_model.Actor{ID: 1, Kind: "user", Name: "user1", Transport: "api"}, "instance", 0, 0, 0, input, false)
	require.NoError(t, err)
}
