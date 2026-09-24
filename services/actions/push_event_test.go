// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_branch_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	unit_model "gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	actions_module "gitea.dev/modules/actions"
	git_model "gitea.dev/modules/git"
	"gitea.dev/modules/gitrepo"
	api "gitea.dev/modules/structs"
	webhook_module "gitea.dev/modules/webhook"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPushEventUsesAfterCommitAndCurrentSchedules(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit_model.TypeActions, Config: &repo_model.ActionsConfig{}}))
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	worktree := filepath.Join(t.TempDir(), "push-event")
	gitCommand := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s: %s", strings.Join(args, " "), output)
		return strings.TrimSpace(string(output))
	}
	gitCommand("clone", "--quiet", repo.RepoPath(), worktree)
	workflowPath := filepath.Join(worktree, ".gitea", "workflows", "push-event-commit.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(workflowPath), 0o755))
	writeWorkflow := func(cron, message string) string {
		t.Helper()
		content := "name: push-event-commit\non:\n  push:\n"
		if cron != "" {
			content += "  schedule:\n    - cron: '" + cron + "'\n"
		}
		content += "jobs:\n  check:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo checked\n# test revision: " + message + "\n"
		require.NoError(t, os.MkdirAll(filepath.Dir(workflowPath), 0o755))
		require.NoError(t, os.WriteFile(workflowPath, []byte(content), 0o644))
		gitCommand("-C", worktree, "add", ".gitea/workflows/push-event-commit.yaml")
		gitCommand("-C", worktree, "commit", "--quiet", "-m", message)
		gitCommand("-C", worktree, "push", "--quiet", "origin", "HEAD:refs/heads/master")
		return gitCommand("-C", worktree, "rev-parse", "HEAD")
	}
	oldCommit := writeWorkflow("0 1 * * *", "workflow 0 1 * * *")
	newCommit := writeWorkflow("0 2 * * *", "workflow 0 2 * * *")
	assert.NotEqual(t, oldCommit, newCommit)
	gitRepo, err := gitrepo.OpenRepository(ctx, repo)
	require.NoError(t, err)
	defer gitRepo.Close()
	oldGitCommit, err := gitRepo.GetCommit(oldCommit)
	require.NoError(t, err)
	oldSchedules, err := actions_module.DetectScheduledWorkflows(gitRepo, oldGitCommit)
	require.NoError(t, err)
	require.Len(t, oldSchedules, 1)
	syncBranch := func(sha string) {
		t.Helper()
		updated, err := db.GetEngine(ctx).Where("repo_id = ? AND name = ?", repo.ID, repo.DefaultBranch).Cols("commit_id").Update(&git_branch_model.Branch{CommitID: sha})
		require.NoError(t, err)
		require.Equal(t, int64(1), updated)
	}
	input := newNotifyInput(repo, doer, webhook_module.HookEventPush).
		WithRef(git_model.RefNameFromBranch(repo.DefaultBranch).String()).
		WithPayload(&api.PushPayload{Ref: git_model.RefNameFromBranch(repo.DefaultBranch).String(), After: oldCommit, HeadCommit: &api.PayloadCommit{ID: oldCommit}})
	// The persisted branch can lag Git HEAD while an earlier push notification is processed.
	// Its schedule update must fail closed without dropping the valid push workflow.
	syncBranch(oldCommit)
	require.NoError(t, notify(ctx, input))
	var laggedRun actions_model.ActionRun
	hasLaggedRun, err := db.GetEngine(ctx).Where("repo_id = ? AND workflow_id = ?", repo.ID, "push-event-commit.yaml").Get(&laggedRun)
	require.NoError(t, err)
	require.True(t, hasLaggedRun)
	assert.Equal(t, oldCommit, laggedRun.CommitSHA)
	laggedSchedules, err := db.Find[actions_model.ActionSchedule](ctx, actions_model.FindScheduleOptions{RepoID: repo.ID})
	require.NoError(t, err)
	assert.Empty(t, laggedSchedules, "the conflicting schedule update must not be committed")

	syncBranch(newCommit) // post-receive persists the branch before enqueuing its notification
	require.NoError(t, notify(ctx, input))

	var run actions_model.ActionRun
	has, err := db.GetEngine(ctx).Where("repo_id = ? AND workflow_id = ?", repo.ID, "push-event-commit.yaml").Desc("id").Get(&run)
	require.NoError(t, err)
	require.True(t, has, "the historical push must still create a run")
	assert.Equal(t, oldCommit, run.CommitSHA)
	assert.Equal(t, oldCommit, run.WorkflowCommitSHA)
	assert.Equal(t, "workflow 0 1 * * *", run.Title)
	pushPayload, err := run.GetPushEventPayload()
	require.NoError(t, err)
	assert.Equal(t, oldCommit, pushPayload.After)
	assert.NotEmpty(t, findCommitStatusesForContext(t, repo.ID, oldCommit, "push-event-commit / check (push)"))
	assert.Empty(t, findCommitStatusesForContext(t, repo.ID, newCommit, "push-event-commit / check (push)"))

	schedules, err := db.Find[actions_model.ActionSchedule](ctx, actions_model.FindScheduleOptions{RepoID: repo.ID})
	require.NoError(t, err)
	require.Len(t, schedules, 1)
	assert.Equal(t, newCommit, schedules[0].CommitSHA)
	// A prepared H1 plan must not overwrite B's already committed H2 plan.
	require.ErrorIs(t, handleSchedules(ctx, oldSchedules, oldGitCommit, input, input.Ref), governance_model.ErrConflict)
	schedules, err = db.Find[actions_model.ActionSchedule](ctx, actions_model.FindScheduleOptions{RepoID: repo.ID})
	require.NoError(t, err)
	require.Len(t, schedules, 1)
	assert.Equal(t, newCommit, schedules[0].CommitSHA)

	// A skipped historical push must not run, but the default-branch schedule still follows current HEAD.
	skippedCommit := writeWorkflow("0 3 * * *", "workflow [skip ci]")
	latestCommit := writeWorkflow("0 4 * * *", "workflow latest")
	syncBranch(latestCommit)
	input.Payload = &api.PushPayload{Ref: input.Ref.String(), After: skippedCommit, HeadCommit: &api.PayloadCommit{ID: skippedCommit}}
	require.NoError(t, notify(ctx, input))
	schedules, err = db.Find[actions_model.ActionSchedule](ctx, actions_model.FindScheduleOptions{RepoID: repo.ID})
	require.NoError(t, err)
	require.Len(t, schedules, 1)
	assert.Equal(t, latestCommit, schedules[0].CommitSHA)
	gitCommand("-C", worktree, "rm", ".gitea/workflows/push-event-commit.yaml")
	gitCommand("-C", worktree, "commit", "--quiet", "-m", "remove schedule [skip ci]")
	gitCommand("-C", worktree, "push", "--quiet", "origin", "HEAD:refs/heads/master")
	syncBranch(gitCommand("-C", worktree, "rev-parse", "HEAD"))
	input.Payload = &api.PushPayload{Ref: input.Ref.String(), After: oldCommit, HeadCommit: &api.PayloadCommit{ID: oldCommit}}
	require.NoError(t, notify(ctx, input))
	schedules, err = db.Find[actions_model.ActionSchedule](ctx, actions_model.FindScheduleOptions{RepoID: repo.ID})
	require.NoError(t, err)
	assert.Empty(t, schedules, "removing the current schedule must clear the old plan")

	// A queued push remains bound to its commit even if its non-default ref was removed.
	gitCommand("-C", worktree, "push", "--quiet", "origin", oldCommit+":refs/heads/removed")
	gitCommand("-C", worktree, "push", "--quiet", "origin", ":refs/heads/removed")
	input.Ref = git_model.RefNameFromBranch("removed")
	input.Payload = &api.PushPayload{Ref: input.Ref.String(), After: oldCommit, HeadCommit: &api.PayloadCommit{ID: oldCommit}}
	require.NoError(t, notify(ctx, input))
	run = actions_model.ActionRun{}
	has, err = db.GetEngine(ctx).Where("repo_id = ? AND workflow_id = ? AND ref = ?", repo.ID, "push-event-commit.yaml", input.Ref.String()).Get(&run)
	require.NoError(t, err)
	require.True(t, has)
	assert.Equal(t, oldCommit, run.CommitSHA)

	input.Ref = git_model.RefNameFromBranch(repo.DefaultBranch)
	input.Payload = &api.PushPayload{Ref: input.Ref.String(), After: "refs/heads/master"}
	require.ErrorContains(t, notify(ctx, input), "invalid after commit")

	// Even a push-only workflow reaches the schedule replacement path.
	pushOnlyCommit := writeWorkflow("", "push only")
	latestPushOnlyCommit := writeWorkflow("", "push only latest")
	syncBranch(pushOnlyCommit)
	input.Payload = &api.PushPayload{Ref: input.Ref.String(), After: pushOnlyCommit, HeadCommit: &api.PayloadCommit{ID: pushOnlyCommit}}
	require.NoError(t, notify(ctx, input))
	var pushOnlyRun actions_model.ActionRun
	hasPushOnlyRun, err := db.GetEngine(ctx).Where("repo_id = ? AND commit_sha = ?", repo.ID, pushOnlyCommit).Get(&pushOnlyRun)
	require.NoError(t, err)
	require.True(t, hasPushOnlyRun)
	assert.Equal(t, pushOnlyCommit, pushOnlyRun.WorkflowCommitSHA)
	schedules, err = db.Find[actions_model.ActionSchedule](ctx, actions_model.FindScheduleOptions{RepoID: repo.ID})
	require.NoError(t, err)
	assert.Empty(t, schedules)

	// A stale owner/scope snapshot also conflicts, but final run insertion must reject it.
	syncBranch(latestPushOnlyCommit)
	updated, err := db.GetEngine(ctx).ID(repo.ID).Cols("actions_scope_revision").Update(&repo_model.Repository{ActionsScopeRevision: repo.ActionsScopeRevision + 1})
	require.NoError(t, err)
	require.Equal(t, int64(1), updated)
	input.Payload = &api.PushPayload{Ref: input.Ref.String(), After: latestPushOnlyCommit, HeadCommit: &api.PayloadCommit{ID: latestPushOnlyCommit}}
	require.NoError(t, notify(ctx, input))
	staleRunExists, err := db.GetEngine(ctx).Where("repo_id = ? AND commit_sha = ?", repo.ID, latestPushOnlyCommit).Exist(new(actions_model.ActionRun))
	require.NoError(t, err)
	assert.False(t, staleRunExists, "the old workflow snapshot must not get a run after scope revocation")
}
