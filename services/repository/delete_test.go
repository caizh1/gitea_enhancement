// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository_test

import (
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/models/webhook"
	repo_service "gitea.dev/services/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeam_HasRepository(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	test := func(teamID, repoID int64, expected bool) {
		team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: teamID})
		assert.Equal(t, expected, repo_service.HasRepository(t.Context(), team, repoID))
	}
	test(1, 1, false)
	test(1, 3, true)
	test(1, 5, true)
	test(1, unittest.NonexistentID, false)

	test(2, 3, true)
	test(2, 5, false)
}

func TestTeam_RemoveRepository(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	testSuccess := func(teamID, repoID int64) {
		team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: teamID})
		assert.NoError(t, repo_service.RemoveRepositoryFromTeam(t.Context(), team, repoID))
		unittest.AssertNotExistsBean(t, &organization.TeamRepo{TeamID: teamID, RepoID: repoID})
		unittest.CheckConsistencyFor(t, &organization.Team{ID: teamID}, &repo_model.Repository{ID: repoID})
	}
	testSuccess(2, 3)
	testSuccess(2, 5)
	testSuccess(1, unittest.NonexistentID)
}

func TestDeleteOwnerRepositoriesDirectly(t *testing.T) {
	unittest.PrepareTestEnv(t)

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	assert.NoError(t, repo_service.DeleteOwnerRepositoriesDirectly(t.Context(), user))
}

func TestDeleteRepositoryDirectlyPurgesRepoScopedRows(t *testing.T) {
	unittest.PrepareTestEnv(t)

	// One row per table that repository deletion used to leave behind (#38494).
	assert.NoError(t, db.Insert(t.Context(),
		&actions_model.ActionVariable{RepoID: 1, Name: "to_purge", Data: "value"},
		&actions_model.ActionRunAttempt{RepoID: 1, RunID: unittest.NonexistentID, Attempt: 1},
		&actions_model.ActionTasksVersion{RepoID: 1, Version: 1},
		&git_model.RenamedBranch{RepoID: 1, From: "old-name", To: "new-name"},
		&git_model.CommitStatusSummary{RepoID: 1, SHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", State: "success"},
		&repo_model.RepoTransfer{RepoID: 1, DoerID: 2, RecipientID: 3},
		&governance_model.Membership{ScopeType: "repository", ScopeID: 1, UserID: 4, Role: governance_model.Reporter},
		&governance_model.Share{ScopeType: "repository", ScopeID: 1, GroupID: 3, MaxRole: governance_model.Reporter},
		&governance_model.AccessRequest{ScopeType: "repository", ScopeID: 1, UserID: 5, Username: "user5", ScopePath: "user2/repo1"},
		&governance_model.AccessRequestSetting{ScopeType: "repository", ScopeID: 1, Disabled: true},
	))
	unittest.AssertExistsAndLoadBean(t, &git_model.CommitStatusIndex{RepoID: 1})

	assert.NoError(t, repo_service.DeleteRepositoryDirectly(t.Context(), 1))

	unittest.AssertNotExistsBean(t, &actions_model.ActionVariable{RepoID: 1})
	unittest.AssertNotExistsBean(t, &actions_model.ActionRunAttempt{RepoID: 1})
	unittest.AssertNotExistsBean(t, &actions_model.ActionTasksVersion{RepoID: 1})
	unittest.AssertNotExistsBean(t, &git_model.RenamedBranch{RepoID: 1})
	unittest.AssertNotExistsBean(t, &git_model.CommitStatusSummary{RepoID: 1})
	unittest.AssertNotExistsBean(t, &git_model.CommitStatusIndex{RepoID: 1})
	unittest.AssertNotExistsBean(t, &repo_model.RepoTransfer{RepoID: 1})
	unittest.AssertNotExistsBean(t, &governance_model.Membership{ScopeType: "repository", ScopeID: 1})
	unittest.AssertNotExistsBean(t, &governance_model.Share{ScopeType: "repository", ScopeID: 1})
	unittest.AssertNotExistsBean(t, &governance_model.AccessRequest{ScopeType: "repository", ScopeID: 1})
	unittest.AssertCount(t, &governance_model.AccessRequestSetting{ScopeType: "repository", ScopeID: 1}, 0)
}

func TestDeleteRepositoryRetainsRedactedWebhookFacts(t *testing.T) {
	unittest.PrepareTestEnv(t)
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	hook := &webhook.Webhook{RepoID: 1, IsActive: true}
	require.NoError(t, db.Insert(t.Context(), hook))
	pending := &webhook.HookTask{HookID: hook.ID, RepoID: 1, UUID: "delete-repo-pending", PayloadContent: "测试载荷", HookSnapshotEncrypted: "测试快照", RequestContent: "测试请求"}
	sent := &webhook.HookTask{HookID: hook.ID, RepoID: 1, UUID: "delete-repo-sent", IsDelivered: true, IsSucceed: true, Delivered: 123, PayloadContent: "测试载荷"}
	require.NoError(t, db.Insert(t.Context(), pending, sent))
	require.NoError(t, repo_service.DeleteRepositoryDirectly(t.Context(), 1))
	unittest.AssertNotExistsBean(t, &webhook.Webhook{ID: hook.ID})
	for _, id := range []int64{pending.ID, sent.ID} {
		fact := unittest.AssertExistsAndLoadBean(t, &webhook.HookTask{ID: id})
		require.True(t, fact.IsDelivered)
		require.Empty(t, fact.PayloadContent)
		require.Empty(t, fact.HookSnapshotEncrypted)
		require.Empty(t, fact.RequestContent)
		require.Contains(t, fact.ResponseContent, "redacted")
	}
	completed := unittest.AssertExistsAndLoadBean(t, &webhook.HookTask{ID: sent.ID})
	require.True(t, completed.IsSucceed)
	require.EqualValues(t, 123, completed.Delivered)
}
