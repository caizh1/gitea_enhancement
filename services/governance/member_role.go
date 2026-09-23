// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
)

// checkMemberRole 直接添加、批准申请与邮件邀请共用能力上限，不能按角色数值比较。
func checkMemberRole(ctx context.Context, scope string, ownerID int64, option GroupMemberOption, ceiling governance_model.Abilities) error {
	if scope == "repository" && option.Role == governance_model.MinimalAccess {
		return governance_model.ErrInvalid
	}
	if option.ExpiresUnix != 0 && option.ExpiresUnix <= time.Now().Unix() {
		return governance_model.ErrInvalid
	}
	if scope == "repository" && option.Role == governance_model.Owner && !ceiling[repositoryOwnerAuthority] {
		return governance_model.ErrNotFound
	}
	var extra []string
	if option.CustomRoleID != 0 {
		role, has, err := db.GetByID[governance_model.CustomRole](ctx, option.CustomRoleID)
		if err != nil {
			return err
		}
		chain, err := governance_model.Ancestors(ctx, ownerID)
		if err != nil {
			return err
		}
		if !has || role.RootID != chain[len(chain)-1].ID || role.BaseRole != option.Role {
			return governance_model.ErrInvalid
		}
		extra = role.Abilities
	}
	allowed, err := governance_model.AbilitiesFor(option.Role, extra)
	if err != nil {
		return err
	}
	for ability := range allowed {
		if !ceiling[ability] {
			return governance_model.ErrNotFound
		}
	}
	return nil
}
