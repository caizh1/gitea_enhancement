// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package convert

import (
	"testing"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/organization"
	perm_model "gitea.dev/models/perm"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToNotificationThreadIncludesRepoForAccessibleUser(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	n := newRepoNotification(t, 1, 4)
	thread := ToNotificationThread(t.Context(), n)

	if assert.NotNil(t, thread.Repository) {
		assert.Equal(t, n.Repository.FullName(), thread.Repository.FullName)
		assert.Nil(t, thread.Repository.Permissions)
	}
}

func TestToNotificationThreadOmitsRepoWhenAccessRevoked(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	n := newRepoNotification(t, 2, 4)
	thread := ToNotificationThread(t.Context(), n)

	assert.Nil(t, thread.Repository)
}

func TestToNotificationThreadOmitsSubjectWhenAccessRevoked(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	ctx := t.Context()
	// repo 2 is private; user 4 has no access to it
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	assert.NoError(t, repo.LoadOwner(ctx))
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 4, RepoID: repo.ID})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})

	n := &activities_model.Notification{
		ID:          12345,
		UserID:      user.ID,
		RepoID:      repo.ID,
		Status:      activities_model.NotificationStatusUnread,
		Source:      activities_model.NotificationSourceIssue,
		IssueID:     issue.ID,
		UpdatedUnix: timeutil.TimeStampNow(),
		Issue:       issue,
		Repository:  repo,
		User:        user,
	}

	thread := ToNotificationThread(ctx, n)

	// must not leak private issue metadata once access is revoked
	assert.Nil(t, thread.Repository)
	assert.Nil(t, thread.Subject)
}

func TestToNotificationThreadOmitsRevokedIssueUnit(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})
	reader := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	repo := &repo_model.Repository{OwnerID: owner.ID, OwnerName: owner.Name, OwnerNamespace: owner.Name, Name: "notification-unit-revocation", LowerName: "notification-unit-revocation", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeIssues}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypePullRequests}))
	require.NoError(t, db.Insert(ctx, &organization.TeamRepo{OrgID: owner.ID, TeamID: 2, RepoID: repo.ID}))
	_, err := db.GetEngine(ctx).Where("team_id = ? AND type <> ?", 2, unit.TypeCode).Delete(&organization.TeamUnit{})
	require.NoError(t, err)
	issue := &issues_model.Issue{RepoID: repo.ID, Index: 1, PosterID: owner.ID, Title: "授权时的议题标题"}
	require.NoError(t, db.Insert(ctx, issue))

	n := &activities_model.Notification{ID: 12346, UserID: reader.ID, RepoID: repo.ID, Status: activities_model.NotificationStatusUnread, Source: activities_model.NotificationSourceIssue, IssueID: issue.ID, Repository: repo, Issue: issue, User: reader}
	require.NoError(t, db.Insert(ctx, n))
	issue.Title = "撤权后更新的议题标题"
	_, err = db.GetEngine(ctx).ID(issue.ID).Cols("name").Update(issue)
	require.NoError(t, err)
	thread := ToNotificationThread(ctx, n)
	require.NotNil(t, thread)
	assert.Nil(t, thread.Subject)
	assert.Nil(t, thread.Repository)

	pull := &issues_model.Issue{RepoID: repo.ID, Index: 2, PosterID: owner.ID, Title: "撤权后的合并请求标题", IsPull: true}
	require.NoError(t, db.Insert(ctx, pull))
	require.NoError(t, db.Insert(ctx,
		&activities_model.Notification{UserID: reader.ID, RepoID: repo.ID, Status: activities_model.NotificationStatusUnread, Source: activities_model.NotificationSourcePullRequest, IssueID: pull.ID},
		&activities_model.Notification{UserID: reader.ID, RepoID: repo.ID, Status: activities_model.NotificationStatusUnread, Source: activities_model.NotificationSourceCommit, CommitID: "abc123"},
		&activities_model.Notification{UserID: reader.ID, RepoID: repo.ID, Status: activities_model.NotificationStatusUnread, Source: activities_model.NotificationSourceRepository},
	))
	opts := activities_model.FindNotificationOptions{UserID: reader.ID, Status: []activities_model.NotificationStatus{activities_model.NotificationStatusUnread}}
	visible, total, err := activities_model.FindVisibleNotifications(ctx, reader, opts)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, visible, 2)
	assert.ElementsMatch(t, []activities_model.NotificationSource{activities_model.NotificationSourceRepository, activities_model.NotificationSourceCommit}, []activities_model.NotificationSource{visible[0].Source, visible[1].Source})
	count, err := activities_model.CountVisibleNotifications(ctx, reader, opts)
	require.NoError(t, err)
	assert.EqualValues(t, 2, count)
	counts, err := activities_model.GetUIDsAndNotificationCounts(ctx, timeutil.TimeStampNow().Add(-10), timeutil.TimeStampNow().Add(10))
	require.NoError(t, err)
	require.Len(t, counts, 1)
	assert.Equal(t, reader.ID, counts[0].UserID)
	assert.EqualValues(t, 2, counts[0].Count)
	opts.ListOptions = db.ListOptions{PageSize: 1, Page: 2}
	secondPage, pageTotal, err := activities_model.FindVisibleNotifications(ctx, reader, opts)
	require.NoError(t, err)
	assert.EqualValues(t, 2, pageTotal)
	require.Len(t, secondPage, 1)
	assert.NotEqual(t, activities_model.NotificationSourceIssue, secondPage[0].Source)
	assert.NotEqual(t, activities_model.NotificationSourcePullRequest, secondPage[0].Source)
	opts.ListOptions = db.ListOptions{}

	_, err = db.GetEngine(ctx).Where("team_id = ?", 2).Delete(&organization.TeamUnit{})
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &organization.TeamUnit{OrgID: owner.ID, TeamID: 2, Type: unit.TypeIssues, AccessMode: perm_model.AccessModeRead}))
	visible, total, err = activities_model.FindVisibleNotifications(ctx, reader, opts)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, visible, 2)
	assert.ElementsMatch(t, []activities_model.NotificationSource{activities_model.NotificationSourceIssue, activities_model.NotificationSourceRepository}, []activities_model.NotificationSource{visible[0].Source, visible[1].Source})

	_, err = db.GetEngine(ctx).Where("team_id = ?", 2).Delete(&organization.TeamUnit{})
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &organization.TeamUnit{OrgID: owner.ID, TeamID: 2, Type: unit.TypePullRequests, AccessMode: perm_model.AccessModeRead}))
	visible, total, err = activities_model.FindVisibleNotifications(ctx, reader, opts)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, visible, 2)
	assert.ElementsMatch(t, []activities_model.NotificationSource{activities_model.NotificationSourcePullRequest, activities_model.NotificationSourceRepository}, []activities_model.NotificationSource{visible[0].Source, visible[1].Source})
}

