// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
)

type ApprovalPolicies struct {
	ScopeType string                           `json:"scope_type"`
	ScopeID   int64                            `json:"scope_id"`
	FullPath  string                           `json:"full_path"`
	Rules     []*governance_model.ApprovalRule `json:"rules"`
}

func checkApprovalPolicyAccess(ctx context.Context, actorID int64, scope string, id int64) (string, error) {
	actor, err := activeActor(ctx, actorID)
	if err != nil {
		return "", err
	}
	if scope == "instance" && id == 0 && actor.IsAdmin {
		return "实例", nil
	}
	if scope != "group" || id <= 0 {
		return "", governance_model.ErrNotFound
	}
	group, err := CheckGroupAccess(ctx, actorID, id, governance_model.ManageGroup)
	if err != nil {
		return "", err
	}
	if group.Archived || group.DeleteAfter != 0 {
		return "", governance_model.ErrConflict
	}
	return group.FullPath, nil
}

func ListApprovalPolicies(ctx context.Context, actorID int64, scope string, id int64) (*ApprovalPolicies, error) {
	path, err := checkApprovalPolicyAccess(ctx, actorID, scope, id)
	if err != nil {
		return nil, err
	}
	result := &ApprovalPolicies{ScopeType: scope, ScopeID: id, FullPath: path, Rules: []*governance_model.ApprovalRule{}}
	err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", scope, id).Asc("id").Find(&result.Rules)
	return result, err
}

// SaveApprovalPolicy 祖先策略始终独立叠加，不复制到可编辑的 PR 默认规则中。
func SaveApprovalPolicy(ctx context.Context, actor governance_model.Actor, scope string, scopeID, ruleID, revision int64, input governance_model.ApprovalRule, remove bool) (*governance_model.ApprovalRule, error) {
	if input.NativeProtectionID != 0 || input.BranchMode == "protection_ids" {
		return nil, governance_model.ErrInvalid
	}
	var result *governance_model.ApprovalRule
	err := withActorWrite(ctx, actor, []string{governance_model.Resource(scope, scopeID)}, func(ctx context.Context) error {
		path, err := checkApprovalPolicyAccess(ctx, actor.EffectiveUserID(), scope, scopeID)
		if err != nil {
			return err
		}
		var previous *governance_model.ApprovalRule
		if ruleID != 0 {
			var has bool
			previous, has, err = db.GetByID[governance_model.ApprovalRule](ctx, ruleID)
			if err != nil {
				return err
			}
			if !has || previous.ScopeType != scope || previous.ScopeID != scopeID {
				return governance_model.ErrNotFound
			}
			if previous.Revision != revision {
				return governance_model.ErrConflict
			}
		} else if revision != 0 || remove {
			return governance_model.ErrInvalid
		}
		if remove {
			if _, err := db.GetEngine(ctx).ID(ruleID).Delete(new(governance_model.ApprovalRule)); err != nil {
				return err
			}
		} else {
			next := input
			next.ID, next.ScopeType, next.ScopeID, next.Locked, next.Revision = ruleID, scope, scopeID, true, revision+1
			if err := validateApprovalRuleSubjects(ctx, actor, nil, &next, previous); err != nil {
				return err
			}
			duplicate, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND name = ? AND id <> ?", scope, scopeID, next.Name, ruleID).Exist(new(governance_model.ApprovalRule))
			if err != nil {
				return err
			}
			if duplicate {
				return governance_model.ErrConflict
			}
			if ruleID == 0 {
				err = db.Insert(ctx, &next)
			} else {
				_, err = db.GetEngine(ctx).ID(ruleID).AllCols().Update(&next)
			}
			if err != nil {
				return err
			}
			result = &next
		}
		details, err := json.Marshal(map[string]any{"before": previous, "after": result})
		if err != nil {
			return err
		}
		objectID := ruleID
		if result != nil {
			objectID = result.ID
		}
		ancestors := []int64{}
		if scope == "group" {
			ancestors = append(ancestors, scopeID)
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "approval.policy_changed", Actor: actor, ScopeType: scope, ScopeID: scopeID, AncestorIDs: ancestors, ObjectType: "approval_rule", ObjectID: objectID, ObjectPath: path, Result: "success", Details: details})
	})
	return result, err
}
