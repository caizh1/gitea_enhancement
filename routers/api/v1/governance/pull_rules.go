// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"

	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/modules/web"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
	issue_service "gitea.dev/services/issue"
)

type PullApprovalRuleOption struct {
	Rule     governance_model.ApprovalRule `json:"rule"`
	RuleID   int64                         `json:"rule_id"`
	Revision int64                         `json:"revision"`
	Remove   bool                          `json:"remove"`
}

func PullApprovalRules(ctx *context.APIContext) {
	// swagger:operation GET /governance/pulls/{id}/approval-rules governance governancePullApprovalRules
	// ---
	// summary: 查看 PR 独立审批规则、当前版本及强制策略
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   type: integer
	//   format: int64
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernancePullApprovalRules"
	result, err := governance_service.ListPullApprovalRules(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	pr, err := issues_model.GetPullRequestByID(ctx, ctx.PathParamInt64("id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	result, err = governance_service.ProjectPullApprovalRules(ctx, ctx.Doer.ID, pr.BaseRepoID, result)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func SavePullApprovalRule(ctx *context.APIContext) {
	// swagger:operation PUT /governance/pulls/{id}/approval-rules governance governanceSavePullApprovalRule
	// ---
	// summary: 按 PR 版本增删改一条独立规则，规则 ID 为零表示新增
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   type: integer
	//   format: int64
	//   required: true
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/PullApprovalRuleOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernancePullRuleVersion"
	//   "409":
	//     description: PR 版本已变化或最终合并授权正在进行
	option := web.GetForm(ctx).(*PullApprovalRuleOption)
	result, err := governance_service.SavePullApprovalRule(ctx, requestActor(ctx), ctx.PathParamInt64("id"), option.RuleID, option.Revision, option.Rule, option.Remove)
	if err != nil {
		respondError(ctx, err)
		return
	}
	if pr, pullErr := issues_model.GetPullRequestByID(ctx, ctx.PathParamInt64("id")); pullErr == nil {
		issue_service.SyncGovernanceReviewRequests(ctx, pr, ctx.Doer)
	}
	ctx.JSON(http.StatusOK, result)
}
