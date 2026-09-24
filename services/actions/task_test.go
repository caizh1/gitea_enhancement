// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"strconv"
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	perm_model "gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	secret_model "gitea.dev/models/secret"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTryPickTaskThrottled(t *testing.T) {
	sem := taskPickLimiter()

	// Saturate every assignment slot so the next attempt must be throttled.
	for range cap(sem) {
		sem <- struct{}{}
	}
	defer func() {
		for range cap(sem) {
			<-sem
		}
	}()

	// No DB access happens on the throttled path, so this is safe without fixtures.
	task, ok, throttled, err := TryPickTask(t.Context(), &actions_model.ActionRunner{})
	require.NoError(t, err)
	assert.Nil(t, task)
	assert.False(t, ok)
	assert.True(t, throttled)
}

// TestReleaseTaskForRunnerCleanup verifies the cleanup used by PickTask releases a claimed task through a
// fresh context. PickTask reaches this path when the request context is already canceled, and on
// PostgreSQL/MySQL a DB transaction on a canceled context fails immediately; reusing it would strand the
// claimed job in running state, so the cleanup must not use the request context.
func TestReleaseTaskForRunnerCleanup(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	require.NoError(t, db.Insert(t.Context(), &repo_model.RepoUnit{RepoID: 1, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))

	run := &actions_model.ActionRun{
		Title: "cleanup-run", RepoID: 1, OwnerID: 2, WorkflowID: "test.yaml",
		TriggerUserID: 2, Ref: "refs/heads/main",
		CommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0", Event: "push", TriggerEvent: "push",
		Status: actions_model.StatusWaiting,
	}
	require.NoError(t, db.Insert(t.Context(), run))
	job := &actions_model.ActionRunJob{
		RunID: run.ID, RepoID: run.RepoID, OwnerID: run.OwnerID, CommitSHA: run.CommitSHA,
		Name: "cleanup-job", Attempt: 1, JobID: "cleanup-job", Status: actions_model.StatusWaiting,
		RunsOn:          []string{"ubuntu-latest"},
		WorkflowPayload: []byte("on: push\njobs:\n  cleanup-job:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"),
	}
	require.NoError(t, db.Insert(t.Context(), job))
	runner := &actions_model.ActionRunner{Name: "cleanup-runner", AgentLabels: []string{"ubuntu-latest"}}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(t.Context(), runner))

	task, ok, err := actions_model.CreateTaskForRunner(t.Context(), runner)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, actions_model.StatusRunning, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID}).Status)

	// the cleanup helper uses its own context, so the claimed job is returned to the waiting queue
	releaseTaskForRunnerCleanup(task)
	released := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID})
	assert.Equal(t, actions_model.StatusWaiting, released.Status)
	assert.Zero(t, released.TaskID)
	unittest.AssertNotExistsBean(t, &actions_model.ActionTask{ID: task.ID})
}

func TestPickTaskDoesNotDiscloseNewOwnerSecretsToOldRun(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	ctx := t.Context()
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 1, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	_, err := secret_model.InsertEncryptedSecret(ctx, 5, 0, "TARGET_SECRET", "destination-value", "")
	require.NoError(t, err)
	run := &actions_model.ActionRun{
		Title: "source-owner-run", RepoID: 1, OwnerID: 2, WorkflowID: "test.yaml", Index: 9913,
		TriggerUserID: 2, Ref: "refs/heads/master", CommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0", Event: "push", TriggerEvent: "push", Status: actions_model.StatusWaiting,
	}
	require.NoError(t, db.Insert(ctx, run))
	job := &actions_model.ActionRunJob{
		RunID: run.ID, RepoID: 1, OwnerID: 2, CommitSHA: run.CommitSHA,
		Name: "source-owner-job", Attempt: 1, JobID: "source-owner-job", Status: actions_model.StatusWaiting,
		RunsOn: []string{"ubuntu-latest"}, WorkflowPayload: []byte("on: push\njobs:\n  source-owner-job:\n    runs-on: ubuntu-latest\n"),
	}
	require.NoError(t, db.Insert(ctx, job))
	runner := &actions_model.ActionRunner{Name: "global-test-runner", AgentLabels: []string{"ubuntu-latest"}}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, runner))
	_, err = db.GetEngine(ctx).ID(1).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 5})
	require.NoError(t, err)

	payload, ok, err := PickTask(ctx, runner)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Nil(t, payload)
	assert.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID}).Status)
}

