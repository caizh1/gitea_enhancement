// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package asymkey

import (
	"context"
	"errors"

	asymkey_model "gitea.dev/models/asymkey"
	governance_model "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/util"
	governance_service "gitea.dev/services/governance"
)

// withDeployKeyAuthorization 防止入口授权后的转移或撤权被旧请求越过。
func withDeployKeyAuthorization(ctx context.Context, actor governance_model.Actor, repoID int64, write func(context.Context, *repo_model.Repository) error) error {
	ctx = governance_model.WithAuditActor(ctx, actor)
	err := governance_service.WithActorWrite(ctx, actor, nil, func(ctx context.Context) error {
		doer, err := user_model.GetUserByID(ctx, actor.EffectiveUserID())
		if err != nil {
			return err
		}
		if !doer.IsActive || doer.ProhibitLogin || doer.IsOrganization() || doer.IsGiteaActions() || doer.IsGhost() {
			return util.ErrPermissionDenied
		}
		repo, err := repo_model.GetRepositoryByID(ctx, repoID)
		if err != nil {
			return err
		}
		permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, doer)
		if err != nil {
			return err
		}
		permission = permission.ForMutation()
		if !permission.IsAdmin() {
			return util.ErrPermissionDenied
		}
		return write(ctx, repo)
	})
	if errors.Is(err, governance_model.ErrNotFound) || repo_model.IsErrRepoNotExist(err) || user_model.IsErrUserNotExist(err) {
		return util.ErrPermissionDenied
	}
	if err != nil {
		return err
	}
	return SyncSSHKeyFiles(ctx)
}

func AddDeployKeyForActor(ctx context.Context, actor governance_model.Actor, repoID int64, name, content string, readOnly bool) (*asymkey_model.DeployKey, error) {
	var key *asymkey_model.DeployKey
	err := withDeployKeyAuthorization(ctx, actor, repoID, func(ctx context.Context, repo *repo_model.Repository) error {
		var err error
		key, err = asymkey_model.AddDeployKey(ctx, repo.ID, name, content, readOnly)
		return err
	})
	return key, err
}

func DeleteDeployKeyForActor(ctx context.Context, actor governance_model.Actor, repoID, keyID int64) error {
	return withDeployKeyAuthorization(ctx, actor, repoID, func(ctx context.Context, repo *repo_model.Repository) error {
		return DeleteDeployKey(ctx, repo, keyID)
	})
}
