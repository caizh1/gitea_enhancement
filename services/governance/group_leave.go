// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
)

// LeaveGroup 只移除操作者在该群组的直接成员关系。
func LeaveGroup(ctx context.Context, actor governance_model.Actor, groupID int64) error {
	if actor.ActingAsID != 0 {
		return governance_model.ErrNotFound
	}
	return withActorWrite(ctx, actor, []string{governance_model.Resource("group", groupID), governance_model.Resource("user", actor.ID)}, func(ctx context.Context) error {
		doer, err := activeActor(ctx, actor.ID)
		if err != nil {
			return err
		}
		group, err := governance_model.GetNamespace(ctx, groupID)
		if err != nil {
			return err
		}
		if group.Kind != "group" {
			return governance_model.ErrNotFound
		}
		var member governance_model.Membership
		has, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "group", groupID, actor.ID).Get(&member)
		if err != nil {
			return err
		}
		if !has {
			return governance_model.ErrNotFound
		}
		if _, err := db.GetEngine(ctx).ID(member.ID).Delete(new(governance_model.Membership)); err != nil {
			return err
		}
		if err := requirePermanentOwner(ctx, groupID); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).ID(groupID).Incr("revision").Update(new(governance_model.Namespace)); err != nil {
			return err
		}
		return groupSubjectAudit(ctx, actor, group, "member.removed", "user", actor.ID, doer.Name, map[string]any{"before": member, "after": nil, "source": "direct", "removed": true, "group_path": group.FullPath})
	})
}
