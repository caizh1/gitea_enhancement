// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"slices"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	access_model "gitea.dev/models/perm/access"
	"gitea.dev/models/unit"
	"gitea.dev/modules/json"
)

type PullApprovalRules struct {
	FullPath  string                                      `json:"full_path"`
	CanManage bool                                        `json:"can_manage"`
	Version   *governance_model.PullRuleVersion           `json:"version"`
	Policies  []*governance_model.ApprovalRule            `json:"policies"`
	Settings  *governance_model.EffectiveApprovalSettings `json:"settings"`
}

func ListPullApprovalRules(ctx context.Context, actorID, pullID int64) (*PullApprovalRules, error) {
	pr, err := issues_model.GetPullRequestByID(ctx, pullID)
	if issues_model.IsErrPullRequestNotExist(err) {
		return nil, governance_model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	configuration, err := ListRepositoryApprovalRules(ctx, actorID, pr.BaseRepoID)
	if err != nil {
		return nil, err
	}
	if err := pr.LoadIssue(ctx); err != nil {
		return nil, err
	}
	repo, manage, err := checkApprovalRuleAccess(ctx, actorID, pr.BaseRepoID, false)
	if err != nil {
		return nil, err
	}
	if actorID > 0 {
		actor, err := activeActor(ctx, actorID)
		if err != nil {
			return nil, err
		}
		permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, actor)
		if err != nil {
			return nil, err
		}
		manage = manage || actor.IsAdmin || pr.Issue.PosterID == actorID && permission.CanWrite(unit.TypeCode)
	}
	manage = manage && !configuration.Settings.Settings.PreventOverrides && !pr.HasMerged && !pr.Issue.IsClosed && !repo.IsArchived
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		return nil, err
	}
	for _, ancestor := range chain {
		if ancestor.Archived || ancestor.DeleteAfter != 0 {
			manage = false
		}
	}
	saved, has, err := governance_model.LatestPullRuleVersion(ctx, pullID)
	if err != nil {
		return nil, err
	}
	if !has {
		saved = &governance_model.PullRuleVersion{PullID: pullID, Rules: []governance_model.ApprovalRule{}}
	}
	result := &PullApprovalRules{FullPath: repo.FullPath(), CanManage: manage, Version: saved, Policies: []*governance_model.ApprovalRule{}, Settings: configuration.Settings}
	useProject := !has || configuration.Settings.Settings.PreventOverrides
	if useProject {
		result.Version.Rules = []governance_model.ApprovalRule{}
	}
	for _, rule := range configuration.Rules {
		if rule.ScopeType != "repository" || rule.NativeProtectionID > 0 {
			result.Policies = append(result.Policies, rule)
		} else if useProject {
			result.Version.Rules = append(result.Version.Rules, *rule)
		}
	}
	result.Version.Rules = slices.DeleteFunc(slices.Clone(result.Version.Rules), func(rule governance_model.ApprovalRule) bool { return rule.NativeProtectionID > 0 })
	return result, nil
}

// SavePullApprovalRule 以整个 PR 规则版本作为并发条件，既不覆盖项目模板，也不接受强制策略 ID。
func SavePullApprovalRule(ctx context.Context, actor governance_model.Actor, pullID, ruleID, revision int64, input governance_model.ApprovalRule, remove bool) (*governance_model.PullRuleVersion, error) {
	var result *governance_model.PullRuleVersion
	err := withActorWrite(ctx, actor, []string{governance_model.Resource("pull", pullID)}, func(ctx context.Context) error {
		state, err := ListPullApprovalRules(ctx, actor.EffectiveUserID(), pullID)
		if err != nil {
			return err
		}
		if !state.CanManage {
			return governance_model.ErrForbidden
		}
		if state.Version.Revision != revision {
			return governance_model.ErrConflict
		}
		pr, err := issues_model.GetPullRequestByID(ctx, pullID)
		if err != nil {
			return err
		}
		return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", pr.BaseRepoID)}, func(ctx context.Context) error {
			repo, _, err := checkApprovalRuleAccess(ctx, actor.EffectiveUserID(), pr.BaseRepoID, false)
			if err != nil {
				return err
			}
			index := slices.IndexFunc(state.Version.Rules, func(rule governance_model.ApprovalRule) bool { return rule.ID == ruleID })
			if ruleID != 0 && index < 0 {
				return governance_model.ErrNotFound
			}
			if input.NativeProtectionID != 0 || index >= 0 && state.Version.Rules[index].NativeProtectionID != 0 {
				return governance_model.ErrForbidden
			}
			if remove && ruleID == 0 {
				return governance_model.ErrInvalid
			}
			var previous *governance_model.ApprovalRule
			if index >= 0 {
				previous = &state.Version.Rules[index]
			}
			result = &governance_model.PullRuleVersion{PullID: pullID, Revision: revision + 1, Rules: slices.Clone(state.Version.Rules), CreatedAt: time.Now().UTC()}
			if remove {
				result.Rules = slices.Delete(result.Rules, index, index+1)
			} else {
				next := input
				next.ID, next.ScopeType, next.ScopeID, next.Locked, next.Revision = ruleID, "pull", pullID, false, 1
				if previous != nil {
					next.Revision = previous.Revision + 1
				}
				if err := preserveHiddenApprovalSubjects(ctx, actor.EffectiveUserID(), repo, &next, previous); err != nil {
					return err
				}
				if err := validateApprovalRuleSubjects(ctx, actor, repo, &next, previous); err != nil {
					return err
				}
				for _, rule := range result.Rules {
					if rule.ID != ruleID && rule.Name == next.Name {
						return governance_model.ErrConflict
					}
				}
				if ruleID == 0 {
					// 原生规则表只分配稳定 ID；PR 的实际生效内容始终来自不可变版本。
					if err := db.Insert(ctx, &next); err != nil {
						return err
					}
					result.Rules = append(result.Rules, next)
				} else {
					result.Rules[index] = next
				}
			}
			if err := db.Insert(ctx, result); err != nil {
				return err
			}
			chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
			if err != nil {
				return err
			}
			var ancestors []int64
			for _, ancestor := range chain {
				if ancestor.Kind == "group" {
					ancestors = append(ancestors, ancestor.ID)
				}
			}
			details, err := json.Marshal(map[string]any{"before": state.Version, "after": result})
			if err != nil {
				return err
			}
			return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "approval.rule_changed", Actor: actor, ScopeType: "repository", ScopeID: repo.ID, AncestorIDs: ancestors, ObjectType: "pull", ObjectID: pullID, ObjectPath: repo.FullPath(), Result: "success", Details: details})
		})
	})
	return result, err
}
