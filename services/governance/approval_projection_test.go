// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestApprovalProjectionHidesPrivateGroupFromRepositoryReader(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	privateGroup, err := CreateGroup(ctx, owner, GroupOption{Path: "hidden-approvers", Visibility: 2})
	require.NoError(t, err)
	repo := &repo_model.Repository{OwnerID: 3, OwnerName: "org3", OwnerNamespace: "org3", Name: "approval-projection", LowerName: "approval-projection", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypePullRequests}))
	group, err := governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, owner, 3, GroupMemberOption{UserID: 4, Role: governance_model.Reporter, Revision: group.Revision}, false))
	rule := &governance_model.ApprovalRule{ID: 77, ScopeType: "repository", ScopeID: repo.ID, Name: "私有审批组", Required: 1, GroupIDs: []int64{privateGroup.ID}, BranchMode: "all", Enabled: true}
	projected, err := ProjectRepositoryApprovalRules(ctx, 4, repo.ID, &RepositoryApprovalRules{Rules: []*governance_model.ApprovalRule{rule}})
	require.NoError(t, err)
	require.True(t, projected.Rules[0].SubjectsHidden)
	require.Empty(t, projected.Rules[0].GroupIDs)
	require.Equal(t, []int64{privateGroup.ID}, rule.GroupIDs, "响应投影不能修改内部计票对象")
	next := *rule
	next.Name, next.Required, next.GroupIDs = "只修改名称和人数", 2, nil
	require.NoError(t, preserveHiddenApprovalSubjects(ctx, 4, repo, &next, rule))
	require.Equal(t, []int64{privateGroup.ID}, next.GroupIDs, "修改其他字段不能顺带删除不可见审批主体")
}
