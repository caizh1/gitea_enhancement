// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pull

import (
	"context"
	"strconv"
	"strings"
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/commitstatus"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/glob"

	"github.com/stretchr/testify/require"
)

func TestRequiredScopedSourceReferenceSnapshot(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	consumer := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	sourceRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	require.NoError(t, actions_model.AddScopedWorkflowSource(ctx, 0, sourceRepo.ID))
	require.NoError(t, actions_model.SetScopedWorkflowSourceConfigs(ctx, 0, sourceRepo.ID, map[string]*actions_model.ScopedWorkflowConfig{"ci.yaml": {Required: true, Patterns: []string{"* / build (*)"}}}))
	_, err := captureRequiredScopedSourceSnapshot(ctx, consumer)
	require.ErrorIs(t, err, ErrNotReadyToMerge, "legacy required sources without a verified hook fail closed")
	require.NoError(t, gitrepo.InstallReferenceTransactionHook(ctx, sourceRepo))
	source, err := actions_model.GetScopedWorkflowSource(ctx, 0, sourceRepo.ID)
	require.NoError(t, err)
	snapshot, err := captureRequiredScopedSourceSnapshot(ctx, consumer)
	require.NoError(t, err)
	require.NotEmpty(t, snapshot.sources[source.ID].gitSHA, "source SHA comes from the real Git ref")

	head := strings.Repeat("a", 40)
	runner := &actions_model.ActionRunner{UUID: "trusted-source-runner", Name: "trusted-source-runner"}
	require.NoError(t, db.Insert(ctx, runner))
	insert := func(sourceSHA string) (*actions_model.ActionRun, *actions_model.ActionRunAttempt, *actions_model.ActionRunJob) {
		run := &actions_model.ActionRun{RepoID: consumer.ID, OwnerID: consumer.OwnerID, WorkflowID: "ci.yaml", Index: int64(900 + len(sourceSHA)), CommitSHA: head, WorkflowRepoID: sourceRepo.ID, WorkflowCommitSHA: sourceSHA, IsScopedRun: true, WorkflowSourceScopeRevision: source.SourceScopeRevision, ScopedConfigRevisions: map[string]int64{strconv.FormatInt(source.ID, 10): source.ConfigRevision}, Status: actions_model.StatusSuccess, TriggerEvent: "push"}
		require.NoError(t, db.Insert(ctx, run))
		attempt := &actions_model.ActionRunAttempt{RepoID: run.RepoID, RunID: run.ID, Attempt: 1, Status: actions_model.StatusSuccess}
		require.NoError(t, db.Insert(ctx, attempt))
		_, err := db.GetEngine(ctx).Exec("UPDATE action_run SET latest_attempt_id = ? WHERE id = ?", attempt.ID, run.ID)
		require.NoError(t, err)
		job := &actions_model.ActionRunJob{RunID: run.ID, RunAttemptID: attempt.ID, RepoID: run.RepoID, OwnerID: run.OwnerID, Name: "build", WorkflowPayload: []byte("name: CI\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: true\n"), Status: actions_model.StatusSuccess}
		require.NoError(t, db.Insert(ctx, job))
		task := &actions_model.ActionTask{JobID: job.ID, RunnerID: runner.ID, RepoID: run.RepoID, OwnerID: run.OwnerID, Status: actions_model.StatusSuccess}
		task.GenerateAndFillToken()
		require.NoError(t, db.Insert(ctx, task))
		_, err = db.GetEngine(ctx).ID(job.ID).Cols("task_id").Update(&actions_model.ActionRunJob{TaskID: task.ID})
		require.NoError(t, err)
		return run, attempt, job
	}
	insert(strings.Repeat("c", 40))
	_, err = requiredScopedSourceDependencies(ctx, consumer, head, snapshot)
	require.ErrorIs(t, err, ErrNotReadyToMerge, "an old source commit cannot satisfy the current Git ref")
	_, err = db.GetEngine(ctx).Where("repo_id = ? AND workflow_repo_id = ?", consumer.ID, sourceRepo.ID).Delete(new(actions_model.ActionRun))
	require.NoError(t, err)
	run, attempt, job := insert(snapshot.sources[source.ID].gitSHA)
	dependencies, err := requiredScopedSourceDependencies(ctx, consumer, head, snapshot)
	require.NoError(t, err)
	require.Contains(t, dependencies, governance_model.Resource("repository", sourceRepo.ID))
	for _, sample := range []struct {
		status  actions_model.Status
		message string
	}{
		{actions_model.StatusWaiting, "required scoped run is pending"},
		{actions_model.StatusRunning, "required scoped run is pending"},
		{actions_model.StatusFailure, "required scoped run failed"},
		{actions_model.StatusCancelled, "required scoped run was cancelled"},
	} {
		_, err = db.GetEngine(ctx).ID(run.ID).Cols("status").Update(&actions_model.ActionRun{Status: sample.status})
		require.NoError(t, err)
		_, err = db.GetEngine(ctx).ID(attempt.ID).Cols("status").Update(&actions_model.ActionRunAttempt{Status: sample.status})
		require.NoError(t, err)
		_, err = db.GetEngine(ctx).ID(job.ID).Cols("status").Update(&actions_model.ActionRunJob{Status: sample.status})
		require.NoError(t, err)
		_, err = requiredScopedSourceDependencies(ctx, consumer, head, snapshot)
		require.ErrorIs(t, err, ErrNotReadyToMerge)
		require.ErrorContains(t, err, sample.message)
	}
	for _, update := range []struct {
		id   int64
		bean any
	}{
		{run.ID, &actions_model.ActionRun{Status: actions_model.StatusSuccess}},
		{attempt.ID, &actions_model.ActionRunAttempt{Status: actions_model.StatusSuccess}},
		{job.ID, &actions_model.ActionRunJob{Status: actions_model.StatusSuccess}},
	} {
		_, err = db.GetEngine(ctx).ID(update.id).Cols("status").Update(update.bean)
		require.NoError(t, err)
	}
	_, err = requiredScopedSourceDependencies(ctx, consumer, head, snapshot)
	require.NoError(t, err, "a successful trusted run remains valid")
	_, err = db.GetEngine(ctx).ID(attempt.ID).Cols("status").Update(&actions_model.ActionRunAttempt{Status: actions_model.StatusFailure})
	require.NoError(t, err)
	_, err = requiredScopedSourceDependencies(ctx, consumer, head, snapshot)
	require.ErrorContains(t, err, "required scoped run failed", "the bound latest attempt also determines the diagnostic")
	_, err = db.GetEngine(ctx).ID(attempt.ID).Cols("status").Update(&actions_model.ActionRunAttempt{Status: actions_model.StatusSuccess})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(job.ID).Cols("name").Update(&actions_model.ActionRunJob{Name: "other"})
	require.NoError(t, err)
	_, err = requiredScopedSourceDependencies(ctx, consumer, head, snapshot)
	require.ErrorContains(t, err, "needs a successful run", "a missing required job cannot be treated as trusted success")
	_, err = db.GetEngine(ctx).ID(job.ID).Cols("name").Update(&actions_model.ActionRunJob{Name: "build"})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(runner.ID).Cols("repo_id").Update(&actions_model.ActionRunner{RepoID: consumer.ID})
	require.NoError(t, err)
	_, err = requiredScopedSourceDependencies(ctx, consumer, head, snapshot)
	require.ErrorContains(t, err, "runner managed by its required source scope", "a successful job on an untrusted runner remains blocked")
	_, err = db.GetEngine(ctx).ID(runner.ID).Cols("repo_id").Update(&actions_model.ActionRunner{RepoID: 0})
	require.NoError(t, err)
	_, err = requiredScopedSourceDependencies(ctx, consumer, head, snapshot)
	require.NoError(t, err, "restoring trusted runner proof restores readiness")

	operation := &governance_model.ReferenceTransaction{RepoID: sourceRepo.ID, Actor: governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "git_http"}, Changes: []governance_model.ReferenceChange{{Ref: "refs/heads/other", Old: strings.Repeat("1", 40), New: strings.Repeat("2", 40)}}}
	require.NoError(t, governance_model.PrepareReferenceTransaction(ctx, operation, nil, func(context.Context) error { return nil }))
	_, err = captureRequiredScopedSourceSnapshot(ctx, consumer)
	require.ErrorIs(t, err, ErrNotReadyToMerge, "a prepared source ref update cannot leave the UI green")
	_, err = requiredScopedSourceDependencies(ctx, consumer, head, snapshot)
	require.ErrorIs(t, err, governance_model.ErrConflict, "a prepared source ref update blocks final authorization before Git changes")
	_, err = governance_model.ReconcileReferenceTransaction(ctx, operation.ID, map[string]string{"refs/heads/other": operation.Changes[0].Old}, nil)
	require.NoError(t, err)
	_, err = requiredScopedSourceDependencies(ctx, consumer, head, snapshot)
	require.ErrorIs(t, err, ErrNotReadyToMerge, "reference revision also detects an update that returned to its old SHA")

	refreshed, err := captureRequiredScopedSourceSnapshot(ctx, consumer)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(sourceRepo.ID).Cols("default_branch").Update(&repo_model.Repository{DefaultBranch: "inconsistent"})
	require.NoError(t, err)
	_, err = captureRequiredScopedSourceSnapshot(ctx, consumer)
	require.ErrorContains(t, err, "Git default branch differs", "interrupted Git/DB default-branch changes fail closed")
	_, err = db.GetEngine(ctx).ID(sourceRepo.ID).Cols("default_branch").Update(&repo_model.Repository{DefaultBranch: refreshed.sources[source.ID].defaultBranch})
	require.NoError(t, err)
	require.NoError(t, actions_model.SetScopedWorkflowSourceConfigs(ctx, 0, sourceRepo.ID, map[string]*actions_model.ScopedWorkflowConfig{"ci.yaml": {Required: true, Patterns: []string{"* / build (*)"}}}))
	_, err = requiredScopedSourceDependencies(ctx, consumer, head, refreshed)
	require.ErrorIs(t, err, ErrNotReadyToMerge, "a policy update after the Git snapshot cannot reuse its old run")
	refreshed, err = captureRequiredScopedSourceSnapshot(ctx, consumer)
	require.NoError(t, err)
	require.NoError(t, actions_model.AddScopedWorkflowSource(ctx, 0, 1))
	require.NoError(t, actions_model.SetScopedWorkflowSourceConfigs(ctx, 0, 1, map[string]*actions_model.ScopedWorkflowConfig{"other.yaml": {Required: true, Patterns: []string{"* / other (*)"}}}))
	_, err = requiredScopedSourceDependencies(ctx, consumer, head, refreshed)
	require.ErrorIs(t, err, ErrNotReadyToMerge, "newly required sources are re-enumerated inside the final transaction")
}

