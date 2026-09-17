// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"

	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

func navigationQuery(ctx *context.APIContext) governance_service.NavigationQuery {
	return governance_service.NavigationQuery{Q: ctx.FormString("q"), State: ctx.FormString("state"), Sort: ctx.FormString("sort"), Direction: ctx.FormString("direction"), Cursor: ctx.FormString("cursor"), Limit: ctx.FormInt("limit"), GroupsOnly: ctx.FormBool("groups_only")}
}

func NavigationGroups(ctx *context.APIContext) {
	// swagger:operation GET /governance/navigation/groups governance governanceNavigationGroups
	// ---
	// summary: 查询我的群组入口，按真实授权来源筛选
	// produces:
	// - application/json
	// parameters:
	// - name: q
	//   in: query
	//   type: string
	// - name: state
	//   in: query
	//   type: string
	//   enum: [active, inactive]
	// - name: sort
	//   in: query
	//   type: string
	//   enum: [name, created, updated]
	// - name: direction
	//   in: query
	//   type: string
	//   enum: [asc, desc]
	// - name: limit
	//   in: query
	//   type: integer
	//   default: 20
	//   minimum: 1
	//   maximum: 100
	// - name: cursor
	//   in: query
	//   type: string
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceNavigationPage"

	actorID := int64(0)
	if ctx.Doer != nil {
		actorID = ctx.Doer.ID
	}
	page, err := governance_service.ListNavigationGroups(ctx, actorID, navigationQuery(ctx))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, page)
}

func NavigationGroup(ctx *context.APIContext) {
	// swagger:operation GET /governance/navigation/groups/{id} governance governanceNavigationGroup
	// ---
	// summary: 查询群组层级、面包屑及允许的操作
	// produces:
	// - application/json
	// parameters:
	// - name: q
	//   in: query
	//   type: string
	// - name: state
	//   in: query
	//   type: string
	//   enum: [active, inactive]
	// - name: sort
	//   in: query
	//   type: string
	//   enum: [name, created, updated]
	// - name: direction
	//   in: query
	//   type: string
	//   enum: [asc, desc]
	// - name: limit
	//   in: query
	//   type: integer
	//   default: 20
	//   minimum: 1
	//   maximum: 100
	// - name: cursor
	//   in: query
	//   type: string
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: groups_only
	//   in: query
	//   type: boolean
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceGroupNavigation"

	actorID := int64(0)
	if ctx.Doer != nil {
		actorID = ctx.Doer.ID
	}
	page, err := governance_service.GetGroupNavigation(ctx, actorID, ctx.PathParamInt64("id"), navigationQuery(ctx))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, page)
}
