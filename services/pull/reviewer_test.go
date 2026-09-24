// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pull_test

import (
	"slices"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	governance_service "gitea.dev/services/governance"
	issue_service "gitea.dev/services/issue"
	pull_service "gitea.dev/services/pull"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepoGetReviewersIncludesGovernanceMember(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	user := &user_model.User{Name: "governance-reviewer", LowerName: "governance-reviewer", IsActive: true}
	require.NoError(t, db.Insert(ctx, user))
	require.NoError(t, db.Insert(ctx, &governance_model.Membership{ScopeType: "group", ScopeID: repo.OwnerID, UserID: user.ID, Role: governance_model.Reporter}))
	reviewers, err := pull_service.GetReviewers(ctx, repo, 2, 0)
	require.NoError(t, err)
	assert.Contains(t, reviewerIDs(reviewers), user.ID)
}

func reviewerIDs(users []*user_model.User) []int64 {
	ids := make([]int64, 0, len(users))
	for _, user := range users {
		ids = append(ids, user.ID)
	}
	return ids
}

func TestRepoCollaborationCandidatesRespectAncestorShareAndRevocation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	child, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "collaboration-child", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	sibling, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "collaboration-sibling", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	repo := &repo_model.Repository{OwnerID: child.ID, OwnerName: child.InternalName, Name: "collaboration-repo", LowerName: "collaboration-repo", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx,
		&repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode},
		&repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeIssues},
		&repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypePullRequests},
	))
	users := make([]*user_model.User, 4)
	for i, name := range []string{"ancestor-candidate", "shared-candidate", "sibling-candidate", "guest-candidate"} {
		users[i] = &user_model.User{Name: name, LowerName: name, IsActive: true}
		require.NoError(t, db.Insert(ctx, users[i]))
	}
	require.NoError(t, db.Insert(ctx,
		&governance_model.Membership{ScopeType: "group", ScopeID: 3, UserID: users[0].ID, Role: governance_model.Developer},
		&governance_model.Membership{ScopeType: "group", ScopeID: 17, UserID: users[1].ID, Role: governance_model.Developer},
		&governance_model.Membership{ScopeType: "group", ScopeID: sibling.ID, UserID: users[2].ID, Role: governance_model.Developer},
		&governance_model.Membership{ScopeType: "group", ScopeID: 3, UserID: users[3].ID, Role: governance_model.Guest},
	))
	share := &governance_model.Share{ScopeType: "repository", ScopeID: repo.ID, GroupID: 17, MaxRole: governance_model.Reporter}
	require.NoError(t, db.Insert(ctx, share))
	require.NoError(t, repo.LoadOwner(ctx))
	check := func(ancestor, shared bool) {
		t.Helper()
		reviewers, err := pull_service.GetReviewers(ctx, repo, 2, 0)
		require.NoError(t, err)
		assignees, err := access_model.GetRepoAssignees(ctx, repo)
		require.NoError(t, err)
		for _, ids := range [][]int64{reviewerIDs(reviewers), reviewerIDs(assignees)} {
			assert.Equal(t, ancestor, containsID(ids, users[0].ID))
			assert.Equal(t, shared, containsID(ids, users[1].ID))
			assert.NotContains(t, ids, users[2].ID, "兄弟群组不能进入候选")
			assert.NotContains(t, ids, users[3].ID, "Guest 不能成为审查或指派候选")
		}
		assert.Equal(t, ancestor, issue_service.CanDoerChangeReviewRequests(ctx, users[0], repo, 0))
		assert.False(t, issue_service.CanDoerChangeReviewRequests(ctx, users[1], repo, 0), "Reporter 共享只能成为候选，不能修改他人的审查请求")
		assert.False(t, issue_service.CanDoerChangeReviewRequests(ctx, users[2], repo, 0))
		assert.False(t, issue_service.CanDoerChangeReviewRequests(ctx, users[3], repo, 0))
	}
	check(true, true)
	_, err = db.GetEngine(ctx).ID(share.ID).Cols("max_role").Update(&governance_model.Share{MaxRole: governance_model.Guest})
	require.NoError(t, err)
	check(true, false)
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "group", 3, users[0].ID).Delete(new(governance_model.Membership))
	require.NoError(t, err)
	check(false, false)
}

func containsID(ids []int64, id int64) bool {
	return slices.Contains(ids, id)
}

func TestRepoGetReviewers(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	// test public repo
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	ctx := t.Context()
	reviewers, err := pull_service.GetReviewers(ctx, repo1, 2, 0)
	assert.NoError(t, err)
	if assert.Len(t, reviewers, 1) {
		assert.ElementsMatch(t, []int64{2}, []int64{reviewers[0].ID})
	}

	// should not include doer and remove the poster
	reviewers, err = pull_service.GetReviewers(ctx, repo1, 11, 2)
	assert.NoError(t, err)
	assert.Empty(t, reviewers)

	// should not include PR poster, if PR poster would be otherwise eligible
	reviewers, err = pull_service.GetReviewers(ctx, repo1, 11, 4)
	assert.NoError(t, err)
	assert.Len(t, reviewers, 1)

	// test private user repo
	repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})

	reviewers, err = pull_service.GetReviewers(ctx, repo2, 2, 4)
	assert.NoError(t, err)
	assert.Empty(t, reviewers, "仓库未启用 PR 单元时，仓库 Owner 也不应出现在审查候选中")

	// test private org repo
	repo3 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})

	reviewers, err = pull_service.GetReviewers(ctx, repo3, 2, 1)
	assert.NoError(t, err)
	assert.Len(t, reviewers, 2)

	reviewers, err = pull_service.GetReviewers(ctx, repo3, 2, 2)
	assert.NoError(t, err)
	assert.Len(t, reviewers, 1)
}

func TestRepoGetReviewerTeams(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	teams, err := pull_service.GetReviewerTeams(t.Context(), repo2)
	assert.NoError(t, err)
	assert.Empty(t, teams)

	repo3 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	teams, err = pull_service.GetReviewerTeams(t.Context(), repo3)
	assert.NoError(t, err)
	assert.Len(t, teams, 2)
}