func TestScopedRunRunnerProofFollowsReusedTask(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	require.NoError(t, actions_model.AddScopedWorkflowSource(ctx, 0, 3))
	require.NoError(t, actions_model.SetScopedWorkflowSourceConfigs(ctx, 0, 3, map[string]*actions_model.ScopedWorkflowConfig{"ci.yaml": {Required: true, Patterns: []string{"* / build (*)"}}}))
	run := &actions_model.ActionRun{RepoID: 1, OwnerID: 2, WorkflowRepoID: 3, WorkflowID: "ci.yaml", IsScopedRun: true}
	require.NoError(t, db.Insert(ctx, run))
	first := &actions_model.ActionRunAttempt{RepoID: 1, RunID: run.ID, Attempt: 1, Status: actions_model.StatusSuccess}
	require.NoError(t, db.Insert(ctx, first))
	original := &actions_model.ActionRunJob{RunID: run.ID, RunAttemptID: first.ID, RepoID: 1, OwnerID: 2, Status: actions_model.StatusSuccess}
	require.NoError(t, db.Insert(ctx, original))
	runner := &actions_model.ActionRunner{UUID: "trusted-reused-runner", Name: "trusted-reused-runner"}
	require.NoError(t, db.Insert(ctx, runner))
	task := &actions_model.ActionTask{JobID: original.ID, RunnerID: runner.ID, RepoID: 1, OwnerID: 2, Status: actions_model.StatusSuccess}
	task.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, task))
	_, err := db.GetEngine(ctx).ID(original.ID).Cols("task_id").Update(&actions_model.ActionRunJob{TaskID: task.ID})
	require.NoError(t, err)
	latest := &actions_model.ActionRunAttempt{RepoID: 1, RunID: run.ID, Attempt: 2, Status: actions_model.StatusSuccess}
	require.NoError(t, db.Insert(ctx, latest))
	require.NoError(t, db.Insert(ctx, &actions_model.ActionRunJob{RunID: run.ID, RunAttemptID: latest.ID, RepoID: 1, OwnerID: 2, IsReusableCaller: true, Status: actions_model.StatusSuccess}))
	require.NoError(t, db.Insert(ctx, &actions_model.ActionRunJob{RunID: run.ID, RunAttemptID: latest.ID, RepoID: 1, OwnerID: 2, SourceTaskID: task.ID, Status: actions_model.StatusSuccess}))
	run.LatestAttemptID = latest.ID
	trusted, err := scopedRunRunnerProof(ctx, run)
	require.NoError(t, err)
	require.True(t, trusted)
	_, err = db.GetEngine(ctx).ID(runner.ID).Cols("repo_id").Update(&actions_model.ActionRunner{RepoID: 1})
	require.NoError(t, err)
	trusted, err = scopedRunRunnerProof(ctx, run)
	require.NoError(t, err)
	require.False(t, trusted, "a pass-through job cannot reuse lower-trust Runner success")
}

