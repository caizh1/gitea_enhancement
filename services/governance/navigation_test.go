// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNavigationPrivateHierarchyAndRevocation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	actor := gm.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	parent, err := CreateGroup(ctx, actor, GroupOption{Name: "私有父组", Path: "navigation-parent", Visibility: 2})
	require.NoError(t, err)
	child, err := CreateGroup(ctx, actor, GroupOption{Name: "授权子组", Path: "child", ParentID: parent.ID, Visibility: 2})
	require.NoError(t, err)
	sibling, err := CreateGroup(ctx, actor, GroupOption{Name: "隐藏兄弟", Path: "hidden", ParentID: parent.ID, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &gm.Membership{ScopeType: "group", ScopeID: child.ID, UserID: 4, Role: gm.Reporter}))
	page, err := ListNavigationGroups(ctx, 4, NavigationQuery{Q: "navigation-parent"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, child.ID, page.Items[0].ID)
	_, err = GetGroupNavigation(ctx, 4, parent.ID, NavigationQuery{})
	require.ErrorIs(t, err, gm.ErrNotFound)
	_, err = GetGroupNavigation(ctx, 4, sibling.ID, NavigationQuery{})
	require.ErrorIs(t, err, gm.ErrNotFound)
	detail, err := GetGroupNavigation(ctx, 4, child.ID, NavigationQuery{})
	require.NoError(t, err)
	require.Len(t, detail.Breadcrumbs, 2)
	assert.Empty(t, detail.Breadcrumbs[0].URL)
	assert.Equal(t, "navigation-parent", detail.Breadcrumbs[0].Name)
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "group", child.ID, 4).Delete(new(gm.Membership))
	require.NoError(t, err)
	_, err = GetGroupNavigation(ctx, 4, child.ID, NavigationQuery{})
	require.ErrorIs(t, err, gm.ErrNotFound)
}

func TestNavigationPaginationBoundaries(t *testing.T) {
	nodes := []NavigationNode{{ID: 2, Type: "repository", Name: "a"}, {ID: 3, Type: "group", Name: "同名"}, {ID: 1, Type: "group", Name: "同名"}}
	q := NavigationQuery{Limit: 1}
	first, err := paginateNavigation(nodes, 4, 3, q)
	require.NoError(t, err)
	assert.Equal(t, int64(1), first.Items[0].ID)
	require.True(t, first.HasMore)
	q.Cursor = first.NextCursor
	second, err := paginateNavigation(nodes, 4, 3, q)
	require.NoError(t, err)
	assert.Equal(t, int64(3), second.Items[0].ID)
	q.Cursor = second.NextCursor
	last, err := paginateNavigation(nodes, 4, 3, q)
	require.NoError(t, err)
	assert.Equal(t, "repository", last.Items[0].Type)
	assert.False(t, last.HasMore)
	q.Cursor = first.NextCursor
	_, err = paginateNavigation(nodes, 5, 3, q)
	require.Error(t, err)
	_, err = paginateNavigation(nodes, 4, 9, q)
	require.Error(t, err)
	q.Q = "变更搜索"
	_, err = paginateNavigation(nodes, 4, 3, q)
	require.Error(t, err)
	q.Q = ""
	q.Cursor = "x" + q.Cursor
	_, err = paginateNavigation(nodes, 4, 3, q)
	require.Error(t, err)
}

func TestNavigationPaginationFreshPermissions(t *testing.T) {
	q := NavigationQuery{Limit: 1, Direction: "desc"}
	nodes := []NavigationNode{{ID: 1, Type: "group", Name: "a"}, {ID: 2, Type: "group", Name: "b"}, {ID: 3, Type: "repository", Name: "z"}}
	first, err := paginateNavigation(nodes, 4, 0, q)
	require.NoError(t, err)
	assert.Equal(t, int64(2), first.Items[0].ID)
	q.Cursor = first.NextCursor
	// 前一页的节点已撤权，不依赖它仍存在，也不会按位置跳过剩余节点。
	second, err := paginateNavigation([]NavigationNode{nodes[1], nodes[2]}, 4, 0, q)
	require.NoError(t, err)
	assert.Equal(t, int64(1), second.Items[0].ID)
}

// 继承权限必须进入原生仓库搜索的候选集，并在撤销后立即消失。
func TestInheritedRepositoriesInNativeSearch(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	actor := gm.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	child, err := CreateGroup(ctx, actor, GroupOption{Path: "search-child", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, 3, GroupMemberOption{UserID: 4, Role: gm.Developer, Revision: 1}, false))
	repo := &repo_model.Repository{OwnerID: child.ID, OwnerName: child.InternalName, Name: "inherited-search", LowerName: "inherited-search", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}))
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	opts := repo_model.SearchRepoOptions{Actor: user, OwnerID: user.ID, Private: true, Keyword: "inherited-search", ListOptions: db.ListOptions{Page: 1, PageSize: 1}}
	repos, count, err := repo_model.SearchRepository(ctx, opts)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.Len(t, repos, 1)
	assert.Equal(t, repo.ID, repos[0].ID)
	count, err = repo_model.CountRepository(ctx, opts)
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
	ids, count, err := repo_model.SearchRepositoryIDs(ctx, opts)
	require.NoError(t, err)
	assert.Equal(t, []int64{repo.ID}, ids)
	assert.EqualValues(t, 1, count)
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "group", 3, 4).Delete(new(gm.Membership))
	require.NoError(t, err)
	repos, count, err = repo_model.SearchRepository(ctx, opts)
	require.NoError(t, err)
	assert.Empty(t, repos)
	assert.Zero(t, count)
}
