// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"errors"
	"fmt"
	governance_model "gitea.dev/models/governance"
	governance_service "gitea.dev/services/governance"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	git_model "gitea.dev/models/git"
	"gitea.dev/models/organization"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/modules/base"
	"gitea.dev/modules/glob"
	"gitea.dev/modules/json"
	"gitea.dev/modules/templates"
	"gitea.dev/modules/web"
	"gitea.dev/routers/web/repo"
	"gitea.dev/services/context"
	"gitea.dev/services/forms"
	pull_service "gitea.dev/services/pull"
	"gitea.dev/services/repository"
)

const (
	tplProtectedBranch templates.TplName = "repo/settings/protected_branch"
)

// ProtectedBranchRules render the page to protect the repository
func ProtectedBranchRules(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.settings.branches")
	ctx.Data["PageIsSettingsBranches"] = true

	rules, err := git_model.FindRepoProtectedBranchRules(ctx, ctx.Repo.Repository.ID)
	if err != nil {
		ctx.ServerError("GetProtectedBranches", err)
		return
	}
	ctx.Data["ProtectedBranches"] = rules
	groupRules, sourceID, err := git_model.GroupProtectedRulesForRepo(ctx, ctx.Repo.Repository)
	if err != nil {
		ctx.ServerError("GroupProtectedRulesForRepo", err)
		return
	}
	ctx.Data["InheritedGroupProtectedBranches"] = groupRules
	ctx.Data["GroupProtectionSource"] = fmt.Sprintf("受限群组来源 #%d", sourceID)
	if sourceID != 0 {
		if source, err := governance_service.CheckGroupAccess(ctx, ctx.Doer.ID, sourceID, governance_model.ReadGroup); err == nil {
			ctx.Data["GroupProtectionSource"] = source.FullPath
			ctx.Data["GroupProtectionManageURL"] = ""
			if source.Abilities[governance_model.ManageGroup] {
				ctx.Data["GroupProtectionManageURL"] = fmt.Sprintf("/governance/groups/%d?tab=settings", sourceID)
			}
		} else if !errors.Is(err, governance_model.ErrNotFound) {
			ctx.ServerError("CheckGroupAccess", err)
			return
		}
	}
	summaries := map[int64]string{}
	for _, rule := range rules {
		configuration, err := governance_service.ReadBranchApprovals(ctx, ctx.Doer.ID, ctx.Repo.Repository.ID, rule.ID)
		if err != nil {
			ctx.ServerError("ReadBranchApprovals", err)
			return
		}
		local, inherited, attention := 0, 0, 0
		for _, approval := range configuration.Rules {
			if !approval.Enabled || !configuration.Presentation[approval.ID].Applicable {
				continue
			}
			if configuration.Presentation[approval.ID].Notice != "" {
				attention++
			}
			if approval.ScopeType == "repository" {
				local++
			} else {
				inherited++
			}
		}
		summaries[rule.ID] = fmt.Sprintf("审批：本仓库 %d 条 · 继承 %d 条", local, inherited)
		if attention > 0 {
			summaries[rule.ID] += fmt.Sprintf(" · %d 条规则需要处理", attention)
		}
	}
	ctx.Data["ApprovalSummaries"] = summaries

	repo.PrepareBranchList(ctx)
	if ctx.Written() {
		return
	}

	ctx.HTML(http.StatusOK, tplBranches)
}

