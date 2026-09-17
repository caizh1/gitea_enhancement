// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	stdcontext "context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/base"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
	issue_service "gitea.dev/services/issue"
)

func ApprovalRules(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	if _, err := governance_service.ListRepositoryApprovalRules(ctx, ctx.Doer.ID, id); err != nil {
		respondError(ctx, err)
		return
	}
	repo, err := repo_model.GetRepositoryByID(ctx, id)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Redirect(repo.Link() + "/settings/pulls")
}

func PullApprovalRules(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	if _, err := governance_service.ListPullApprovalRules(ctx, ctx.Doer.ID, id); err != nil {
		respondError(ctx, err)
		return
	}
	pr, err := issues_model.GetPullRequestByID(ctx, id)
	if err != nil {
		respondError(ctx, err)
		return
	}
	repo, err := repo_model.GetRepositoryByID(ctx, pr.BaseRepoID)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Redirect(repo.Link() + "/pulls/" + strconv.FormatInt(pr.Index, 10) + "/approvals")
}

func NativePullApprovalRules(ctx *context.Context) {
	pr, err := issues_model.GetPullRequestByIndex(ctx, ctx.Repo.Repository.ID, ctx.PathParamInt64("index"))
	if err != nil {
		respondError(ctx, governance_model.ErrNotFound)
		return
	}
	ctx.SetPathParam("id", strconv.FormatInt(pr.ID, 10))
	ctx.Data["PullRequestLink"] = ctx.Repo.RepoLink + "/pulls/" + strconv.FormatInt(pr.Index, 10)
	if ctx.Req.Method == http.MethodPost {
		SavePullApprovalRule(ctx)
		return
	}
	approvalRulesPage(ctx, true)
}

func approvalRulesPage(ctx *context.Context, pullMode bool) {
	id := ctx.PathParamInt64("id")
	pullID := id
	if pullMode {
		pr, err := issues_model.GetPullRequestByID(ctx, pullID)
		if err != nil {
			respondError(ctx, governance_model.ErrNotFound)
			return
		}
		id = pr.BaseRepoID
	}
	state, err := governance_service.ListRepositoryApprovalRules(ctx, ctx.Doer.ID, id)
	if err != nil {
		respondError(ctx, err)
		return
	}
	editableScope := "repository"
	if pullMode {
		pullState, err := governance_service.ListPullApprovalRules(ctx, ctx.Doer.ID, pullID)
		if err != nil {
			respondError(ctx, err)
			return
		}
		state.CanManage = pullState.CanManage
		useProjectRules := pullState.Settings.Settings.PreventOverrides || pullState.Version.ID == 0
		ctx.Data["PullUsesProjectRules"] = useProjectRules
		state.Rules = append([]*governance_model.ApprovalRule{}, pullState.Policies...)
		for _, rule := range pullState.Version.Rules {
			view := rule
			if !useProjectRules {
				view.ScopeType, view.ScopeID = "pull", pullID
			}
			state.Rules = append(state.Rules, &view)
		}
		ctx.Data["PullRuleRevision"] = pullState.Version.Revision
		ctx.Data["PullMode"] = true
		evaluation, evaluationErr := governance_service.ReadPullApprovalState(ctx, ctx.Doer.ID, pullID)
		if evaluationErr != nil {
			ctx.Data["EvaluationError"] = "审批状态正在变化或暂时无法核对，请刷新后查看。最终合并仍由服务端重新判定。"
		} else {
			projected, projectErr := governance_service.ProjectPullApprovalResult(ctx, ctx.Doer.ID, pullID, id, evaluation)
			if projectErr != nil {
				ctx.Data["EvaluationError"] = "审批状态正在变化或暂时无法核对，请刷新后查看。最终合并仍由服务端重新判定。"
			} else {
				ctx.Data["ApprovalEvaluation"] = projected
			}
		}
		editableScope = "pull"
	}
	state, err = governance_service.ProjectRepositoryApprovalRules(ctx, ctx.Doer.ID, id, state)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Data["EditableScope"] = editableScope
	choices, err := governance_service.ListApprovalChoices(ctx, ctx.Doer.ID, id)
	if err != nil {
		respondError(ctx, err)
		return
	}
	renderApprovalRules(ctx, state, choices, editableScope, id)
}

