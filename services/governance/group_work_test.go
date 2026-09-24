// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"
	"time"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestListGroupSharedRepositoriesFiltersBeforePagination(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	actor := gm.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	root, err := CreateGroup(ctx, actor, GroupOption{Path: "shared-list", Visibility: 2})
	require.NoError(t, err)
	outside, err := CreateGroup(ctx, actor, GroupOption{Path: "shared-source", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, root.ID, GroupMemberOption{UserID: 4, Role: gm.Developer, Revision: root.Revision}, false))
	newRepo := func(owner *GroupState, name string) *repo_model.Repository {
		repo := &repo_model.Repository{OwnerID: owner.ID, OwnerName: owner.InternalName, OwnerNamespace: owner.FullPath, Name: name, LowerName: name, IsPrivate: true}
		require.NoError(t, db.Insert(ctx, repo))
		require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}))
		return repo
	}
	first := newRepo(outside, "first")
	second := newRepo(outside, "second")
	local := newRepo(root, "local")
	for _, repo := range []*repo_model.Repository{first, second, local} {
		require.NoError(t, db.Insert(ctx, &gm.Share{ScopeType: "repository", ScopeID: repo.ID, GroupID: root.ID, MaxRole: gm.Reporter}))
	}
	_, err = db.GetEngine(ctx).ID(second.ID).Cols("is_archived").Update(&repo_model.Repository{IsArchived: true})
	require.NoError(t, err)
	page, err := ListGroupSharedRepositories(ctx, 4, root.ID, 1, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Count, "本树项目不能伪装为共享项目")
	require.Len(t, page.Repositories, 1)
	require.Equal(t, first.ID, page.Repositories[0].ID)
	page, err = ListGroupSharedRepositories(ctx, 4, root.ID, 2, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Count)
	require.Equal(t, second.ID, page.Repositories[0].ID)
	page, err = ListGroupSharedRepositories(ctx, 5, root.ID, 1, 20)
	require.ErrorIs(t, err, gm.ErrNotFound)
	require.Nil(t, page)
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND group_id = ?", "repository", first.ID, root.ID).Delete(new(gm.Share))
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND group_id = ?", "repository", second.ID, root.ID).Cols("expires_unix").Update(&gm.Share{ExpiresUnix: time.Now().Add(-time.Hour).Unix()})
	require.NoError(t, err)
	page, err = ListGroupSharedRepositories(ctx, 4, root.ID, 1, 20)
	require.NoError(t, err)
	require.Zero(t, page.Count, "撤销和到期后的下一次查询不再保留项目或计数")
	require.Empty(t, page.Repositories)
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND group_id = ?", "repository", second.ID, root.ID).Cols("expires_unix").Update(&gm.Share{ExpiresUnix: 0})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "group", root.ID, 4).Cols("role").Update(&gm.Membership{Role: gm.Guest})
	require.NoError(t, err)
	page, err = ListGroupSharedRepositories(ctx, 4, root.ID, 1, 20)
	require.NoError(t, err)
	require.Zero(t, page.Count, "降为无代码读取权后，共享关联不能单独授予仓库读取")
}

