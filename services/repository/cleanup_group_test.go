// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package repository

import (
	"os"
	"path/filepath"
	"testing"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/require"
)

func TestRemoveEmptyGroupGitDirectoryKeepsTransferredRepositories(t *testing.T) {
	root := t.TempDir()
	defer test.MockVariableValue(&setting.RepoRootPath, root)()
	groupPath := filepath.Join(root, "old-group")
	require.NoError(t, os.Mkdir(groupPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(groupPath, "transferred.git"), []byte("stable"), 0o600))

	require.NoError(t, removeEmptyGroupGitDirectory(governance_model.CleanupObject{Kind: "git", Path: "old-group"}))
	_, err := os.Stat(filepath.Join(groupPath, "transferred.git"))
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(groupPath, "transferred.git")))
	require.NoError(t, removeEmptyGroupGitDirectory(governance_model.CleanupObject{Kind: "git", Path: "old-group"}))
	_, err = os.Stat(groupPath)
	require.True(t, os.IsNotExist(err))
}

func TestRemoveEmptyGroupGitDirectoryNeverUsesWorkingDirectory(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	defer test.MockVariableValue(&setting.RepoRootPath, root)()
	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(cwd))
	t.Cleanup(func() { _ = os.Chdir(previous) })
	require.NoError(t, os.Mkdir("old-group", 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(root, "old-group"), 0o755))

	require.NoError(t, removeEmptyGroupGitDirectory(governance_model.CleanupObject{Kind: "git", Path: "old-group"}))
	_, err = os.Stat(filepath.Join(cwd, "old-group"))
	require.NoError(t, err)
}
