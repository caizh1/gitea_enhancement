// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

func GroupInvitations(ctx *context.Context)         { invitations(ctx, "group") }
func RepositoryInvitations(ctx *context.Context)    { invitations(ctx, "repository") }
func SaveGroupInvitation(ctx *context.Context)      { saveInvitation(ctx, "group") }
func SaveRepositoryInvitation(ctx *context.Context) { saveInvitation(ctx, "repository") }

func invitationPath(scope string, id int64) string {
	prefix := "groups"
	if scope == "repository" {
		prefix = "repositories"
	}
	return setting.AppSubURL + "/governance/" + prefix + "/" + strconv.FormatInt(id, 10) + "/invitations"
}

func invitations(ctx *context.Context, scope string) {
	state, err := governance_service.ListInvitations(ctx, ctx.Doer.ID, scope, ctx.PathParamInt64("id"), ctx.FormInt64("after_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	ctx.Data["Title"] = "成员邀请"
	ctx.Data["State"] = state
	ctx.Data["Scope"] = scope
	ctx.Data["RoleNames"] = map[governance_model.Role]string{5: "Minimal Access", 10: "Guest", 15: "Planner", 20: "Reporter", 30: "Developer", 40: "Maintainer", 50: "Owner"}
	ctx.Data["DeliveryNames"] = map[string]string{"pending": "待投递", "sending": "正在投递", "sent": "已交给邮件服务器", "failed": "投递失败，等待重试", "blocked": "邀请者权限或资源状态变化，暂停投递"}
	ctx.HTML(http.StatusOK, "governance/invitations")
}

func saveInvitation(ctx *context.Context, scope string) {
	id := ctx.PathParamInt64("id")
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	var err error
	switch ctx.FormString("action") {
	case "revoke":
		err = governance_service.RevokeInvitation(ctx, actor, scope, id, ctx.FormInt64("invitation_id"))
	case "create":
		option := governance_service.InvitationOption{Email: ctx.FormString("email"), Role: governance_model.Role(ctx.FormInt("role")), CustomRoleID: ctx.FormInt64("custom_role_id"), Revision: ctx.FormInt64("revision")}
		if expiry := ctx.FormString("expires"); expiry != "" {
			date, e := time.Parse("2006-01-02", expiry)
			if e != nil {
				respondError(ctx, governance_model.ErrInvalid)
				return
			}
			option.ExpiresUnix = date.Unix()
		}
		_, err = governance_service.CreateInvitation(ctx, actor, scope, id, option)
	default:
		err = governance_model.ErrInvalid
	}
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Flash.Success("邀请状态已保存；投递结果以列表中的状态为准")
	ctx.Redirect(invitationPath(scope, id))
}

func InvitationLanding(ctx *context.Context) {
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	ctx.Resp.Header().Set("Referrer-Policy", "no-referrer")
	ctx.Data["Title"] = "成员邀请"
	ctx.Data["InvitationID"] = ctx.PathParamInt64("id")
	path := setting.AppSubURL + "/governance/invitations/" + strconv.FormatInt(ctx.PathParamInt64("id"), 10)
	ctx.Data["InvitationPath"] = path
	ctx.Data["LoginPath"] = setting.AppSubURL + "/user/login?redirect_to=" + url.QueryEscape(path)
	ctx.HTML(http.StatusOK, "governance/invitation_accept")
}

func DecideInvitation(ctx *context.Context) {
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	ctx.Resp.Header().Set("Referrer-Policy", "no-referrer")
	id := ctx.PathParamInt64("id")
	if err := ctx.Req.ParseForm(); err != nil {
		respondError(ctx, governance_model.ErrInvalid)
		return
	}
	token := ctx.Req.PostForm.Get("token")
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	ctx.Data["Title"] = "成员邀请"
	ctx.Data["InvitationID"] = id
	ctx.Data["InvitationPath"] = setting.AppSubURL + "/governance/invitations/" + strconv.FormatInt(id, 10)
	var err error
	switch ctx.Req.PostForm.Get("action") {
	case "preview":
		ctx.Data["Invitation"], err = governance_service.PreviewInvitation(ctx, ctx.Doer.ID, id, token)
		ctx.Data["Token"] = token
	case "accept":
		err = governance_service.AcceptInvitation(ctx, actor, id, token)
		ctx.Data["Completed"] = true
		ctx.Data["Outcome"] = "邀请已接受，授权已生效"
	case "decline":
		err = governance_service.DeclineInvitation(ctx, actor, id, token)
		ctx.Data["Completed"] = true
		ctx.Data["Outcome"] = "邀请已拒绝"
	default:
		err = governance_model.ErrInvalid
	}
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Data["RoleNames"] = map[governance_model.Role]string{5: "Minimal Access", 10: "Guest", 15: "Planner", 20: "Reporter", 30: "Developer", 40: "Maintainer", 50: "Owner"}
	ctx.HTML(http.StatusOK, "governance/invitation_accept")
}
