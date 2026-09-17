// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package access

import (
	"context"

	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/util"
)

// CheckAuditorParticipation 防止内部调用把只读身份当成可评论、评审的资源授权。
func CheckAuditorParticipation(ctx context.Context, repoID, userID int64, unitType unit.Type) error {
	auditor, err := user_model.IsActiveAuditor(ctx, userID)
	if err != nil || !auditor {
		return err
	}
	user, err := user_model.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return err
	}
	permission, err := GetIndividualUserRepoPermission(ctx, repo, user)
	if err != nil {
		return err
	}
	permission = permission.ForMutation()
	if !permission.CanRead(unitType) {
		return util.ErrPermissionDenied
	}
	return nil
}
