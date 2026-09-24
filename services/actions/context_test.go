// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"strconv"
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	actions_module "gitea.dev/modules/actions"
	"gitea.dev/modules/json"
	api "gitea.dev/modules/structs"
	webhook_module "gitea.dev/modules/webhook"

	act_model "gitea.com/gitea/runner/act/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluateRunConcurrency_RunIDFallback(t *testing.T) {
	// Unit-level check that EvaluateRunConcurrencyFillModel resolves github.run_id from run.ID.
	// The full-flow regression (run.ID non-zero by evaluation time) is TestPrepareRunAndInsert_ExpressionsSeeRunID.
	assert.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()

	runA := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: 791})
	runB := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: 792})

	attemptA := &actions_model.ActionRunAttempt{RepoID: runA.RepoID, RunID: runA.ID, Attempt: 1}
	attemptB := &actions_model.ActionRunAttempt{RepoID: runB.RepoID, RunID: runB.ID, Attempt: 1}

	expr := &act_model.RawConcurrency{
		Group:            "${{ github.workflow }}-${{ github.head_ref || github.run_id }}",
		CancelInProgress: "True",
	}

	assert.NoError(t, EvaluateRunConcurrencyFillModel(ctx, runA, attemptA, expr, nil, nil))
	assert.NoError(t, EvaluateRunConcurrencyFillModel(ctx, runB, attemptB, expr, nil, nil))

	assert.True(t, attemptA.ConcurrencyCancel)
	assert.Contains(t, attemptA.ConcurrencyGroup, "791")
	assert.Contains(t, attemptB.ConcurrencyGroup, "792")
	assert.NotEqual(t, attemptA.ConcurrencyGroup, attemptB.ConcurrencyGroup)
}

func TestPrepareRunAndInsert_ExpressionsSeeRunID(t *testing.T) {
	// Regression for the cross-branch concurrency leak: github.run_id must be available during both
	// jobparser.Parse (run-name) and concurrency evaluation; inserting run after either leaves run.ID at 0.
	assert.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()

	content := []byte(`name: cross-branch
run-name: "Run ${{ github.run_id }}"
on: push
concurrency:
  group: group-${{ github.run_id }}
  cancel-in-progress: true
jobs:
  hello:
    runs-on: ubuntu-latest
    steps:
      - run: echo hi
`)

	run := &actions_model.ActionRun{
		Title:             "before parse",
		RepoID:            4,
		OwnerID:           5,
		WorkflowID:        "expr-runid.yaml",
		TriggerUserID:     1,
		Ref:               "refs/heads/master",
		CommitSHA:         "c2d72f548424103f01ee1dc02889c1e2bff816b0",
		Event:             "push",
		TriggerEvent:      "push",
		EventPayload:      "{}",
		WorkflowRepoID:    4,
		WorkflowCommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0",
	}
	require.NoError(t, PrepareRunAndInsert(ctx, content, run, nil))
	require.Positive(t, run.ID)

	persisted := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: run.ID})
	runIDStr := strconv.FormatInt(run.ID, 10)
	assert.Equal(t, "Run "+runIDStr, persisted.Title)
	// ConcurrencyGroup lives on the latest attempt after migration v331.
	require.Positive(t, persisted.LatestAttemptID)
	attempt := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunAttempt{ID: persisted.LatestAttemptID})
	assert.Equal(t, "group-"+runIDStr, attempt.ConcurrencyGroup)
	// Rerun reads raw_concurrency from the DB to re-evaluate the group;
	// see services/actions/rerun.go. Must survive the insert.
	assert.NotEmpty(t, persisted.RawConcurrency)
}

