// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
)

type GroupBranchProtectionInput struct {
	ID             int64
	GroupID        int64
	Revision       int64
	RuleName       string
	PushRole       governance_model.Role
	MergeRole      governance_model.Role
	AllowForcePush bool
	Delete         bool
}

// SaveGroupBranchProtection checks current ownership and in-flight Git writes in one short transaction.
func SaveGroupBranchProtection(ctx context.Context, actor governance_model.Actor, input GroupBranchProtectionInput) (*git_model.GroupProtectedBranch, error) {
	if input.GroupID <= 0 || input.ID < 0 || input.Revision <= 0 || !input.Delete && (!git_model.ValidGroupProtectionPattern(input.RuleName) || !git_model.ValidGroupProtectionRole(input.PushRole) || !git_model.ValidGroupProtectionRole(input.MergeRole)) {
		return nil, governance_model.ErrInvalid
	}
	var saved *git_model.GroupProtectedBranch
	err := governance_model.WithWrite(governance_model.WithAuditActor(ctx, actor), nil, func(tx context.Context) error {
		state, err := CheckGroupAccess(tx, actor.EffectiveUserID(), input.GroupID, governance_model.ManageGroup)
		if err != nil {
			return err
		}
		group := state.Namespace
		if group.Kind != "group" || group.ParentID != 0 || group.Archived || group.DeleteAfter != 0 || group.Revision != input.Revision {
			return governance_model.ErrConflict
		}
		_, repos, err := groupResourceTree(tx, group)
		if err != nil {
			return err
		}
		for start := 0; start < len(repos); start += 250 {
			end := min(start+250, len(repos))
			resources := make([]string, 0, end-start)
			for _, repo := range repos[start:end] {
				resources = append(resources, governance_model.Resource("repository", repo.ID))
			}
			for _, reservation := range []any{new(governance_model.ReferenceReservation), new(governance_model.Reservation)} {
				busy, err := db.GetEngine(tx).In("resource", resources).Exist(reservation)
				if err != nil {
					return err
				}
				if busy {
					return fmt.Errorf("%w：子树仓库仍有未完成的引用写入或合并，请完成或核对后再修改保护规则", governance_model.ErrConflict)
				}
			}
		}
		if !input.Delete {
			var duplicate git_model.GroupProtectedBranch
			has, err := db.GetEngine(tx).Where("group_id = ? AND rule_name = ?", group.ID, input.RuleName).Get(&duplicate)
			if err != nil {
				return err
			}
			if has && duplicate.ID != input.ID {
				return fmt.Errorf("%w：同名保护规则已存在", governance_model.ErrConflict)
			}
		}
		if input.ID > 0 {
			var existing git_model.GroupProtectedBranch
			has, err := db.GetEngine(tx).ID(input.ID).Where("group_id = ?", group.ID).Get(&existing)
			if err != nil {
				return err
			}
			if !has {
				return governance_model.ErrNotFound
			}
			if input.Delete {
				if _, err := db.GetEngine(tx).ID(existing.ID).Delete(new(git_model.GroupProtectedBranch)); err != nil {
					return err
				}
				saved = &existing
			} else {
				existing.RuleName, existing.PushRole, existing.MergeRole, existing.AllowForcePush = input.RuleName, input.PushRole, input.MergeRole, input.AllowForcePush
				if _, err := db.GetEngine(tx).ID(existing.ID).Cols("rule_name", "push_role", "merge_role", "allow_force_push").Update(&existing); err != nil {
					return err
				}
				saved = &existing
			}
		} else {
			if input.Delete {
				return governance_model.ErrInvalid
			}
			saved = &git_model.GroupProtectedBranch{GroupID: group.ID, RuleName: input.RuleName, PushRole: input.PushRole, MergeRole: input.MergeRole, AllowForcePush: input.AllowForcePush}
			if err := db.Insert(tx, saved); err != nil {
				return err
			}
		}
		changed, err := db.GetEngine(tx).ID(group.ID).Where("revision = ?", group.Revision).Incr("revision").Update(new(governance_model.Namespace))
		if err != nil {
			return err
		}
		if changed != 1 {
			return governance_model.ErrConflict
		}
		action := "save"
		if input.Delete {
			action = "delete"
		}
		return groupSubjectAudit(tx, actor, group, "group.updated", "group_branch_protection", saved.ID, saved.RuleName, map[string]any{"action": action, "rule_name": saved.RuleName, "push_role": saved.PushRole, "merge_role": saved.MergeRole, "allow_force_push": saved.AllowForcePush})
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

func ListGroupBranchProtections(ctx context.Context, actorID, groupID int64) ([]*git_model.GroupProtectedBranch, *GroupState, error) {
	state, err := CheckGroupAccess(ctx, actorID, groupID, governance_model.ReadGroup)
	if err != nil {
		return nil, nil, err
	}
	if state.ParentID != 0 {
		return nil, nil, governance_model.ErrNotFound
	}
	rules, err := git_model.FindGroupProtectedBranchRules(ctx, groupID)
	return rules, state, err
}
