// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"

	governance_model "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
)

// CheckRepositoryContentWrite must run inside the final governance write transaction.
func CheckRepositoryContentWrite(ctx context.Context, doer *user_model.User, repoID int64, unitType unit.Type) error {
	if doer == nil {
		return governance_model.ErrForbidden
	}
	if actor := governance_model.AuditActor(ctx); actor.EffectiveUserID() != 0 {
		if err := checkDelegation(ctx, actor); err != nil {
			return err
		}
	}
	if err := CheckRepositoryContentLifecycle(ctx, repoID); err != nil {
		return err
	}
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return err
	}
	currentDoer := doer
	if doer.ID > 0 {
		currentDoer, err = user_model.GetUserByID(ctx, doer.ID)
		if err != nil {
			return err
		}
		if !currentDoer.IsActive || currentDoer.ProhibitLogin {
			return governance_model.ErrForbidden
		}
	}
	permission, err := access_model.GetDoerRepoPermission(ctx, repo, currentDoer)
	if err != nil {
		return err
	}
	permission = permission.ForMutation()
	if !permission.CanWrite(unitType) {
		return governance_model.ErrForbidden
	}
	return nil
}

func CheckRepositoryContentLifecycle(ctx context.Context, repoID int64) error {
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return err
	}
	if repo.IsArchived {
		return governance_model.ErrConflict
	}
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		return err
	}
	for _, ancestor := range chain {
		if ancestor.Archived || ancestor.DeleteAfter != 0 {
			return governance_model.ErrConflict
		}
	}
	return nil
}