func TestPrepareRunAndInsertOrganizationTriggerCannotQueue(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, actions_model.AddScopedWorkflowSource(ctx, 3, 3))
	require.NoError(t, actions_model.SetScopedWorkflowSourceConfigs(ctx, 3, 3, map[string]*actions_model.ScopedWorkflowConfig{
		"deploy-key.yaml": {Required: true},
	}))
	const commit = "c2d72f548424103f01ee1dc02889c1e2bff816b0"
	run := &actions_model.ActionRun{
		RepoID: 3, OwnerID: 3, WorkflowRepoID: 3, WorkflowID: "deploy-key.yaml",
		IsScopedRun:   true,
		TriggerUserID: 3, Ref: "refs/heads/master", CommitSHA: commit,
		WorkflowCommitSHA: commit, Event: "push", TriggerEvent: "push", EventPayload: "{}",
	}
	content := []byte("on: push\njobs:\n  check:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n")
	require.NoError(t, PrepareRunAndInsert(ctx, content, run, nil))
	stored := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: run.ID})
	assert.Equal(t, actions_model.StatusCancelled, stored.Status)
	attempt := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunAttempt{ID: stored.LatestAttemptID})
	assert.Equal(t, actions_model.StatusCancelled, attempt.Status)
	jobs, err := actions_model.GetRunJobsByRunAndAttemptID(ctx, run.ID, attempt.ID)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, actions_model.StatusCancelled, jobs[0].Status)
	assert.Zero(t, jobs[0].TaskID)

	repo, err := repo_model.GetRepositoryByID(ctx, run.RepoID)
	require.NoError(t, err)
	require.NoError(t, repo.LoadUnits(ctx))
	human, err := user_model.GetUserByID(ctx, 1)
	require.NoError(t, err)
	rerunAttempt, err := RerunWorkflowRunJobs(ctx, repo, stored, human, nil)
	require.NoError(t, err)
	assert.Equal(t, human.ID, rerunAttempt.TriggerUserID)
	assert.Equal(t, actions_model.StatusWaiting, rerunAttempt.Status)
	rerunJobs, err := actions_model.GetRunJobsByRunAndAttemptID(ctx, run.ID, rerunAttempt.ID)
	require.NoError(t, err)
	require.Len(t, rerunJobs, 1)
	assert.Equal(t, actions_model.StatusWaiting, rerunJobs[0].Status)

	humanRun := &actions_model.ActionRun{
		RepoID: 3, OwnerID: 3, WorkflowRepoID: 3, WorkflowID: "human.yaml",
		TriggerUserID: human.ID, Ref: "refs/heads/master", CommitSHA: commit,
		WorkflowCommitSHA: commit, Event: "push", TriggerEvent: "push", EventPayload: "{}",
	}
	require.NoError(t, PrepareRunAndInsert(ctx, content, humanRun, nil))
	assert.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: humanRun.ID}).Status)

	_, err = db.GetEngine(ctx).Table("branch").Where("repo_id = ? AND name = ?", repo.ID, repo.DefaultBranch).
		Cols("commit_id").Update(&struct{ CommitID string }{CommitID: commit})
	require.NoError(t, err)
	schedule := &actions_model.ActionSchedule{RepoID: 3, OwnerID: 3, ScopeRevision: repo.ActionsScopeRevision, TriggerUserID: user_model.ActionsUserID, WorkflowID: "schedule.yaml", Ref: "refs/heads/master", CommitSHA: commit}
	require.NoError(t, db.Insert(ctx, schedule))
	scheduledRun := &actions_model.ActionRun{
		RepoID: 3, OwnerID: 3, WorkflowRepoID: 3, WorkflowID: "schedule.yaml", ScheduleID: schedule.ID,
		TriggerUserID: user_model.ActionsUserID, Ref: "refs/heads/master", CommitSHA: commit,
		WorkflowCommitSHA: commit, Event: "schedule", TriggerEvent: "schedule", EventPayload: "{}",
	}
	require.NoError(t, PrepareRunAndInsert(ctx, content, scheduledRun, nil))
	assert.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: scheduledRun.ID}).Status)
}

func TestPrepareRunAndInsertRejectsChangedScopedSource(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	ctx := t.Context()
	require.NoError(t, actions_model.AddScopedWorkflowSource(ctx, 2, 1))
	_, err := db.GetEngine(ctx).ID(1).Incr("actions_scope_revision").Update(new(repo_model.Repository))
	require.NoError(t, err)
	run := &actions_model.ActionRun{
		RepoID: 2, OwnerID: 2, WorkflowRepoID: 1, IsScopedRun: true,
		WorkflowID: "source.yaml", WorkflowSourceScopeRevision: 0,
		TriggerUserID: 2, Ref: "refs/heads/master", Event: "push", TriggerEvent: "push",
		CommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0", EventPayload: "{}",
	}
	err = PrepareRunAndInsert(ctx, []byte("on: push\njobs:\n  check:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n"), run, nil)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	if run.ID != 0 {
		exists, err := db.ExistByID[actions_model.ActionRun](ctx, run.ID)
		require.NoError(t, err)
		require.False(t, exists)
	}
}