func renderApprovalRules(ctx *context.Context, state *governance_service.RepositoryApprovalRules, choices *governance_service.ApprovalChoices, editableScope string, id int64) {
	ctx.Data["EditableScope"] = editableScope
	edited := &governance_model.ApprovalRule{Required: 1, BranchMode: "all", Enabled: true}
	if editID := ctx.FormInt64("edit"); editID != 0 {
		var found bool
		for _, rule := range state.Rules {
			if rule.ID == editID && rule.ScopeType == editableScope {
				edited = rule
				found = true
				break
			}
		}
		if !found || !state.CanManage {
			respondError(ctx, governance_model.ErrNotFound)
			return
		}
	}
	names := func(subjects *[]governance_service.ApprovalSubject, selected []int64) map[int64]string {
		result := make(map[int64]string)
		for _, subject := range *subjects {
			result[subject.ID] = subject.Name
		}
		for _, id := range selected {
			if result[id] == "" {
				result[id] = "不可用或不可见的历史选择"
				*subjects = append(*subjects, governance_service.ApprovalSubject{ID: id, Name: result[id]})
			}
		}
		return result
	}
	visibleUserIDs, visibleGroupIDs, visibleTeamIDs := slices.Clone(edited.UserIDs), slices.Clone(edited.GroupIDs), slices.Clone(edited.TeamIDs)
	for _, rule := range state.Rules {
		visibleUserIDs = append(visibleUserIDs, rule.UserIDs...)
		visibleGroupIDs = append(visibleGroupIDs, rule.GroupIDs...)
		visibleTeamIDs = append(visibleTeamIDs, rule.TeamIDs...)
	}
	ctx.Data["UserNames"] = names(&choices.Users, visibleUserIDs)
	ctx.Data["GroupNames"] = names(&choices.Groups, visibleGroupIDs)
	ctx.Data["TeamNames"] = names(&choices.Teams, visibleTeamIDs)
	selected := func(ids []int64) map[int64]bool {
		result := make(map[int64]bool)
		for _, id := range ids {
			result[id] = true
		}
		return result
	}
	ctx.Data["SelectedUsers"], ctx.Data["SelectedGroups"], ctx.Data["SelectedTeams"] = selected(edited.UserIDs), selected(edited.GroupIDs), selected(edited.TeamIDs)
	ctx.Data["Title"], ctx.Data["State"], ctx.Data["Choices"], ctx.Data["Edited"], ctx.Data["RepositoryID"] = "审批规则", state, choices, edited, id
	ctx.Data["BranchText"] = strings.Join(edited.Branches, "\n")
	canReauthenticate, err := governance_service.CanReauthenticateApproval(ctx, ctx.Doer.ID)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Data["CanReauthenticateApproval"] = canReauthenticate
	ctx.Data["SettingLabels"] = map[string]string{"prevent_author": "禁止作者批准自己的 PR", "prevent_committer": "禁止提交者批准", "prevent_overrides": "禁止在 PR 中覆盖项目规则", "reset_on_change": "代码差异变化后取消批准", "require_reauthentication": "批准前再次认证"}
	if ctx.Data["PolicyMode"] == true {
		if editableScope == "instance" {
			ctx.HTML(http.StatusOK, "admin/approvals")
		} else {
			ctx.HTML(http.StatusOK, "org/settings/approvals")
		}
	} else {
		ctx.HTML(http.StatusOK, "repo/issue/approval_rules")
	}
}