func TestTaskCredentialRejectsRevokedPrivateSourceRead(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	ctx := t.Context()
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 2, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	grant := &access_model.Access{RepoID: 2, UserID: 4, Mode: perm_model.AccessModeRead}
	require.NoError(t, db.Insert(ctx, grant))
	run := &actions_model.ActionRun{RepoID: 2, OwnerID: 2, WorkflowID: "test.yaml", Index: 9914, TriggerUserID: 4, Status: actions_model.StatusRunning}
	require.NoError(t, db.Insert(ctx, run))
	job := &actions_model.ActionRunJob{RepoID: 2, OwnerID: 2, RunID: run.ID, JobID: "private-source", Status: actions_model.StatusRunning}
	require.NoError(t, db.Insert(ctx, job))
	runner := &actions_model.ActionRunner{UUID: "phase1-private-source-runner", Name: "private-source-runner"}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, runner))
	task := &actions_model.ActionTask{RepoID: 2, OwnerID: 2, JobID: job.ID, RunnerID: runner.ID, Status: actions_model.StatusRunning}
	task.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, task))
	job.TaskID = task.ID
	_, err := db.GetEngine(ctx).ID(job.ID).Cols("task_id").Update(job)
	require.NoError(t, err)

	valid, err := TaskCredentialValid(ctx, task)
	require.NoError(t, err)
	require.True(t, valid)
	_, err = db.GetEngine(ctx).ID(grant.ID).Delete(new(access_model.Access))
	require.NoError(t, err)
	valid, err = TaskCredentialValid(ctx, task)
	require.NoError(t, err)
	assert.False(t, valid)
}

func TestRerunSourceReadUsesCurrentAttemptTriggerer(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	ctx := t.Context()
	run := &actions_model.ActionRun{RepoID: 2, OwnerID: 2, TriggerUserID: 4, Status: actions_model.StatusRunning}
	require.NoError(t, db.Insert(ctx, run))
	attempt := &actions_model.ActionRunAttempt{RepoID: 2, RunID: run.ID, Attempt: 2, TriggerUserID: 2, Status: actions_model.StatusRunning}
	require.NoError(t, db.Insert(ctx, attempt))
	job := &actions_model.ActionRunJob{RepoID: 2, OwnerID: 2, RunID: run.ID, RunAttemptID: attempt.ID, JobID: "rerun", Status: actions_model.StatusRunning}
	require.NoError(t, db.Insert(ctx, job))
	task := &actions_model.ActionTask{RepoID: 2, OwnerID: 2, JobID: job.ID, Status: actions_model.StatusRunning}
	canRead, err := access_model.TaskTriggererCanReadSource(ctx, task)
	require.NoError(t, err)
	require.True(t, canRead)
	_, err = db.GetEngine(ctx).ID(attempt.ID).Cols("trigger_user_id").Update(&actions_model.ActionRunAttempt{TriggerUserID: 4})
	require.NoError(t, err)
	canRead, err = access_model.TaskTriggererCanReadSource(ctx, task)
	require.NoError(t, err)
	require.False(t, canRead)
}

