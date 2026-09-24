// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"
	"strconv"

	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
	issue_service "gitea.dev/services/issue"
)

func ApprovalPolicies(ctx *context.Context) {
	scope, id := ctx.PathParam("scope"), ctx.PathParamInt64("scope_id")
	policies, err := governance_service.ListApprovalPolicies(ctx, ctx.Doer.ID, scope, id)
	if err != nil {
		respondError(ctx, err)
		return
	}
	if ctx.Data["NativeApprovalPolicy"] != true {
		if scope == "instance" {
			ctx.Redirect(setting.AppSubURL + "/-/admin/approvals")
			return
		}
		user, err := user_model.GetUserByID(ctx, id)
		if err != nil {
			respondError(ctx, err)
			return
		}
		ctx.Redirect(user.OrganisationLink() + "/settings/approvals")
		return
	}
	choices, err := governance_service.ListApprovalPolicyChoices(ctx, ctx.Doer.ID, scope, id)
	if err != nil {
		respondError(ctx, err)
		return
	}
	settings, err := governance_service.ListScopeApprovalSettings(ctx, ctx.Doer.ID, scope, id)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Data["ScopeSettingsLocked"] = settings.Local.Locked
	ctx.Data["HasLocalSettings"] = settings.Local.ID != 0 && !settings.Local.Inherit
	ctx.Data["PolicyMode"] = true
	state := &governance_service.RepositoryApprovalRules{FullPath: policies.FullPath, CanManage: true, Rules: policies.Rules, Settings: settings.Effective, SettingsRevision: settings.Local.Revision}
	renderApprovalRules(ctx, state, choices, scope, id)
}

func SaveApprovalPolicy(ctx *context.Context) {
	scope, id := ctx.PathParam("scope"), ctx.PathParamInt64("scope_id")
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	if action := ctx.FormString("action"); action == "settings" || action == "inherit_settings" {
		option := governance_service.ScopeApprovalSettingsOption{ApprovalSettingsOption: approvalSettingsForm(ctx), Locked: ctx.FormBool("locked")}
		if _, err := governance_service.SaveScopeApprovalSettings(ctx, actor, scope, id, option, action == "inherit_settings"); err != nil {
			respondError(ctx, err)
			return
		}
		issue_service.SyncScopeGovernanceReviewRequests(ctx, scope, id, ctx.Doer)
		ctx.Flash.Success("审批设置已更新，后代项目按设置来源继承")
		ctx.Redirect(setting.AppSubURL + "/governance/approval-policies/" + scope + "/" + strconv.FormatInt(id, 10))
		return
	}
	rule, err := approvalRuleForm(ctx)
	if err != nil {
		respondError(ctx, err)
		return
	}
	_, err = governance_service.SaveApprovalPolicy(ctx, actor, scope, id, ctx.FormInt64("rule_id"), ctx.FormInt64("revision"), rule, ctx.FormString("action") == "remove")
	if err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncScopeGovernanceReviewRequests(ctx, scope, id, ctx.Doer)
	ctx.Flash.Success("强制审批策略已更新，后代项目不能降低该要求")
	ctx.Redirect(setting.AppSubURL + "/governance/approval-policies/" + scope + "/" + strconv.FormatInt(id, 10))
}

func NativeGroupApprovalPolicies(ctx *context.Context) {
	ctx.SetPathParam("scope", "group")
	ctx.SetPathParam("scope_id", strconv.FormatInt(ctx.Org.Organization.ID, 10))
	ctx.Data["NativeApprovalPolicy"], ctx.Data["PageIsSettingsApprovals"] = true, true
	if ctx.Req.Method == http.MethodPost {
		SaveApprovalPolicy(ctx)
		return
	}
	ApprovalPolicies(ctx)
}

func NativeInstanceApprovalPolicies(ctx *context.Context) {
	if ctx.Doer == nil || !ctx.Doer.IsAdmin {
		respondError(ctx, governance_model.ErrNotFound)
		return
	}
	ctx.SetPathParam("scope", "instance")
	ctx.SetPathParam("scope_id", "0")
	ctx.Data["NativeApprovalPolicy"], ctx.Data["PageIsAdminApprovals"] = true, true
	if ctx.Req.Method == http.MethodPost {
		SaveApprovalPolicy(ctx)
		return
	}
	ApprovalPolicies(ctx)
}
