// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"
	"time"

	"gitea.dev/modules/web"
	"gitea.dev/services/context"
	repo_service "gitea.dev/services/repository"
)

type RepositoryDeletionOption = repo_service.DeletionOption

// swagger:response GovernanceRepositoryDeletion
// 项目删除计划与恢复状态。
type swaggerRepositoryDeletion struct {
	// in:body
	Body repo_service.DeletionState
}

func RepositoryDeletionState(ctx *context.APIContext) {
	// swagger:operation GET /governance/repositories/{id}/deletion governance governanceRepositoryDeletionState
	// ---
	// summary: 查看项目删除与恢复状态
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceRepositoryDeletion"
	state, err := repo_service.GetRepositoryDeletionState(ctx, requestActor(ctx), ctx.PathParamInt64("id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, state)
}

func ScheduleRepositoryDeletion(ctx *context.APIContext) {
	// swagger:operation POST /governance/repositories/{id}/deletion governance governanceScheduleRepositoryDeletion
	// ---
	// summary: 计划删除项目并保留恢复期
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
	//     "$ref": "#/definitions/RepositoryDeletionOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceRepositoryDeletion"
	_, err := repo_service.ScheduleRepositoryDeletion(ctx, requestActor(ctx), ctx.PathParamInt64("id"), *web.GetForm(ctx).(*RepositoryDeletionOption))
	if err != nil {
		respondError(ctx, err)
		return
	}
	RepositoryDeletionState(ctx)
}

func RestoreRepositoryDeletion(ctx *context.APIContext) {
	// swagger:operation POST /governance/repositories/{id}/restore governance governanceRestoreRepositoryDeletion
	// ---
	// summary: 恢复待删除项目的原路径与归档状态
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
	//     "$ref": "#/definitions/RepositoryDeletionOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceRepositoryDeletion"
	err := repo_service.RestoreRepositoryDeletion(ctx, requestActor(ctx), ctx.PathParamInt64("id"), *web.GetForm(ctx).(*RepositoryDeletionOption))
	if err != nil {
		respondError(ctx, err)
		return
	}
	RepositoryDeletionState(ctx)
}

func DeletePendingRepository(ctx *context.APIContext) {
	// swagger:operation POST /governance/repositories/{id}/delete-permanently governance governanceDeletePendingRepository
	// ---
	// summary: 确认永久删除已进入恢复期的项目
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
	//     "$ref": "#/definitions/RepositoryDeletionOption"
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	actor := requestActor(ctx)
	err := repo_service.DeleteScheduledRepository(ctx, ctx.PathParamInt64("id"), time.Now(), &actor, *web.GetForm(ctx).(*RepositoryDeletionOption))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