func SaveApprovalRule(ctx *context.Context) {
	repoID, ruleID := ctx.PathParamInt64("id"), ctx.FormInt64("rule_id")
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	if ctx.FormString("action") == "install_reference_hook" {
		if err := governance_service.InstallRepositoryReferenceHook(ctx, actor, repoID); err != nil {
			respondError(ctx, err)
			return
		}
		ctx.Flash.Success("引用事务入口已安装并回读验证")
		ctx.Redirect(setting.AppSubURL + "/governance/repositories/" + strconv.FormatInt(repoID, 10) + "/approval-rules")
		return
	}
	if ctx.FormString("action") == "settings" {
		option := approvalSettingsForm(ctx)
		if _, err := governance_service.SaveRepositoryApprovalSettings(ctx, actor, repoID, option); err != nil {
			respondError(ctx, err)
			return
		}
		issue_service.SyncRepositoryGovernanceReviewRequests(ctx, repoID, ctx.Doer)
		ctx.Flash.Success("审批设置已更新，开放 PR 的规则按覆盖设置处理")
		ctx.Redirect(setting.AppSubURL + "/governance/repositories/" + strconv.FormatInt(repoID, 10) + "/approval-rules")
		return
	}

	remove := ctx.FormString("action") == "remove"
	rule, err := approvalRuleForm(ctx)
	if err != nil {
		respondError(ctx, err)
		return
	}
	if ctx.FormBool("every_person") && !remove {
		if ruleID != 0 || len(rule.UserIDs) == 0 || len(rule.UserIDs) > 100 || len(rule.GroupIDs)+len(rule.TeamIDs) > 0 || rule.AllEligible {
			respondError(ctx, governance_model.ErrInvalid)
			return
		}
		err = governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repoID)}, func(tx stdcontext.Context) error {
			for _, id := range rule.UserIDs {
				user, err := user_model.GetUserByID(tx, id)
				if err != nil {
					return err
				}
				individual := rule
				individual.UserIDs, individual.Required, individual.Name = []int64{id}, 1, rule.Name+" · "+user.Name
				if _, err := governance_service.SaveRepositoryApprovalRule(tx, actor, repoID, 0, 0, individual, false); err != nil {
					return err
				}
			}
			return nil
		})
	} else {
		_, err = governance_service.SaveRepositoryApprovalRule(ctx, actor, repoID, ruleID, ctx.FormInt64("revision"), rule, remove)
	}
	if err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncRepositoryGovernanceReviewRequests(ctx, repoID, ctx.Doer)
	ctx.Flash.Success("审批规则已保存")
	ctx.Redirect(setting.AppSubURL + "/governance/repositories/" + strconv.FormatInt(repoID, 10) + "/approval-rules")
}

func approvalRuleForm(ctx *context.Context) (governance_model.ApprovalRule, error) {
	rule := governance_model.ApprovalRule{Name: ctx.FormString("name"), Required: ctx.FormInt("required"), BranchMode: ctx.FormString("branch_mode"), Enabled: ctx.FormBool("enabled"), AllEligible: ctx.FormBool("all_eligible"), RespectNativeApprovalPool: ctx.FormBool("respect_native_approval_pool")}
	var err error
	rule.UserIDs, err = base.StringsToInt64s(ctx.FormStrings("users"))
	if err == nil {
		rule.GroupIDs, err = base.StringsToInt64s(ctx.FormStrings("groups"))
	}
	if err == nil {
		rule.TeamIDs, err = base.StringsToInt64s(ctx.FormStrings("teams"))
	}
	if err != nil {
		return rule, governance_model.ErrInvalid
	}
	for branch := range strings.SplitSeq(ctx.FormString("branches"), "\n") {
		if branch = strings.TrimSpace(branch); branch != "" {
			rule.Branches = append(rule.Branches, branch)
		}
	}
	return rule, nil
}

func SavePullApprovalRule(ctx *context.Context) {
	rule, err := approvalRuleForm(ctx)
	if err != nil {
		respondError(ctx, err)
		return
	}
	pullID := ctx.PathParamInt64("id")
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	_, err = governance_service.SavePullApprovalRule(ctx, actor, pullID, ctx.FormInt64("rule_id"), ctx.FormInt64("revision"), rule, ctx.FormString("action") == "remove")
	if err != nil {
		respondError(ctx, err)
		return
	}
	if pr, pullErr := issues_model.GetPullRequestByID(ctx, pullID); pullErr == nil {
		issue_service.SyncGovernanceReviewRequests(ctx, pr, ctx.Doer)
	}
	ctx.Flash.Success("当前 PR 的审批规则已保存，项目模板及上级策略保持原有内容")
	ctx.Redirect(setting.AppSubURL + "/governance/pulls/" + strconv.FormatInt(pullID, 10) + "/approval-rules")
}

func approvalSettingsForm(ctx *context.Context) governance_service.ApprovalSettingsOption {
	return governance_service.ApprovalSettingsOption{Revision: ctx.FormInt64("revision"), PreventAuthor: ctx.FormBool("prevent_author"), PreventCommitter: ctx.FormBool("prevent_committer"), PreventOverrides: ctx.FormBool("prevent_overrides"), ResetOnChange: ctx.FormBool("reset_on_change"), RequireReauthentication: ctx.FormBool("require_reauthentication")}
}
