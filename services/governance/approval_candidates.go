// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"slices"
	"time"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
)

// ApprovalRuleApplies 使用目标项目的保护分支规则，Fork 不提供审批规则。
func ApprovalRuleApplies(ctx context.Context, repoID int64, branch string, rule *governance_model.ApprovalRule) (bool, error) {
	if err := governance_model.ValidateApprovalRule(rule); err != nil {
		return false, err
	}
	if !rule.Enabled {
		return false, nil
	}
	if rule.NativeProtectionID > 0 {
		protected, err := git_model.GetFirstMatchProtectedBranchRule(ctx, repoID, branch)
		return protected != nil && protected.ID == rule.NativeProtectionID, err
	}
	switch rule.BranchMode {
	case "protection_ids":
		for _, id := range rule.ProtectionIDs {
			protected, err := git_model.GetProtectedBranchRuleByID(ctx, repoID, id)
			if err != nil {
				return false, err
			}
			if protected != nil && protected.Match(branch) {
				return true, nil
			}
		}
		return false, nil
	case "all":
		return true, nil
	case "branches":
		protected, err := git_model.GetFirstMatchProtectedBranchRule(ctx, repoID, branch)
		return protected != nil && slices.Contains(rule.Branches, branch), err
	case "protected":
		protected, err := git_model.GetFirstMatchProtectedBranchRule(ctx, repoID, branch)
		return protected != nil, err
	case "protection_rules":
		for _, name := range rule.Branches {
			protected, err := git_model.GetProtectedBranchRuleByName(ctx, repoID, name)
			if err != nil {
				return false, err
			}
			if protected != nil && protected.Match(branch) {
				return true, nil
			}
		}
	}
	return false, nil
}

