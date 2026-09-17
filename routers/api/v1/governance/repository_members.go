// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"

	"gitea.dev/modules/web"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
	issue_service "gitea.dev/services/issue"
)

func RepositoryMembers(ctx *context.APIContext) {
	// swagger:operation GET /governance/repositories/{id}/members governance governanceRepositoryMembers
	// ---
	// summary: 列出项目成员，可选择包含继承与原生来源
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: after_id
	//   in: query
	//   type: integer
	//   format: int64
	// - name: include_inherited
	//   in: query
	//   type: boolean
	//   description: 包含继承、共享、原生协作者与团队来源，默认只返回直接成员
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceRepositoryMembers"
	list := governance_service.ListRepositoryMembers
	if ctx.FormBool("include_inherited") {
		list = governance_service.ListRepositoryAllMembers
	}
	result, err := list(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"), ctx.FormInt64("after_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func SetRepositoryMember(ctx *context.APIContext) {
	// swagger:operation PUT /governance/repositories/{id}/members/{user_id} governance governanceSetRepositoryMember
	// ---
	// summary: 创建或修改项目直接成员来源
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: user_id
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
	//     description: 直接授权已保存
	option := *web.GetForm(ctx).(*GroupMemberOption)
	option.UserID = ctx.PathParamInt64("user_id")
	if err := governance_service.SetRepositoryMember(ctx, requestActor(ctx), ctx.PathParamInt64("id"), option, false); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncRepositoryGovernanceReviewRequests(ctx, ctx.PathParamInt64("id"), ctx.Doer)
	ctx.Status(http.StatusNoContent)
}

func RemoveRepositoryMember(ctx *context.APIContext) {
	// swagger:operation DELETE /governance/repositories/{id}/members/{user_id} governance governanceRemoveRepositoryMember
	// ---
	// summary: 按修订号撤销项目直接来源，保留其他有效权限来源
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: user_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: revision
	//   in: query
	//   required: true
	//   type: integer
	//   format: int64
	// responses:
	//   "204":
	//     description: 直接授权已撤销
	option := GroupMemberOption{UserID: ctx.PathParamInt64("user_id"), Revision: ctx.FormInt64("revision")}
	if err := governance_service.SetRepositoryMember(ctx, requestActor(ctx), ctx.PathParamInt64("id"), option, true); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncRepositoryGovernanceReviewRequests(ctx, ctx.PathParamInt64("id"), ctx.Doer)
	ctx.Status(http.StatusNoContent)
}
