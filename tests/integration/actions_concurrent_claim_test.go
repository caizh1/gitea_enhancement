// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm/contexts"
)

type actionsCandidateScanHook struct {
	once    sync.Once
	scanned chan struct{}
}

func (*actionsCandidateScanHook) BeforeProcess(c *contexts.ContextHook) (context.Context, error) {
	return c.Ctx, nil
}

func (h *actionsCandidateScanHook) AfterProcess(c *contexts.ContextHook) error {
	if strings.HasPrefix(strings.ToUpper(c.SQL), "SELECT") && strings.Contains(c.SQL, "action_run_job") {
		h.once.Do(func() { close(h.scanned) })
	}
	return nil
}

// minimalWorkflowPayload returns the minimal YAML for a single-job workflow with no steps.
func minimalConcurrentWorkflowPayload(jobID string) []byte {
	return []byte("on: push\njobs:\n  " + jobID + ":\n    runs-on: ubuntu-latest\n")
}

// TestCreateTaskForRunnerConcurrentClaim verifies that when multiple runners
// poll simultaneously and all initially see the same first waiting job,
// each runner claims a distinct job rather than all but one being left
// empty-handed. This is the regression test for the race condition where
// runners losing the optimistic-lock on job #1 would receive latestVersion
// and never retry the remaining 49+ jobs.
//
// It lives in tests/integration rather than a unit test because SQLite
// serializes write transactions, so the contended optimistic-lock path this
// guards only runs concurrently against MySQL/PostgreSQL in CI.
func TestCreateTaskForRunnerConcurrentClaim(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	require.NoError(t, db.Insert(t.Context(), &repo_model.RepoUnit{RepoID: 1, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))

	const numJobs = 3

	run := &actions_model.ActionRun{
		Title:         "concurrent-claim-test-run",
		RepoID:        1,
		OwnerID:       2,
		WorkflowID:    "test.yaml",
		Index:         9901,
		TriggerUserID: 2,
		Ref:           "refs/heads/main",
		CommitSHA:     "c2d72f548424103f01ee1dc02889c1e2bff816b0",
		Event:         "push",
		TriggerEvent:  "push",
		Status:        actions_model.StatusWaiting,
	}
	require.NoError(t, db.Insert(t.Context(), run))

	jobs := make([]*actions_model.ActionRunJob, numJobs)
	for i := range numJobs {
		jobID := "concurrent-job-" + string(rune('a'+i))
		jobs[i] = &actions_model.ActionRunJob{
			RunID:           run.ID,
			RepoID:          run.RepoID,
			OwnerID:         run.OwnerID,
			CommitSHA:       run.CommitSHA,
			Name:            jobID,
			Attempt:         1,
			JobID:           jobID,
			Status:          actions_model.StatusWaiting,
			RunsOn:          []string{"ubuntu-latest"},
			WorkflowPayload: minimalConcurrentWorkflowPayload(jobID),
		}
		require.NoError(t, db.Insert(t.Context(), jobs[i]))
	}

	runners := make([]*actions_model.ActionRunner, numJobs)
	for i := range numJobs {
		r := &actions_model.ActionRunner{
			UUID:        "concurrent-runner-uuid-" + string(rune('a'+i)),
			Name:        "concurrent-runner-" + string(rune('a'+i)),
			AgentLabels: []string{"ubuntu-latest"},
		}
		r.GenerateAndFillToken()
		runners[i] = r
		require.NoError(t, db.Insert(t.Context(), runners[i]))
	}

	// Simulate the burst: all runners call CreateTaskForRunner concurrently,
	// as happens when all see the same stale tasksVersion simultaneously.
	type result struct {
		task *actions_model.ActionTask
		ok   bool
		err  error
	}
	results := make([]result, numJobs)
	var wg sync.WaitGroup
	for i := range numJobs {
		wg.Go(func() {
			task, ok, err := actions_model.CreateTaskForRunner(t.Context(), runners[i])
			results[i] = result{task, ok, err}
		})
	}
	wg.Wait()

	// Every runner must have received a task without error.
	claimedJobIDs := make(map[int64]bool)
	for i, r := range results {
		require.NoError(t, r.err, "runner %d got an unexpected error", i)
		require.True(t, r.ok, "runner %d did not get a task even though free jobs exist", i)
		require.NotNil(t, r.task)
		assert.False(t, claimedJobIDs[r.task.JobID], "job %d was claimed by more than one runner", r.task.JobID)
		claimedJobIDs[r.task.JobID] = true
	}
	assert.Len(t, claimedJobIDs, numJobs, "expected %d distinct jobs to be claimed", numJobs)

	// All jobs must now be running with a task assigned.
	for _, j := range jobs {
		updated := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: j.ID})
		assert.Equal(t, actions_model.StatusRunning, updated.Status)
		assert.NotZero(t, updated.TaskID)
	}
}

func TestCreateTaskForRunnerRejectsOwnerChangeAfterCandidateScan(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	require.NoError(t, db.Insert(t.Context(), &repo_model.RepoUnit{RepoID: 1, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	run := &actions_model.ActionRun{RepoID: 1, OwnerID: 2, WorkflowID: "test.yaml", Index: 9902, TriggerUserID: 2, Status: actions_model.StatusWaiting}
	require.NoError(t, db.Insert(t.Context(), run))
	job := &actions_model.ActionRunJob{
		RunID: run.ID, RepoID: 1, OwnerID: 2, JobID: "transfer-race", Attempt: 1, Status: actions_model.StatusWaiting,
		RunsOn: []string{"ubuntu-latest"}, WorkflowPayload: minimalConcurrentWorkflowPayload("transfer-race"),
	}
	require.NoError(t, db.Insert(t.Context(), job))
	runner := &actions_model.ActionRunner{UUID: "phase1-transfer-race-runner", Name: "transfer-race-runner", OwnerID: 2, AgentLabels: []string{"ubuntu-latest"}}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(t.Context(), runner))

	hook := &actionsCandidateScanHook{scanned: make(chan struct{})}
	unittest.GetXORMEngine().AddHook(hook)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	writerReady := make(chan struct{})
	releaseWriter := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
			_, err := db.GetEngine(ctx).ID(1).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 5})
			if err != nil {
				return err
			}
			close(writerReady)
			select {
			case <-releaseWriter:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-writerReady:
	case err := <-writerDone:
		require.NoError(t, err)
		return
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	type result struct {
		task *actions_model.ActionTask
		ok   bool
		err  error
	}
	claimDone := make(chan result, 1)
	go func() {
		task, ok, err := actions_model.CreateTaskForRunner(ctx, runner)
		claimDone <- result{task, ok, err}
	}()
	select {
	case <-hook.scanned:
	case r := <-claimDone:
		require.NoError(t, r.err)
		require.FailNow(t, "claim returned before scanning a candidate")
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
	close(releaseWriter)
	require.NoError(t, <-writerDone)
	r := <-claimDone
	require.NoError(t, r.err)
	assert.False(t, r.ok)
	assert.Nil(t, r.task)
	assert.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID}).Status)
}
