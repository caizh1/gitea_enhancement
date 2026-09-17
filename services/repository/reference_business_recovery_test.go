// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"testing"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestApplyReferenceBusinessOperationRecoversBranchDeletion(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	branch := &git_model.Branch{RepoID: 1, Name: "待恢复删除", CommitID: "0123456789012345678901234567890123456789"}
	require.NoError(t, db.Insert(ctx, branch))
	operationID := uuid.NewString()
	operation := &governance_model.ReferenceTransaction{ID: uuid.NewString(), RepoID: 1, State: "committed", BusinessOperationID: operationID, BusinessOperation: "branch_delete", BusinessOldBranch: branch.Name, Actor: governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}}
	require.NoError(t, db.Insert(ctx, operation))
	require.NoError(t, db.Insert(ctx, &governance_model.ReferenceReservation{Resource: governance_model.Resource("repository", 1), TransactionID: operation.ID}))

	require.NoError(t, ApplyReferenceBusinessOperation(ctx, operationID))
	require.NoError(t, ApplyReferenceBusinessOperation(ctx, operationID), "恢复任务重复执行必须幂等")
	stored, err := git_model.GetBranch(ctx, 1, branch.Name)
	require.NoError(t, err)
	require.True(t, stored.IsDeleted)
	storedOperation, err := governance_model.GetReferenceBusinessOperation(ctx, operationID)
	require.NoError(t, err)
	require.True(t, storedOperation.BusinessApplied)
	unittest.AssertCount(t, &governance_model.ReferenceReservation{TransactionID: operation.ID}, 0)
}

func TestApplyRegisteredReferenceBusinessOperationRestoresActor(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	operationType := "test_registered_actor_" + uuid.NewString()
	want := governance_model.Actor{ID: 2, Name: "原发布者", Kind: "user", Transport: "api", RequestID: "request-1"}
	governance_model.RegisterReferenceBusinessApplier(operationType, func(ctx context.Context, _ *governance_model.ReferenceTransaction) error {
		require.Equal(t, want, governance_model.AuditActor(ctx))
		return nil
	})
	operationID := uuid.NewString()
	operation := &governance_model.ReferenceTransaction{ID: uuid.NewString(), RepoID: 1, State: "committed", BusinessOperationID: operationID, BusinessOperation: operationType, Actor: want}
	require.NoError(t, db.Insert(ctx, operation))
	require.NoError(t, db.Insert(ctx, &governance_model.ReferenceReservation{Resource: governance_model.Resource("repository", 1), TransactionID: operation.ID}))
	require.NoError(t, ApplyReferenceBusinessOperation(ctx, operationID))
}
