// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"os"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/util"

	"github.com/stretchr/testify/assert"
)

func TestCreateRepositoryDirectly(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	// a successful creating repository
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	createdRepo, err := CreateRepositoryDirectly(t.Context(), user2, user2, CreateRepoOptions{
		Name: "created-repo",
	}, true)
	assert.NoError(t, err)
	assert.NotNil(t, createdRepo)

	exist, err := util.IsExist(repo_model.RepoPath(user2.Name, createdRepo.Name))
	assert.NoError(t, err)
	assert.True(t, exist)

	unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerName: user2.Name, Name: createdRepo.Name})
	unittest.AssertExistsAndLoadBean(t, &governance_model.RepositoryCreation{RepoID: createdRepo.ID, State: "succeeded"})
	unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "repository.created", ObjectID: createdRepo.ID})

	err = DeleteRepositoryDirectly(t.Context(), createdRepo.ID)
	assert.NoError(t, err)

	// a failed creating because some mock data
	// create the repository directory so that the creation will fail after database record created.
	assert.NoError(t, os.MkdirAll(repo_model.RepoPath(user2.Name, createdRepo.Name), os.ModePerm))

	createdRepo2, err := CreateRepositoryDirectly(t.Context(), user2, user2, CreateRepoOptions{
		Name: "created-repo",
	}, true)
	assert.Nil(t, createdRepo2)
	assert.Error(t, err)

	// assert the cleanup is successful
	unittest.AssertNotExistsBean(t, &repo_model.Repository{OwnerName: user2.Name, Name: createdRepo.Name})
	unittest.AssertExistsAndLoadBean(t, &governance_model.RepositoryCreation{RepoID: createdRepo.ID, State: "succeeded"})

	exist, err = util.IsExist(repo_model.RepoPath(user2.Name, createdRepo.Name))
	assert.NoError(t, err)
	assert.False(t, exist)
}

func TestRepositoryCreationRecoveryRequiresServiceCompletionMarker(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	assert.NoError(t, repo.LoadOwner(t.Context()))
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web", RequestID: "create-recovery-test"})
	ctx = withRepositoryCreation(ctx, "template", map[string]any{"source_repository_id": int64(2)})
	assert.NoError(t, governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		if _, err := db.GetEngine(ctx).Where("repo_id = ?", repo.ID).Delete(new(governance_model.RepositoryCreation)); err != nil {
			return err
		}
		return prepareRepositoryCreation(ctx, repo)
	}))
	var operation governance_model.RepositoryCreation
	has, err := db.GetEngine(ctx).Where("repo_id = ?", repo.ID).Get(&operation)
	assert.NoError(t, err)
	assert.True(t, has)

	state, err := RecoverRepositoryCreation(ctx, operation.ID)
	assert.NoError(t, err)
	assert.Equal(t, "unknown", state)
	fresh := unittest.AssertExistsAndLoadBean(t, &governance_model.RepositoryCreation{ID: operation.ID})
	assert.Equal(t, "completion_marker_missing", fresh.LastReasonCode)
	unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "repository.creation_recovery_required", ObjectID: repo.ID, Result: "unknown"})
}

func TestCompleteRepositoryCreationIsIdempotent(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	assert.NoError(t, repo.LoadOwner(t.Context()))
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web", RequestID: "create-idempotent-test"})
	ctx = withRepositoryCreation(ctx, "created", nil)
	assert.NoError(t, governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		if _, err := db.GetEngine(ctx).Where("repo_id = ?", repo.ID).Delete(new(governance_model.RepositoryCreation)); err != nil {
			return err
		}
		if err := prepareRepositoryCreation(ctx, repo); err != nil {
			return err
		}
		return markRepositoryCreationStorageComplete(ctx, repo.ID)
	}))
	assert.NoError(t, governance_model.WithWrite(ctx, nil, func(ctx context.Context) error { return completeRepositoryCreation(ctx, repo) }))
	assert.NoError(t, governance_model.WithWrite(ctx, nil, func(ctx context.Context) error { return completeRepositoryCreation(ctx, repo) }))
	count, err := db.GetEngine(ctx).Where("type = ? AND object_id = ?", "repository.created", repo.ID).Count(new(governance_model.AuditEvent))
	assert.NoError(t, err)
	assert.EqualValues(t, 1, count)
}

func TestRepositoryCreationRecoveryCompletesVerifiedStorage(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	assert.NoError(t, repo.LoadOwner(t.Context()))
	assert.NoError(t, gitrepo.InstallReferenceTransactionHook(t.Context(), repo))
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web", RequestID: "create-recovery-success"})
	ctx = withRepositoryCreation(ctx, "created", nil)
	assert.NoError(t, governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		if _, err := db.GetEngine(ctx).Where("repo_id = ?", repo.ID).Delete(new(governance_model.RepositoryCreation)); err != nil {
			return err
		}
		if err := prepareRepositoryCreation(ctx, repo); err != nil {
			return err
		}
		return markRepositoryCreationStorageComplete(ctx, repo.ID)
	}))
	_, err := db.GetEngine(ctx).ID(repo.ID).Cols("status").Update(&repo_model.Repository{Status: repo_model.RepositoryBeingMigrated})
	assert.NoError(t, err)
	var operation governance_model.RepositoryCreation
	has, err := db.GetEngine(ctx).Where("repo_id = ?", repo.ID).Get(&operation)
	assert.NoError(t, err)
	assert.True(t, has)
	state, err := RecoverRepositoryCreation(ctx, operation.ID)
	assert.NoError(t, err)
	assert.Equal(t, "succeeded", state)
	freshRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	assert.Equal(t, repo_model.RepositoryReady, freshRepo.Status)
	freshOperation := unittest.AssertExistsAndLoadBean(t, &governance_model.RepositoryCreation{ID: operation.ID})
	assert.Equal(t, "succeeded", freshOperation.State)
}
