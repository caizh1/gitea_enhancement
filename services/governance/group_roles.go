// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
)

type GroupRoleOption struct {
	Name      string                `json:"name"`
	BaseRole  governance_model.Role `json:"base_role"`
	Abilities []string              `json:"abilities"`
	Revision  int64                 `json:"revision"`
}

// SaveGroupRole 自定义角色在顶级群组定义；修改立即影响使用此角色的后代成员。
func SaveGroupRole(ctx context.Context, actor governance_model.Actor, rootID, roleID int64, option GroupRoleOption, remove bool) (*governance_model.CustomRole, error) {
	var result *governance_model.CustomRole
	err := withActorWrite(ctx, actor, []string{governance_model.Resource("group", rootID), governance_model.Resource("custom_role", roleID)}, func(ctx context.Context) error {
		root, err := CheckGroupAccess(ctx, actor.EffectiveUserID(), rootID, governance_model.ManageGroup)
		if err != nil {
			namespace, namespaceErr := governance_model.GetNamespace(ctx, rootID)
			user, userErr := activeActor(ctx, actor.EffectiveUserID())
			if namespaceErr != nil || userErr != nil || namespace.Kind != "user" || (!user.IsAdmin && user.ID != rootID) {
				return err
			}
			abilities, _ := governance_model.AbilitiesFor(governance_model.Owner, nil)
			root = &GroupState{Namespace: namespace, Abilities: abilities}
		}
		if root.ParentID != 0 || root.Revision != option.Revision || root.Archived || root.DeleteAfter != 0 {
			return governance_model.ErrConflict
		}
		var previous governance_model.CustomRole
		if roleID != 0 {
			has, err := db.GetEngine(ctx).ID(roleID).Get(&previous)
			if err != nil {
				return err
			}
			if !has || previous.RootID != rootID {
				return governance_model.ErrNotFound
			}
		} else if remove {
			return governance_model.ErrNotFound
		}
		if remove {
			used, err := customRoleInUse(ctx, roleID)
			if err != nil {
				return err
			}
			if used {
				return governance_model.ErrConflict
			}
			if _, err := db.GetEngine(ctx).ID(roleID).Delete(new(governance_model.CustomRole)); err != nil {
				return err
			}
		} else {
			name := strings.TrimSpace(option.Name)
			if name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsAny(name, "\r\n\x00") {
				return governance_model.ErrInvalid
			}
			abilities, err := governance_model.AbilitiesFor(option.BaseRole, option.Abilities)
			if err != nil {
				return err
			}
			for ability := range abilities {
				if !root.Abilities[ability] {
					return governance_model.ErrNotFound
				}
			}
			// 已有授权的基础角色不能静默改变，否则其声明角色与实际能力会不一致。
			if roleID != 0 && previous.BaseRole != option.BaseRole {
				used, err := customRoleInUse(ctx, roleID)
				if err != nil {
					return err
				}
				if used {
					return governance_model.ErrConflict
				}
			}
			extras := slices.Clone(option.Abilities)
			slices.Sort(extras)
			extras = slices.Compact(extras)
			result = &governance_model.CustomRole{ID: roleID, RootID: rootID, Name: name, BaseRole: option.BaseRole, Abilities: extras}
			if roleID == 0 {
				if err := db.Insert(ctx, result); err != nil {
					return err
				}
			} else if _, err := db.GetEngine(ctx).ID(roleID).Cols("name", "base_role", "abilities").Update(result); err != nil {
				return err
			}
		}
		if _, err := db.GetEngine(ctx).ID(rootID).Incr("revision").Update(new(governance_model.Namespace)); err != nil {
			return err
		}
		kind := "role.updated"
		if roleID == 0 {
			kind = "role.created"
		}
		if remove {
			kind = "role.removed"
		}
		auditRole := result
		if auditRole == nil {
			auditRole = &previous
		}
		return groupSubjectAudit(ctx, actor, root.Namespace, kind, "custom_role", auditRole.ID, root.FullPath+"/roles/"+auditRole.Name, map[string]any{"before": previous, "after": result, "removed": remove})
	})
	return result, err
}

// SaveRepositoryRootRole 管理个人命名空间项目转入后使用的实例级自定义角色。
func SaveRepositoryRootRole(ctx context.Context, actor governance_model.Actor, repoID, roleID int64, option GroupRoleOption, remove bool) (*governance_model.CustomRole, error) {
	repo, _, err := repositoryMemberManager(ctx, actor.EffectiveUserID(), repoID)
	if err != nil {
		return nil, err
	}
	root, err := governance_model.GetNamespace(ctx, repo.OwnerID)
	if err != nil {
		return nil, err
	}
	if root.Kind != "user" {
		return nil, governance_model.ErrNotFound
	}
	return SaveGroupRole(ctx, actor, root.ID, roleID, option, remove)
}

func customRoleInUse(ctx context.Context, roleID int64) (bool, error) {
	used, err := db.GetEngine(ctx).Where("custom_role_id = ?", roleID).Exist(new(governance_model.Membership))
	if err != nil || used {
		return used, err
	}
	return db.GetEngine(ctx).Where("custom_role_id = ? AND expires_unix > ?", roleID, time.Now().Unix()).Exist(new(governance_model.Invitation))
}

func ListGroupRoles(ctx context.Context, actorID, groupID, afterID int64) ([]*governance_model.CustomRole, error) {
	if _, err := CheckGroupAccess(ctx, actorID, groupID, governance_model.ManageGroupMembers); err != nil {
		return nil, err
	}
	if afterID < 0 {
		return nil, governance_model.ErrInvalid
	}
	chain, err := governance_model.Ancestors(ctx, groupID)
	if err != nil {
		return nil, err
	}
	roles := make([]*governance_model.CustomRole, 0)
	err = db.GetEngine(ctx).Where("root_id = ? AND id > ?", chain[len(chain)-1].ID, afterID).Asc("id").Limit(100).Find(&roles)
	return roles, err
}
