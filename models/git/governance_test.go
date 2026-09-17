// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtectedBranchMutationsRespectMergeReservation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	rule := &ProtectedBranch{RepoID: repo.ID, RuleName: "治理验收", Priority: 3, ApprovalsWhitelistUserIDs: []int64{2}}
	require.NoError(t, db.Insert(ctx, rule))
	operation := &governance_model.MergeAuthorization{PullID: 999, RepoID: repo.ID, Branch: "main", Head: "head", OldTarget: "before", NewTarget: "after", Actor: governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, nil, func(context.Context) error { return nil }))
	assert.ErrorIs(t, UpdateProtectBranch(ctx, repo, rule, WhitelistOptions{}), governance_model.ErrConflict)
	assert.ErrorIs(t, UpdateProtectBranchPriorities(ctx, repo, []int64{rule.ID}), governance_model.ErrConflict)
	assert.ErrorIs(t, DeleteProtectedBranch(ctx, repo, rule.ID), governance_model.ErrConflict)
	assert.ErrorIs(t, RemoveUserIDFromProtectedBranch(ctx, rule, 2), governance_model.ErrConflict)
	current := unittest.AssertExistsAndLoadBean(t, &ProtectedBranch{ID: rule.ID})
	assert.Equal(t, int64(3), current.Priority)
	assert.Equal(t, []int64{2}, current.ApprovalsWhitelistUserIDs)
	_, err := governance_model.ReconcileMerge(ctx, operation.ID, "before")
	require.NoError(t, err)
	require.NoError(t, UpdateProtectBranchPriorities(ctx, repo, []int64{rule.ID}))
	require.NoError(t, RemoveUserIDFromProtectedBranch(ctx, rule, 2))
	current = unittest.AssertExistsAndLoadBean(t, &ProtectedBranch{ID: rule.ID})
	assert.Equal(t, int64(1), current.Priority)
	assert.Empty(t, current.ApprovalsWhitelistUserIDs)
	require.NoError(t, DeleteProtectedBranch(ctx, repo, rule.ID))
	unittest.AssertNotExistsBean(t, &ProtectedBranch{ID: rule.ID})
}
