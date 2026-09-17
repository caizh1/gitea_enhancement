// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenameBranchReferencesUsesOneAtomicGitTransaction(t *testing.T) {
	work, bare := filepath.Join(t.TempDir(), "work"), filepath.Join(t.TempDir(), "repo.git")
	require.NoError(t, exec.Command("git", "init", "-b", "main", work).Run())
	cmd := exec.Command("git", "-C", work, "commit", "--allow-empty", "-m", "initial")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	require.NoError(t, cmd.Run())
	require.NoError(t, exec.Command("git", "clone", "--bare", work, bare).Run())
	logPath := filepath.Join(t.TempDir(), "reference.log")
	hook := filepath.Join(bare, "hooks", "reference-transaction")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$REFERENCE_LOG\"\ncat >> \"$REFERENCE_LOG\"\n"), 0o755))
	repo := &mockRepository{path: bare}
	require.NoError(t, RenameBranchReferencesWithEnv(t.Context(), repo, "main", "renamed", append(os.Environ(), "REFERENCE_LOG="+logPath)))
	require.False(t, IsBranchExist(t.Context(), repo, "main"))
	require.True(t, IsBranchExist(t.Context(), repo, "renamed"))
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	log := string(data)
	require.Contains(t, log, "refs/heads/main")
	require.Contains(t, log, "refs/heads/renamed")
	require.Contains(t, log, "prepared\n0000000000000000000000000000000000000000 ")
	require.Contains(t, log, "refs/heads/renamed\n")
	require.GreaterOrEqual(t, strings.Count(log, "committed\n"), 1, "Git 可能额外发出会被入口忽略的 0→0 物理回调")
}

func TestDeleteBranchReferenceWithEnvUsesReferenceTransaction(t *testing.T) {
	repo := initBareBranchRepository(t)
	logPath := filepath.Join(t.TempDir(), "reference.log")
	installReferenceLoggingHook(t, repo, logPath)
	old, err := GetBranchCommitID(t.Context(), repo, "main")
	require.NoError(t, err)

	require.NoError(t, DeleteBranchReferenceWithEnv(t.Context(), repo, "main", old, append(os.Environ(), "REFERENCE_LOG="+logPath)))
	require.False(t, IsBranchExist(t.Context(), repo, "main"))

	log, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(log), "prepared")
	require.Contains(t, string(log), "refs/heads/main")
}

func initBareBranchRepository(t *testing.T) *mockRepository {
	t.Helper()
	work, bare := filepath.Join(t.TempDir(), "work"), filepath.Join(t.TempDir(), "repo.git")
	require.NoError(t, exec.Command("git", "init", "-b", "main", work).Run())
	cmd := exec.Command("git", "-C", work, "commit", "--allow-empty", "-m", "initial")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	require.NoError(t, cmd.Run())
	require.NoError(t, exec.Command("git", "clone", "--bare", work, bare).Run())
	return &mockRepository{path: bare}
}

func installReferenceLoggingHook(t *testing.T, repo *mockRepository, logPath string) {
	t.Helper()
	hook := filepath.Join(repo.path, "hooks", "reference-transaction")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$REFERENCE_LOG\"\ncat >> \"$REFERENCE_LOG\"\n"), 0o755))
}
