// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGovernanceHookPreservesExistingEntry(t *testing.T) {
	repo := &mockRepository{path: t.TempDir()}
	require.NoError(t, exec.Command("git", "init", "--bare", repo.path).Run())
	hooks := filepath.Join(repo.path, "hooks")
	require.NoError(t, os.MkdirAll(hooks, 0o755))
	target := filepath.Join(hooks, "reference-transaction")
	previous := []byte("#!/bin/sh\n# 已有第三方入口\nexit 0\n")
	require.NoError(t, os.WriteFile(target, previous, 0o755))
	require.Error(t, InstallReferenceTransactionHook(t.Context(), repo))
	actual, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, previous, actual)
	require.NoError(t, os.Remove(target))
	require.NoError(t, os.Symlink("外部入口", target))
	require.Error(t, InstallReferenceTransactionHook(t.Context(), repo))
	link, err := os.Readlink(target)
	require.NoError(t, err)
	require.Equal(t, "外部入口", link)
	require.NoError(t, os.Remove(target))
	require.NoError(t, InstallReferenceTransactionHook(t.Context(), repo))
	require.NoError(t, InstallReferenceTransactionHook(t.Context(), repo))
	installed, err := ReferenceTransactionHookInstalled(repo)
	require.NoError(t, err)
	require.True(t, installed)
	require.NoError(t, exec.Command("git", "-C", repo.path, "config", "--unset", "gc.auto").Run())
	installed, err = ReferenceTransactionHookInstalled(repo)
	require.NoError(t, err)
	require.False(t, installed, "旧仓库缺少维护配置时应显示未接入，而不是让配置页返回500")
}
