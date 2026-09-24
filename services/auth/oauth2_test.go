// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/reqctx"
	"gitea.dev/services/actions"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createAuthorizedRunningTask(t *testing.T) int64 {
	t.Helper()
	ctx := t.Context()
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 1, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	run := &actions_model.ActionRun{RepoID: 1, OwnerID: 2, WorkflowID: "auth-test.yml", Index: 9914, TriggerUserID: 2, Status: actions_model.StatusRunning}
	require.NoError(t, db.Insert(ctx, run))
	job := &actions_model.ActionRunJob{RepoID: 1, OwnerID: 2, RunID: run.ID, JobID: "auth-test", Status: actions_model.StatusRunning}
	require.NoError(t, db.Insert(ctx, job))
	runner := &actions_model.ActionRunner{Name: "auth-test-runner"}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, runner))
	task := &actions_model.ActionTask{RepoID: 1, OwnerID: 2, JobID: job.ID, RunnerID: runner.ID, Status: actions_model.StatusRunning}
	task.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, task))
	job.TaskID = task.ID
	_, err := db.GetEngine(ctx).ID(job.ID).Cols("task_id").Update(job)
	require.NoError(t, err)
	return task.ID
}

func TestUserIDFromToken(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	t.Run("Actions JWT", func(t *testing.T) {
		runningTaskID := createAuthorizedRunningTask(t)
		token, err := actions.CreateAuthorizationToken(runningTaskID, 1, 2)
		assert.NoError(t, err)

		ds := make(reqctx.ContextData)

		o := OAuth2{}
		u, err := o.userFromToken(t.Context(), token, ds)
		require.NoError(t, err)
		assert.Equal(t, user_model.ActionsUserID, u.ID)
		taskID, ok := user_model.GetActionsUserTaskID(u)
		assert.True(t, ok)
		assert.Equal(t, runningTaskID, taskID)
	})
}

func TestCheckTaskIsRunning(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	runningTaskID := createAuthorizedRunningTask(t)

	cases := map[string]struct {
		TaskID   int64
		Expected bool
	}{
		"Running":          {TaskID: runningTaskID, Expected: true},
		"ChangedOwnership": {TaskID: 47, Expected: false},
		"Missing":          {TaskID: 1, Expected: false},
		"Cancelled":        {TaskID: 46, Expected: false},
	}

	for name := range cases {
		c := cases[name]
		t.Run(name, func(t *testing.T) {
			actual := CheckTaskIsRunning(t.Context(), c.TaskID)
			assert.Equal(t, c.Expected, actual)
		})
	}
}