func TestComputeReusableCallerOutputs(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()

	var nextRunIndex int64 = 9001
	insertRun := func(t *testing.T, workflowID string) *actions_model.ActionRun {
		t.Helper()
		run := &actions_model.ActionRun{
			Title:         "reusable-out",
			RepoID:        4,
			Index:         nextRunIndex,
			OwnerID:       1,
			WorkflowID:    workflowID,
			TriggerUserID: 1,
			Ref:           "refs/heads/master",
			CommitSHA:     "c2d72f548424103f01ee1dc02889c1e2bff816b0",
			Event:         "push",
			TriggerEvent:  "push",
			EventPayload:  "{}",
			Status:        actions_model.StatusSuccess,
		}
		nextRunIndex++
		require.NoError(t, db.Insert(ctx, run))
		return run
	}

	insertCaller := func(t *testing.T, run *actions_model.ActionRun, jobID string, parentID int64, content, callPayload string) *actions_model.ActionRunJob {
		t.Helper()
		job := &actions_model.ActionRunJob{
			RunID:                   run.ID,
			RepoID:                  run.RepoID,
			OwnerID:                 run.OwnerID,
			CommitSHA:               run.CommitSHA,
			Name:                    jobID,
			JobID:                   jobID,
			Attempt:                 1,
			Status:                  actions_model.StatusSuccess,
			ParentJobID:             parentID,
			IsReusableCaller:        true,
			IsExpanded:              true,
			ReusableWorkflowContent: []byte(content),
			CallPayload:             callPayload,
		}
		require.NoError(t, db.Insert(ctx, job))
		return job
	}

	// Each call to insertChildJobAndTask with non-empty outputs allocates a fresh TaskID
	// so its action_task_output rows stay isolated per subtest.
	var nextTaskID int64 = 90001
	insertChildJobAndTask := func(t *testing.T, run *actions_model.ActionRun, jobID string, parentID int64, outputs map[string]string) *actions_model.ActionRunJob {
		t.Helper()
		var taskID int64
		if len(outputs) > 0 {
			taskID = nextTaskID
			nextTaskID++
		}
		job := &actions_model.ActionRunJob{
			RunID:       run.ID,
			RepoID:      run.RepoID,
			OwnerID:     run.OwnerID,
			CommitSHA:   run.CommitSHA,
			Name:        jobID,
			JobID:       jobID,
			Attempt:     1,
			Status:      actions_model.StatusSuccess,
			ParentJobID: parentID,
			TaskID:      taskID,
		}
		require.NoError(t, db.Insert(ctx, job))
		for k, v := range outputs {
			require.NoError(t, db.Insert(ctx, &actions_model.ActionTaskOutput{
				TaskID:      taskID,
				OutputKey:   k,
				OutputValue: v,
			}))
		}
		return job
	}

	// childrenByParentOfRun returns the run's jobs indexed by ParentJobID, the shape computeReusableCallerOutputs expects.
	childrenByParentOfRun := func(t *testing.T, runID int64) map[int64][]*actions_model.ActionRunJob {
		t.Helper()
		all, err := db.Find[actions_model.ActionRunJob](ctx, actions_model.FindRunJobOptions{RunID: runID})
		require.NoError(t, err)
		index := make(map[int64][]*actions_model.ActionRunJob)
		for _, j := range all {
			if j.ParentJobID != 0 {
				index[j.ParentJobID] = append(index[j.ParentJobID], j)
			}
		}
		return index
	}

	t.Run("returns empty when callee declares no outputs", func(t *testing.T) {
		run := insertRun(t, "no-outputs.yaml")
		caller := insertCaller(t, run, "caller", 0, `on:
  workflow_call:
    outputs: {}
`, "")
		out, err := computeReusableCallerOutputs(ctx, caller, childrenByParentOfRun(t, run.ID))
		require.NoError(t, err)
		assert.Empty(t, out)
	})

	t.Run("unexpanded (skipped) caller yields empty outputs without error", func(t *testing.T) {
		run := insertRun(t, "skipped-caller.yaml")
		// A reusable caller skipped before expansion: IsExpanded=false, empty ReusableWorkflowContent, no children.
		caller := &actions_model.ActionRunJob{
			RunID:            run.ID,
			RepoID:           run.RepoID,
			OwnerID:          run.OwnerID,
			CommitSHA:        run.CommitSHA,
			Name:             "caller",
			JobID:            "caller",
			Attempt:          1,
			Status:           actions_model.StatusSkipped,
			IsReusableCaller: true,
			IsExpanded:       false,
		}
		require.NoError(t, db.Insert(ctx, caller))
		out, err := computeReusableCallerOutputs(ctx, caller, childrenByParentOfRun(t, run.ID))
		require.NoError(t, err)
		assert.Empty(t, out)
	})

	t.Run("literal output value passes through", func(t *testing.T) {
		run := insertRun(t, "literal-out.yaml")
		caller := insertCaller(t, run, "caller", 0, `on:
  workflow_call:
    outputs:
      hello:
        value: world
`, "")
		out, err := computeReusableCallerOutputs(ctx, caller, childrenByParentOfRun(t, run.ID))
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"hello": "world"}, out)
	})

	t.Run("output expression reads child task outputs", func(t *testing.T) {
		run := insertRun(t, "child-out.yaml")
		caller := insertCaller(t, run, "caller", 0, `on:
  workflow_call:
    outputs:
      result:
        value: ${{ jobs.child.outputs.foo }}
`, "")
		insertChildJobAndTask(t, run, "child", caller.ID, map[string]string{"foo": "bar"})

		out, err := computeReusableCallerOutputs(ctx, caller, childrenByParentOfRun(t, run.ID))
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"result": "bar"}, out)
	})

	t.Run("CallPayload inputs reachable in output expression", func(t *testing.T) {
		run := insertRun(t, "payload-out.yaml")
		payload, err := json.Marshal(api.WorkflowCallPayload{
			Inputs: map[string]any{"env": "staging"},
		})
		require.NoError(t, err)
		caller := insertCaller(t, run, "caller", 0, `on:
  workflow_call:
    inputs:
      env:
        type: string
    outputs:
      env:
        value: ${{ inputs.env }}
`, string(payload))

		out, err := computeReusableCallerOutputs(ctx, caller, childrenByParentOfRun(t, run.ID))
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"env": "staging"}, out)
	})

	t.Run("nested caller outputs propagate to outer", func(t *testing.T) {
		run := insertRun(t, "nested-out.yaml")
		outer := insertCaller(t, run, "outer", 0, `on:
  workflow_call:
    outputs:
      bubbled:
        value: ${{ jobs.inner.outputs.up }}
`, "")
		inner := insertCaller(t, run, "inner", outer.ID, `on:
  workflow_call:
    outputs:
      up:
        value: ${{ jobs.leaf.outputs.foo }}
`, "")
		insertChildJobAndTask(t, run, "leaf", inner.ID, map[string]string{"foo": "bubble-value"})

		out, err := computeReusableCallerOutputs(ctx, outer, childrenByParentOfRun(t, run.ID))
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"bubbled": "bubble-value"}, out)
	})

	t.Run("matrix children with same JobID prefer non-empty values", func(t *testing.T) {
		run := insertRun(t, "matrix-out.yaml")
		caller := insertCaller(t, run, "caller", 0, `on:
  workflow_call:
    outputs:
      foo:
        value: ${{ jobs.matrix.outputs.foo }}
`, "")
		insertChildJobAndTask(t, run, "matrix", caller.ID, map[string]string{"foo": ""})
		insertChildJobAndTask(t, run, "matrix", caller.ID, map[string]string{"foo": "filled"})

		out, err := computeReusableCallerOutputs(ctx, caller, childrenByParentOfRun(t, run.ID))
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"foo": "filled"}, out)
	})
}

