// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"errors"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
)

// RequireWebhookManager 必须在治理写事务内调用，避免已撤权的请求修改端点或重投历史。
func RequireWebhookManager(ctx context.Context, hook *Webhook) error {
	actor := governance_model.AuditActor(ctx)
	if actor.Kind == "system" {
		return nil
	}
	if actor.ActingAsID > 0 {
		admin, err := user_model.GetUserByID(ctx, actor.ID)
		if err != nil {
			return err
		}
		if !admin.IsAdmin || !admin.IsActive || admin.ProhibitLogin {
			return governance_model.ErrForbidden
		}
	}
	doer, err := user_model.GetUserByID(ctx, actor.EffectiveUserID())
	if err != nil {
		return err
	}
	if !doer.IsActive || doer.ProhibitLogin || doer.IsOrganization() || doer.IsGiteaActions() {
		return governance_model.ErrForbidden
	}
	if hook.RepoID != 0 {
		repo, err := repo_model.GetRepositoryByID(ctx, hook.RepoID)
		if err != nil {
			return err
		}
		permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, doer)
		if err != nil {
			return err
		}
		permission = permission.ForMutation()
		if !permission.IsAdmin() {
			return governance_model.ErrForbidden
		}
		return nil
	}
	if hook.OwnerID == 0 {
		if !doer.IsAdmin {
			return governance_model.ErrForbidden
		}
		return nil
	}
	owner, err := user_model.GetUserByID(ctx, hook.OwnerID)
	if err != nil {
		return err
	}
	chain, err := governance_model.Ancestors(ctx, owner.ID)
	if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
		return err
	}
	for _, namespace := range chain {
		if namespace.Archived || namespace.DeleteAfter != 0 {
			return governance_model.ErrConflict
		}
	}
	allowed := doer.IsAdmin || owner.ID == doer.ID
	if !allowed && owner.IsOrganization() {
		allowed, err = organization.IsOrganizationOwner(ctx, owner.ID, doer.ID)
		if err != nil {
			return err
		}
	}
	if !allowed {
		return governance_model.ErrForbidden
	}
	return nil
}