// ApprovalCandidates 每次读取当前成员和项目访问权限；审批资格不扩大推送或合并权限。
func ApprovalCandidates(ctx context.Context, repo *repo_model.Repository, branch string, authorID int64, committers []int64, settings governance_model.ApprovalSettings, rule governance_model.ApprovalRule) (governance_model.RuleCandidates, error) {
	result := governance_model.RuleCandidates{Rule: rule, UserIDs: []int64{}}
	if err := governance_model.ValidateApprovalRule(&rule); err != nil {
		return result, err
	}
	ids := slices.Clone(rule.UserIDs)
	extraApprovers := make(map[int64]bool)
	now := time.Now().Unix()
	for _, groupID := range rule.GroupIDs {
		group, has, err := db.GetByID[governance_model.Namespace](ctx, groupID)
		if err != nil {
			return result, err
		}
		if !has || group.Kind != "group" || group.DeleteAfter != 0 {
			continue
		}
		var memberships []governance_model.Membership
		if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "group", groupID).Find(&memberships); err != nil {
			return result, err
		}
		governanceMembers := make(map[int64]bool, len(memberships))
		for _, membership := range memberships {
			governanceMembers[membership.UserID] = true
			if membership.ExpiresUnix == 0 || membership.ExpiresUnix > now {
				ids = append(ids, membership.UserID)
			}
		}
		if rule.BranchMode == "branches" {
			shared, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND group_id = ? AND (expires_unix = 0 OR expires_unix > ?)", "repository", repo.ID, groupID, now).
				In("max_role", []governance_model.Role{governance_model.Reporter, governance_model.Developer, governance_model.Maintainer, governance_model.Owner}).Exist(new(governance_model.Share))
			if err != nil {
				return result, err
			}
			if shared {
				var eligible []int64
				if err := db.GetEngine(ctx).Table(new(governance_model.Membership)).Cols("user_id").Where("scope_type = ? AND scope_id = ? AND (expires_unix = 0 OR expires_unix > ?)", "group", groupID, now).
					In("role", []governance_model.Role{governance_model.Planner, governance_model.Reporter, governance_model.Developer, governance_model.Maintainer, governance_model.Owner}).Find(&eligible); err != nil {
					return result, err
				}
				for _, id := range eligible {
					extraApprovers[id] = true
				}
			}
		}
		var direct []int64
		if err := db.GetEngine(ctx).Table("org_user").Cols("uid").Where("org_id = ?", groupID).Find(&direct); err != nil {
			return result, err
		}
		for _, id := range direct {
			// 治理成员记录一旦存在即为资格真源，过期记录不能被尚未清理的原生组织关系复活。
			if !governanceMembers[id] {
				ids = append(ids, id)
			}
		}
	}
	for _, teamID := range rule.TeamIDs {
		team, has, err := db.GetByID[organization.Team](ctx, teamID)
		if err != nil {
			return result, err
		}
		// 原生团队主体只能来自目标项目所属组织，不能把任意团队扩充为候选人。
		if !has || team.OrgID != repo.OwnerID {
			continue
		}
		var direct []int64
		if err := db.GetEngine(ctx).Table("team_user").Cols("uid").Where("team_id = ?", teamID).Find(&direct); err != nil {
			return result, err
		}
		ids = append(ids, direct...)
	}
	if rule.AllEligible {
		var active []int64
		if err := db.GetEngine(ctx).Table(new(user_model.User)).Cols("id").Where("is_active = ? AND prohibit_login = ?", true, false).Find(&active); err != nil {
			return result, err
		}
		ids = append(ids, active...)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var protectedBranch *git_model.ProtectedBranch
	if rule.RespectNativeApprovalPool {
		var err error
		protectedBranch, err = git_model.GetFirstMatchProtectedBranchRule(ctx, repo.ID, branch)
		if err != nil {
			return result, err
		}
		if protectedBranch == nil {
			return result, nil
		}
	}
	for _, id := range ids {
		if settings.PreventAuthor && id == authorID || settings.PreventCommitter && slices.Contains(committers, id) {
			continue
		}
		user, err := user_model.GetUserByID(ctx, id)
		if user_model.IsErrUserNotExist(err) {
			continue
		}
		if err != nil {
			return result, err
		}
		if !user.IsActive || user.ProhibitLogin || user.IsOrganization() || user.IsGiteaActions() || user.IsGhost() {
			continue
		}
		permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
		if err != nil {
			return result, err
		}
		permission = permission.ForMutation()
		if !permission.CanRead(unit.TypePullRequests) {
			continue
		}
		if rule.RespectNativeApprovalPool {
			official, err := git_model.IsUserOfficialReviewer(ctx, protectedBranch, user)
			if err != nil {
				return result, err
			}
			if !official {
				continue
			}
			result.UserIDs = append(result.UserIDs, id)
			continue
		}
		// 只读人员需指定保护分支的共享群组授权，单独列名不扩大审批资格。
		if !permission.CanWrite(unit.TypeCode) && !extraApprovers[id] {
			grants, err := governance_model.RepositoryGrants(ctx, repo.ID, repo.OwnerID, id, time.Now())
			if err != nil {
				return result, err
			}
			if !governance_model.EffectiveAbilities(grants)[governance_model.ApproveCode] {
				continue
			}
		}
		result.UserIDs = append(result.UserIDs, id)
	}
	return result, nil
}

// ApplicableApprovalCandidates 先筛选分支，再建立总人数池，避免不适用规则扩大审批资格。
func ApplicableApprovalCandidates(ctx context.Context, repo *repo_model.Repository, branch string, authorID int64, committers []int64, settings governance_model.ApprovalSettings, rules []governance_model.ApprovalRule) ([]governance_model.RuleCandidates, error) {
	results := make([]governance_model.RuleCandidates, 0, len(rules))
	explicit := make([]int64, 0)
	for _, rule := range rules {
		applies, err := ApprovalRuleApplies(ctx, repo.ID, branch, &rule)
		if err != nil {
			return nil, err
		}
		if !applies {
			continue
		}
		candidates, err := ApprovalCandidates(ctx, repo, branch, authorID, committers, settings, rule)
		if err != nil {
			return nil, err
		}
		results = append(results, candidates)
		if !rule.AllEligible {
			explicit = append(explicit, candidates.UserIDs...)
		}
	}
	for i := range results {
		if !results[i].Rule.AllEligible {
			continue
		}
		if results[i].Rule.RespectNativeApprovalPool {
			// 该池已从全部当前用户按原生资格筛过，不能再由无约束显式规则扩充。
			continue
		}
		// 已有适用规则明确授予 Reporter/Planner 审批资格，其票也计入去重总人数。
		ids := results[i].UserIDs
		ids = append(ids, explicit...)
		slices.Sort(ids)
		results[i].UserIDs = slices.Compact(ids)
	}
	return results, nil
}
