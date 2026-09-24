// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// 旧制品 fixture 的 owner 和 Runner 不完整；正例显式补齐当前合法任务，不放宽生产鉴权。
func prepareAuthorizedFixtureTask(t *testing.T, taskID int64) *actions_model.ActionTask {
	t.Helper()
	ctx := t.Context()
	task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: taskID})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: task.RepoID})
	job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: task.JobID})
	runner := &actions_model.ActionRunner{UUID: uuid.NewString(), Name: "artifact-authorization", RepoID: repo.ID}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, runner))
	task.OwnerID, task.RunnerID, task.Status = repo.OwnerID, runner.ID, actions_model.StatusRunning
	require.NoError(t, actions_model.UpdateTask(ctx, task, "owner_id", "runner_id", "status"))
	_, err := db.GetEngine(ctx).ID(job.ID).Cols("owner_id", "task_id", "status").Update(&actions_model.ActionRunJob{
		OwnerID: repo.OwnerID, TaskID: task.ID, Status: actions_model.StatusRunning,
	})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(job.RunID).Cols("owner_id", "trigger_user_id", "status").Update(&actions_model.ActionRun{
		OwnerID: repo.OwnerID, TriggerUserID: repo.OwnerID, Status: actions_model.StatusRunning,
	})
	require.NoError(t, err)
	return task
}
