// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"
	"fmt"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/globallock"
)

var ErrRequiredWorkflowDefaultBranch = errors.New("该仓库仍提供必选工作流，请由策略来源管理员先解除必选，再修改默认分支并重新确认策略")

// LockRepositoryWorking 将工作目录变更与必选来源的启用串行化，不持有数据库事务。
func LockRepositoryWorking(ctx context.Context, repoID int64) (globallock.ReleaseFunc, error) {
	return globallock.Lock(ctx, getRepoWorkingLockKey(repoID))
}

func refreshBranchMutationRepository(ctx context.Context, repo *repo_model.Repository) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
		if err != nil {
			return err
		}
		if fresh.OwnerID != repo.OwnerID || fresh.Name != repo.Name || fresh.ActionsScopeRevision != repo.ActionsScopeRevision {
			return fmt.Errorf("%w：仓库归属或路径已变化，请刷新后重试", governance_model.ErrConflict)
		}
		if err := fresh.MustNotBeArchived(); err != nil {
			return err
		}
		repo.DefaultBranch = fresh.DefaultBranch
		return nil
	})
}

func checkRequiredWorkflowDefaultBranch(ctx context.Context, repoID int64) error {
	sources, err := db.Find[actions_model.ActionScopedWorkflowSource](ctx, actions_model.FindScopedWorkflowSourceOpts{SourceRepoID: repoID})
	if err != nil {
		return err
	}
	for _, source := range sources {
		for _, config := range source.WorkflowConfigs {
			if config != nil && config.Required {
				return ErrRequiredWorkflowDefaultBranch
			}
		}
	}
	return nil
}
