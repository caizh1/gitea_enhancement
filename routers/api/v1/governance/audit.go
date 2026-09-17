// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	stdcontext "context"
	"errors"
	"net/http"
	"strconv"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/log"
	"gitea.dev/modules/web"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

// AuditExportOption 创建后台导出任务。
// swagger:model
type AuditExportOption struct {
	Filter governance_model.AuditFilter `json:"filter"`
	Format string                       `json:"format" binding:"Required;In(csv,json)"`
}

// GovernanceCapabilities 描述原生治理接口能力与独立验收状态。
// swagger:model
type GovernanceCapabilities struct {
	Version                   int               `json:"version"`
	ApprovalRules             bool              `json:"approval_rules"`
	HierarchicalGroups        bool              `json:"hierarchical_groups"`
	AuditQuery                bool              `json:"audit_query"`
	AuditExports              bool              `json:"audit_exports"`
	RepositoryVisibility      []string          `json:"repository_visibility"`
	RepositoryStorageIdentity bool              `json:"repository_storage_identity"`
	FeatureStages             map[string]string `json:"feature_stages"`
	AuditEventCoverage        string            `json:"audit_event_coverage"`
	ProductionReady           bool              `json:"production_ready"`
	AuditHealth               map[string]any    `json:"audit_health"`
}

func requestActor(ctx *context.APIContext) governance_model.Actor {
	return governance_service.APIRequestActor(ctx.Doer, ctx.AuthenticatedUser, ctx.RemoteAddr())
}

func respondError(ctx *context.APIContext, err error) {
	switch {
	case errors.Is(err, governance_model.ErrNotFound), errors.Is(err, governance_model.ErrForbidden):
		ctx.APIErrorNotFound()
	case errors.Is(err, governance_model.ErrInvalid):
		ctx.APIError(http.StatusBadRequest, err.Error())
	case errors.Is(err, governance_model.ErrConflict):
		ctx.APIError(http.StatusConflict, err.Error())
	default:
		ctx.APIErrorInternal(err)
	}
}

func Capabilities(ctx *context.APIContext) {
	// swagger:operation GET /governance/capabilities governance governanceCapabilities
	// ---
	// summary: 查询原生企业治理已接入的能力
	// produces:
	// - application/json
	// responses:
	//   "200":
	//     description: 当前实现状态
	//     schema:
	//       "$ref": "#/definitions/GovernanceCapabilities"

	// 功能布尔值表示原生接口可用；验收阶段与生产门禁由后续字段独立表达。
	ctx.JSON(http.StatusOK, GovernanceCapabilities{
		Version: 1, ApprovalRules: true, HierarchicalGroups: true, AuditQuery: true, AuditExports: true,
		RepositoryVisibility:      []string{"public", "internal", "private"},
		RepositoryStorageIdentity: true,
		FeatureStages: map[string]string{
			"approval_rules":        "integration_ready",
			"hierarchical_groups":   "development",
			"repository_visibility": "development",
		},
		AuditEventCoverage: "incomplete", ProductionReady: false,
		AuditHealth: governance_model.AuditHealthStatus(),
	})
}

func AuditEvents(ctx *context.APIContext) {
	// swagger:operation GET /governance/audit-events governance governanceAuditEvents
	// ---
	// summary: 查询授权范围内的治理事件，时间范围最多三十天
	// produces:
	// - application/json
	// parameters:
	// - name: scope_type
	//   in: query
	//   type: string
	//   enum: [instance, group, repository, user]
	// - name: scope_id
	//   in: query
	//   type: integer
	//   format: int64
	// - name: from
	//   in: query
	//   type: string
	//   format: date-time
	// - name: to
	//   in: query
	//   type: string
	//   format: date-time
	// - name: actor_id
	//   in: query
	//   type: integer
	//   format: int64
	// - name: event_type
	//   in: query
	//   type: string
	// - name: entity_type
	//   in: query
	//   type: string
	// - name: entity_id
	//   in: query
	//   type: integer
	//   format: int64
	// - name: result
	//   in: query
	//   type: string
	// - name: request_id
	//   in: query
	//   type: string
	// - name: before
	//   in: query
	//   type: integer
	//   format: int64
	// - name: limit
	//   in: query
	//   type: integer
	//   minimum: 1
	//   maximum: 100
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceAuditPage"
	//   "404":
	//     "$ref": "#/responses/notFound"
	filter, err := governance_service.ParseAuditFilter(ctx.Req.URL.Query(), ctx.Doer.ID, false)
	if err != nil {
		respondError(ctx, err)
		return
	}
	if err := governance_service.CheckAuditAccess(ctx, ctx.Doer.ID, filter.ScopeType, filter.ScopeID); err != nil {
		respondError(ctx, err)
		return
	}
	before, limit := int64(0), 50
	if raw := ctx.FormString("before"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			respondError(ctx, governance_model.ErrInvalid)
			return
		}
	}
	if raw := ctx.FormString("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			respondError(ctx, governance_model.ErrInvalid)
			return
		}
	}
	page, err := governance_model.QueryAudit(ctx, filter, before, limit)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	ctx.JSON(http.StatusOK, page)
}

func CreateExport(ctx *context.APIContext) {
	// swagger:operation POST /governance/audit-exports governance governanceCreateAuditExport
	// ---
	// summary: 申请分段生成 CSV 或 JSON 审计证据，临时副本保存七天
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/AuditExportOption"
	// responses:
	//   "202":
	//     "$ref": "#/responses/GovernanceAuditExport"
	//   "404":
	//     "$ref": "#/responses/notFound"
	form := web.GetForm(ctx).(*AuditExportOption)
	job, err := governance_model.CreateAuditExport(ctx, requestActor(ctx), form.Filter, form.Format, func(c stdcontext.Context) error {
		return governance_service.CheckAuditAccess(c, ctx.Doer.ID, form.Filter.ScopeType, form.Filter.ScopeID)
	})
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	ctx.JSON(http.StatusAccepted, job)
}

func GetExport(ctx *context.APIContext) {
	// swagger:operation GET /governance/audit-exports/{id} governance governanceGetAuditExport
	// ---
	// summary: 查询当前用户的审计导出进度
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: string
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceAuditExport"
	//   "404":
	//     "$ref": "#/responses/notFound"
	job, err := governance_service.GetAuditExport(ctx, ctx.Doer.ID, ctx.PathParam("id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	ctx.JSON(http.StatusOK, job)
}

func DownloadExport(ctx *context.APIContext) {
	// swagger:operation GET /governance/audit-exports/{id}/download governance governanceDownloadAuditExport
	// ---
	// summary: 重新验证权限后下载已完成的审计证据
	// produces:
	// - application/json
	// - text/csv
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: string
	// responses:
	//   "200":
	//     description: UTF-8 CSV 或 JSON 文件，时间为 UTC
	//     schema:
	//       type: file
	//   "404":
	//     "$ref": "#/responses/notFound"
	actor := requestActor(ctx)
	job, err := governance_service.PrepareAuditDownload(ctx, actor, ctx.PathParam("id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	ctx.Resp.Header().Set("Content-Disposition", "attachment; filename=audit-"+job.ID+"."+job.Format)
	contentType := "application/json; charset=utf-8"
	if job.Format == "csv" {
		contentType = "text/csv; charset=utf-8"
	}
	ctx.Resp.Header().Set("Content-Type", contentType)
	if err := governance_service.WriteAuditExport(ctx, actor, job, ctx.Resp); err != nil {
		log.Error("审计导出下载中断 %s：%v", job.ID, err)
		panic(http.ErrAbortHandler)
	}
}
