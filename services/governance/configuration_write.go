// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/util"
)

// WithConfigurationWrite 在配置持久化事务内复核当前管理权和资源生命周期。
func WithConfigurationWrite(ctx context.Context, actor governance_model.Actor, ownerID, repoID int64, write func(context.Context) error) error {
	return withConfigurationWrite(ctx, actor, ownerID, repoID, false, write)
}

// WithActionsWorkflowWrite 保留原生 Actions 写者启停工作流的权限边界。
func WithActionsWorkflowWrite(ctx context.Context, actor governance_model.Actor, repoID int64, write func(context.Context) error) error {
	if repoID <= 0 {
		return util.ErrInvalidArgument
	}
	return withConfigurationWrite(ctx, actor, 0, repoID, true, write)
}

func withConfigurationWrite(ctx context.Context, actor governance_model.Actor, ownerID, repoID int64, allowActionsWriter bool, write func(context.Context) error) error {
	if ownerID < 0 || repoID < 0 || ownerID != 0 && repoID != 0 || write == nil {
		return util.ErrInvalidArgument
	}
	if actor.EffectiveUserID() <= 0 {
		return util.ErrPermissionDenied
	}
	resources := []string{governance_model.Resource("instance", 0)}
	if repoID > 0 {
		resources = []string{governance_model.Resource("repository", repoID)}
	} else if ownerID > 0 {
		resources = []string{governance_model.Resource("group", ownerID), governance_model.Resource("user", ownerID)}
	}
	err := WithActorWrite(governance_model.WithAuditActor(ctx, actor), actor, resources, func(tx context.Context) error {
		doer, err := activeActor(tx, actor.EffectiveUserID())
		if err != nil {
			return err
		}
		allowed := doer.IsAdmin
		switch {
		case repoID > 0:
			repo, err := repo_model.GetRepositoryByID(tx, repoID)
			if err != nil {
				return err
			}
			if repo.IsArchived {
				return governance_model.ErrConflict
			}
			if _, pending, err := db.GetByID[governance_model.RepositoryDeletion](tx, repoID); err != nil {
				return err
			} else if pending {
				return governance_model.ErrConflict
			}
			if err := checkConfigurationOwnerLifecycle(tx, repo.OwnerID); err != nil {
				return err
			}
			if !allowed {
				permission, err := access_model.GetIndividualUserRepoPermission(tx, repo, doer)
				if err != nil {
					return err
				}
				permission = permission.ForMutation()
				allowed = permission.IsAdmin() || allowActionsWriter && permission.CanWrite(unit.TypeActions)
			}
		case ownerID > 0:
			owner, err := user_model.GetUserByID(tx, ownerID)
			if err != nil {
				return err
			}
			if err := checkConfigurationOwnerLifecycle(tx, ownerID); err != nil {
				return err
			}
			if owner.IsOrganization() {
				if !allowed {
					allowed, err = organization.IsOrganizationOwner(tx, ownerID, doer.ID)
					if err != nil {
						return err
					}
				}
			} else {
				allowed = allowed || ownerID == doer.ID
			}
		}
		if !allowed {
			return util.ErrPermissionDenied
		}
		return write(tx)
	})
	if errors.Is(err, governance_model.ErrNotFound) || errors.Is(err, governance_model.ErrForbidden) || repo_model.IsErrRepoNotExist(err) || user_model.IsErrUserNotExist(err) {
		return util.ErrPermissionDenied
	}
	return err
}

func checkConfigurationOwnerLifecycle(ctx context.Context, ownerID int64) error {
	chain, err := governance_model.Ancestors(ctx, ownerID)
	if err != nil {
		return err
	}
	for _, scope := range chain {
		if scope.Archived || scope.DeleteAfter != 0 {
			return governance_model.ErrConflict
		}
	}
	return nil
}
