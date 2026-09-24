// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"testing"

	activities_model "gitea.dev/models/activities"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterNotificationsByRepoAccess(t *testing.T) {
	require.NoError(t, unittest.LoadFixtures())

	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 40})
	inaccessibleRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	require.True(t, inaccessibleRepo.IsPrivate)
	accessibleRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	notifications := activities_model.NotificationList{
		{ID: 1, RepoID: inaccessibleRepo.ID, Source: activities_model.NotificationSourceRepository, Repository: inaccessibleRepo},
		{ID: 2, RepoID: accessibleRepo.ID, Source: activities_model.NotificationSourceRepository, Repository: accessibleRepo},
	}

	filtered, failures, err := notifications.FilterByAccess(t.Context(), doer)
	require.NoError(t, err)

	assert.Equal(t, []int{0}, failures)
	require.Len(t, filtered, 1)
	assert.EqualValues(t, 2, filtered[0].ID)
}