// SettingsProtectedBranch renders the protected branch setting page
func SettingsProtectedBranch(c *context.Context) {
	ruleName := c.FormString("rule_name")
	var rule *git_model.ProtectedBranch
	if id := c.FormInt64("rule_id"); id > 0 {
		var err error
		rule, err = git_model.GetProtectedBranchRuleByID(c, c.Repo.Repository.ID, id)
		if err != nil {
			c.ServerError("GetProtectedBranchRuleByID", err)
			return
		}
		if rule == nil {
			c.NotFound(nil)
			return
		}
	} else if ruleName != "" {
		var err error
		rule, err = git_model.GetProtectedBranchRuleByName(c, c.Repo.Repository.ID, ruleName)
		if err != nil {
			c.ServerError("GetProtectBranchOfRepoByName", err)
			return
		}
	}

	if rule == nil {
		// No options found, create defaults.
		rule = &git_model.ProtectedBranch{}
	}

	if err := prepareBranchApprovalEditor(c, rule.ID); err != nil {
		c.ServerError("ReadBranchApprovals", err)
		return
	}
	if state := c.Data["BranchApprovals"].(*governance_service.BranchApprovalConfiguration); state.Protection != nil {
		rule = state.Protection
	}
	c.Data["PageIsSettingsBranches"] = true
	c.Data["Title"] = c.Locale.TrString("repo.settings.protected_branch") + " - " + rule.RuleName
	users, err := access_model.GetUsersWithAnyUnitAccess(c, c.Repo.Repository, perm.AccessModeRead, unit.TypeCode, unit.TypePullRequests)
	if err != nil {
		c.ServerError("GetUsersWithUnitAccess", err)
		return
	}
	c.Data["Users"] = users
	c.Data["whitelist_users"] = strings.Join(base.Int64sToStrings(rule.WhitelistUserIDs), ",")
	c.Data["force_push_allowlist_users"] = strings.Join(base.Int64sToStrings(rule.ForcePushAllowlistUserIDs), ",")
	c.Data["merge_whitelist_users"] = strings.Join(base.Int64sToStrings(rule.MergeWhitelistUserIDs), ",")
	c.Data["bypass_allowlist_users"] = strings.Join(base.Int64sToStrings(rule.BypassAllowlistUserIDs), ",")
	c.Data["approvals_whitelist_users"] = strings.Join(base.Int64sToStrings(rule.ApprovalsWhitelistUserIDs), ",")
	c.Data["status_check_contexts"] = strings.Join(rule.StatusCheckContexts, "\n")
	contexts, _ := git_model.FindRepoRecentCommitStatusContexts(c, c.Repo.Repository.ID, 7*24*time.Hour) // Find last week status check contexts
	c.Data["recent_status_checks"] = contexts

	if c.Repo.Owner.IsOrganization() {
		teams, err := organization.GetTeamsWithAccessToAnyRepoUnit(c, c.Repo.Owner.ID, c.Repo.Repository.ID, perm.AccessModeRead, unit.TypeCode, unit.TypePullRequests)
		if err != nil {
			c.ServerError("Repo.Owner.TeamsWithAccessToRepo", err)
			return
		}
		c.Data["Teams"] = teams
		c.Data["whitelist_teams"] = strings.Join(base.Int64sToStrings(rule.WhitelistTeamIDs), ",")
		c.Data["force_push_allowlist_teams"] = strings.Join(base.Int64sToStrings(rule.ForcePushAllowlistTeamIDs), ",")
		c.Data["merge_whitelist_teams"] = strings.Join(base.Int64sToStrings(rule.MergeWhitelistTeamIDs), ",")
		c.Data["bypass_allowlist_teams"] = strings.Join(base.Int64sToStrings(rule.BypassAllowlistTeamIDs), ",")
		c.Data["approvals_whitelist_teams"] = strings.Join(base.Int64sToStrings(rule.ApprovalsWhitelistTeamIDs), ",")
	}

	c.Data["Rule"] = rule
	c.HTML(http.StatusOK, tplProtectedBranch)
}

