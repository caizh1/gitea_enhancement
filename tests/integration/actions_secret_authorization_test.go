// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"crypto/sha256"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	secret_model "gitea.dev/models/secret"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	repo_service "gitea.dev/services/repository"
	secret_service "gitea.dev/services/secrets"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestSecretRepositoryTransferUsesCurrentOwner(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	source := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	target := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	require.NoError(t, repo.LoadOwner(ctx))
	sourceCtx := governance_model.WithAuditActor(ctx, governance_model.Actor{ID: source.ID, Name: source.Name, Kind: "user", Transport: "api"})
	targetCtx := governance_model.WithAuditActor(ctx, governance_model.Actor{ID: target.ID, Name: target.Name, Kind: "user", Transport: "api"})
	protected := true
	secret, created, err := secret_service.CreateOrUpdateSecret(sourceCtx, 0, repo.ID, "TRANSFER_SECRET", "synthetic-value", "before", &protected)
	require.NoError(t, err)
	require.True(t, created)
	before, has, err := db.GetByID[secret_model.Secret](ctx, secret.ID)
	require.NoError(t, err)
	require.True(t, has)
	require.NoError(t, repo_service.StartRepositoryTransfer(sourceCtx, source, target, repo, nil))
	require.NoError(t, repo_service.AcceptTransferOwnership(targetCtx, repo, target))
	current := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	require.Equal(t, target.ID, current.OwnerID)
	changed := false
	_, _, err = secret_service.CreateOrUpdateSecret(sourceCtx, 0, repo.ID, secret.Name, "synthetic-changed", "after", &changed)
	require.Error(t, err, "原所有者失去当前仓库管理权后不能改写")
	require.Error(t, secret_service.DeleteSecretByName(sourceCtx, 0, repo.ID, secret.Name))
	stored, has, err := db.GetByID[secret_model.Secret](ctx, secret.ID)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, sha256.Sum256([]byte(before.Data)), sha256.Sum256([]byte(stored.Data)), "拒绝改写后密文保持不变")
	require.Equal(t, before.Protected, stored.Protected)
	require.Equal(t, before.Description, stored.Description)
	_, created, err = secret_service.CreateOrUpdateSecret(targetCtx, 0, repo.ID, secret.Name, "synthetic-authorized", "after", &changed)
	require.NoError(t, err)
	require.False(t, created)
	require.NoError(t, secret_service.DeleteSecretByName(targetCtx, 0, repo.ID, secret.Name))
}
