// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	actions_module "gitea.dev/modules/actions"
	"gitea.dev/modules/container"
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/storage"
	"gitea.dev/modules/timeutil"
	governance_service "gitea.dev/services/governance"

	"xorm.io/builder"
)

// Cleanup removes expired actions logs, data, artifacts and used ephemeral runners
func Cleanup(ctx context.Context) error {
	// clean up expired artifacts
	if err := CleanupArtifacts(ctx); err != nil {
		return fmt.Errorf("cleanup artifacts: %w", err)
	}

	// clean up old logs
	if err := CleanupExpiredLogs(ctx); err != nil {
		return fmt.Errorf("cleanup logs: %w", err)
	}

	// clean up old ephemeral runners
	if err := CleanupEphemeralRunners(ctx); err != nil {
		return fmt.Errorf("cleanup old ephemeral runners: %w", err)
	}

	return nil
}

// CleanupArtifacts removes expired add need-deleted artifacts and set records expired status
func CleanupArtifacts(taskCtx context.Context) error {
	if err := cleanExpiredArtifacts(taskCtx); err != nil {
		return err
	}
	return cleanNeedDeleteArtifacts(taskCtx)
}

func cleanExpiredArtifacts(taskCtx context.Context) error {
	artifacts, err := actions_model.ListNeedExpiredArtifacts(taskCtx)
	if err != nil {
		return err
	}
	log.Info("Found %d expired artifacts", len(artifacts))
	for _, artifact := range artifacts {
		if err := actions_model.SetArtifactExpired(taskCtx, artifact.ID); err != nil {
			log.Error("Cannot set artifact %d expired: %v", artifact.ID, err)
			continue
		}
		if err := storage.ActionsArtifacts.Delete(artifact.StoragePath); err != nil {
			log.Error("Cannot delete artifact %d: %v", artifact.ID, err)
			// go on
		}
		log.Info("Artifact %d is deleted (due to expiration)", artifact.ID)
	}
	return nil
}

// deleteArtifactBatchSize is the batch size of deleting artifacts
const deleteArtifactBatchSize = 100

func cleanNeedDeleteArtifacts(taskCtx context.Context) error {
	for {
		artifacts, err := actions_model.ListPendingDeleteArtifacts(taskCtx, deleteArtifactBatchSize)
		if err != nil {
			return err
		}
		log.Info("Found %d artifacts pending deletion", len(artifacts))
		for _, artifact := range artifacts {
			if err := actions_model.SetArtifactDeleted(taskCtx, artifact.ID); err != nil {
				log.Error("Cannot set artifact %d deleted: %v", artifact.ID, err)
				continue
			}
			if err := storage.ActionsArtifacts.Delete(artifact.StoragePath); err != nil {
				log.Error("Cannot delete artifact %d: %v", artifact.ID, err)
				// go on
			}
			log.Info("Artifact %d is deleted (due to pending deletion)", artifact.ID)
		}
		if len(artifacts) < deleteArtifactBatchSize {
			log.Debug("No more artifacts pending deletion")
			break
		}
	}
	return nil
}

const deleteLogBatchSize = 100

func removeTaskLog(ctx context.Context, task *actions_model.ActionTask) {
	if err := actions_module.RemoveLogs(ctx, task.LogInStorage, task.LogFilename); err != nil {
		log.Error("Failed to remove log %s (in storage %v) of task %v: %v", task.LogFilename, task.LogInStorage, task.ID, err)
		// do not return error here, go on
	}
}

