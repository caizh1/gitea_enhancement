// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"fmt"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestRepositoryMembersPreserveSources(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	repo := &repo_model.Repository{OwnerID: 3, OwnerName: "org3", OwnerNamespace: "org3", Name: "direct-member", LowerName: "direct-member", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypePullRequests}))
	revision := func() int64 {
		n, err := governance_model.GetNamespace(ctx, 3)
		require.NoError(t, err)
		return n.Revision
	}
	require.NoError(t, SetGroupMember(ctx, actor, 3, GroupMemberOption{UserID: 4, Role: governance_model.Reporter, Revision: revision()}, false))
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
	require.NoError(t, err)
	require.True(t, permission.CanRead(unit.TypeCode))
	require.False(t, permission.CanWrite(unit.TypeCode))
	option := GroupMemberOption{UserID: 4, Role: governance_model.Developer, Revision: revision()}
	bad := actor
	bad.Transport = ""
	require.Error(t, SetRepositoryMember(ctx, bad, repo.ID, option, false))
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 4}, 0)
	require.NoError(t, SetRepositoryMember(ctx, actor, repo.ID, option, false))
	permission, err = access_model.GetIndividualUserRepoPermission(ctx, repo, user)
	require.NoError(t, err)
	require.True(t, permission.CanWrite(unit.TypeCode))
	require.ErrorIs(t, SetRepositoryMember(ctx, actor, repo.ID, option, true), governance_model.ErrConflict)
	option.Revision = revision()
	require.NoError(t, SetRepositoryMember(ctx, actor, repo.ID, option, true))
	permission, err = access_model.GetIndividualUserRepoPermission(ctx, repo, user)
	require.NoError(t, err)
	require.True(t, permission.CanRead(unit.TypeCode), "删除项目直接授权后仍保留父组 Reporter")
	require.False(t, permission.CanWrite(unit.TypeCode))

	require.NoError(t, SetRepositoryMember(ctx, actor, repo.ID, GroupMemberOption{UserID: 5, Role: governance_model.Planner, Revision: revision()}, false))
	planner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	permission, err = access_model.GetIndividualUserRepoPermission(ctx, repo, planner)
	require.NoError(t, err)
	require.True(t, permission.CanRead(unit.TypePullRequests))
	require.False(t, permission.CanRead(unit.TypeCode), "Planner 不能因角色数字得到代码访问")
	role := &governance_model.CustomRole{RootID: 3, Name: "有限成员管理", BaseRole: governance_model.Guest, Abilities: []string{governance_model.ManageMembers}}
	require.NoError(t, db.Insert(ctx, role))
	require.NoError(t, SetRepositoryMember(ctx, actor, repo.ID, GroupMemberOption{UserID: 5, Role: governance_model.Guest, CustomRoleID: role.ID, Revision: revision()}, false))
	limited := governance_model.Actor{ID: 5, Name: "user5", Kind: "user", Transport: "api"}
	require.ErrorIs(t, SetRepositoryMember(ctx, limited, repo.ID, GroupMemberOption{UserID: 4, Role: governance_model.Developer, Revision: revision()}, false), governance_model.ErrNotFound)
	require.NoError(t, SetRepositoryMember(ctx, limited, repo.ID, GroupMemberOption{UserID: 4, Role: governance_model.Guest, Revision: revision()}, false))
}

func TestRepositoryAllMembersSources(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	repo := &repo_model.Repository{OwnerID: 3, OwnerName: "org3", OwnerNamespace: "org3", Name: "all-member", LowerName: "all-member", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}))
	group, err := governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, 3, GroupMemberOption{UserID: 4, Role: governance_model.Reporter, Revision: group.Revision}, false))
	require.NoError(t, db.Insert(ctx, &repo_model.Collaboration{RepoID: repo.ID, UserID: 5, Mode: 2}))
	shared, err := CreateGroup(ctx, actor, GroupOption{Path: "member-source-shared", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, shared.ID, GroupMemberOption{UserID: 8, Role: governance_model.Developer, Revision: shared.Revision}, false))
	require.NoError(t, db.Insert(ctx, &governance_model.Share{ScopeType: "repository", ScopeID: repo.ID, GroupID: shared.ID, MaxRole: governance_model.Reporter}))
	direct, err := ListRepositoryMembers(ctx, actor.ID, repo.ID, 0)
	require.NoError(t, err)
	require.Empty(t, direct.Members, "直接来源接口不把继承成员混入")
	team := &organization.Team{OrgID: 3, Name: "单元权限样本", LowerName: "unit-member", AccessMode: 0}
	require.NoError(t, db.Insert(ctx, team))
	require.NoError(t, db.Insert(ctx, &organization.TeamRepo{OrgID: 3, TeamID: team.ID, RepoID: repo.ID}, &organization.TeamUser{OrgID: 3, TeamID: team.ID, UID: 9}, &organization.TeamUnit{OrgID: 3, TeamID: team.ID, Type: unit.TypePullRequests, AccessMode: 1}))
	state, err := ListRepositoryAllMembers(ctx, actor.ID, repo.ID, 0)
	require.NoError(t, err)
	found := map[int64]*GroupMemberState{}
	for _, member := range state.Members {
		found[member.UserID] = member
	}
	require.Contains(t, found, int64(2), "原生所有者团队带来的继承成员不能遗漏")
	require.Contains(t, found, int64(4))
	require.Nil(t, found[4].Direct)
	require.Equal(t, "inherited", found[4].Grants[0].Source)
	require.Contains(t, found, int64(5))
	require.Equal(t, "collaborator", found[5].NativeSources[0].Kind)
	require.Contains(t, found, int64(9), "原生分单元权限总体 none 时仍是独立来源")
	require.False(t, found[9].Active, "已停用用户仍显示授权来源但必须标明不可用")
	require.Equal(t, "none", found[9].NativeSources[0].Access)
	require.Equal(t, "read", found[9].NativeSources[0].Units["repo.pulls"])
	require.Contains(t, found, int64(8))
	require.Equal(t, "shared", found[8].Grants[0].Source)
	require.False(t, found[8].Grants[0].Abilities[governance_model.PushCode], "共享上限不能被来源角色掩盖")
	later, err := ListRepositoryAllMembers(ctx, actor.ID, repo.ID, 4)
	require.NoError(t, err)
	for _, member := range later.Members {
		require.Greater(t, member.UserID, int64(4))
	}
	_, err = ListRepositoryAllMembers(ctx, 4, repo.ID, 0)
	require.ErrorIs(t, err, governance_model.ErrNotFound, "普通成员不能枚举完整成员清单")
}

