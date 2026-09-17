// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"strconv"
	"time"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

func GroupAccessRequests(ctx *context.Context)         { accessRequests(ctx, "group") }
func RepositoryAccessRequests(ctx *context.Context)    { accessRequests(ctx, "repository") }
func SaveGroupAccessRequest(ctx *context.Context)      { saveAccessRequest(ctx, "group") }
func SaveRepositoryAccessRequest(ctx *context.Context) { saveAccessRequest(ctx, "repository") }

func accessRequestPath(scope string, id int64) string {
	prefix := "groups"
	if scope == "repository" {
		prefix = "repositories"
	}
	return setting.AppSubURL + "/governance/" + prefix + "/" + strconv.FormatInt(id, 10) + "/access-requests"
}

func accessRequests(ctx *context.Context, scope string) {
	state, err := governance_service.GetAccessRequestState(ctx, ctx.Doer.ID, scope, ctx.PathParamInt64("id"), ctx.FormInt64("after_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Data["Title"] = "访问申请"
	ctx.Data["State"] = state
	ctx.Data["Scope"] = scope
	if scope == "repository" {
		ctx.Data["MemberPath"] = setting.AppSubURL + "/governance/repositories/" + strconv.FormatInt(ctx.PathParamInt64("id"), 10) + "/members"
	}
	ctx.Data["RequestPath"] = accessRequestPath(scope, ctx.PathParamInt64("id"))
	ctx.Data["RoleNames"] = map[governance_model.Role]string{5: "Minimal Access", 10: "Guest", 15: "Planner", 20: "Reporter", 30: "Developer", 40: "Maintainer", 50: "Owner"}
	ctx.HTML(200, "governance/access_requests")
}

func saveAccessRequest(ctx *context.Context, scope string) {
	id := ctx.PathParamInt64("id")
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	var err error
	switch ctx.FormString("action") {
	case "request":
		_, err = governance_service.RequestAccess(ctx, actor, scope, id)
	case "withdraw", "deny":
		err = governance_service.DecideAccessRequest(ctx, actor, scope, id, ctx.FormInt64("request_id"), governance_service.GroupMemberOption{}, false)
	case "approve":
		option := governance_service.GroupMemberOption{Role: governance_model.Role(ctx.FormInt("role")), CustomRoleID: ctx.FormInt64("custom_role_id"), Revision: ctx.FormInt64("revision")}
		if expiry := ctx.FormString("expires"); expiry != "" {
			date, parseErr := time.Parse("2006-01-02", expiry)
			if parseErr != nil {
				respondError(ctx, governance_model.ErrInvalid)
				return
			}
			option.ExpiresUnix = date.Unix()
		}
		err = governance_service.DecideAccessRequest(ctx, actor, scope, id, ctx.FormInt64("request_id"), option, true)
	case "settings":
		_, err = governance_service.SaveAccessRequestSetting(ctx, actor, scope, id, governance_service.AccessRequestSettingOption{Disabled: ctx.FormBool("disabled"), Revision: ctx.FormInt64("setting_revision")})
	default:
		err = governance_model.ErrInvalid
	}
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Flash.Success("访问申请状态已更新")
	ctx.Redirect(accessRequestPath(scope, id))
}

func OwnAccessRequests(ctx *context.Context) {
	requests, err := governance_service.OwnAccessRequests(ctx, ctx.Doer.ID, ctx.FormInt64("after_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Data["Title"] = "我的访问申请"
	ctx.Data["Requests"] = requests
	if len(requests) == 100 {
		ctx.Data["NextID"] = requests[99].ID
	}
	ctx.HTML(200, "governance/own_access_requests")
}

func WithdrawOwnAccessRequest(ctx *context.Context) {
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	if err := governance_service.WithdrawOwnAccessRequest(ctx, actor, ctx.FormInt64("request_id")); err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Flash.Success("申请已撤回")
	ctx.Redirect(setting.AppSubURL + "/governance/access-requests")
}
