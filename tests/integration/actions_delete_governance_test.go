// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package integration

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	actions_model "gitea.dev/models/actions"
	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/util"
	actions_service "gitea.dev/services/actions"

	"github.com/stretchr/testify/require"
)

func ensureActionsDeleteFixtureOwner(t *testing.T, owner *user_model.User) {
	t.Helper()
	_, err := governance_model.GetNamespace(t.Context(), owner.ID)
	if errors.Is(err, governance_model.ErrNotFound) {
		kind := "user"
		if owner.IsOrganization() {
			kind = "group"
		}
		require.NoError(t, governance_model.RegisterNativeNamespace(t.Context(), &governance_model.Namespace{ID: owner.ID, Slug: owner.Name, Kind: kind}))
		return
	}
	require.NoError(t, err)
}

func TestArchivedRepositoryRejectsUserActionsDeletion(t *testing.T) {
	for _, method := range []string{"web artifact", "api artifact", "web run", "api run"} {
		t.Run(method, func(t *testing.T) {
			defer prepareTestEnvActionsArtifacts(t)()
			repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
			require.NoError(t, db.Insert(t.Context(), &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeActions}))
			owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
			ensureActionsDeleteFixtureOwner(t, owner)
			session := loginUser(t, owner.Name)
			token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
			require.NoError(t, repo_model.SetArchiveRepoState(t.Context(), repo, true))

			switch method {
			case "web artifact":
				path := fmt.Sprintf("/%s/actions/runs/795/artifacts/artifact-795-1", repo.FullName())
				session.MakeRequest(t, NewRequest(t, "DELETE", path), http.StatusLocked)
			case "api artifact":
				path := fmt.Sprintf("/api/v1/repos/%s/actions/artifacts/24", repo.FullName())
				MakeRequest(t, NewRequest(t, "DELETE", path).AddTokenAuth(token), http.StatusLocked)
			case "web run":
				path := fmt.Sprintf("/%s/actions/runs/795/delete", repo.FullName())
				session.MakeRequest(t, NewRequest(t, "POST", path), http.StatusLocked)
			case "api run":
				path := fmt.Sprintf("/api/v1/repos/%s/actions/runs/795", repo.FullName())
				MakeRequest(t, NewRequest(t, "DELETE", path).AddTokenAuth(token), http.StatusLocked)
			}
			artifact := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionArtifact{ID: 24})
			require.Equal(t, actions_model.ArtifactStatusUploadConfirmed, artifact.Status)
			unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: 795, RepoID: repo.ID})
		})
	}
}

func TestActionsDeleteRunRejectsStaleCompletedSnapshot(t *testing.T) {
	defer prepareTestEnvActionsArtifacts(t)()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	require.NoError(t, db.Insert(t.Context(), &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeActions}))
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	ensureActionsDeleteFixtureOwner(t, owner)
	run, err := actions_model.GetRunByRepoAndID(t.Context(), repo.ID, 795)
	require.NoError(t, err)
	require.True(t, run.Status.IsDone())
	attempt := &actions_model.ActionRunAttempt{RepoID: repo.ID, RunID: run.ID, Attempt: 2, TriggerUserID: owner.ID, Status: actions_model.StatusWaiting}
	require.NoError(t, db.Insert(t.Context(), attempt))
	_, err = db.GetEngine(t.Context()).ID(run.ID).Cols("status", "latest_attempt_id").Update(&actions_model.ActionRun{Status: actions_model.StatusWaiting, LatestAttemptID: attempt.ID})
	require.NoError(t, err)
	require.ErrorIs(t, actions_service.DeleteRun(t.Context(), run, owner), governance_model.ErrConflict)
	current, err := actions_model.GetRunByRepoAndID(t.Context(), repo.ID, run.ID)
	require.NoError(t, err)
	require.Equal(t, actions_model.StatusWaiting, current.Status)
	require.Equal(t, attempt.ID, current.LatestAttemptID)
	unittest.AssertExistsAndLoadBean(t, &actions_model.ActionArtifact{ID: 24})
}

func TestActionsDeletionRechecksCurrentActor(t *testing.T) {
	defer prepareTestEnvActionsArtifacts(t)()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	require.NoError(t, db.Insert(t.Context(), &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeActions}))
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	ensureActionsDeleteFixtureOwner(t, owner)
	run, err := actions_model.GetRunByRepoAndID(t.Context(), repo.ID, 795)
	require.NoError(t, err)
	_, err = db.GetEngine(t.Context()).ID(owner.ID).Cols("is_active").Update(&user_model.User{IsActive: false})
	require.NoError(t, err)
	require.ErrorIs(t, actions_service.DeleteRun(t.Context(), run, owner), governance_model.ErrForbidden)
	require.ErrorIs(t, actions_service.RequestArtifactDeletionByRunAttempt(t.Context(), owner, repo.ID, run.ID, 0, "artifact-795-1"), governance_model.ErrForbidden)
	unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: run.ID, RepoID: repo.ID})
	artifact := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionArtifact{ID: 24})
	require.Equal(t, actions_model.ArtifactStatusUploadConfirmed, artifact.Status)
}

func TestExpiredV4ArtifactUserDeletion(t *testing.T) {
	defer prepareTestEnvActionsArtifacts(t)()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	ensureActionsDeleteFixtureOwner(t, owner)
	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
	_, err := db.GetEngine(t.Context()).ID(22).Cols("status").Update(&actions_model.ActionArtifact{Status: actions_model.ArtifactStatusExpired})
	require.NoError(t, err)
	path := fmt.Sprintf("/api/v1/repos/%s/actions/artifacts/22", repo.FullName())
	MakeRequest(t, NewRequest(t, "DELETE", path).AddTokenAuth(token), http.StatusNoContent)
	artifact := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionArtifact{ID: 22})
	require.Equal(t, actions_model.ArtifactStatusPendingDeletion, artifact.Status)
	MakeRequest(t, NewRequest(t, "GET", path).AddTokenAuth(token), http.StatusNotFound)
}

func TestActionsDeletionRejectsForeignTargets(t *testing.T) {
	defer prepareTestEnvActionsArtifacts(t)()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	require.NoError(t, db.Insert(t.Context(), &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeActions}))
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	ensureActionsDeleteFixtureOwner(t, owner)
	require.ErrorIs(t, actions_service.RequestArtifactDeletionByID(t.Context(), owner, repo.ID, 22), util.ErrNotExist)
	attempt := &actions_model.ActionRunAttempt{RepoID: repo.ID, RunID: 803, Attempt: 99, TriggerUserID: owner.ID, Status: actions_model.StatusWaiting}
	require.NoError(t, db.Insert(t.Context(), attempt))
	require.ErrorIs(t, actions_service.RequestArtifactDeletionByRunAttempt(t.Context(), owner, repo.ID, 795, attempt.ID, "artifact-795-1"), util.ErrNotExist)
	artifact := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionArtifact{ID: 24})
	require.Equal(t, actions_model.ArtifactStatusUploadConfirmed, artifact.Status)
}
