// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestRepositoryUpdatesRespectFinalMergeAuthorization(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	op := &governance_model.MergeAuthorization{PullID: 2, RepoID: repo.ID, Head: "提交", OldTarget: "旧", NewTarget: "新", Branch: "master", Actor: governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, op, nil, func(context.Context) error { return nil }))
	repo.IsPrivate = true
	require.ErrorIs(t, UpdateRepository(ctx, repo, true), governance_model.ErrConflict)
	require.ErrorIs(t, MakeRepoPrivate(ctx, repo, true), governance_model.ErrConflict)
	require.ErrorIs(t, UpdateRepositoryUnits(ctx, repo, nil, []unit.Type{unit.TypePullRequests}), governance_model.ErrConflict)
	stored := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	require.False(t, stored.IsPrivate)
	_, err := governance_model.ReconcileMerge(ctx, op.ID, "旧")
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(repo.ID).Cols("name", "lower_name").Update(&repo_model.Repository{Name: "new-name", LowerName: "new-name"})
	require.NoError(t, err)
	require.ErrorIs(t, UpdateRepository(ctx, repo, false), governance_model.ErrConflict, "旧页面不能把移动或改名后的仓库写回旧路径")
}
