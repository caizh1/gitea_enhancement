// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	git_model "gitea.dev/models/git"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/gitrepo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncBranchesToDBUsesCurrentRefForLateHooks(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	worktree := filepath.Join(t.TempDir(), "branch-sync")
	gitCommand := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s: %s", strings.Join(args, " "), output)
		return strings.TrimSpace(string(output))
	}
	gitCommand("clone", "--quiet", repo.RepoPath(), worktree)
	writeCommit := func(message string) string {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(worktree, "branch-sync.txt"), []byte(message), 0o644))
		gitCommand("-C", worktree, "add", "branch-sync.txt")
		gitCommand("-C", worktree, "commit", "--quiet", "-m", message)
		gitCommand("-C", worktree, "push", "--quiet", "origin", "HEAD:refs/heads/master")
		return gitCommand("-C", worktree, "rev-parse", "HEAD")
	}
	first := writeCommit("first")
	latest := writeCommit("latest")
	assert.NotEqual(t, first, latest)
	gitRepo, err := gitrepo.OpenRepository(ctx, repo)
	require.NoError(t, err)
	defer gitRepo.Close()
	syncBranch := func(name string) *git_model.Branch {
		t.Helper()
		require.NoError(t, SyncBranchesToDB(ctx, repo.ID, 1, []string{name}, gitRepo))
		branch, err := git_model.GetBranch(ctx, repo.ID, name)
		require.NoError(t, err)
		return branch
	}

	assert.Equal(t, latest, syncBranch("master").CommitID)
	// A delayed first hook must not restore its old payload after the latest hook.
	assert.Equal(t, latest, syncBranch("master").CommitID)

	const feature = "branch-sync-recreated"
	gitCommand("-C", worktree, "push", "--quiet", "origin", "HEAD:refs/heads/"+feature)
	assert.False(t, syncBranch(feature).IsDeleted)
	gitCommand("-C", worktree, "push", "--quiet", "origin", ":refs/heads/"+feature)
	assert.True(t, syncBranch(feature).IsDeleted)
	gitCommand("-C", worktree, "push", "--quiet", "origin", "HEAD:refs/heads/"+feature)
	assert.False(t, syncBranch(feature).IsDeleted)
	// A late deletion callback reads the recreated ref before touching the DB.
	assert.Equal(t, latest, syncBranch(feature).CommitID)
	assert.False(t, syncBranch(feature).IsDeleted)
}