// CleanupExpiredLogs removes logs which are older than the configured retention time
func CleanupExpiredLogs(ctx context.Context) error {
	olderThan := timeutil.TimeStampNow().AddDuration(-time.Duration(setting.Actions.LogRetentionDays) * 24 * time.Hour)

	count := 0
	for {
		tasks, err := actions_model.FindOldTasksToExpire(ctx, olderThan, deleteLogBatchSize)
		if err != nil {
			return fmt.Errorf("find old tasks: %w", err)
		}
		for _, task := range tasks {
			removeTaskLog(ctx, task)
			task.LogIndexes = nil // clear log indexes since it's a heavy field
			task.LogExpired = true
			if err := actions_model.UpdateTask(ctx, task, "log_indexes", "log_expired"); err != nil {
				log.Error("Failed to update task %v: %v", task.ID, err)
				// do not return error here, continue to next task
				continue
			}
			count++
			log.Trace("Removed log %s of task %v", task.LogFilename, task.ID)
		}
		if len(tasks) < deleteLogBatchSize {
			break
		}
	}

	log.Info("Removed %d logs", count)
	return nil
}

// CleanupEphemeralRunners removes used ephemeral runners which are no longer able to process jobs
func CleanupEphemeralRunners(ctx context.Context) error {
	subQuery := builder.Select("`action_runner`.id").
		From(builder.Select("*").From("`action_runner`"), "`action_runner`"). // mysql needs this redundant subquery
		Join("INNER", "`action_task`", "`action_task`.`runner_id` = `action_runner`.`id`").
		Where(builder.Eq{"`action_runner`.`ephemeral`": true}).
		And(builder.NotIn("`action_task`.`status`", actions_model.StatusWaiting, actions_model.StatusRunning, actions_model.StatusBlocked, actions_model.StatusCancelling))
	b := builder.Delete(builder.In("id", subQuery)).From("`action_runner`")
	res, err := db.GetEngine(ctx).Exec(b)
	if err != nil {
		return fmt.Errorf("find runners: %w", err)
	}
	affected, _ := res.RowsAffected()
	log.Info("Removed %d runners", affected)
	return nil
}

// CleanupEphemeralRunnersByPickedTaskOfRepo removes all ephemeral runners that have active/finished tasks on the given repository
func CleanupEphemeralRunnersByPickedTaskOfRepo(ctx context.Context, repoID int64) error {
	subQuery := builder.Select("`action_runner`.id").
		From(builder.Select("*").From("`action_runner`"), "`action_runner`"). // mysql needs this redundant subquery
		Join("INNER", "`action_task`", "`action_task`.`runner_id` = `action_runner`.`id`").
		Where(builder.And(builder.Eq{"`action_runner`.`ephemeral`": true}, builder.Eq{"`action_task`.`repo_id`": repoID}))
	b := builder.Delete(builder.In("id", subQuery)).From("`action_runner`")
	res, err := db.GetEngine(ctx).Exec(b)
	if err != nil {
		return fmt.Errorf("find runners: %w", err)
	}
	affected, _ := res.RowsAffected()
	log.Info("Removed %d runners", affected)
	return nil
}