// SettingsProtectedBranchPost updates the protected branch settings
func SettingsProtectedBranchPost(ctx *context.Context) {
	f := web.GetForm(ctx).(*forms.ProtectBranchForm)
	var approvals []governance_service.BranchApprovalUpdate
	if f.ApprovalConfiguration != "" {
		var option governance_service.BranchApprovalUpdate
		if err := json.Unmarshal([]byte(f.ApprovalConfiguration), &option); err != nil || option.ProtectionID != f.RuleID {
			ctx.JSON(http.StatusUnprocessableEntity, map[string]string{"message": "审批草稿格式无效"})
			return
		}
		approvals = append(approvals, option)
	}
	var protectBranch *git_model.ProtectedBranch
	if f.RuleName == "" {
		ctx.Flash.Error(ctx.Tr("repo.settings.protected_branch_required_rule_name"))
		ctx.Redirect(ctx.Repo.RepoLink + "/settings/branches/edit")
		return
	}

	var err error
	if f.RuleID > 0 {
		// If the RuleID isn't 0, it must be an edit operation. So we get rule by id.
		protectBranch, err = git_model.GetProtectedBranchRuleByID(ctx, ctx.Repo.Repository.ID, f.RuleID)
		if err != nil {
			ctx.ServerError("GetProtectBranchOfRepoByID", err)
			return
		}
		if protectBranch == nil && len(approvals) > 0 {
			ctx.JSON(http.StatusConflict, map[string]string{"message": "保护规则已被删除，请保留草稿并重新核对"})
			return
		}
		if protectBranch != nil && protectBranch.RuleName != f.RuleName {
			// RuleName changed. We need to check if there is a rule with the same name.
			// If a rule with the same name exists, an error should be returned.
			sameNameProtectBranch, err := git_model.GetProtectedBranchRuleByName(ctx, ctx.Repo.Repository.ID, f.RuleName)
			if err != nil {
				ctx.ServerError("GetProtectBranchOfRepoByName", err)
				return
			}
			if sameNameProtectBranch != nil {
				ctx.Flash.Error(ctx.Tr("repo.settings.protected_branch_duplicate_rule_name"))
				ctx.Redirect(fmt.Sprintf("%s/settings/branches/edit?rule_name=%s", ctx.Repo.RepoLink, protectBranch.RuleName))
				return
			}
		}
	} else {
		// FIXME: If a new ProtectBranch has a duplicate RuleName, an error should be returned.
		// Currently, if a new ProtectBranch with a duplicate RuleName is created, the existing ProtectBranch will be updated.
		// But we cannot modify this logic now because many unit tests rely on it.
		protectBranch, err = git_model.GetProtectedBranchRuleByName(ctx, ctx.Repo.Repository.ID, f.RuleName)
		if err != nil {
			ctx.ServerError("GetProtectBranchOfRepoByName", err)
			return
		}
		if protectBranch != nil && len(approvals) > 0 {
			ctx.JSON(http.StatusConflict, map[string]string{"message": "同名保护规则已存在，请重新读取后编辑"})
			return
		}
	}
	if protectBranch == nil {
		// No options found, create defaults.
		protectBranch = &git_model.ProtectedBranch{
			RepoID:   ctx.Repo.Repository.ID,
			RuleName: f.RuleName,
		}
	}

	var whitelistUsers, whitelistTeams, forcePushAllowlistUsers, forcePushAllowlistTeams, mergeWhitelistUsers, mergeWhitelistTeams, approvalsWhitelistUsers, approvalsWhitelistTeams, bypassAllowlistUsers, bypassAllowlistTeams []int64
	protectBranch.RuleName = f.RuleName
	if f.RequiredApprovals < 0 {
		ctx.Flash.Error(ctx.Tr("repo.settings.protected_branch_required_approvals_min"))
		ctx.Redirect(fmt.Sprintf("%s/settings/branches/edit?rule_name=%s", ctx.Repo.RepoLink, f.RuleName))
		return
	}

	switch f.EnablePush {
	case "all":
		protectBranch.CanPush = true
		protectBranch.EnableWhitelist = false
		protectBranch.WhitelistDeployKeys = false
	case "whitelist":
		protectBranch.CanPush = true
		protectBranch.EnableWhitelist = true
		protectBranch.WhitelistDeployKeys = f.WhitelistDeployKeys
		if strings.TrimSpace(f.WhitelistUsers) != "" {
			whitelistUsers, _ = base.StringsToInt64s(strings.Split(f.WhitelistUsers, ","))
		}
		if strings.TrimSpace(f.WhitelistTeams) != "" {
			whitelistTeams, _ = base.StringsToInt64s(strings.Split(f.WhitelistTeams, ","))
		}
	default:
		protectBranch.CanPush = false
		protectBranch.EnableWhitelist = false
		protectBranch.WhitelistDeployKeys = false
	}

	switch f.EnableForcePush {
	case "all":
		protectBranch.CanForcePush = true
		protectBranch.EnableForcePushAllowlist = false
		protectBranch.ForcePushAllowlistDeployKeys = false
	case "whitelist":
		protectBranch.CanForcePush = true
		protectBranch.EnableForcePushAllowlist = true
		protectBranch.ForcePushAllowlistDeployKeys = f.ForcePushAllowlistDeployKeys
		if strings.TrimSpace(f.ForcePushAllowlistUsers) != "" {
			forcePushAllowlistUsers, _ = base.StringsToInt64s(strings.Split(f.ForcePushAllowlistUsers, ","))
		}
		if strings.TrimSpace(f.ForcePushAllowlistTeams) != "" {
			forcePushAllowlistTeams, _ = base.StringsToInt64s(strings.Split(f.ForcePushAllowlistTeams, ","))
		}
	default:
		protectBranch.CanForcePush = false
		protectBranch.EnableForcePushAllowlist = false
		protectBranch.ForcePushAllowlistDeployKeys = false
	}

	protectBranch.EnableMergeWhitelist = f.EnableMergeWhitelist
	if f.EnableMergeWhitelist {
		if strings.TrimSpace(f.MergeWhitelistUsers) != "" {
			mergeWhitelistUsers, _ = base.StringsToInt64s(strings.Split(f.MergeWhitelistUsers, ","))
		}
		if strings.TrimSpace(f.MergeWhitelistTeams) != "" {
			mergeWhitelistTeams, _ = base.StringsToInt64s(strings.Split(f.MergeWhitelistTeams, ","))
		}
	}

	protectBranch.EnableBypassAllowlist = f.EnableBypassAllowlist
	if f.EnableBypassAllowlist {
		if strings.TrimSpace(f.BypassAllowlistUsers) != "" {
			bypassAllowlistUsers, _ = base.StringsToInt64s(strings.Split(f.BypassAllowlistUsers, ","))
		}
		if strings.TrimSpace(f.BypassAllowlistTeams) != "" {
			bypassAllowlistTeams, _ = base.StringsToInt64s(strings.Split(f.BypassAllowlistTeams, ","))
		}
	}

	protectBranch.EnableStatusCheck = f.EnableStatusCheck
	if f.EnableStatusCheck {
		patterns := strings.Split(strings.ReplaceAll(f.StatusCheckContexts, "\r", "\n"), "\n")
		validPatterns := make([]string, 0, len(patterns))
		for _, pattern := range patterns {
			trimmed := strings.TrimSpace(pattern)
			if trimmed == "" {
				continue
			}
			if _, err := glob.Compile(trimmed); err != nil {
				ctx.Flash.Error(ctx.Tr("repo.settings.protect_invalid_status_check_pattern", pattern))
				ctx.Redirect(fmt.Sprintf("%s/settings/branches/edit?rule_name=%s", ctx.Repo.RepoLink, url.QueryEscape(protectBranch.RuleName)))
				return
			}
			validPatterns = append(validPatterns, trimmed)
		}
		if len(validPatterns) == 0 {
			// if status check is enabled, patterns slice is not allowed to be empty
			ctx.Flash.Error(ctx.Tr("repo.settings.protect_no_valid_status_check_patterns"))
			ctx.Redirect(fmt.Sprintf("%s/settings/branches/edit?rule_name=%s", ctx.Repo.RepoLink, url.QueryEscape(protectBranch.RuleName)))
			return
		}
		protectBranch.StatusCheckContexts = validPatterns
	} else {
		protectBranch.StatusCheckContexts = nil
	}

	if len(approvals) == 0 {
		protectBranch.RequiredApprovals = f.RequiredApprovals
		protectBranch.EnableApprovalsWhitelist = f.EnableApprovalsWhitelist
		if f.EnableApprovalsWhitelist {
			if strings.TrimSpace(f.ApprovalsWhitelistUsers) != "" {
				approvalsWhitelistUsers, _ = base.StringsToInt64s(strings.Split(f.ApprovalsWhitelistUsers, ","))
			}
			if strings.TrimSpace(f.ApprovalsWhitelistTeams) != "" {
				approvalsWhitelistTeams, _ = base.StringsToInt64s(strings.Split(f.ApprovalsWhitelistTeams, ","))
			}
		}
	} else {
		approvalsWhitelistUsers = protectBranch.ApprovalsWhitelistUserIDs
		approvalsWhitelistTeams = protectBranch.ApprovalsWhitelistTeamIDs
	}
	protectBranch.BlockOnRejectedReviews = f.BlockOnRejectedReviews
	protectBranch.BlockOnOfficialReviewRequests = f.BlockOnOfficialReviewRequests
	if len(approvals) == 0 {
		protectBranch.DismissStaleApprovals = f.DismissStaleApprovals
		protectBranch.IgnoreStaleApprovals = f.IgnoreStaleApprovals
	}
	protectBranch.RequireSignedCommits = f.RequireSignedCommits
	protectBranch.ProtectedFilePatterns = f.ProtectedFilePatterns
	protectBranch.UnprotectedFilePatterns = f.UnprotectedFilePatterns
	protectBranch.BlockOnOutdatedBranch = f.BlockOnOutdatedBranch
	protectBranch.BlockAdminMergeOverride = f.BlockAdminMergeOverride
	protectBranch.RequireGovernanceApproval = f.RequireGovernanceApproval

	if err = pull_service.CreateOrUpdateProtectedBranch(ctx, ctx.Repo.Repository, protectBranch, git_model.WhitelistOptions{
		UserIDs:          whitelistUsers,
		TeamIDs:          whitelistTeams,
		ForcePushUserIDs: forcePushAllowlistUsers,
		ForcePushTeamIDs: forcePushAllowlistTeams,
		MergeUserIDs:     mergeWhitelistUsers,
		MergeTeamIDs:     mergeWhitelistTeams,
		ApprovalsUserIDs: approvalsWhitelistUsers,
		ApprovalsTeamIDs: approvalsWhitelistTeams,
		BypassUserIDs:    bypassAllowlistUsers,
		BypassTeamIDs:    bypassAllowlistTeams,
	}, approvals...); err != nil {
		var saved *pull_service.ProtectionSavedRefreshError
		if errors.As(err, &saved) {
			ctx.Flash.Warning(saved.Error())
			if len(approvals) > 0 {
				ctx.JSON(http.StatusOK, map[string]string{"redirect": ctx.Repo.RepoLink + "/settings/branches", "warning": saved.Error()})
			} else {
				ctx.Redirect(ctx.Repo.RepoLink + "/settings/branches")
			}
			return
		}
		if len(approvals) > 0 {
			status := http.StatusUnprocessableEntity
			if errors.Is(err, governance_model.ErrConflict) {
				status = http.StatusConflict
			}
			if errors.Is(err, governance_model.ErrForbidden) {
				status = http.StatusForbidden
			}
			message := map[string]any{"message": err.Error()}
			var ruleError *governance_service.ApprovalRuleSaveError
			if errors.As(err, &ruleError) {
				message["rule_index"] = ruleError.Index
			}
			ctx.JSON(status, message)
			return
		}
		ctx.ServerError("CreateOrUpdateProtectedBranch", err)
		return
	}

	if len(approvals) > 0 {
		ctx.JSON(http.StatusOK, map[string]string{"redirect": ctx.Repo.RepoLink + "/settings/branches"})
		return
	}
	ctx.Flash.Success(ctx.Tr("repo.settings.update_protect_branch_success", protectBranch.RuleName))
	ctx.Redirect(fmt.Sprintf("%s/settings/branches?rule_name=%s", ctx.Repo.RepoLink, protectBranch.RuleName))
}

