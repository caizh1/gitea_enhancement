// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package private

import (
	"testing"

	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestRepositoryByStorageIdentityUsesStableIDAndNormalizedPhysicalPath(t *testing.T) {
	unittest.PrepareTestEnv(t)
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	repo.GovernanceStorageOwner = "OldOwner"
	repo.GovernanceStorageName = "OldRepo"
	repo.OwnerName = "new-owner"
	repo.Name = "new-name"
	require.NoError(t, repo_model.UpdateRepositoryColsNoAutoTime(t.Context(), repo, "governance_storage_owner", "governance_storage_name", "owner_name", "name"))

	resolved, err := repositoryByStorageIdentity(t.Context(), repo.ID, "oldowner", "OLDREPO")
	require.NoError(t, err)
	require.Equal(t, "new-name", resolved.Name, "公开逻辑名保持改名后值")
	_, err = repositoryByStorageIdentity(t.Context(), repo.ID, "new-owner", "new-name")
	require.ErrorIs(t, err, governance_model.ErrNotFound, "即使新逻辑路径复用了同名，也不能替代稳定物理身份")
	_, err = repositoryByStorageIdentity(t.Context(), repo.ID+1, "oldowner", "oldrepo")
	require.Error(t, err, "物理路径正确也必须同时匹配稳定项目 ID")
}