func TestScopedTaskCredentialRejectsArchivedWorkflowSource(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	ctx := t.Context()
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 2, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	require.NoError(t, actions_model.AddScopedWorkflowSource(ctx, 2, 1))
	run := &actions_model.ActionRun{RepoID: 2, OwnerID: 2, WorkflowRepoID: 1, IsScopedRun: true, TriggerUserID: 2, Status: actions_model.StatusRunning}
	require.NoError(t, db.Insert(ctx, run))
	job := &actions_model.ActionRunJob{RepoID: 2, OwnerID: 2, RunID: run.ID, JobID: "scoped", Status: actions_model.StatusRunning}
	require.NoError(t, db.Insert(ctx, job))
	runner := &actions_model.ActionRunner{UUID: "archived-source-runner", Name: "archived-source-runner"}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, runner))
	task := &actions_model.ActionTask{RepoID: 2, OwnerID: 2, JobID: job.ID, RunnerID: runner.ID, Status: actions_model.StatusRunning}
	task.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, task))
	job.TaskID = task.ID
	_, err := db.GetEngine(ctx).ID(job.ID).Cols("task_id").Update(job)
	require.NoError(t, err)
	valid, err := TaskCredentialValid(ctx, task)
	require.NoError(t, err)
	require.False(t, valid)
	registration := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScopedWorkflowSource{OwnerID: 2, SourceRepoID: 1})
	run.ScopedConfigRevisions = map[string]int64{strconv.FormatInt(registration.ID, 10): registration.ConfigRevision}
	run.WorkflowSourceScopeRevision = registration.SourceScopeRevision
	_, err = db.GetEngine(ctx).ID(run.ID).Cols("scoped_config_revisions", "workflow_source_scope_revision").Update(run)
	require.NoError(t, err)
	valid, err = TaskCredentialValid(ctx, task)
	require.NoError(t, err)
	require.True(t, valid)
	_, err = db.GetEngine(ctx).ID(1).Cols("is_archived").Update(&repo_model.Repository{IsArchived: true})
	require.NoError(t, err)
	valid, err = TaskCredentialValid(ctx, task)
	require.NoError(t, err)
	require.False(t, valid)
	_, err = db.GetEngine(ctx).ID(1).Cols("is_archived").Update(&repo_model.Repository{IsArchived: false})
	require.NoError(t, err)
	valid, err = TaskCredentialValid(ctx, task)
	require.NoError(t, err)
	require.True(t, valid)
	_, err = db.GetEngine(ctx).ID(1).Incr("actions_scope_revision").Update(new(repo_model.Repository))
	require.NoError(t, err)
	valid, err = TaskCredentialValid(ctx, task)
	require.NoError(t, err)
	require.False(t, valid)
}

func TestCreatedGroupWithInactiveUserFlagCanClaimActions(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	ctx := t.Context()
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "phase1-actions", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	owner, err := user_model.GetUserByID(ctx, group.ID)
	require.NoError(t, err)
	require.False(t, owner.IsActive)
	repo := &repo_model.Repository{OwnerID: owner.ID, OwnerName: owner.Name, Name: "actions", LowerName: "actions", Status: repo_model.RepositoryReady}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	run := &actions_model.ActionRun{RepoID: repo.ID, OwnerID: owner.ID, WorkflowID: "test.yaml", Index: 9915, TriggerUserID: 2, Status: actions_model.StatusWaiting}
	require.NoError(t, db.Insert(ctx, run))
	job := &actions_model.ActionRunJob{
		RepoID: repo.ID, OwnerID: owner.ID, RunID: run.ID, JobID: "created-group", Attempt: 1, Status: actions_model.StatusWaiting,
		RunsOn: []string{"ubuntu-latest"}, WorkflowPayload: []byte("on: push\njobs:\n  created-group:\n    runs-on: ubuntu-latest\n"),
	}
	require.NoError(t, db.Insert(ctx, job))
	runner := &actions_model.ActionRunner{UUID: "phase1-group-runner", Name: "phase1-group-runner", AgentLabels: []string{"ubuntu-latest"}}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, runner))
	task, ok, err := actions_model.CreateTaskForRunner(ctx, runner)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, owner.ID, task.OwnerID)
}
