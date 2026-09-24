// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/glob"
	"gitea.dev/modules/timeutil"
)

// GroupProtectedBranch is a top-level group's basic rule, evaluated against its current repository tree.
type GroupProtectedBranch struct {
	ID             int64                 `xorm:"pk autoincr" json:"id"`
	GroupID        int64                 `xorm:"UNIQUE(group_rule) INDEX NOT NULL" json:"group_id"`
	RuleName       string                `xorm:"UNIQUE(group_rule) NOT NULL" json:"rule_name"`
	PushRole       governance_model.Role `xorm:"NOT NULL DEFAULT 0" json:"push_role"`
	MergeRole      governance_model.Role `xorm:"NOT NULL DEFAULT 0" json:"merge_role"`
	AllowForcePush bool                  `xorm:"NOT NULL DEFAULT false" json:"allow_force_push"`
	CreatedUnix    timeutil.TimeStamp    `xorm:"created" json:"created_unix"`
	UpdatedUnix    timeutil.TimeStamp    `xorm:"updated" json:"updated_unix"`
}

func init() { db.RegisterModel(new(GroupProtectedBranch)) }

func ValidGroupProtectionRole(role governance_model.Role) bool {
	return role == 0 || role == governance_model.Developer || role == governance_model.Maintainer
}

func ValidGroupProtectionPattern(pattern string) bool {
	if pattern == "" || len(pattern) > 255 || strings.TrimSpace(pattern) != pattern || strings.HasPrefix(pattern, "refs/") {
		return false
	}
	_, err := glob.Compile(pattern)
	return err == nil
}

func (rule *GroupProtectedBranch) Match(branch string) bool {
	pattern, err := glob.Compile(rule.RuleName)
	return err == nil && pattern.Match(branch)
}

func FindGroupProtectedBranchRules(ctx context.Context, groupID int64) ([]*GroupProtectedBranch, error) {
	var rules []*GroupProtectedBranch
	err := db.GetEngine(ctx).Where("group_id = ?", groupID).Asc("rule_name").Find(&rules)
	return rules, err
}

// GroupProtectedRulesForRepo uses only ownership ancestry, never repository sharing.
func GroupProtectedRulesForRepo(ctx context.Context, repo *repo_model.Repository) ([]*GroupProtectedBranch, int64, error) {
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		return nil, 0, err
	}
	root := chain[len(chain)-1]
	if root.Kind != "group" {
		return nil, 0, nil
	}
	rules, err := FindGroupProtectedBranchRules(ctx, root.ID)
	return rules, root.ID, err
}

// EffectiveBranchProtection keeps the native advanced rule separate from every matching group basic rule.
type EffectiveBranchProtection struct {
	Native *ProtectedBranch
	Group  []*GroupProtectedBranch
	Repo   *repo_model.Repository
}

func (p *EffectiveBranchProtection) IsProtected() bool {
	return p != nil && (p.Native != nil || len(p.Group) > 0)
}

func EvaluateEffectiveBranchProtection(ctx context.Context, repoID int64, branch string) (*EffectiveBranchProtection, error) {
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return nil, err
	}
	native, err := GetFirstMatchProtectedBranchRule(ctx, repoID, branch)
	if err != nil {
		return nil, err
	}
	result := &EffectiveBranchProtection{Native: native, Repo: repo}
	rules, _, err := GroupProtectedRulesForRepo(ctx, repo)
	if err != nil {
		return nil, err
	}
	for _, rule := range rules {
		if rule.Match(branch) {
			result.Group = append(result.Group, rule)
		}
	}
	return result, nil
}

func groupRoleAllows(ctx context.Context, repo *repo_model.Repository, user *user_model.User, required governance_model.Role, ability string) (bool, error) {
	if required == 0 || user == nil || !user.IsActive || user.ProhibitLogin {
		return false, nil
	}
	permission, err := access_model.GetDoerRepoPermission(ctx, repo, user)
	if err != nil {
		return false, err
	}
	if ability == governance_model.PushCode && !permission.CanWrite(unit.TypeCode) {
		return false, nil
	}
	native := permission.WithoutGovernance()
	if required == governance_model.Developer && native.CanWrite(unit.TypeCode) || required == governance_model.Maintainer && native.IsAdmin() {
		return true, nil
	}
	grants, err := governance_model.RepositoryGrants(ctx, repo.ID, repo.OwnerID, user.ID, time.Now())
	if err != nil {
		return false, err
	}
	for _, grant := range grants {
		role := grant.Role
		if grant.CeilingRole > 0 && role > grant.CeilingRole {
			role = grant.CeilingRole
		}
		if role >= required && grant.Abilities[ability] {
			return true, nil
		}
	}
	return false, nil
}

func (p *EffectiveBranchProtection) CanUserPush(ctx context.Context, user *user_model.User) (bool, error) {
	if !p.IsProtected() {
		return true, nil
	}
	if user == nil {
		return false, nil
	}
	permission, err := access_model.GetDoerRepoPermission(ctx, p.Repo, user)
	if err != nil || !permission.CanWrite(unit.TypeCode) {
		return false, err
	}
	if p.Native != nil && p.Native.CanUserPush(ctx, user) {
		return true, nil
	}
	for _, rule := range p.Group {
		allowed, err := groupRoleAllows(ctx, p.Repo, user, rule.PushRole, governance_model.PushCode)
		if err != nil || allowed {
			return allowed, err
		}
	}
	return false, nil
}

func (p *EffectiveBranchProtection) CanUserMerge(ctx context.Context, user *user_model.User, permission access_model.Permission) (bool, error) {
	if user == nil {
		return false, nil
	}
	nativePermission := permission.WithoutGovernance()
	governanceMerge, err := access_model.HasGovernanceAbility(ctx, p.Repo, user, governance_model.MergeCode)
	if err != nil || !nativePermission.CanWrite(unit.TypeCode) && !governanceMerge {
		return false, err
	}
	if p.Native != nil && IsUserMergeWhitelisted(ctx, p.Native, user.ID, nativePermission) {
		return true, nil
	}
	if p.Native != nil && !p.Native.EnableMergeWhitelist && governanceMerge {
		return true, nil
	}
	for _, rule := range p.Group {
		allowed, err := groupRoleAllows(ctx, p.Repo, user, rule.MergeRole, governance_model.MergeCode)
		if err != nil || allowed {
			return allowed, err
		}
		allowed, err = groupRoleAllows(ctx, p.Repo, user, rule.PushRole, governance_model.MergeCode)
		if err != nil || allowed {
			return allowed, err
		}
	}
	return false, nil
}

func (p *EffectiveBranchProtection) AllowsForcePush(ctx context.Context, user *user_model.User) (bool, error) {
	canPush, err := p.CanUserPush(ctx, user)
	if err != nil || !canPush {
		return false, err
	}
	if p.Native != nil && p.Native.CanUserForcePush(ctx, user) {
		return true, nil
	}
	for _, rule := range p.Group {
		if rule.AllowForcePush {
			return true, nil
		}
	}
	return false, nil
}

func (p *EffectiveBranchProtection) CanDeployKeyPush(force bool) bool {
	if p.Native == nil || !p.Native.CanPush || p.Native.EnableWhitelist && !p.Native.WhitelistDeployKeys {
		return false
	}
	if !force {
		return true
	}
	if p.Native.CanForcePush && (!p.Native.EnableForcePushAllowlist || p.Native.ForcePushAllowlistDeployKeys) {
		return true
	}
	for _, rule := range p.Group {
		if rule.AllowForcePush {
			return true
		}
	}
	return false
}
