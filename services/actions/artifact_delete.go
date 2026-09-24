// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/util"
	governance_service "gitea.dev/services/governance"
)

// RequestArtifactDeletionByID guards a user's v4 artifact deletion at the final write.
func RequestArtifactDeletionByID(ctx context.Context, doer *user_model.User, repoID, artifactID int64) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repoID)}, func(ctx context.Context) error {
		if err := governance_service.CheckRepositoryContentWrite(ctx, doer, repoID, unit.TypeActions); err != nil {
			return err
		}
		artifact, ok, err := db.GetByID[actions_model.ActionArtifact](ctx, artifactID)
		if err != nil {
			return err
		}
		if !ok || artifact.RepoID != repoID || !IsArtifactV4(artifact) || artifact.Status != actions_model.ArtifactStatusUploadConfirmed && artifact.Status != actions_model.ArtifactStatusExpired {
			return util.ErrNotExist
		}
		if _, err := actions_model.GetRunByRepoAndID(ctx, repoID, artifact.RunID); err != nil {
			return err
		}
		_, err = db.GetEngine(ctx).ID(artifactID).
			In("status", actions_model.ArtifactStatusUploadConfirmed, actions_model.ArtifactStatusExpired).
			Cols("status").Update(&actions_model.ActionArtifact{Status: actions_model.ArtifactStatusPendingDeletion})
		return err
	})
}

// RequestArtifactDeletionByRunAttempt guards a user's run-scoped artifact deletion.
func RequestArtifactDeletionByRunAttempt(ctx context.Context, doer *user_model.User, repoID, runID, attemptID int64, name string) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repoID)}, func(ctx context.Context) error {
		if err := governance_service.CheckRepositoryContentWrite(ctx, doer, repoID, unit.TypeActions); err != nil {
			return err
		}
		if _, err := actions_model.GetRunByRepoAndID(ctx, repoID, runID); err != nil {
			return err
		}
		if attemptID > 0 {
			attempt, err := actions_model.GetRunAttemptByRepoAndID(ctx, repoID, attemptID)
			if err != nil {
				return err
			}
			if attempt.RunID != runID {
				return util.ErrNotExist
			}
		}
		changed, err := db.GetEngine(ctx).
			Where("repo_id=? AND run_id=? AND run_attempt_id=? AND artifact_name=? AND status=?", repoID, runID, attemptID, name, actions_model.ArtifactStatusUploadConfirmed).
			Cols("status").Update(&actions_model.ActionArtifact{Status: actions_model.ArtifactStatusPendingDeletion})
		if err != nil {
			return err
		}
		if changed == 0 {
			return util.ErrNotExist
		}
		return nil
	})
}
