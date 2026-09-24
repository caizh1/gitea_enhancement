// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/json"
	api "gitea.dev/modules/structs"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplaceSchedulesRejectsStaleRepoSnapshot(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	creator := governance_model.Actor{ID: 1, Kind: "user", Name: "user1", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, creator, governance_service.GroupOption{Path: "schedule-before-move", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	repo := &repo_model.Repository{OwnerID: group.ID, OwnerName: group.InternalName, Name: "workflow", LowerName: "workflow", Status: repo_model.RepositoryReady}
	require.NoError(t, db.Insert(ctx, repo))
	repo.OwnerNamespace, err = governance_model.RegisterNativeRepository(ctx, repo.ID, group.ID, repo.Name)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(repo.ID).Cols("owner_namespace").Update(repo)
	require.NoError(t, err)
	oldSnapshot, err := repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	_, err = governance_service.MoveGroup(ctx, governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "api"}, group.ID, governance_service.GroupOption{Path: "schedule-before-move", Revision: group.Revision})
	require.NoError(t, err)
	currentRepo, err := repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	currentPlan := &actions_model.ActionSchedule{RepoID: repo.ID, OwnerID: group.ID, ScopeRevision: currentRepo.ActionsScopeRevision}
	require.NoError(t, db.Insert(ctx, currentPlan))
	require.ErrorIs(t, replaceSchedulesForRepo(ctx, oldSnapshot, "stale", nil), governance_model.ErrConflict)
	unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{ID: currentPlan.ID})
	moved, err := governance_model.GetNamespace(ctx, group.ID)
	require.NoError(t, err)
	_, err = governance_service.MoveGroup(ctx, creator, group.ID, governance_service.GroupOption{Path: "schedule-before-move", ParentID: 3, Revision: moved.Revision})
	require.NoError(t, err)
	secondPlan := &actions_model.ActionSchedule{RepoID: repo.ID, OwnerID: group.ID, ScopeRevision: currentRepo.ActionsScopeRevision + 1}
	require.NoError(t, db.Insert(ctx, secondPlan))
	require.ErrorIs(t, replaceSchedulesForRepo(ctx, oldSnapshot, "stale", nil), governance_model.ErrConflict)
	unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{ID: secondPlan.ID})
}

func TestNextScheduleTimeKeepsNextMinute(t *testing.T) {
	now := time.Date(2026, time.September, 24, 8, 0, 30, 0, time.UTC)
	next, err := nextScheduleTime(&actions_model.ActionScheduleSpec{Spec: "* * * * *"}, now)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, time.September, 24, 8, 1, 0, 0, time.UTC).Unix(), int64(next))
}

func TestWithScheduleInEventPayload(t *testing.T) {
	t.Run("adds schedule to existing payload", func(t *testing.T) {
		payload := `{"ref":"refs/heads/main"}`
		updated := withScheduleInEventPayload(payload, "*/5 * * * *", nil)

		event := map[string]any{}
		assert.NoError(t, json.Unmarshal([]byte(updated), &event))
		assert.Equal(t, "*/5 * * * *", event["schedule"])
		assert.Equal(t, "refs/heads/main", event["ref"])
	})

	t.Run("adds schedule to null payload", func(t *testing.T) {
		updated := withScheduleInEventPayload("null", "37 12 5 1 2", nil)

		event := map[string]any{}
		assert.NoError(t, json.Unmarshal([]byte(updated), &event))
		assert.Equal(t, "37 12 5 1 2", event["schedule"])
	})

	t.Run("adds schedule to empty payload", func(t *testing.T) {
		updated := withScheduleInEventPayload("", "37 12 5 1 2", nil)

		event := map[string]any{}
		assert.NoError(t, json.Unmarshal([]byte(updated), &event))
		assert.Equal(t, "37 12 5 1 2", event["schedule"])
	})

	t.Run("adds schedule with repository, sender, organization", func(t *testing.T) {
		updated := withScheduleInEventPayload("null", "@weekly", map[string]any{
			"repository":   &api.Repository{Name: "test-repo"},
			"sender":       &api.User{UserName: "test-user"},
			"organization": &api.Organization{Name: "test-org"},
		})

		event := map[string]any{}
		assert.NoError(t, json.Unmarshal([]byte(updated), &event))
		assert.Equal(t, "@weekly", event["schedule"])
		assert.Equal(t, "test-repo", event["repository"].(map[string]any)["name"])
		assert.Equal(t, "test-user", event["sender"].(map[string]any)["login"])
		assert.Equal(t, "test-org", event["organization"].(map[string]any)["name"])
	})

	t.Run("keeps payload when schedule empty", func(t *testing.T) {
		payload := `{"ref":"refs/heads/main"}`
		updated := withScheduleInEventPayload(payload, "", nil)
		assert.Equal(t, payload, updated)
	})

	t.Run("keeps payload when malformed JSON", func(t *testing.T) {
		payload := `not a json object`
		updated := withScheduleInEventPayload(payload, "*/5 * * * *", nil)
		assert.Equal(t, payload, updated)
	})
}
