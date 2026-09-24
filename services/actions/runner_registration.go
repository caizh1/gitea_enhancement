// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"

	actions_model "gitea.dev/models/actions"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/util"
	governance_service "gitea.dev/services/governance"
)

// GetRunnerRegistrationToken 将当前管理权和令牌披露与转移、撤权排列在同一短事务中。
func GetRunnerRegistrationToken(ctx context.Context, actor governance_model.Actor, ownerID, repoID int64, rotate bool) (*actions_model.ActionRunnerToken, error) {
	if ownerID < 0 || repoID < 0 || (ownerID != 0 && repoID != 0) {
		return nil, util.ErrInvalidArgument
	}
	if actor.EffectiveUserID() <= 0 {
		return nil, util.ErrPermissionDenied
	}
	ctx = governance_model.WithAuditActor(ctx, actor)
	var token *actions_model.ActionRunnerToken
	err := governance_service.WithActorWrite(ctx, actor, nil, func(ctx context.Context) error {
		doer, err := user_model.GetUserByID(ctx, actor.EffectiveUserID())
		if user_model.IsErrUserNotExist(err) {
			return util.ErrPermissionDenied
		}
		if err != nil {
			return err
		}
		if !doer.IsActive || doer.ProhibitLogin || doer.IsOrganization() || doer.IsGiteaActions() || doer.IsGhost() {
			return util.ErrPermissionDenied
		}
		allowed := doer.IsAdmin
		switch {
		case repoID != 0:
			repo, err := repo_model.GetRepositoryByID(ctx, repoID)
			if err != nil {
				return err
			}
			permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, doer)
			if err != nil {
				return err
			}
			permission = permission.ForMutation()
			allowed = permission.IsAdmin()
		case ownerID != 0:
			owner, err := user_model.GetUserByID(ctx, ownerID)
			if err != nil {
				return err
			}
			if !allowed && owner.IsOrganization() {
				allowed, err = organization.IsOrganizationOwner(ctx, ownerID, doer.ID)
				if err != nil {
					return err
				}
			} else if !allowed {
				allowed = ownerID == doer.ID
			}
		}
		if !allowed {
			return util.ErrPermissionDenied
		}
		if !rotate {
			token, err = actions_model.GetLatestRunnerToken(ctx, ownerID, repoID)
			if err == nil || !errors.Is(err, util.ErrNotExist) {
				return err
			}
		}
		token, err = actions_model.NewRunnerToken(ctx, ownerID, repoID)
		return err
	})
	if errors.Is(err, governance_model.ErrNotFound) || repo_model.IsErrRepoNotExist(err) || user_model.IsErrUserNotExist(err) {
		return nil, util.ErrPermissionDenied
	}
	if err != nil {
		return nil, err
	}
	return token, nil
}