func TestRequiredScopedWorkflowFinalMergeProof(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: 2})
	sourceRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	head, other := strings.Repeat("a", 40), strings.Repeat("b", 40)
	const requiredPattern = "* / build (*)"
	require.NoError(t, CheckPullBranchProtectionsAtHead(ctx, pr, head), "without a required source there is no global block")
	require.NoError(t, actions_model.AddScopedWorkflowSource(ctx, 0, 3))
	require.NoError(t, actions_model.SetScopedWorkflowSourceConfigs(ctx, 0, 3, map[string]*actions_model.ScopedWorkflowConfig{"ci.yaml": {Required: true, Patterns: []string{requiredPattern}}}))
	source, err := actions_model.GetScopedWorkflowSource(ctx, 0, 3)
	require.NoError(t, err)
	require.ErrorIs(t, CheckPullBranchProtectionsAtHead(ctx, pr, head), ErrNotReadyToMerge, "an unprotected branch still needs a trusted run")
	require.NoError(t, db.Insert(ctx, &git_model.CommitStatus{RepoID: pr.BaseRepoID, SHA: head, Context: "anything", ContextHash: git_model.HashCommitStatusContext("anything"), State: commitstatus.CommitStatusSuccess, Index: 1, CreatorID: 2}))
	require.ErrorIs(t, CheckPullBranchProtectionsAtHead(ctx, pr, head), ErrNotReadyToMerge, "ordinary status cannot satisfy the requirement")

	runIndex := int64(900)
	insertRun := func(sha string, revision int64, status actions_model.Status, jobName string) {
		runIndex++
		run := &actions_model.ActionRun{
			RepoID: pr.BaseRepoID, OwnerID: 2, WorkflowID: "ci.yaml", Index: runIndex,
			CommitSHA: sha, WorkflowRepoID: 3, WorkflowCommitSHA: strings.Repeat("c", 40),
			IsScopedRun: true, WorkflowSourceScopeRevision: source.SourceScopeRevision,
			ScopedConfigRevisions: map[string]int64{strconv.FormatInt(source.ID, 10): revision},
			Status:                status, TriggerEvent: "push",
		}
		require.NoError(t, db.Insert(ctx, run))
		attempt := &actions_model.ActionRunAttempt{RepoID: run.RepoID, RunID: run.ID, Attempt: 1, Status: status}
		require.NoError(t, db.Insert(ctx, attempt))
		_, err := db.GetEngine(ctx).Exec("UPDATE action_run SET latest_attempt_id = ? WHERE id = ?", attempt.ID, run.ID)
		require.NoError(t, err)
		require.NoError(t, db.Insert(ctx, &actions_model.ActionRunJob{
			RunID: run.ID, RunAttemptID: attempt.ID, RepoID: run.RepoID, OwnerID: run.OwnerID,
			Name: jobName, WorkflowPayload: []byte("name: CI\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: true\n"),
			Status: status,
		}))
		loaded, err := actions_model.GetRunByRepoAndID(ctx, run.RepoID, run.ID)
		require.NoError(t, err)
		require.Equal(t, revision, loaded.ScopedConfigRevisions[strconv.FormatInt(source.ID, 10)])
		matched, passed, err := scopedRunPatternResult(ctx, loaded, glob.MustCompile(requiredPattern))
		require.NoError(t, err)
		require.Equal(t, jobName == "build", matched)
		require.Equal(t, status == actions_model.StatusSuccess && jobName == "build", passed)
		if jobName == "build" {
			legacy := sourceRepo.FullName() + ": CI / build (push)"
			matched, passed, err = scopedRunPatternResult(ctx, loaded, glob.MustCompile(legacy))
			require.NoError(t, err)
			require.True(t, matched, "a saved source-name pattern still matches the run")
			require.Equal(t, status == actions_model.StatusSuccess, passed)
		}
	}
	insertRun(other, source.ConfigRevision, actions_model.StatusSuccess, "build")
	require.ErrorIs(t, CheckPullBranchProtectionsAtHead(ctx, pr, head), ErrNotReadyToMerge, "a run for another commit is not proof")
	insertRun(head, source.ConfigRevision, actions_model.StatusSuccess, "build")
	require.NoError(t, CheckPullBranchProtectionsAtHead(ctx, pr, head))
	insertRun(head, source.ConfigRevision, actions_model.StatusFailure, "build")
	require.ErrorIs(t, CheckPullBranchProtectionsAtHead(ctx, pr, head), ErrNotReadyToMerge, "a newer failed run cannot be masked by an older success")
	insertRun(head, source.ConfigRevision, actions_model.StatusSuccess, "other")
	require.ErrorIs(t, CheckPullBranchProtectionsAtHead(ctx, pr, head), ErrNotReadyToMerge, "a newer run missing the required job cannot reuse an older success")
	insertRun(head, source.ConfigRevision, actions_model.StatusSuccess, "build")
	require.NoError(t, CheckPullBranchProtectionsAtHead(ctx, pr, head))
	require.NoError(t, actions_model.SetScopedWorkflowSourceConfigs(ctx, 0, 3, map[string]*actions_model.ScopedWorkflowConfig{"ci.yaml": {Required: true, Patterns: []string{requiredPattern}}}))
	source, err = actions_model.GetScopedWorkflowSource(ctx, 0, 3)
	require.NoError(t, err)
	require.ErrorIs(t, CheckPullBranchProtectionsAtHead(ctx, pr, head), ErrNotReadyToMerge, "the old run cannot satisfy a new config revision")
	insertRun(head, source.ConfigRevision, actions_model.StatusSuccess, "build")
	require.NoError(t, CheckPullBranchProtectionsAtHead(ctx, pr, head))
	require.NoError(t, actions_model.RemoveScopedWorkflowSource(ctx, 0, 3))
	require.NoError(t, actions_model.AddScopedWorkflowSource(ctx, 0, 3))
	require.NoError(t, actions_model.SetScopedWorkflowSourceConfigs(ctx, 0, 3, map[string]*actions_model.ScopedWorkflowConfig{"ci.yaml": {Required: true, Patterns: []string{requiredPattern}}}))
	source, err = actions_model.GetScopedWorkflowSource(ctx, 0, 3)
	require.NoError(t, err)
	require.ErrorIs(t, CheckPullBranchProtectionsAtHead(ctx, pr, head), ErrNotReadyToMerge, "a removed source registration cannot lend proof to its replacement")
	insertRun(head, source.ConfigRevision, actions_model.StatusSuccess, "build")
	require.NoError(t, CheckPullBranchProtectionsAtHead(ctx, pr, head))
	_, err = db.GetEngine(ctx).ID(3).Cols("is_archived").Update(&repo_model.Repository{IsArchived: true})
	require.NoError(t, err)
	require.ErrorIs(t, CheckPullBranchProtectionsAtHead(ctx, pr, head), ErrNotReadyToMerge, "source unavailability keeps the requirement")
}
