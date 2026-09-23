// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"encoding/json" //nolint:depguard // 严格拒绝未知字段，兼容两套 JSON 实现。
	"errors"
	"io"
	"net/http"

	gm "gitea.dev/models/governance"
	"gitea.dev/services/context"
	gs "gitea.dev/services/governance"
	issue_service "gitea.dev/services/issue"
)

func repositoryChange(ctx *context.Context, preview bool) {
	var change gs.RepositoryMemberChange
	decoder := json.NewDecoder(io.LimitReader(ctx.Req.Body, 16385))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&change)
	if err == nil {
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			err = gm.ErrInvalid
		}
	}
	if err != nil {
		ctx.JSON(http.StatusBadRequest, map[string]string{"message": "变更参数无效"})
		return
	}
	actor := gs.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	var result any
	if preview {
		result, err = gs.PreviewRepositoryMemberChange(ctx, actor, ctx.Repo.Repository.ID, change)
	} else {
		token := change.Member.PreviewToken
		if change.Kind == "share" {
			token = change.Share.PreviewToken
		}
		if token == "" {
			err = gm.ErrInvalid
		} else {
			err = gs.ApplyRepositoryMemberChange(ctx, actor, ctx.Repo.Repository.ID, change)
		}
	}
	if err != nil {
		status, message := http.StatusInternalServerError, "操作失败，请联系管理员查看对应请求日志"
		switch {
		case errors.Is(err, gm.ErrNotFound), errors.Is(err, gm.ErrForbidden):
			status, message = 404, "无权修改此来源或目标不存在"
		case errors.Is(err, gm.ErrConflict):
			status, message = 409, err.Error()
		case errors.Is(err, gm.ErrInvalid):
			status, message = 400, "角色、有效期或预览参数不符合要求"
		}
		ctx.JSON(status, map[string]string{"message": message})
		return
	}
	if !preview {
		issue_service.SyncRepositoryGovernanceReviewRequests(ctx, ctx.Repo.Repository.ID, ctx.Doer)
		result = map[string]bool{"ok": true}
	}
	ctx.JSON(http.StatusOK, result)
}

func PreviewRepositoryMemberChange(ctx *context.Context) { repositoryChange(ctx, true) }
func ApplyRepositoryMemberChange(ctx *context.Context)   { repositoryChange(ctx, false) }