// DeleteProtectedBranchRulePost delete protected branch rule by id
func DeleteProtectedBranchRulePost(ctx *context.Context) {
	ruleID := ctx.PathParamInt64("id")
	if ruleID <= 0 {
		ctx.Flash.Error(ctx.Tr("repo.settings.remove_protected_branch_failed", strconv.FormatInt(ruleID, 10)))
		ctx.JSONRedirect(ctx.Repo.RepoLink + "/settings/branches")
		return
	}

	rule, err := git_model.GetProtectedBranchRuleByID(ctx, ctx.Repo.Repository.ID, ruleID)
	if err != nil {
		ctx.Flash.Error(ctx.Tr("repo.settings.remove_protected_branch_failed", strconv.FormatInt(ruleID, 10)))
		ctx.JSONRedirect(ctx.Repo.RepoLink + "/settings/branches")
		return
	}

	if rule == nil {
		ctx.Flash.Error(ctx.Tr("repo.settings.remove_protected_branch_failed", strconv.FormatInt(ruleID, 10)))
		ctx.JSONRedirect(ctx.Repo.RepoLink + "/settings/branches")
		return
	}

	if err := git_model.DeleteProtectedBranch(ctx, ctx.Repo.Repository, ruleID); err != nil {
		ctx.Flash.Error(ctx.Tr("repo.settings.remove_protected_branch_failed", rule.RuleName))
		ctx.JSONRedirect(ctx.Repo.RepoLink + "/settings/branches")
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.settings.remove_protected_branch_success", rule.RuleName))
	ctx.JSONRedirect(ctx.Repo.RepoLink + "/settings/branches")
}

func UpdateBranchProtectionPriories(ctx *context.Context) {
	var form struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(ctx.Req.Body).Decode(&form); err != nil {
		ctx.JSONError("invalid argument")
		return
	}
	if err := git_model.UpdateProtectBranchPriorities(ctx, ctx.Repo.Repository, form.IDs); err != nil {
		ctx.ServerError("UpdateProtectBranchPriorities", err)
		return
	}
}

// RenameBranchPost responses for rename a branch
func RenameBranchPost(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.RenameBranchForm)

	if !ctx.Repo.CanCreateBranch() {
		ctx.NotFound(nil)
		return
	}

	if ctx.HasError() {
		ctx.Flash.Error(ctx.GetErrMsg())
		ctx.Redirect(ctx.Repo.RepoLink + "/branches")
		return
	}

	msg, err := repository.RenameBranch(ctx, ctx.Repo.Repository, ctx.Doer, form.From, form.To)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrRequiredWorkflowDefaultBranch), errors.Is(err, governance_model.ErrConflict):
			ctx.Flash.Error(err.Error())
			ctx.Redirect(ctx.Repo.RepoLink + "/branches")
		case repo_model.IsErrUserDoesNotHaveAccessToRepo(err):
			ctx.Flash.Error(ctx.Tr("repo.branch.rename_default_or_protected_branch_error"))
			ctx.Redirect(ctx.Repo.RepoLink + "/branches")
		case git_model.IsErrBranchAlreadyExists(err):
			ctx.Flash.Error(ctx.Tr("repo.branch.branch_already_exists", form.To))
			ctx.Redirect(ctx.Repo.RepoLink + "/branches")
		case errors.Is(err, git_model.ErrBranchIsProtected):
			ctx.Flash.Error(ctx.Tr("repo.branch.rename_protected_branch_failed"))
			ctx.Redirect(ctx.Repo.RepoLink + "/branches")
		default:
			ctx.ServerError("RenameBranch", err)
		}
		return
	}

	if msg == "target_exist" {
		ctx.Flash.Error(ctx.Tr("repo.settings.rename_branch_failed_exist", form.To))
		ctx.Redirect(ctx.Repo.RepoLink + "/branches")
		return
	}

	if msg == "from_not_exist" {
		ctx.Flash.Error(ctx.Tr("repo.settings.rename_branch_failed_not_exist", form.From))
		ctx.Redirect(ctx.Repo.RepoLink + "/branches")
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.settings.rename_branch_success", form.From, form.To))
	ctx.Redirect(ctx.Repo.RepoLink + "/branches")
}
