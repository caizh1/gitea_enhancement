// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"
	"strconv"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/web"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

// AuditRevisionOption 防止过期配置页面改变新目标。
// swagger:model
type AuditRevisionOption struct {
	Revision int64 `json:"revision"`
}

type AuditStreamOption = governance_service.AuditStreamOption

func AuditStreams(ctx *context.APIContext) {
	// swagger:operation GET /governance/audit-streams governance governanceAuditStreams
	// ---
	// summary: 查看实例或顶级群组的外送目标与积压状态
	// produces:
	// - application/json
	// parameters:
	// - name: scope_type
	//   in: query
	//   type: string
	//   enum: [instance, group]
	//   required: true
	// - name: scope_id
	//   in: query
	//   type: integer
	//   format: int64
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceAuditStreams"
	id := int64(0)
	if raw := ctx.FormString("scope_id"); raw != "" {
		var err error
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			respondError(ctx, governance_model.ErrInvalid)
			return
		}
	}
	streams, err := governance_service.ListAuditStreams(ctx, ctx.Doer.ID, ctx.FormString("scope_type"), id)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	ctx.JSON(http.StatusOK, streams)
}

func CreateAuditStream(ctx *context.APIContext) {
	// swagger:operation POST /governance/audit-streams governance governanceCreateAuditStream
	// ---
	// summary: 创建外送目标，自动生成的验证标识只在创建结果中返回
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/AuditStreamOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/GovernanceAuditStreamSaved"
	saveStream(ctx, 0)
}

func UpdateAuditStream(ctx *context.APIContext) {
	// swagger:operation PUT /governance/audit-streams/{id} governance governanceUpdateAuditStream
	// ---
	// summary: 按修订号更新目标配置；省略凭据时保留原值，积压期间不能变更实际接收地址
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
	//     "$ref": "#/definitions/AuditStreamOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceAuditStreamSaved"
	id, err := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	if err != nil || id <= 0 {
		respondError(ctx, governance_model.ErrInvalid)
		return
	}
	saveStream(ctx, id)
}

func saveStream(ctx *context.APIContext, id int64) {
	option := web.GetForm(ctx).(*governance_service.AuditStreamOption)
	result, err := governance_service.SaveAuditStream(ctx, requestActor(ctx), id, *option)
	if err != nil {
		respondError(ctx, err)
		return
	}
	status := http.StatusOK
	if id == 0 {
		status = http.StatusCreated
	}
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	ctx.JSON(status, result)
}

func TestAuditStream(ctx *context.APIContext) {
	// swagger:operation POST /governance/audit-streams/{id}/test governance governanceTestAuditStream
	// ---
	// summary: 持久化连通性测试事件并由真实发送器投递
	// consumes:
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
	//     "$ref": "#/definitions/AuditRevisionOption"
	// responses:
	//   "202":
	//     description: 测试事件已入队，可通过目标投递状态核对结果
	id, err := strconv.ParseInt(ctx.PathParam("id"), 10, 64)
	if err != nil || id <= 0 {
		respondError(ctx, governance_model.ErrInvalid)
		return
	}
	option := web.GetForm(ctx).(*AuditRevisionOption)
	if err := governance_service.TestAuditStream(ctx, requestActor(ctx), id, option.Revision); err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Status(http.StatusAccepted)
}