func TestToNotificationThread(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	t.Run("issue notification", func(t *testing.T) {
		// Notification 1: source=issue, issue_id=1, status=unread
		n := unittest.AssertExistsAndLoadBean(t, &activities_model.Notification{ID: 1})
		require.NoError(t, n.LoadAttributes(t.Context()))

		thread := ToNotificationThread(t.Context(), n)
		assert.Equal(t, int64(1), thread.ID)
		assert.True(t, thread.Unread)
		assert.False(t, thread.Pinned)
		require.NotNil(t, thread.Subject)
		assert.Equal(t, api.NotifySubjectIssue, thread.Subject.Type)
		assert.Equal(t, api.NotifySubjectStateOpen, thread.Subject.State)
	})

	t.Run("pinned notification", func(t *testing.T) {
		// Notification 3: status=pinned
		n := unittest.AssertExistsAndLoadBean(t, &activities_model.Notification{ID: 3})
		require.NoError(t, n.LoadAttributes(t.Context()))

		thread := ToNotificationThread(t.Context(), n)
		assert.False(t, thread.Unread)
		assert.True(t, thread.Pinned)
	})

	t.Run("merged pull request returns merged state", func(t *testing.T) {
		// Issue 2 is a pull request; pull_request 1 has has_merged=true.
		issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})

		n := &activities_model.Notification{
			ID:         999,
			UserID:     2,
			RepoID:     repo.ID,
			Status:     activities_model.NotificationStatusUnread,
			Source:     activities_model.NotificationSourcePullRequest,
			IssueID:    issue.ID,
			Issue:      issue,
			Repository: repo,
		}

		thread := ToNotificationThread(t.Context(), n)
		require.NotNil(t, thread.Subject)
		assert.Equal(t, api.NotifySubjectPull, thread.Subject.Type)
		assert.Equal(t, api.NotifySubjectStateMerged, thread.Subject.State)
	})

	t.Run("open pull request returns open state", func(t *testing.T) {
		// Issue 3 is a pull request; pull_request 2 has has_merged=false.
		issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})

		n := &activities_model.Notification{
			ID:         998,
			UserID:     2,
			RepoID:     repo.ID,
			Status:     activities_model.NotificationStatusUnread,
			Source:     activities_model.NotificationSourcePullRequest,
			IssueID:    issue.ID,
			Issue:      issue,
			Repository: repo,
		}

		thread := ToNotificationThread(t.Context(), n)
		require.NotNil(t, thread.Subject)
		assert.Equal(t, api.NotifySubjectPull, thread.Subject.Type)
		assert.Equal(t, api.NotifySubjectStateOpen, thread.Subject.State)
	})
}

func newRepoNotification(t *testing.T, repoID, userID int64) *activities_model.Notification {
	t.Helper()

	ctx := t.Context()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repoID})
	assert.NoError(t, repo.LoadOwner(ctx))
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: userID})

	return &activities_model.Notification{
		ID:          repoID*1000 + userID,
		UserID:      user.ID,
		RepoID:      repo.ID,
		Status:      activities_model.NotificationStatusUnread,
		Source:      activities_model.NotificationSourceRepository,
		UpdatedUnix: timeutil.TimeStampNow(),
		Repository:  repo,
		User:        user,
	}
}