func TestRepositoryAllMembersPagination(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo := &repo_model.Repository{OwnerID: 2, OwnerName: "user2", OwnerNamespace: "user2", Name: "page-members", LowerName: "page-members", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	for i := range 205 {
		user := &user_model.User{Name: fmt.Sprintf("member-page-%d", i), LowerName: fmt.Sprintf("member-page-%d", i), Email: fmt.Sprintf("page-%d@example.invalid", i), IsActive: true}
		require.NoError(t, db.Insert(ctx, user))
		require.NoError(t, db.Insert(ctx, &governance_model.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: user.ID, Role: governance_model.Reporter}))
	}
	seen := map[int64]bool{}
	cursor := int64(0)
	for range 4 {
		state, err := ListRepositoryAllMembers(ctx, 2, repo.ID, cursor)
		require.NoError(t, err)
		require.LessOrEqual(t, len(state.Members), 100)
		for _, member := range state.Members {
			require.False(t, seen[member.UserID])
			seen[member.UserID] = true
		}
		if state.NextID == 0 {
			break
		}
		require.Greater(t, state.NextID, cursor)
		cursor = state.NextID
	}
	require.Len(t, seen, 206, "205 个明确成员与个人所有者均应恰好出现一次")
}

func TestRepositoryAllMembersSharingExpansion(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	_, err := db.GetEngine(ctx).ID(9).Cols("is_active").Update(&user_model.User{IsActive: true})
	require.NoError(t, err)
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	create := func(path string, parent, userID int64) *GroupState {
		var revision int64
		if parent != 0 {
			n, err := governance_model.GetNamespace(ctx, parent)
			require.NoError(t, err)
			revision = n.Revision
		}
		group, err := CreateGroup(ctx, actor, GroupOption{Path: path, ParentID: parent, Revision: revision, Visibility: 2})
		require.NoError(t, err)
		require.NoError(t, SetGroupMember(ctx, actor, group.ID, GroupMemberOption{UserID: userID, Role: governance_model.Reporter, Revision: group.Revision}, false))
		return group
	}
	root := create("source-parent", 0, 4)
	child := create("source-child", root.ID, 5)
	create("source-grandchild", child.ID, 8)
	foreignRoot := create("foreign-parent", 0, 10)
	foreign := create("foreign-child", foreignRoot.ID, 9)
	require.NoError(t, db.Insert(ctx, &governance_model.Share{ScopeType: "group", ScopeID: child.ID, GroupID: foreign.ID, MaxRole: governance_model.Reporter}))
	require.NoError(t, db.Insert(ctx, &governance_model.Share{ScopeType: "group", ScopeID: 3, GroupID: child.ID, MaxRole: governance_model.Reporter}))
	repo := &repo_model.Repository{OwnerID: 3, OwnerName: "org3", OwnerNamespace: "org3", Name: "sharing-members", LowerName: "sharing-members", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	list := func() map[int64]bool {
		state, err := ListRepositoryAllMembers(ctx, 2, repo.ID, 0)
		require.NoError(t, err)
		found := map[int64]bool{}
		for _, member := range state.Members {
			require.False(t, found[member.UserID])
			found[member.UserID] = true
		}
		return found
	}
	found := list()
	require.True(t, found[5])
	for _, id := range []int64{4, 8, 9, 10} {
		require.False(t, found[id], "群组共享不能递归展开被邀请组的继承、共享和子组成员")
	}
	require.NoError(t, db.Insert(ctx, &governance_model.Share{ScopeType: "repository", ScopeID: repo.ID, GroupID: child.ID, MaxRole: governance_model.Reporter}))
	found = list()
	for _, id := range []int64{4, 5, 9} {
		require.True(t, found[id], "项目共享应包含被邀请组的继承和共享来源")
	}
	require.False(t, found[8], "项目共享不应把所有子组成员扩成项目成员")
	require.False(t, found[10], "群组共享内层仍只采用直接成员")
}
