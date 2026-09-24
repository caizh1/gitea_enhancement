// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package integration

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/modules/storage"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestWebArtifactDownloadUsesRunIDAndAttemptNumber(t *testing.T) {
	for _, fixture := range []struct {
		repoID int64
		owner  string
		actor  string
	}{{2, "user2", "user2"}, {3, "org3", "user1"}} {
		t.Run(fixture.owner, func(t *testing.T) {
			testWebArtifactDownloadUsesRunIDAndAttemptNumber(t, fixture.repoID, fixture.owner, fixture.actor)
		})
	}
}

func testWebArtifactDownloadUsesRunIDAndAttemptNumber(t *testing.T, repoID int64, repoOwner, actor string) {
	defer tests.PrepareTestEnv(t)()
	tests.PrepareArtifactsStorage(t)
	repo, err := repo_model.GetRepositoryByID(t.Context(), repoID)
	require.NoError(t, err)
	require.NoError(t, db.Insert(t.Context(), &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeActions}))
	run := &actions_model.ActionRun{RepoID: repo.ID, OwnerID: repo.OwnerID, Index: 1901, WorkflowID: "artifact.yml", TriggerUserID: 2, Ref: "refs/heads/master", Status: actions_model.StatusSuccess}
	require.NoError(t, db.Insert(t.Context(), run))
	paths := []string{"first.txt", "second.txt"}
	for i, name := range paths {
		attempt := &actions_model.ActionRunAttempt{RepoID: repo.ID, RunID: run.ID, Attempt: int64(i + 1), Status: actions_model.StatusSuccess}
		require.NoError(t, db.Insert(t.Context(), attempt))
		path := fmt.Sprintf("web-artifact-test/%d/%d.chunk", run.ID, attempt.ID)
		_, err := storage.ActionsArtifacts.Save(path, strings.NewReader(name), int64(len(name)))
		require.NoError(t, err)
		require.NoError(t, db.Insert(t.Context(), &actions_model.ActionArtifact{RepoID: repo.ID, OwnerID: repo.OwnerID, RunID: run.ID, RunAttemptID: attempt.ID, ArtifactName: "phase1-ancestor-result", ArtifactPath: "result.txt", StoragePath: path, FileSize: int64(len(name)), Status: actions_model.ArtifactStatusUploadConfirmed}))
		run.LatestAttemptID = attempt.ID
	}
	_, err = db.GetEngine(t.Context()).ID(run.ID).Cols("latest_attempt_id").Update(run)
	require.NoError(t, err)
	owner := loginUser(t, actor)
	base := fmt.Sprintf("/%s/%s/actions/runs/%d/artifacts/phase1-ancestor-result", repoOwner, repo.Name, run.ID)
	for _, tc := range []struct {
		query, want string
	}{{"?attempt=1", "first.txt"}, {"?attempt=2", "second.txt"}, {"", "second.txt"}} {
		response := owner.MakeRequest(t, NewRequest(t, "GET", base+tc.query), http.StatusOK)
		archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
		require.NoError(t, err)
		require.Len(t, archive.File, 1)
		reader, err := archive.File[0].Open()
		require.NoError(t, err)
		body, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, tc.want, string(body))
	}
	owner.MakeRequest(t, NewRequest(t, "GET", base+"?attempt=3"), http.StatusNotFound)
	loginUser(t, "user5").MakeRequest(t, NewRequest(t, "GET", base+"?attempt=1"), http.StatusNotFound)
	owner.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/user5/repo4/actions/runs/%d/artifacts/phase1-ancestor-result?attempt=1", run.ID)), http.StatusNotFound)
}
