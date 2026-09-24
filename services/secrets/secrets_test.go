// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	"crypto/sha256"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	secret_model "gitea.dev/models/secret"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestSecretProtectionOmittedUpdatePreservesPolicy(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"})
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	protected := true
	secret, created, err := CreateOrUpdateSecret(ctx, 2, 0, "SYNTHETIC_KEY", "synthetic-value-1", "", &protected)
	require.NoError(t, err)
	require.True(t, created)
	require.True(t, secret.Protected)
	_, created, err = CreateOrUpdateSecret(ctx, 2, 0, "SYNTHETIC_KEY", "synthetic-value-2", "")
	require.NoError(t, err)
	require.False(t, created)
	stored, has, err := db.GetByID[secret_model.Secret](ctx, secret.ID)
	require.NoError(t, err)
	require.True(t, has)
	require.True(t, stored.Protected)
	protected = false
	_, _, err = CreateOrUpdateSecret(ctx, 2, 0, "SYNTHETIC_KEY", "synthetic-value-3", "", &protected)
	require.NoError(t, err)
	stored, has, err = db.GetByID[secret_model.Secret](ctx, secret.ID)
	require.NoError(t, err)
	require.True(t, has)
	require.False(t, stored.Protected)
	var events []*governance_model.AuditEvent
	require.NoError(t, db.GetEngine(ctx).Where("object_type = ? AND object_id = ?", "actions_secret", secret.ID).Find(&events))
	require.NotEmpty(t, events)
	for _, event := range events {
		require.NotContains(t, string(event.Details), "synthetic-value")
	}
}

func TestSecretWriteRevocation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	manager := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	formerOwner := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, manager, governance_service.GroupOption{Path: "secret-revocation", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, manager, group.ID, governance_service.GroupMemberOption{UserID: formerOwner.ID, Role: governance_model.Owner, Revision: group.Revision}, false))
	repo := &repo_model.Repository{OwnerID: group.ID, OwnerName: group.InternalName, OwnerNamespace: group.FullPath, Name: "secrets", LowerName: "secrets", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeActions}))
	actorCtx := governance_model.WithAuditActor(ctx, formerOwner)
	protected := true
	groupSecret, created, err := CreateOrUpdateSecret(actorCtx, group.ID, 0, "GROUP_EXISTING", "synthetic-value", "before", &protected)
	require.NoError(t, err)
	require.True(t, created)
	repoSecret, created, err := CreateOrUpdateSecret(actorCtx, 0, repo.ID, "REPO_EXISTING", "synthetic-value", "before", &protected)
	require.NoError(t, err)
	require.True(t, created)
	groupBefore, has, err := db.GetByID[secret_model.Secret](ctx, groupSecret.ID)
	require.NoError(t, err)
	require.True(t, has)
	repoBefore, has, err := db.GetByID[secret_model.Secret](ctx, repoSecret.ID)
	require.NoError(t, err)
	require.True(t, has)
	current, err := governance_model.GetNamespace(ctx, group.ID)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, manager, group.ID, governance_service.GroupMemberOption{UserID: formerOwner.ID, Revision: current.Revision}, true))

	for _, scope := range []struct {
		ownerID, repoID, secretID int64
		name                      string
		before                    *secret_model.Secret
	}{
		{group.ID, 0, groupSecret.ID, "GROUP_EXISTING", groupBefore},
		{0, repo.ID, repoSecret.ID, "REPO_EXISTING", repoBefore},
	} {
		_, _, err = CreateOrUpdateSecret(actorCtx, scope.ownerID, scope.repoID, "NEW_SECRET", "synthetic-new", "", &protected)
		require.Error(t, err, "撤权后不能创建配置")
		unprotected := false
		_, _, err = CreateOrUpdateSecret(actorCtx, scope.ownerID, scope.repoID, scope.name, "synthetic-update", "after", &unprotected)
		require.Error(t, err, "撤权后不能修改值或 Protected")
		require.Error(t, DeleteSecretByID(actorCtx, scope.ownerID, scope.repoID, scope.secretID), "撤权后不能按 ID 删除")
		require.Error(t, DeleteSecretByName(actorCtx, scope.ownerID, scope.repoID, scope.name), "撤权后不能按名称删除")
		stored, exists, getErr := db.GetByID[secret_model.Secret](ctx, scope.secretID)
		require.NoError(t, getErr)
		require.True(t, exists)
		require.Equal(t, sha256.Sum256([]byte(scope.before.Data)), sha256.Sum256([]byte(stored.Data)), "撤权后密文保持不变")
		require.Equal(t, scope.before.Protected, stored.Protected)
		require.Equal(t, scope.before.Description, stored.Description)
	}
	_, created, err = CreateOrUpdateSecret(governance_model.WithAuditActor(ctx, manager), group.ID, 0, "AUTHORIZED", "synthetic-authorized", "")
	require.NoError(t, err)
	require.True(t, created)
}
