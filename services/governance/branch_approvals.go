// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
)

type ApprovalRuleChange struct {
	Rule     governance_model.ApprovalRule `json:"rule"`
	Revision int64                         `json:"revision"`
	Remove   bool                          `json:"remove"`
}

type BranchApprovalUpdate struct {
	Version      string                  `json:"version"`
	ProtectionID int64                   `json:"protection_id"`
	Rules        []ApprovalRuleChange    `json:"rules"`
	Settings     *ApprovalSettingsOption `json:"settings,omitempty"`
}

type ApprovalRulePresentation struct {
	Notice     string `json:"notice,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Source     string `json:"source"`
	SourceURL  string `json:"source_url,omitempty"`
	Editable   bool   `json:"editable"`
	Applicable bool   `json:"applicable"`
	Shared     bool   `json:"shared"`
}

type BranchApprovalConfiguration struct {
	Presentation      map[int64]ApprovalRulePresentation `json:"presentation"`
	Protections       []BranchProtectionChoice           `json:"protections"`
	CanReauthenticate bool                               `json:"can_reauthenticate"`

	Protection                *git_model.ProtectedBranch                  `json:"-"`
	Version                   string                                      `json:"version"`
	ProtectionID              int64                                       `json:"protection_id"`
	Rules                     []*governance_model.ApprovalRule            `json:"rules"`
	Settings                  *governance_model.EffectiveApprovalSettings `json:"settings"`
	SettingsRevision          int64                                       `json:"settings_revision"`
	CanManage                 bool                                        `json:"can_manage"`
	ReferenceEnforcementReady bool                                        `json:"reference_enforcement_ready"`
}

type BranchProtectionChoice struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type approvalBatchContextKey struct{}

// ApprovalRuleSaveError 将校验错误定位到提交的草稿行，不泄露其他规则。
type ApprovalRuleSaveError struct {
	Index int
	Err   error
}

func (e *ApprovalRuleSaveError) Error() string { return e.Err.Error() }
func (e *ApprovalRuleSaveError) Unwrap() error { return e.Err }

// BranchApprovalVersion 使用配置内容而非全局写入序号，其他项目的写入不会使当前草稿过期。
func BranchApprovalVersion(ctx context.Context, actorID, repoID int64) (string, error) {
	state, err := ListRepositoryApprovalRules(ctx, actorID, repoID)
	if err != nil {
		return "", err
	}
	protections, err := git_model.FindRepoProtectedBranchRules(ctx, repoID)
	if err != nil {
		return "", err
	}
	var sources []governance_model.ApprovalSettingSource
	for _, name := range []string{"prevent_author", "prevent_committer", "prevent_overrides", "reset_on_change", "require_reauthentication"} {
		sources = append(sources, state.Settings.Sources[name])
	}
	data, err := json.Marshal(struct {
		Rules       []*governance_model.ApprovalRule
		Settings    []governance_model.ApprovalSettingSource
		Revision    int64
		Protections []*git_model.ProtectedBranch
	}{state.Rules, sources, state.SettingsRevision, protections})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func ReadBranchApprovals(ctx context.Context, actorID, repoID, protectionID int64) (*BranchApprovalConfiguration, error) {
	var result *BranchApprovalConfiguration
	err := governance_model.WithStableRead(ctx, func(tx context.Context) error {
		var err error
		result, err = readBranchApprovals(tx, actorID, repoID, protectionID)
		return err
	})
	return result, err
}

func readBranchApprovals(ctx context.Context, actorID, repoID, protectionID int64) (*BranchApprovalConfiguration, error) {
	state, err := ListRepositoryApprovalRules(ctx, actorID, repoID)
	if err != nil {
		return nil, err
	}
	var protection *git_model.ProtectedBranch
	if protectionID > 0 {
		branch, err := git_model.GetProtectedBranchRuleByID(ctx, repoID, protectionID)
		if err != nil {
			return nil, err
		}
		if branch == nil {
			return nil, governance_model.ErrNotFound
		}
		protection = branch
	}
	version, err := BranchApprovalVersion(ctx, actorID, repoID)
	if err != nil {
		return nil, err
	}
	projected, err := ProjectRepositoryApprovalRules(ctx, actorID, repoID, state)
	if err != nil {
		return nil, err
	}
	shortages := map[int64]bool{}
	if protection != nil && !git_model.IsRuleNameSpecial(protection.RuleName) {
		repo, err := repo_model.GetRepositoryByID(ctx, repoID)
		if err != nil {
			return nil, err
		}
		rules := make([]governance_model.ApprovalRule, 0, len(state.Rules))
		for _, rule := range state.Rules {
			rules = append(rules, *rule)
		}
		candidates, err := ApplicableApprovalCandidates(ctx, repo, protection.RuleName, 0, nil, state.Settings.Settings, rules)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			shortages[candidate.Rule.ID] = candidate.Rule.Enabled && candidate.Rule.Required > len(candidate.UserIDs)
		}
	}
	views := map[int64]ApprovalRulePresentation{}
	for _, rule := range projected.Rules {
		view := ApprovalRulePresentation{Applicable: true, Source: "本仓库", Editable: projected.CanManage && rule.ScopeType == "repository"}
		if protectionID > 0 {
			view.Shared = rule.ScopeType == "repository" && (len(rule.ProtectionIDs) != 1 || rule.ProtectionIDs[0] != protectionID)
			view.Editable = view.Editable && !view.Shared
			if rule.BranchMode == "protection_ids" {
				view.Applicable = slices.Contains(rule.ProtectionIDs, protectionID)
			} else if protection != nil && !git_model.IsRuleNameSpecial(protection.RuleName) {
				view.Applicable, err = ApprovalRuleApplies(ctx, repoID, protection.RuleName, rule)
				if err != nil {
					return nil, err
				}
			}
		}
		if rule.ScopeType == "instance" {
			view.Source = "继承自：实例"
			if actor, err := activeActor(ctx, actorID); err == nil && actor.IsAdmin {
				view.SourceURL = setting.AppSubURL + "/-/admin/approvals"
			}
		} else if rule.ScopeType == "group" {
			view.Source = "继承自：上级群组"
			if rule.ScopeID > 0 {
				if group, err := CheckGroupAccess(ctx, actorID, rule.ScopeID, governance_model.ReadGroup); err == nil {
					view.Source = "继承自：" + group.FullPath
					if _, err := checkApprovalPolicyAccess(ctx, actorID, "group", rule.ScopeID); err == nil {
						if owner, err := user_model.GetUserByID(ctx, rule.ScopeID); err == nil {
							view.SourceURL = owner.OrganisationLink() + "/settings/approvals"
						}
					}
				}
			}
		}
		if rule.Enabled && rule.Required > 0 && !rule.SubjectsHidden && shortages[rule.ID] {
			view.Notice = "规则需要处理：当前合格候选人不足"
		}
		if rule.Enabled && rule.Required > 0 && rule.NativeProtectionID == 0 && !projected.ReferenceEnforcementReady {
			view.Notice = "规则需要处理：尚未接入引用事务入口"
		}
		if protection != nil && git_model.IsRuleNameSpecial(protection.RuleName) {
			view.Scope = "按实际目标分支匹配；保护表达式：" + protection.RuleName
		}
		views[rule.ID] = view
	}
	protections, err := git_model.FindRepoProtectedBranchRules(ctx, repoID)
	if err != nil {
		return nil, err
	}
	choices := make([]BranchProtectionChoice, 0, len(protections))
	for _, branch := range protections {
		choices = append(choices, BranchProtectionChoice{ID: branch.ID, Name: branch.RuleName})
	}
	canReauthenticate, err := CanReauthenticateApproval(ctx, actorID)
	if err != nil {
		return nil, err
	}
	return &BranchApprovalConfiguration{Presentation: views, Protections: choices, CanReauthenticate: canReauthenticate, Protection: protection, Version: version, ProtectionID: protectionID, Rules: projected.Rules, Settings: projected.Settings,
		SettingsRevision: projected.SettingsRevision, CanManage: projected.CanManage, ReferenceEnforcementReady: projected.ReferenceEnforcementReady}, nil
}

// SaveBranchApprovals 与原生保护表写入共享事务；saveProtection 只能执行数据库操作。
func SaveBranchApprovals(ctx context.Context, actor governance_model.Actor, repoID int64, option BranchApprovalUpdate,
	saveProtection func(context.Context) (int64, error)) error {
	if option.Version == "" || len(option.Rules) > 1000 {
		return governance_model.ErrInvalid
	}
	return withActorWrite(governance_model.WithAuditActor(ctx, actor), actor, []string{governance_model.Resource("repository", repoID)}, func(tx context.Context) error {
		state, err := ListRepositoryApprovalRules(tx, actor.EffectiveUserID(), repoID)
		if err != nil {
			return err
		}
		if !state.CanManage && (len(option.Rules) > 0 || option.Settings != nil) {
			return governance_model.ErrForbidden
		}
		if saveProtection != nil {
			repo, err := repo_model.GetRepositoryByID(tx, repoID)
			if err != nil {
				return err
			}
			user, err := activeActor(tx, actor.EffectiveUserID())
			if err != nil {
				return err
			}
			permission, err := access_model.GetIndividualUserRepoPermission(tx, repo, user)
			if err != nil {
				return err
			}
			if !permission.IsAdmin() {
				return governance_model.ErrForbidden
			}
		}
		version, err := BranchApprovalVersion(tx, actor.EffectiveUserID(), repoID)
		if err != nil {
			return err
		}
		if version != option.Version {
			return governance_model.ErrConflict
		}
		for _, change := range option.Rules {
			if !change.Remove && change.Rule.Enabled && change.Rule.Required > 0 && change.Rule.NativeProtectionID == 0 && !state.ReferenceEnforcementReady {
				return fmt.Errorf("%w：请先在拉取请求设置接入并验证引用事务入口", governance_model.ErrConflict)
			}
		}
		protectionID := option.ProtectionID
		beforeNative := map[int64]*governance_model.ApprovalRule{}
		for _, rule := range state.Rules {
			if rule.NativeProtectionID > 0 {
				beforeNative[rule.ID] = rule
			}
		}
		if saveProtection != nil {
			protectionID, err = saveProtection(tx)
			if err != nil {
				return err
			}
			if option.ProtectionID > 0 && option.ProtectionID != protectionID {
				return governance_model.ErrConflict
			}
		} else if protectionID > 0 {
			p, err := git_model.GetProtectedBranchRuleByID(tx, repoID, protectionID)
			if err != nil {
				return err
			}
			if p == nil {
				return governance_model.ErrNotFound
			}
		}
		tx = context.WithValue(tx, approvalBatchContextKey{}, true)
		seen := map[int64]bool{}
		for index, change := range option.Rules {
			next := change.Rule
			if next.ID > 0 {
				if seen[next.ID] {
					return governance_model.ErrInvalid
				}
				seen[next.ID] = true
				previous, has, err := db.GetByID[governance_model.ApprovalRule](tx, next.ID)
				if err != nil {
					return err
				}
				if !has || previous.ScopeType != "repository" || previous.ScopeID != repoID {
					return governance_model.ErrNotFound
				}
				if protectionID > 0 && (len(previous.ProtectionIDs) != 1 || previous.ProtectionIDs[0] != protectionID) {
					return governance_model.ErrForbidden
				}
				if before := beforeNative[next.ID]; saveProtection != nil && before != nil && previous.Revision != before.Revision {
					// 同一请求的新旧字段必须一致；兼容映射产生的修订不是其他用户的并发写入。
					if change.Revision != before.Revision || nativeApprovalFieldsConflict(before, previous, &next) {
						return fmt.Errorf("%w：新旧审批字段相互冲突", governance_model.ErrInvalid)
					}
					change.Revision = previous.Revision
				}
			}
			if protectionID > 0 && next.NativeProtectionID == 0 {
				next.BranchMode, next.Branches, next.ProtectionIDs = "protection_ids", nil, []int64{protectionID}
			}
			if _, err := SaveRepositoryApprovalRule(tx, actor, repoID, next.ID, change.Revision, next, change.Remove); err != nil {
				return &ApprovalRuleSaveError{Index: index, Err: err}
			}
		}
		if option.Settings != nil {
			if _, err := SaveRepositoryApprovalSettings(tx, actor, repoID, *option.Settings); err != nil {
				return err
			}
		}
		return nil
	})
}

func nativeApprovalFieldsConflict(before, legacy, next *governance_model.ApprovalRule) bool {
	idsEqual := func(a, b []int64) bool {
		a, b = slices.Clone(a), slices.Clone(b)
		slices.Sort(a)
		slices.Sort(b)
		return slices.Equal(slices.Compact(a), slices.Compact(b))
	}
	return (before.Required != legacy.Required && legacy.Required != next.Required) ||
		(before.AllEligible != legacy.AllEligible && legacy.AllEligible != next.AllEligible) ||
		(!idsEqual(before.UserIDs, legacy.UserIDs) && !idsEqual(legacy.UserIDs, next.UserIDs)) ||
		(!idsEqual(before.TeamIDs, legacy.TeamIDs) && !idsEqual(legacy.TeamIDs, next.TeamIDs)) ||
		(before.NativeIgnoreStale != legacy.NativeIgnoreStale && legacy.NativeIgnoreStale != next.NativeIgnoreStale) ||
		(before.NativeDismissStale != legacy.NativeDismissStale && legacy.NativeDismissStale != next.NativeDismissStale)
}

func normalizeProtectionIDs(ctx context.Context, repo *repo_model.Repository, next, previous *governance_model.ApprovalRule) error {
	if previous == nil && next.NativeProtectionID != 0 {
		return governance_model.ErrInvalid
	}
	if previous != nil {
		if next.NativeProtectionID != 0 && next.NativeProtectionID != previous.NativeProtectionID {
			return governance_model.ErrInvalid
		}
		next.NativeProtectionID = previous.NativeProtectionID
		if previous.NativeProtectionID > 0 {
			next.ProtectionIDs = slices.Clone(previous.ProtectionIDs)
			next.BranchMode, next.Branches, next.Locked, next.RespectNativeApprovalPool = "protection_ids", nil, true, true
			if !next.Enabled || len(next.GroupIDs) > 0 {
				return governance_model.ErrInvalid
			}
		}
	}
	if next.BranchMode == "protection_rules" && repo != nil {
		next.ProtectionIDs = nil
		for _, name := range next.Branches {
			p, err := git_model.GetProtectedBranchRuleByName(ctx, repo.ID, name)
			if err != nil {
				return err
			}
			if p == nil {
				return fmt.Errorf("%w：保护规则 %s 不存在", governance_model.ErrInvalid, name)
			}
			next.ProtectionIDs = append(next.ProtectionIDs, p.ID)
		}
		next.BranchMode, next.Branches = "protection_ids", nil
	}
	if next.BranchMode == "protection_ids" {
		if repo == nil {
			return governance_model.ErrInvalid
		}
		next.ProtectionIDs = compactInt64s(next.ProtectionIDs)
		for _, id := range next.ProtectionIDs {
			p, err := git_model.GetProtectedBranchRuleByID(ctx, repo.ID, id)
			if err != nil {
				return err
			}
			if p == nil {
				return governance_model.ErrInvalid
			}
		}
	}
	return nil
}
