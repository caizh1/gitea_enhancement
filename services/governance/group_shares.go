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

type GroupShareOption struct {
	PreviewToken string                `json:"preview_token,omitempty"`
	GroupID      int64                 `json:"group_id"`
	MaxRole      governance_model.Role `json:"max_role"`
	ExpiresUnix  int64                 `json:"expires_unix"`
	Revision     int64                 `json:"revision"`
}

// shareConflict 保留冲突状态码，同时向用户说明实际原因。
type shareConflict string

func (err shareConflict) Error() string { return string(err) }

func (err shareConflict) Unwrap() error { return governance_model.ErrConflict }

// checkExternalShareRestriction 只限制新建共享；已有关系保留，可单独调整或撤销。
func checkExternalShareRestriction(ctx context.Context, sourceID, invitedID int64) error {
	sourceChain, err := governance_model.Ancestors(ctx, sourceID)
	if err != nil {
		return err
	}
	invitedChain, err := governance_model.Ancestors(ctx, invitedID)
	if err != nil {
		return err
	}
	sourceRoot, invitedRoot := sourceChain[len(sourceChain)-1], invitedChain[len(invitedChain)-1]
	if sourceRoot.Kind == "group" && sourceRoot.RestrictExternalShares && sourceRoot.ID != invitedRoot.ID {
		return governance_model.ErrForbidden
	}
	return nil
}

// SetExternalShareRestriction 只允许顶级群组修改，子组和项目自动继承。
func SetExternalShareRestriction(ctx context.Context, actor governance_model.Actor, groupID, revision int64, enabled bool) error {
	return withActorWrite(ctx, actor, []string{governance_model.Resource("group", groupID)}, func(ctx context.Context) error {
		group, err := CheckGroupAccess(ctx, actor.EffectiveUserID(), groupID, governance_model.ManageGroup)
		if err != nil {
			return err
		}
		if group.ParentID != 0 || group.Revision != revision || group.Archived || group.DeleteAfter != 0 {
			return governance_model.ErrConflict
		}
		before := group.RestrictExternalShares
		if _, err := db.GetEngine(ctx).ID(groupID).Cols("restrict_external_shares", "revision").Update(&governance_model.Namespace{RestrictExternalShares: enabled, Revision: revision + 1}); err != nil {
			return err
		}
		return groupAudit(ctx, actor, group.Namespace, "group.updated", map[string]any{"setting": "restrict_external_shares", "before": before, "after": enabled})
	})
}

// SetGroupShare 共享变更与权限修订、审计在同一事务内提交。
func SetGroupShare(ctx context.Context, actor governance_model.Actor, groupID int64, option GroupShareOption, remove bool) error {
	return withActorWrite(ctx, actor, []string{governance_model.Resource("group", groupID), governance_model.Resource("group", option.GroupID)}, func(ctx context.Context) error {
		source, err := CheckGroupAccess(ctx, actor.EffectiveUserID(), groupID, governance_model.ManageGroup)
		if err != nil {
			return err
		}
		if source.DeleteAfter != 0 {
			return shareConflict("当前群组正在等待删除，不能修改共享；请先取消删除")
		}
		if source.Archived {
			return shareConflict("当前群组已归档，不能修改共享；请先取消归档")
		}
		if source.Revision != option.Revision {
			return shareConflict("群组数据已更新，当前页面已过期；请刷新页面后重新确认共享设置")
		}
		var previous governance_model.Share
		exists, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND group_id = ?", "group", groupID, option.GroupID).Get(&previous)
		if err != nil {
			return err
		}
		if exists {
			if err := auditGrantExpiry(ctx, previous.ScopeType, previous.ScopeID, previous.ExpiresUnix, "share.expired", previous, time.Now()); err != nil {
				return err
			}
		}
		next := &governance_model.Share{ID: previous.ID, ScopeType: "group", ScopeID: groupID, GroupID: option.GroupID, MaxRole: option.MaxRole, ExpiresUnix: option.ExpiresUnix}
		if remove {
			if !exists {
				return governance_model.ErrNotFound
			}
			if _, err := db.GetEngine(ctx).ID(previous.ID).Delete(new(governance_model.Share)); err != nil {
				return err
			}
		} else {
			invited, err := CheckGroupAccess(ctx, actor.EffectiveUserID(), option.GroupID, governance_model.ReadGroup)
			if err != nil {
				return err
			}
			if invited.DeleteAfter != 0 {
				return shareConflict("被邀请群组正在等待删除，不能添加或修改共享；请先取消删除")
			}
			if invited.Archived {
				return shareConflict("被邀请群组已归档，不能添加或修改共享；请先取消归档")
			}
			if !exists {
				if err := checkExternalShareRestriction(ctx, groupID, option.GroupID); err != nil {
					return err
				}
			}
			ceiling, err := governance_model.AbilitiesFor(option.MaxRole, nil)
			if err != nil {
				return err
			}
			for ability := range ceiling {
				if !source.Abilities[ability] {
					return governance_model.ErrNotFound
				}
			}
			if option.ExpiresUnix != 0 && option.ExpiresUnix <= time.Now().Unix() {
				return governance_model.ErrInvalid
			}
			if err := checkGroupShareCycle(ctx, groupID, option.GroupID); err != nil {
				return err
			}
			if exists {
				if _, err := db.GetEngine(ctx).ID(previous.ID).Cols("max_role", "expires_unix").Update(next); err != nil {
					return err
				}
			} else if err := db.Insert(ctx, next); err != nil {
				return err
			}
		}
		if _, err := db.GetEngine(ctx).ID(groupID).Incr("revision").Update(new(governance_model.Namespace)); err != nil {
			return err
		}
		kind := "share.updated"
		if !exists {
			kind = "share.created"
		} else if remove {
			kind = "share.removed"
		}
		return groupAudit(ctx, actor, source.Namespace, kind, map[string]any{"before": previous, "after": next, "removed": remove})
	})
}

// checkGroupShareCycle 同时检查父级继承与有效共享的依赖方向，禁止闭环。
func checkGroupShareCycle(ctx context.Context, sourceID, invitedID int64) error {
	if sourceID == invitedID {
		return shareConflict("不能将群组共享给自身")
	}
	chain, err := governance_model.Ancestors(ctx, invitedID)
	if err != nil {
		return err
	}
	for _, ancestor := range chain {
		if ancestor.ID == sourceID {
			return shareConflict("不能将父群组共享给自己的子群组或更深层后代群组，这会与权限继承形成循环")
		}
	}
	pending, seen := []int64{invitedID}, map[int64]bool{}
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if id == sourceID {
			return shareConflict("此操作会使群组共享与权限继承形成循环；请先调整已有共享关系")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		n, err := governance_model.GetNamespace(ctx, id)
		if err != nil {
			return err
		}
		if n.ParentID != 0 {
			pending = append(pending, n.ParentID)
		}
		var shares []*governance_model.Share
		if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND (expires_unix = 0 OR expires_unix > ?)", "group", id, time.Now().Unix()).Find(&shares); err != nil {
			return err
		}
		for _, share := range shares {
			pending = append(pending, share.GroupID)
		}
	}
	return nil
}

func ListGroupShares(ctx context.Context, actorID, groupID, afterID int64) ([]*governance_model.Share, error) {
	if _, err := CheckGroupAccess(ctx, actorID, groupID, governance_model.ManageGroup); err != nil {
		return nil, err
	}
	if afterID < 0 {
		return nil, governance_model.ErrInvalid
	}
	shares := make([]*governance_model.Share, 0)
	err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND id > ?", "group", groupID, afterID).Asc("id").Limit(100).Find(&shares)
	return shares, err
}