func TestGroupIssuesFilterScopeBeforeCountsAndPagination(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	actor := gm.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	root, err := CreateGroup(ctx, actor, GroupOption{Path: "work-root"})
	require.NoError(t, err)
	child, err := CreateGroup(ctx, actor, GroupOption{Path: "private", ParentID: root.ID, Visibility: 2})
	require.NoError(t, err)
	deep, err := CreateGroup(ctx, actor, GroupOption{Path: "deep", ParentID: child.ID, Visibility: 2})
	require.NoError(t, err)
	hidden, err := CreateGroup(ctx, actor, GroupOption{Path: "hidden", ParentID: root.ID, Visibility: 2})
	require.NoError(t, err)
	outside, err := CreateGroup(ctx, actor, GroupOption{Path: "outside", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, deep.ID, GroupMemberOption{UserID: 4, Role: gm.Reporter, Revision: deep.Revision}, false))

	newRepo := func(owner *GroupState, name string, private bool) *repo_model.Repository {
		repo := &repo_model.Repository{OwnerID: owner.ID, OwnerName: owner.InternalName, OwnerNamespace: owner.FullPath, Name: name, LowerName: name, IsPrivate: private}
		require.NoError(t, db.Insert(ctx, repo))
		require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeIssues}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypePullRequests}))
		return repo
	}
	publicRepo := newRepo(root, "public", false)
	deepRepo := newRepo(deep, "allowed", true)
	hiddenRepo := newRepo(hidden, "secret", true)
	sharedRepo := newRepo(outside, "external", true)
	for i, repo := range []*repo_model.Repository{publicRepo, deepRepo, hiddenRepo, sharedRepo} {
		issue := &issues_model.Issue{RepoID: repo.ID, Index: 1, PosterID: 4, Title: "工作项目"}
		require.NoError(t, db.Insert(ctx, issue))
		if i == 1 {
			require.NoError(t, db.Insert(ctx, &issues_model.IssueAssignees{IssueID: issue.ID, AssigneeID: 4}))
		}
	}
	page, err := ListGroupIssues(ctx, 4, root.ID, false, GroupWorkQuery{PageSize: 1})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Count)
	require.Len(t, page.Issues, 1)
	require.Len(t, page.Repositories, 2, "不可见仓库不能进入筛选器")
	next, err := ListGroupIssues(ctx, 4, root.ID, false, GroupWorkQuery{Page: 2, PageSize: 1})
	require.NoError(t, err)
	require.Len(t, next.Issues, 1)
	require.NotEqual(t, page.Issues[0].ID, next.Issues[0].ID)
	for _, query := range []GroupWorkQuery{{Scope: "direct"}, {Filter: "assigned"}, {RepoID: deepRepo.ID}, {Filter: "created", RepoID: deepRepo.ID}} {
		page, err = ListGroupIssues(ctx, 4, root.ID, false, query)
		require.NoError(t, err)
		require.EqualValues(t, 1, page.Count)
	}
	page, err = ListGroupIssues(ctx, 4, root.ID, false, GroupWorkQuery{RepoID: hiddenRepo.ID})
	require.NoError(t, err)
	require.Zero(t, page.Count, "伪造仓库 ID 不扩展授权范围")
	page, err = ListGroupIssues(ctx, 0, root.ID, false, GroupWorkQuery{})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Count)
	require.Equal(t, publicRepo.ID, page.Issues[0].RepoID)
	_, err = ListGroupIssues(ctx, 0, root.ID, false, GroupWorkQuery{Filter: "assigned"})
	require.ErrorIs(t, err, gm.ErrInvalid)
	_, err = ListGroupIssues(ctx, 4, hidden.ID, false, GroupWorkQuery{})
	require.ErrorIs(t, err, gm.ErrNotFound)

	require.NoError(t, db.Insert(ctx, &gm.Share{ScopeType: "repository", ScopeID: sharedRepo.ID, GroupID: deep.ID, MaxRole: gm.Reporter}))
	page, err = ListGroupIssues(ctx, 4, deep.ID, false, GroupWorkQuery{Scope: "shared"})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Count)
	require.Equal(t, sharedRepo.ID, page.Issues[0].RepoID)
	page, err = ListGroupIssues(ctx, 4, deep.ID, false, GroupWorkQuery{})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Count)
	require.Equal(t, deepRepo.ID, page.Issues[0].RepoID, "共享项目不能混入本树")
	for _, repo := range []*repo_model.Repository{deepRepo, hiddenRepo} {
		issue := &issues_model.Issue{RepoID: repo.ID, Index: 2, PosterID: 2, Title: "待审查变更", IsPull: true}
		require.NoError(t, db.Insert(ctx, issue))
		require.NoError(t, db.Insert(ctx, &issues_model.PullRequest{IssueID: issue.ID, Index: 2, BaseRepoID: repo.ID, HeadRepoID: repo.ID, BaseBranch: "main", HeadBranch: "feature"}))
		require.NoError(t, db.Insert(ctx, &issues_model.Review{IssueID: issue.ID, ReviewerID: 4, Type: issues_model.ReviewTypeRequest}))
	}
	page, err = ListGroupIssues(ctx, 4, root.ID, true, GroupWorkQuery{Filter: "review_requested"})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Count, "历史审查请求不能暴露当前无权的仓库")
	require.Equal(t, deepRepo.ID, page.Issues[0].RepoID)
	_, err = db.GetEngine(ctx).Where("issue_id = ?", page.Issues[0].ID).Delete(new(issues_model.Review))
	require.NoError(t, err)
	page, err = ListGroupIssues(ctx, 4, root.ID, true, GroupWorkQuery{Filter: "review_requested"})
	require.NoError(t, err)
	require.Zero(t, page.Count)
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "group", deep.ID, 4).Delete(new(gm.Membership))
	require.NoError(t, err)
	page, err = ListGroupIssues(ctx, 4, root.ID, false, GroupWorkQuery{})
	require.NoError(t, err)
	require.EqualValues(t, 1, page.Count, "撤权后的下一次查询不再返回私有内容或计数")
}