func TestFindTaskNeeds(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 51})
	job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: task.JobID})

	ret, err := FindTaskNeeds(t.Context(), job)
	assert.NoError(t, err)
	assert.Len(t, ret, 1)
	assert.Contains(t, ret, "job1")
	assert.Len(t, ret["job1"].Outputs, 2)
	assert.Equal(t, "abc", ret["job1"].Outputs["output_a"])
	assert.Equal(t, "bbb", ret["job1"].Outputs["output_b"])
}

func TestGenerateGiteaContextPullRequestTarget(t *testing.T) {
	payload := api.PullRequestPayload{
		PullRequest: &api.PullRequest{
			Base: &api.PRBranchInfo{
				Name: "owner:main",
				Ref:  "main",
				Sha:  "1234567890abcdef",
			},
			Head: &api.PRBranchInfo{
				Name: "fork:feature",
				Ref:  "feature",
				Sha:  "fedcba0987654321",
			},
		},
	}
	payloadBytes, err := json.Marshal(payload)
	assert.NoError(t, err)

	run := &actions_model.ActionRun{
		Event:        webhook_module.HookEventPullRequest,
		TriggerEvent: string(actions_module.GithubEventPullRequestTarget),
		EventPayload: string(payloadBytes),
		TriggerUser:  &user_model.User{Name: "test-user"},
		Repo:         &repo_model.Repository{Name: "test-repo", OwnerName: "test-owner"},
	}

	giteaCtx := GenerateGiteaContext(t.Context(), run, nil, nil)

	assert.Equal(t, "refs/heads/main", giteaCtx["ref"])
	assert.Equal(t, "main", giteaCtx["ref_name"])
}

