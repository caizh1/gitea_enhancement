// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
	issue_service "gitea.dev/services/issue"
)

func RepositoryShares(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	state, err := governance_service.ListRepositoryShares(ctx, ctx.Doer.ID, id, ctx.FormInt64("after_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	names := make(map[int64]string)
	for _, share := range state.Shares {
		group, err := governance_service.CheckGroupAccess(ctx, ctx.Doer.ID, share.GroupID, governance_model.ReadGroup)
		if errors.Is(err, governance_model.ErrNotFound) {
			names[share.GroupID] = "不可见群组"
			continue
		}
		if err != nil {
			respondError(ctx, err)
			return
		}
		names[share.GroupID] = group.FullPath
	}
	ctx.Data["Title"], ctx.Data["State"], ctx.Data["RepositoryID"] = "项目共享", state, id
	ctx.Data["GroupNames"] = names
	ctx.Data["RoleNames"] = map[governance_model.Role]string{5: "Minimal Access", 10: "Guest", 15: "Planner", 20: "Reporter", 30: "Developer", 40: "Maintainer"}
	if len(state.Shares) == 100 {
		ctx.Data["NextID"] = state.Shares[99].ID
	}
	ctx.HTML(http.StatusOK, "governance/repository_shares")
}

func SaveRepositoryShare(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	remove := ctx.FormString("action") == "remove"
	option := governance_service.GroupShareOption{GroupID: ctx.FormInt64("group_id"), MaxRole: governance_model.Role(ctx.FormInt("role")), Revision: ctx.FormInt64("revision")}
	if !remove {
		path, err := governance_model.ResolvePath(ctx, ctx.FormString("group_path"))
		if err != nil {
			respondError(ctx, err)
			return
		}
		if path.Kind != "group" {
			respondError(ctx, governance_model.ErrNotFound)
			return
		}
		option.GroupID = path.ResourceID
	}
	if expiry := ctx.FormString("expires"); expiry != "" {
		date, err := time.Parse("2006-01-02", expiry)
		if err != nil {
			respondError(ctx, governance_model.ErrInvalid)
			return
		}
		option.ExpiresUnix = date.Add(24 * time.Hour).Unix()
	}
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	if err := governance_service.SetRepositoryShare(ctx, actor, id, option, remove); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncRepositoryGovernanceReviewRequests(ctx, id, ctx.Doer)
	ctx.Flash.Success("项目共享已更新，访问权限立即重新计算")
	ctx.Redirect(setting.AppSubURL + "/governance/repositories/" + strconv.FormatInt(id, 10) + "/shares")
}
