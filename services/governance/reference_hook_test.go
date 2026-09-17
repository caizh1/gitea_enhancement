// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"os"
	"path/filepath"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"

	"github.com/stretchr/testify/require"
)

func TestInstallRepositoryReferenceHookAuditsThirdPartyConflict(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	hookDir := filepath.Join(setting.RepoRootPath, filepath.FromSlash(repo.RelativePath()), "hooks")
	require.NoError(t, os.MkdirAll(hookDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hookDir, "reference-transaction"), []byte("#!/bin/sh\n# 第三方入口\nexit 0\n"), 0o755))

	err := InstallRepositoryReferenceHook(ctx, governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}, repo.ID)
	require.Error(t, err)
	event := new(governance_model.AuditEvent)
	has, queryErr := db.GetEngine(ctx).Where("type = ?", "repository.reference_hook_install_failed").Get(event)
	require.NoError(t, queryErr)
	require.True(t, has)
	require.Equal(t, "failure", event.Result)
	require.Contains(t, string(event.Details), "install_rejected")
	require.NotContains(t, string(event.Details), hookDir)
}
