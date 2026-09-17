// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package repo

import (
	"context"
	"testing"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryConfigurationRespectsMergeReservation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	repo := unittest.AssertExistsAndLoadBean(t, &Repository{ID: 1})
	unit := unittest.AssertExistsAndLoadBean(t, &RepoUnit{ID: 1})
	operation := &governance_model.MergeAuthorization{PullID: 999, RepoID: 1, Branch: "main", Head: "head", OldTarget: "before", NewTarget: "after", Actor: governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, nil, func(context.Context) error { return nil }))
	repo.IsArchived = true
	assert.ErrorIs(t, UpdateRepositoryColsWithAutoTime(ctx, repo, "is_archived"), governance_model.ErrConflict)
	assert.ErrorIs(t, UpdateRepositoryColsNoAutoTime(ctx, repo, "is_private"), governance_model.ErrConflict)
	assert.ErrorIs(t, UpdateRepoUnitConfig(ctx, unit), governance_model.ErrConflict)
	assert.ErrorIs(t, UpdateRepoUnitPublicAccess(ctx, unit), governance_model.ErrConflict)
	current := unittest.AssertExistsAndLoadBean(t, &Repository{ID: 1})
	assert.False(t, current.IsArchived)
	require.NoError(t, UpdateRepositoryColsNoAutoTime(ctx, repo, "description"), "无关展示信息不改变合并依据")
	_, err := governance_model.ReconcileMerge(ctx, operation.ID, "before")
	require.NoError(t, err)
	require.NoError(t, UpdateRepositoryColsWithAutoTime(ctx, repo, "is_archived"))
	current = unittest.AssertExistsAndLoadBean(t, &Repository{ID: 1})
	assert.True(t, current.IsArchived)
}

func TestNativeArchiveRespectsMergeReservation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	repo := unittest.AssertExistsAndLoadBean(t, &Repository{ID: 1})
	operation := &governance_model.MergeAuthorization{PullID: 999, RepoID: 1, Branch: "main", Head: "head", OldTarget: "before", NewTarget: "after", Actor: governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, nil, func(context.Context) error { return nil }))
	assert.ErrorIs(t, SetArchiveRepoState(ctx, repo, true), governance_model.ErrConflict, "原生网页归档不能越过已取得的最终合并授权")
	assert.False(t, repo.IsArchived, "被拒绝的归档不能改变调用者状态")
	current := unittest.AssertExistsAndLoadBean(t, &Repository{ID: 1})
	assert.False(t, current.IsArchived, "被拒绝的归档不能提交数据库")
	_, err := governance_model.ReconcileMerge(ctx, operation.ID, "before")
	require.NoError(t, err)
	require.NoError(t, SetArchiveRepoState(ctx, repo, true))
	assert.True(t, repo.IsArchived)
	assert.Positive(t, repo.ArchivedUnix)
	current = unittest.AssertExistsAndLoadBean(t, &Repository{ID: 1})
	assert.True(t, current.IsArchived)
	require.NoError(t, SetArchiveRepoState(ctx, repo, false))
	assert.False(t, repo.IsArchived)
	assert.Zero(t, repo.ArchivedUnix)
	current = unittest.AssertExistsAndLoadBean(t, &Repository{ID: 1})
	assert.False(t, current.IsArchived)
	assert.Zero(t, current.ArchivedUnix)
}

func TestNativeArchiveAuditAtomicity(t *testing.T) {
	unittest.PrepareTestEnv(t)
	repo := unittest.AssertExistsAndLoadBean(t, &Repository{ID: 1})
	invalidActor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user"}
	require.ErrorIs(t, SetArchiveRepoState(governance_model.WithAuditActor(t.Context(), invalidActor), repo, true), governance_model.ErrInvalid)
	assert.False(t, repo.IsArchived)
	current := unittest.AssertExistsAndLoadBean(t, &Repository{ID: 1})
	assert.False(t, current.IsArchived, "审计失败必须回滚归档状态")
	unittest.AssertNotExistsBean(t, &governance_model.AuditEvent{Type: "repository.archived", ObjectID: repo.ID})
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web", IP: "192.0.2.5", RequestID: "归档审计验收"}
	ctx := governance_model.WithAuditActor(t.Context(), actor)
	require.NoError(t, SetArchiveRepoState(ctx, repo, true))
	event := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "repository.archived", ObjectID: repo.ID})
	assert.Equal(t, actor, event.Actor)
	assert.Equal(t, "user2/repo1", event.ObjectPath)
	assert.JSONEq(t, `{"before":false,"after":true}`, string(event.Details))
	archivedAt := repo.ArchivedUnix
	require.NoError(t, SetArchiveRepoState(ctx, repo, true))
	assert.Equal(t, archivedAt, repo.ArchivedUnix, "重复归档不能改写归档时间")
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "repository.archived", ObjectID: repo.ID}, 1)
	require.NoError(t, SetArchiveRepoState(ctx, repo, false))
	event = unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "repository.unarchived", ObjectID: repo.ID})
	assert.Equal(t, actor, event.Actor)
	assert.JSONEq(t, `{"before":true,"after":false}`, string(event.Details))
}
