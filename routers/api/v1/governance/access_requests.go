// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/web"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

type AccessRequestSettingOption = governance_service.AccessRequestSettingOption

func accessRequestScope(ctx *context.APIContext) string {
	switch ctx.PathParam("scope") {
	case "groups":
		return "group"
	case "repositories":
		return "repository"
	default:
		return ""
	}
}

func AccessRequests(ctx *context.APIContext) {
	// swagger:operation GET /governance/{scope}/{id}/access-requests governance governanceAccessRequests
	// ---
	// summary: 查看自己的访问申请；管理员同时查看待处理队列
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
	//     "$ref": "#/responses/GovernanceAccessRequestState"
	result, err := governance_service.GetAccessRequestState(ctx, ctx.Doer.ID, accessRequestScope(ctx), ctx.PathParamInt64("id"), ctx.FormInt64("after_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func CreateAccessRequest(ctx *context.APIContext) {
	// swagger:operation POST /governance/{scope}/{id}/access-requests governance governanceCreateAccessRequest
	// ---
	// summary: 提交当前登录用户的访问申请
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
	// responses:
	//   "201":
	//     "$ref": "#/responses/GovernanceAccessRequest"
	result, err := governance_service.RequestAccess(ctx, requestActor(ctx), accessRequestScope(ctx), ctx.PathParamInt64("id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, result)
}

func ApproveAccessRequest(ctx *context.APIContext) {
	// swagger:operation POST /governance/{scope}/{id}/access-requests/{request_id}/approve governance governanceApproveAccessRequest
	// ---
	// summary: 按申请稳定 ID 批准并创建直接授权
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
	// - name: request_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/GroupMemberOption"
	// responses:
	//   "204":
	//     description: 操作已完成
	option := *web.GetForm(ctx).(*GroupMemberOption)
	if option.Role == 0 {
		option.Role = governance_model.Developer
	}
	if err := governance_service.DecideAccessRequest(ctx, requestActor(ctx), accessRequestScope(ctx), ctx.PathParamInt64("id"), ctx.PathParamInt64("request_id"), option, true); err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func RejectAccessRequest(ctx *context.APIContext) {
	// swagger:operation DELETE /governance/{scope}/{id}/access-requests/{request_id} governance governanceRejectAccessRequest
	// ---
	// summary: 申请人撤回自己的申请；管理员拒绝指定申请
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
	// - name: request_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// responses:
	//   "204":
	//     description: 操作已完成
	if err := governance_service.DecideAccessRequest(ctx, requestActor(ctx), accessRequestScope(ctx), ctx.PathParamInt64("id"), ctx.PathParamInt64("request_id"), GroupMemberOption{}, false); err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func SaveAccessRequestSetting(ctx *context.APIContext) {
	// swagger:operation PUT /governance/{scope}/{id}/access-requests/settings governance governanceSaveAccessRequestSetting
	// ---
	// summary: 按设置修订号开启或关闭新的访问申请
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
	//     "$ref": "#/definitions/AccessRequestSettingOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceAccessRequestSetting"
	result, err := governance_service.SaveAccessRequestSetting(ctx, requestActor(ctx), accessRequestScope(ctx), ctx.PathParamInt64("id"), *web.GetForm(ctx).(*AccessRequestSettingOption))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func OwnAccessRequests(ctx *context.APIContext) {
	// swagger:operation GET /governance/access-requests governance governanceOwnAccessRequests
	// ---
	// summary: 查看当前用户的待处理申请快照，最多一百条
	// produces:
	// - application/json
	// parameters:
	// - name: after_id
	//   in: query
	//   type: integer
	//   format: int64
	// responses:
	//   "200":
	//     description: 待处理申请，使用最后一条申请 ID 继续分页
	//     schema:
	//       type: array
	//       items:
	//         "$ref": "#/definitions/AccessRequest"
	requests, err := governance_service.OwnAccessRequests(ctx, ctx.Doer.ID, ctx.FormInt64("after_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, requests)
}

func WithdrawOwnAccessRequest(ctx *context.APIContext) {
	// swagger:operation DELETE /governance/access-requests/{request_id} governance governanceWithdrawOwnAccessRequest
	// ---
	// summary: 撤回当前用户的待处理申请，资源变为私有后仍可使用
	// produces:
	// - application/json
	// parameters:
	// - name: request_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// responses:
	//   "204":
	//     description: 申请已撤回
	if err := governance_service.WithdrawOwnAccessRequest(ctx, requestActor(ctx), ctx.PathParamInt64("request_id")); err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
