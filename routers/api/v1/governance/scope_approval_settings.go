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

type ScopeApprovalSettingsOption = governance_service.ScopeApprovalSettingsOption

func ScopeApprovalSettings(ctx *context.APIContext) {
	// swagger:operation GET /governance/approval-settings/{scope}/{scope_id} governance governanceScopeApprovalSettings
	// ---
	// summary: 查看实例或顶级组审批设置及来源
	// produces:
	// - application/json
	// parameters:
	// - name: scope
	//   in: path
	//   type: string
	//   enum: [instance, group]
	//   required: true
	// - name: scope_id
	//   in: path
	//   type: integer
	//   format: int64
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceScopeApprovalSettings"
	result, err := governance_service.ListScopeApprovalSettings(ctx, ctx.Doer.ID, ctx.PathParam("scope"), ctx.PathParamInt64("scope_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func UpdateScopeApprovalSettings(ctx *context.APIContext) {
	// swagger:operation PUT /governance/approval-settings/{scope}/{scope_id} governance governanceUpdateScopeApprovalSettings
	// ---
	// summary: 按修订号保存实例或顶级组审批设置
	// produces:
	// - application/json
	// parameters:
	// - name: scope
	//   in: path
	//   type: string
	//   enum: [instance, group]
	//   required: true
	// - name: scope_id
	//   in: path
	//   type: integer
	//   format: int64
	//   required: true
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/ScopeApprovalSettingsOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceApprovalSettings"
	option := *web.GetForm(ctx).(*ScopeApprovalSettingsOption)
	result, err := governance_service.SaveScopeApprovalSettings(ctx, requestActor(ctx), ctx.PathParam("scope"), ctx.PathParamInt64("scope_id"), option, false)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func RemoveScopeApprovalSettings(ctx *context.APIContext) {
	// swagger:operation DELETE /governance/approval-settings/{scope}/{scope_id} governance governanceRemoveScopeApprovalSettings
	// ---
	// summary: 移除本级设置并恢复继承
	// produces:
	// - application/json
	// parameters:
	// - name: scope
	//   in: path
	//   type: string
	//   enum: [instance, group]
	//   required: true
	// - name: scope_id
	//   in: path
	//   type: integer
	//   format: int64
	//   required: true
	// - name: revision
	//   in: query
	//   type: integer
	//   format: int64
	//   required: true
	// responses:
	//   "204":
	//     description: 已移除
	option := ScopeApprovalSettingsOption{ApprovalSettingsOption: ApprovalSettingsOption{Revision: ctx.FormInt64("revision")}}
	_, err := governance_service.SaveScopeApprovalSettings(ctx, requestActor(ctx), ctx.PathParam("scope"), ctx.PathParamInt64("scope_id"), option, true)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