// DeleteRun deletes a user's completed workflow run, including its logs and artifacts.
func DeleteRun(ctx context.Context, run *actions_model.ActionRun, doer *user_model.User) error {
	if run == nil {
		return governance_model.ErrNotFound
	}
	repoID, runID := run.RepoID, run.ID
	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repoID)}, func(ctx context.Context) error {
		if err := governance_service.CheckRepositoryContentWrite(ctx, doer, repoID, unit.TypeActions); err != nil {
			return err
		}
		run, err := actions_model.GetRunByRepoAndID(ctx, repoID, runID)
		if err != nil {
			return err
		}
		if !run.Status.IsDone() {
			return governance_model.ErrConflict
		}
		jobs, err := actions_model.GetAllRunJobsByRepoAndRunID(ctx, repoID, runID)
		if err != nil {
			return err
		}
		jobIDs := container.FilterSlice(jobs, func(j *actions_model.ActionRunJob) (int64, bool) {
			return j.ID, true
		})
		tasks := make(actions_model.TaskList, 0)
		if len(jobIDs) > 0 {
			if err := db.GetEngine(ctx).Where("repo_id = ?", repoID).In("job_id", jobIDs).Find(&tasks); err != nil {
				return err
			}
		}
		artifacts, err := db.Find[actions_model.ActionArtifact](ctx, actions_model.FindArtifactsOptions{RepoID: repoID, RunID: runID})
		if err != nil {
			return err
		}
		cleanup := &governance_model.ResourceCleanup{Kind: "actions_run", ResourceID: runID, Actor: governance_model.AuditActor(ctx)}
		for _, task := range tasks {
			if task.LogFilename != "" {
				cleanup.Objects = append(cleanup.Objects, governance_model.CleanupObject{Kind: "action_log", Path: task.LogFilename, InStorage: task.LogInStorage})
			}
		}
		for _, artifact := range artifacts {
			if artifact.StoragePath != "" {
				cleanup.Objects = append(cleanup.Objects, governance_model.CleanupObject{Kind: "artifact", Path: artifact.StoragePath})
			}
		}
		recordsToDelete := []any{
			&actions_model.ActionRun{RepoID: repoID, ID: runID},
			&actions_model.ActionRunAttempt{RepoID: repoID, RunID: runID},
			&actions_model.ActionRunJob{RepoID: repoID, RunID: runID},
		}
		for _, task := range tasks {
			recordsToDelete = append(recordsToDelete,
				&actions_model.ActionTask{RepoID: repoID, ID: task.ID},
				&actions_model.ActionTaskStep{RepoID: repoID, TaskID: task.ID},
				&actions_model.ActionTaskOutput{TaskID: task.ID},
			)
		}
		recordsToDelete = append(recordsToDelete,
			&actions_model.ActionArtifact{RepoID: repoID, RunID: runID},
			&actions_model.ActionRunJobSummary{RepoID: repoID, RunID: runID},
		)
		repo, err := repo_model.GetRepositoryByID(ctx, repoID)
		if err != nil {
			return err
		}
		chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
		if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
			return err
		}
		cleanup.ScopeType, cleanup.ScopeID = "repository", repo.ID
		cleanup.ObjectPath = fmt.Sprintf("%s/actions/run-%d", repo.FullPath(), runID)
		for _, ancestor := range chain {
			if ancestor.Kind == "group" {
				cleanup.AncestorIDs = append(cleanup.AncestorIDs, ancestor.ID)
			}
		}
		// TODO: Deleting task records could break current ephemeral runner implementation. This is a temporary workaround suggested by ChristopherHX.
		// Since you delete potentially the only task an ephemeral act_runner has ever run, please delete the affected runners first.
		// one of
		//    call cleanup ephemeral runners first
		//    delete affected ephemeral act_runners
		//    I would make ephemeral runners fully delete directly before formally finishing the task
		//
		// See also: https://github.com/go-gitea/gitea/pull/34337#issuecomment-2862222788
		if err := CleanupEphemeralRunners(ctx); err != nil {
			return err
		}
		if err := actions_model.AppendRunAudit(ctx, run, "actions.run_deleted"); err != nil {
			return err
		}
		if err := db.Insert(ctx, cleanup); err != nil {
			return err
		}
		details, err := json.Marshal(map[string]any{"cleanup_id": cleanup.ID, "storage_cleanup": "pending"})
		if err != nil {
			return err
		}
		if err := governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "resource.deletion_committed", Actor: cleanup.Actor, ScopeType: cleanup.ScopeType, ScopeID: cleanup.ScopeID, AncestorIDs: cleanup.AncestorIDs, ObjectType: cleanup.Kind, ObjectID: cleanup.ResourceID, ObjectPath: cleanup.ObjectPath, Result: "pending", Details: details}); err != nil {
			return err
		}
		return db.DeleteBeans(ctx, recordsToDelete...)
	}); err != nil {
		return err
	}

	actions_model.UpdateRepoRunsNumbers(ctx, repoID)

	return nil
}