// TestGenerateGiteaContext_NilAttempt verifies that, with no explicit attempt,
// use GetLatestAttempt to load the latest attempt and resolve attempt-related context variables.
func TestGenerateGiteaContext_NilAttempt(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	require.NoError(t, repo.LoadOwner(t.Context()))
	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})     // initiated the run
	triggerer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}) // initiated the latest attempt

	run := &actions_model.ActionRun{
		RepoID: repo.ID, Repo: repo, OwnerID: repo.OwnerID,
		TriggerUserID: actor.ID, TriggerUser: actor,
		WorkflowID: "test.yml", Index: 99600, Ref: "refs/heads/main",
		CommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0", TriggerEvent: "push",
		Status: actions_model.StatusRunning,
	}
	require.NoError(t, db.Insert(t.Context(), run))
	attempt := &actions_model.ActionRunAttempt{
		RepoID: repo.ID, RunID: run.ID, Attempt: 3, TriggerUserID: triggerer.ID, Status: actions_model.StatusRunning,
	}
	require.NoError(t, db.Insert(t.Context(), attempt))
	run.LatestAttemptID = attempt.ID
	job := &actions_model.ActionRunJob{
		RunID: run.ID, RunAttemptID: attempt.ID, AttemptJobID: 1, RepoID: repo.ID, OwnerID: repo.OwnerID,
		Name: "j", JobID: "j", Attempt: attempt.Attempt, Status: actions_model.StatusRunning,
	}
	require.NoError(t, db.Insert(t.Context(), job))

	// attempt == nil forces the fallback lookup via run.GetLatestAttempt.
	gitCtx := GenerateGiteaContext(t.Context(), run, nil, job)
	assert.Equal(t, actor.Name, gitCtx["actor"])
	assert.Equal(t, triggerer.Name, gitCtx["triggering_actor"])
	assert.Equal(t, "3", gitCtx["run_attempt"])
}

func TestGenerateGiteaContextProtectedRefUsesCurrentRules(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	require.NoError(t, repo.LoadOwner(ctx))
	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	run := &actions_model.ActionRun{
		RepoID: repo.ID, Repo: repo, OwnerID: repo.OwnerID, TriggerUser: actor,
		Ref: "refs/heads/protected-demo", CommitSHA: "synthetic-commit", TriggerEvent: actions_module.GithubEventPush,
	}
	assert.Equal(t, false, GenerateGiteaContext(ctx, run, nil, nil)["ref_protected"])
	rule := &git_model.ProtectedBranch{RepoID: repo.ID, RuleName: "protected-demo"}
	require.NoError(t, db.Insert(ctx, rule))
	require.NoError(t, db.Insert(ctx, &git_model.Branch{RepoID: repo.ID, Name: "protected-demo", CommitID: "synthetic-commit"}))
	protected, err := git_model.IsBranchProtected(ctx, repo.ID, "protected-demo")
	require.NoError(t, err)
	require.True(t, protected)
	assert.Equal(t, true, GenerateGiteaContext(ctx, run, nil, nil)["ref_protected"])
	run.TriggerEvent = actions_module.GithubEventPullRequestTarget
	assert.Equal(t, false, GenerateGiteaContext(ctx, run, nil, nil)["ref_protected"])
}
