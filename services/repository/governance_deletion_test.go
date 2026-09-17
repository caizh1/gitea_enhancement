// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestRepositoryDeletionRestoresPathAndState(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo, err := repo_model.GetRepositoryByID(ctx, 1)
	require.NoError(t, err)
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	path, gitPath, wikiPath := repo.FullPath(), repo.RelativePath(), repo.WikiStorageRepo()
	bad := actor
	bad.Transport = ""
	_, err = ScheduleRepositoryDeletion(ctx, bad, repo.ID, DeletionOption{ConfirmationPath: path})
	require.Error(t, err, "审计故障必须回滚名称与归档")
	fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	require.Equal(t, path, fresh.FullPath())
	require.False(t, fresh.IsArchived)
	schedule, err := ScheduleRepositoryDeletion(ctx, actor, repo.ID, DeletionOption{ConfirmationPath: path})
	require.NoError(t, err)
	fresh, err = repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	require.True(t, fresh.IsArchived)
	require.Contains(t, fresh.Name, "-deletion-")
	require.Equal(t, gitPath, fresh.RelativePath())
	require.Equal(t, wikiPath, fresh.WikiStorageRepo())
	require.ErrorIs(t, repo_model.SetArchiveRepoState(ctx, fresh, false), governance_model.ErrConflict)
	pendingPath := fresh.FullPath()
	require.ErrorIs(t, RestoreRepositoryDeletion(ctx, actor, repo.ID, DeletionOption{ConfirmationPath: pendingPath, DueUnix: schedule.DueUnix - 1}), governance_model.ErrConflict)
	require.NoError(t, RestoreRepositoryDeletion(ctx, actor, repo.ID, DeletionOption{ConfirmationPath: pendingPath, DueUnix: schedule.DueUnix}))
	fresh, err = repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	require.False(t, fresh.IsArchived)
	require.Equal(t, path, fresh.FullPath())
	require.Equal(t, gitPath, fresh.RelativePath())
	unittest.AssertCount(t, &governance_model.RepositoryDeletion{RepositoryID: repo.ID}, 0)
	require.NoError(t, repo_model.SetArchiveRepoState(ctx, fresh, true))
	schedule, err = ScheduleRepositoryDeletion(ctx, actor, repo.ID, DeletionOption{ConfirmationPath: path})
	require.NoError(t, err)
	fresh, err = repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	require.NoError(t, RestoreRepositoryDeletion(ctx, actor, repo.ID, DeletionOption{ConfirmationPath: fresh.FullPath(), DueUnix: schedule.DueUnix}))
	fresh, err = repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	require.True(t, fresh.IsArchived, "恢复不能解除此前独立归档")
}

func TestRepositoryDeletionRevokedInitiatorRestores(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo, err := repo_model.GetRepositoryByID(ctx, 1)
	require.NoError(t, err)
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	_, err = ScheduleRepositoryDeletion(ctx, actor, repo.ID, DeletionOption{ConfirmationPath: repo.FullPath()})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(2).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: true})
	require.NoError(t, err)
	require.NoError(t, DeleteScheduledRepository(ctx, repo.ID, time.Now().Add(31*24*time.Hour), nil, DeletionOption{}))
	fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	require.Equal(t, repo.Name, fresh.Name)
	require.False(t, fresh.IsArchived)
	unittest.AssertCount(t, &governance_model.RepositoryDeletion{RepositoryID: repo.ID}, 0)
}

func TestRepositoryDeletionOuterRollback(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo, err := repo_model.GetRepositoryByID(ctx, 1)
	require.NoError(t, err)
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	require.ErrorIs(t, db.WithTx(ctx, func(ctx context.Context) error {
		_, err := ScheduleRepositoryDeletion(ctx, actor, repo.ID, DeletionOption{ConfirmationPath: repo.FullPath()})
		if err != nil {
			return err
		}
		return governance_model.ErrConflict
	}), governance_model.ErrConflict)
	fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	require.Equal(t, repo.Name, fresh.Name)
	require.Empty(t, fresh.GovernanceStorageName)
	require.False(t, fresh.IsArchived)
	unittest.AssertCount(t, &governance_model.RepositoryDeletion{RepositoryID: repo.ID}, 0)
}

func TestRepositoryDeletionClosesExternalPulls(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 1, Kind: "user", Name: "user1", Transport: "api"}
	repo, err := repo_model.GetRepositoryByID(ctx, 11)
	require.NoError(t, err)
	schedule, err := ScheduleRepositoryDeletion(ctx, actor, repo.ID, DeletionOption{ConfirmationPath: repo.FullPath()})
	require.NoError(t, err)
	fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	option := DeletionOption{ConfirmationPath: fresh.FullPath(), DueUnix: schedule.DueUnix}
	bad := actor
	bad.Transport = ""
	require.Error(t, DeleteScheduledRepository(ctx, repo.ID, time.Now(), &bad, option))
	pull := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 8})
	require.False(t, pull.IsClosed, "删除审计失败必须回滚外部 PR 状态")
	require.NoError(t, DeleteScheduledRepository(ctx, repo.ID, time.Now(), &actor, option))
	pull = unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 8})
	require.True(t, pull.IsClosed)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "pull.closed_by_repository_deletion", ObjectID: 3}, 1)
}
