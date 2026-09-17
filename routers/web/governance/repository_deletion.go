// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"fmt"
	"net/http"
	"time"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/util"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
	repo_service "gitea.dev/services/repository"
)

func RepositoryDeletion(ctx *context.Context) {
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	state, err := repo_service.GetRepositoryDeletionState(ctx, actor, ctx.PathParamInt64("id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Data["Title"] = "项目删除与恢复"
	ctx.Data["State"] = state
	ctx.Data["RepositoryLink"] = setting.AppSubURL + "/" + util.PathEscapeSegments(state.FullPath)
	ctx.HTML(http.StatusOK, "governance/repository_deletion")
}

func SaveRepositoryDeletion(ctx *context.Context) {
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	id := ctx.PathParamInt64("id")
	option := repo_service.DeletionOption{ConfirmationPath: ctx.FormString("confirmation_path"), DueUnix: ctx.FormInt64("due_unix")}
	var err error
	switch ctx.FormString("action") {
	case "schedule":
		_, err = repo_service.ScheduleRepositoryDeletion(ctx, actor, id, option)
	case "restore":
		err = repo_service.RestoreRepositoryDeletion(ctx, actor, id, option)
	case "delete":
		err = repo_service.DeleteScheduledRepository(ctx, id, time.Now(), &actor, option)
	default:
		err = governance_model.ErrInvalid
	}
	if err != nil {
		respondError(ctx, err)
		return
	}
	if ctx.FormString("action") == "delete" {
		ctx.Flash.Success("项目已永久删除，存储清理结果进入审计记录。")
		ctx.Redirect(setting.AppSubURL + "/")
		return
	}
	ctx.Flash.Success("项目删除状态已更新。")
	ctx.Redirect(fmt.Sprintf("%s/governance/repositories/%d/deletion", setting.AppSubURL, id))
}
