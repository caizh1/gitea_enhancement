// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"

	"gitea.dev/modules/web"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

type (
	InvitationOption      = governance_service.InvitationOption
	InvitationTokenOption = governance_service.InvitationTokenOption
)

func Invitations(ctx *context.APIContext) {
	// swagger:operation GET /governance/{scope}/{id}/invitations governance governanceInvitations
	// ---
	// summary: 查看有权管理的待接受邀请
	// produces:
	// - application/json
	// parameters:
	// - name: scope
	//   in: path
	//   required: true
	//   type: string
	//   enum: [groups, repositories]
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: after_id
	//   in: query
	//   type: integer
	//   format: int64
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceInvitations"
	result, err := governance_service.ListInvitations(ctx, ctx.Doer.ID, accessRequestScope(ctx), ctx.PathParamInt64("id"), ctx.FormInt64("after_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func CreateInvitation(ctx *context.APIContext) {
	// swagger:operation POST /governance/{scope}/{id}/invitations governance governanceCreateInvitation
	// ---
	// summary: 创建邮箱成员邀请
	// produces:
	// - application/json
	// parameters:
	// - name: scope
	//   in: path
	//   required: true
	//   type: string
	//   enum: [groups, repositories]
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/InvitationOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/GovernanceInvitation"
	result, err := governance_service.CreateInvitation(ctx, requestActor(ctx), accessRequestScope(ctx), ctx.PathParamInt64("id"), *web.GetForm(ctx).(*InvitationOption))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, result)
}

func RevokeInvitation(ctx *context.APIContext) {
	// swagger:operation DELETE /governance/{scope}/{id}/invitations/{invitation_id} governance governanceRevokeInvitation
	// ---
	// summary: 撤销待接受邀请
	// produces:
	// - application/json
	// parameters:
	// - name: scope
	//   in: path
	//   required: true
	//   type: string
	//   enum: [groups, repositories]
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: invitation_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// responses:
	//   "204":
	//     description: 操作已完成
	err := governance_service.RevokeInvitation(ctx, requestActor(ctx), accessRequestScope(ctx), ctx.PathParamInt64("id"), ctx.PathParamInt64("invitation_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func PreviewInvitation(ctx *context.APIContext) {
	// swagger:operation POST /governance/invitations/{id}/preview governance governancePreviewInvitation
	// ---
	// summary: 通过正文令牌预览本人邮箱邀请
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/InvitationTokenOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceInvitationPreview"
	result, err := governance_service.PreviewInvitation(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"), web.GetForm(ctx).(*InvitationTokenOption).Token)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func AcceptInvitation(ctx *context.APIContext) {
	// swagger:operation POST /governance/invitations/{id}/accept governance governanceAcceptInvitation
	// ---
	// summary: 验证邮箱与当前权限后接受邀请
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/InvitationTokenOption"
	// responses:
	//   "204":
	//     description: 操作已完成
	err := governance_service.AcceptInvitation(ctx, requestActor(ctx), ctx.PathParamInt64("id"), web.GetForm(ctx).(*InvitationTokenOption).Token)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func DeclineInvitation(ctx *context.APIContext) {
	// swagger:operation POST /governance/invitations/{id}/decline governance governanceDeclineInvitation
	// ---
	// summary: 拒绝本人邮箱邀请
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/InvitationTokenOption"
	// responses:
	//   "204":
	//     description: 操作已完成
	err := governance_service.DeclineInvitation(ctx, requestActor(ctx), ctx.PathParamInt64("id"), web.GetForm(ctx).(*InvitationTokenOption).Token)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
