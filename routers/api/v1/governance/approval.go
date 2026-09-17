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

type ApprovalRuleOption struct {
	Rule     governance_model.ApprovalRule `json:"rule"`
	Revision int64                         `json:"revision"`
}

func RepositoryApprovalRules(ctx *context.APIContext) {
	// swagger:operation GET /governance/repositories/{id}/approval-rules governance governanceRepositoryApprovalRules
	// ---
	// summary: 查看项目审批规则及继承来源，单独报告门禁就绪状态
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
	//     "$ref": "#/responses/GovernanceRepositoryApprovalRules"
	result, err := governance_service.ListRepositoryApprovalRules(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	result, err = governance_service.ProjectRepositoryApprovalRules(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"), result)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

func CreateRepositoryApprovalRule(ctx *context.APIContext) {
	// swagger:operation POST /governance/repositories/{id}/approval-rules governance governanceCreateRepositoryApprovalRule
	// ---
	// summary: 创建项目本级审批规则
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
	//     "$ref": "#/definitions/ApprovalRuleOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/GovernanceApprovalRule"
	saveRepositoryApprovalRule(ctx, 0, http.StatusCreated)
}

func UpdateRepositoryApprovalRule(ctx *context.APIContext) {
	// swagger:operation PUT /governance/repositories/{id}/approval-rules/{rule_id} governance governanceUpdateRepositoryApprovalRule
	// ---
	// summary: 按修订号更新项目本级审批规则
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
	saveRepositoryApprovalRule(ctx, ctx.PathParamInt64("rule_id"), http.StatusOK)
}

func saveRepositoryApprovalRule(ctx *context.APIContext, id int64, status int) {
	option := web.GetForm(ctx).(*ApprovalRuleOption)
	result, err := governance_service.SaveRepositoryApprovalRule(ctx, requestActor(ctx), ctx.PathParamInt64("id"), id, option.Revision, option.Rule, false)
	if err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncRepositoryGovernanceReviewRequests(ctx, ctx.PathParamInt64("id"), ctx.Doer)
	ctx.JSON(status, result)
}

func RemoveRepositoryApprovalRule(ctx *context.APIContext) {
	// swagger:operation DELETE /governance/repositories/{id}/approval-rules/{rule_id} governance governanceRemoveRepositoryApprovalRule
	// ---
	// summary: 按修订号删除项目本级规则，不能删除祖先策略
	// parameters:
	// - name: id
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
	_, err := governance_service.SaveRepositoryApprovalRule(ctx, requestActor(ctx), ctx.PathParamInt64("id"), ctx.PathParamInt64("rule_id"), ctx.FormInt64("revision"), governance_model.ApprovalRule{}, true)
	if err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncRepositoryGovernanceReviewRequests(ctx, ctx.PathParamInt64("id"), ctx.Doer)
	ctx.Status(http.StatusNoContent)
}

func PullApprovalState(ctx *context.APIContext) {
	// swagger:operation GET /governance/pulls/{id}/approval-state governance governancePullApprovalState
	// ---
	// summary: 查询 PR 逐条审批结果、证据版本和当前用户资格
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
	//     "$ref": "#/responses/GovernancePullApprovalState"
	pullID := ctx.PathParamInt64("id")
	result, err := governance_service.ReadPullApprovalState(ctx, ctx.Doer.ID, pullID)
	if err != nil {
		respondError(ctx, err)
		return
	}
	pr, err := issues_model.GetPullRequestByID(ctx, pullID)
	if err != nil {
		respondError(ctx, err)
		return
	}
	result, err = governance_service.ProjectPullApprovalResult(ctx, ctx.Doer.ID, pullID, pr.BaseRepoID, result)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

type ApprovalSettingsOption = governance_service.ApprovalSettingsOption

func UpdateRepositoryApprovalSettings(ctx *context.APIContext) {
	// swagger:operation PUT /governance/repositories/{id}/approval-settings governance governanceUpdateRepositoryApprovalSettings
	// ---
	// summary: 完整更新项目审批设置，保留继承锁定及 PR 规则版本
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
	//     "$ref": "#/definitions/ApprovalSettingsOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceApprovalSettings"
	result, err := governance_service.SaveRepositoryApprovalSettings(ctx, requestActor(ctx), ctx.PathParamInt64("id"), *web.GetForm(ctx).(*ApprovalSettingsOption))
	if err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncRepositoryGovernanceReviewRequests(ctx, ctx.PathParamInt64("id"), ctx.Doer)
	ctx.JSON(http.StatusOK, result)
}

func InstallRepositoryReferenceHook(ctx *context.APIContext) {
	if err := governance_service.InstallRepositoryReferenceHook(ctx, requestActor(ctx), ctx.PathParamInt64("id")); err != nil {
		respondError(ctx, err)
		return
	}
	result, err := governance_service.ListRepositoryApprovalRules(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}

// RepositoryApprovalConfiguration 原子保存本仓库审批草稿，不授予保护分支管理权。
func RepositoryApprovalConfiguration(ctx *context.APIContext) {
	// swagger:operation PUT /governance/repositories/{id}/approval-configuration governance governanceSaveApprovalConfiguration
	// ---
	// summary: 原子保存审批草稿，版本冲突不覆盖其他修改
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
	//     "$ref": "#/definitions/BranchApprovalUpdate"
	// responses:
	//   "200":
	//     "$ref": "#/responses/BranchApprovalConfiguration"
	//   "409":
	//     description: 配置版本已变化，保留草稿后重新核对
	//   "422":
	//     description: 草稿无效，未保存任何修改
	option := web.GetForm(ctx).(*governance_service.BranchApprovalUpdate)
	id := ctx.PathParamInt64("id")
	if err := governance_service.SaveBranchApprovals(ctx, requestActor(ctx), id, *option, nil); err != nil {
		respondError(ctx, err)
		return
	}
	state, err := governance_service.ReadBranchApprovals(ctx, ctx.Doer.ID, id, option.ProtectionID)
	if err != nil {
		// 保存已提交；读取新状态失败不应诱导客户端重放写请求。
		ctx.JSON(http.StatusOK, map[string]any{"saved": true, "refresh_required": true, "message": "审批配置已保存，请重新读取最新状态"})
		return
	}
	ctx.JSON(http.StatusOK, state)
}

type BranchApprovalUpdate = governance_service.BranchApprovalUpdate
