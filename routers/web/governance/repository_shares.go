// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"errors"
	"net/http"
	"strings"
	"time"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
	issue_service "gitea.dev/services/issue"
)

func PrepareRepositoryShares(ctx *context.Context, id int64) bool {
	state, err := governance_service.ListRepositoryShares(ctx, ctx.Doer.ID, id, ctx.FormInt64("share_after_id"))
	if err != nil {
		respondError(ctx, err)
		return false
	}
	names := make(map[int64]string)
	displayNames := make(map[int64]string)
	dates := make(map[int64]string)
	editable := make(map[int64]bool)
	for _, share := range state.Shares {
		if share.ExpiresUnix != 0 {
			dates[share.ID] = time.Unix(share.ExpiresUnix-1, 0).UTC().Format("2006-01-02")
		}
		group, err := governance_service.CheckGroupAccess(ctx, ctx.Doer.ID, share.GroupID, governance_model.ReadGroup)
		if errors.Is(err, governance_model.ErrNotFound) {
			namespace, lookupErr := governance_model.GetNamespace(ctx, share.GroupID)
			if lookupErr != nil {
				respondError(ctx, lookupErr)
				return false
			}
			names[share.GroupID] = namespace.FullPath
			displayNames[share.GroupID] = namespace.FullPath
			continue
		}
		if err != nil {
			respondError(ctx, err)
			return false
		}
		names[share.GroupID] = group.FullPath
		displayNames[share.GroupID] = group.Name
		editable[share.GroupID], err = organization.IsOrganizationMember(ctx, share.GroupID, ctx.Doer.ID)
		if err != nil {
			respondError(ctx, err)
			return false
		}
	}
	ctx.Data["Title"], ctx.Data["State"], ctx.Data["RepositoryID"] = "项目共享", state, id
	ctx.Data["GroupNames"] = names
	ctx.Data["ShareDisplayNames"] = displayNames
	ctx.Data["ShareAfterID"] = ctx.FormInt64("share_after_id")
	ctx.Data["ShareDates"] = dates
	ctx.Data["ShareEditable"] = editable
	ctx.Data["RoleNames"] = map[governance_model.Role]string{10: "Guest", 15: "Planner", 20: "Reporter", 30: "Developer", 40: "Maintainer", 50: "Owner"}
	if len(state.Shares) == 100 {
		ctx.Data["NextID"] = state.Shares[99].ID
	}
	return true
}

func RepositoryShares(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	if !PrepareRepositoryShares(ctx, id) {
		return
	}
	repo, err := repo_model.GetRepositoryByID(ctx, id)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Redirect(repo.Link() + "/settings/collaboration")
}

func RepositoryShareGroups(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	if _, err := governance_service.ListRepositoryShares(ctx, ctx.Doer.ID, id, 0); err != nil {
		respondError(ctx, err)
		return
	}
	query := strings.TrimSpace(ctx.FormString("q"))
	if query == "" {
		ctx.JSON(http.StatusOK, map[string]any{"data": []string{}})
		return
	}
	page, err := governance_service.ListNavigationGroups(ctx, ctx.Doer.ID, governance_service.NavigationQuery{Q: query, Limit: 100})
	if err != nil {
		respondError(ctx, err)
		return
	}
	paths := []string{}
	for _, group := range page.Items {
		member, err := organization.IsOrganizationMember(ctx, group.ID, ctx.Doer.ID)
		if err != nil {
			respondError(ctx, err)
			return
		}
		if member && !group.RestrictedNavigation {
			paths = append(paths, group.FullPath)
		}
	}
	ctx.JSON(http.StatusOK, map[string]any{"data": paths})
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
		// 仅修改角色时保留 API 创建的精确到期时刻，避免延长当天权限。
		if original := ctx.FormInt64("expires_unix"); original > 0 && time.Unix(original-1, 0).UTC().Format("2006-01-02") == expiry {
			option.ExpiresUnix = original
		}
	}
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	if err := governance_service.SetRepositoryShare(ctx, actor, id, option, remove); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncRepositoryGovernanceReviewRequests(ctx, id, ctx.Doer)
	ctx.Flash.Success("项目共享已更新，访问权限立即重新计算")
	repo, err := repo_model.GetRepositoryByID(ctx, id)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Redirect(repo.Link() + "/settings/collaboration")
}
