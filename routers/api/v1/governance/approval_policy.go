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
	issue_service "gitea.dev/services/issue"
)

func ApprovalPolicies(ctx *context.APIContext) {
	// swagger:operation GET /governance/approval-policies/{scope}/{scope_id} governance governanceApprovalPolicies
	// ---
	// summary: 查看实例或顶级组的强制审批策略
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
	//     "$ref": "#/responses/GovernanceApprovalPolicies"
	result, err := governance_service.ListApprovalPolicies(ctx, ctx.Doer.ID, ctx.PathParam("scope"), ctx.PathParamInt64("scope_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func CreateApprovalPolicy(ctx *context.APIContext) {
	// swagger:operation POST /governance/approval-policies/{scope}/{scope_id} governance governanceCreateApprovalPolicy
	// ---
	// summary: 创建不可由后代项目降低的审批策略
	// consumes:
	// - application/json
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
	//     "$ref": "#/definitions/ApprovalRuleOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/GovernanceApprovalRule"
	saveApprovalPolicy(ctx, 0, http.StatusCreated)
}

func UpdateApprovalPolicy(ctx *context.APIContext) {
	// swagger:operation PUT /governance/approval-policies/{scope}/{scope_id}/{rule_id} governance governanceUpdateApprovalPolicy
	// ---
	// summary: 按修订号修改本级审批策略
	// consumes:
	// - application/json
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
	// - name: rule_id
	//   in: path
	//   type: integer
	//   format: int64
	//   required: true
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/ApprovalRuleOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceApprovalRule"
	saveApprovalPolicy(ctx, ctx.PathParamInt64("rule_id"), http.StatusOK)
}

func RemoveApprovalPolicy(ctx *context.APIContext) {
	// swagger:operation DELETE /governance/approval-policies/{scope}/{scope_id}/{rule_id} governance governanceRemoveApprovalPolicy
	// ---
	// summary: 按修订号删除本级审批策略
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
	// - name: rule_id
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
	//     description: 已删除
	_, err := governance_service.SaveApprovalPolicy(ctx, requestActor(ctx), ctx.PathParam("scope"), ctx.PathParamInt64("scope_id"), ctx.PathParamInt64("rule_id"), ctx.FormInt64("revision"), governance_model.ApprovalRule{}, true)
	if err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncScopeGovernanceReviewRequests(ctx, ctx.PathParam("scope"), ctx.PathParamInt64("scope_id"), ctx.Doer)
	ctx.Status(http.StatusNoContent)
}

func saveApprovalPolicy(ctx *context.APIContext, ruleID int64, status int) {
	option := web.GetForm(ctx).(*ApprovalRuleOption)
	result, err := governance_service.SaveApprovalPolicy(ctx, requestActor(ctx), ctx.PathParam("scope"), ctx.PathParamInt64("scope_id"), ruleID, option.Revision, option.Rule, false)
	if err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncScopeGovernanceReviewRequests(ctx, ctx.PathParam("scope"), ctx.PathParamInt64("scope_id"), ctx.Doer)
	ctx.JSON(status, result)
}
