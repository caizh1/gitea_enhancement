// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"
	"slices"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	"gitea.dev/models/organization"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	feed_service "gitea.dev/services/feed"
	governance_service "gitea.dev/services/governance"
)

func prepareNavigation(ctx *context.Context, groupID int64) bool {
	actorID := int64(0)
	if ctx.Doer != nil {
		actorID = ctx.Doer.ID
	}
	navigation, err := governance_service.GetGroupNavigation(ctx, actorID, groupID, governance_service.NavigationQuery{})
	if err != nil {
		respondError(ctx, err)
		return false
	}
	ctx.Data["Title"] = navigation.Group.Name
	ctx.Data["Navigation"] = navigation
	if !navigation.RestrictedNavigation {
		group, err := user_model.GetUserByID(ctx, groupID)
		if err != nil {
			respondError(ctx, err)
			return false
		}
		ctx.Data["GroupDescription"] = group.Description
	}
	ctx.Data["CanCreateSubgroup"] = slices.Contains(navigation.AllowedActions, "create_group") && navigation.Group.State == "active"
	ctx.Data["CanCreateRepository"] = slices.Contains(navigation.AllowedActions, "create_project") && navigation.Group.State == "active"
	if ctx.Doer != nil && !navigation.RestrictedNavigation && navigation.Group.State == "active" {
		org, err := organization.GetOrgByID(ctx, groupID)
		if err != nil {
			respondError(ctx, err)
			return false
		}
		canCreate, err := org.CanCreateOrgRepo(ctx, ctx.Doer.ID)
		if err != nil {
			respondError(ctx, err)
			return false
		}
		ctx.Data["CanCreateRepository"] = canCreate
	}
	ctx.Data["CanManageApprovals"] = slices.Contains(navigation.AllowedActions, "manage_approvals")
	ctx.Data["CanReadAudit"] = slices.Contains(navigation.AllowedActions, "read_audit")
	ctx.Data["CanManageGroup"] = slices.Contains(navigation.AllowedActions, "manage_group")
	ctx.Data["IsPackageEnabled"] = setting.Packages.Enabled
	return true
}

func NavigationHome(ctx *context.Context, groupID int64) {
	if !prepareNavigation(ctx, groupID) {
		return
	}
	ctx.Data["GroupTab"] = "overview"
	if ctx.FormString("tab") == "shared_projects" {
		if ctx.Data["Navigation"].(*governance_service.GroupNavigation).RestrictedNavigation {
			ctx.NotFound(nil)
			return
		}
		actorID := int64(0)
		if ctx.Doer != nil {
			actorID = ctx.Doer.ID
		}
		state, err := governance_service.ListGroupSharedRepositories(ctx, actorID, groupID, max(1, ctx.FormInt("page")), min(max(1, setting.UI.ExplorePagingNum), 100))
		if err != nil {
			respondError(ctx, err)
			return
		}
		ctx.Data["GroupTab"], ctx.Data["SharedProjects"] = "shared_projects", state
		pager := context.NewPagination(state.Count, state.PageSize, state.Page, 5)
		pager.AddParamFromRequest(ctx.Req)
		ctx.Data["Page"] = pager
		ctx.HTML(http.StatusOK, "governance/navigation")
		return
	}
	if tab := ctx.FormString("tab"); tab == "issues" || tab == "pulls" {
		if ctx.Data["Navigation"].(*governance_service.GroupNavigation).RestrictedNavigation {
			ctx.NotFound(nil)
			return
		}
		actorID := int64(0)
		if ctx.Doer != nil {
			actorID = ctx.Doer.ID
		}
		state, err := governance_service.ListGroupIssues(ctx, actorID, groupID, tab == "pulls", governance_service.GroupWorkQuery{
			Scope: ctx.FormString("scope"), RepoID: ctx.FormInt64("repo"), Filter: ctx.FormString("filter"), State: ctx.FormString("state"), Page: ctx.FormInt("page"), PageSize: setting.UI.IssuePagingNum,
		})
		if err != nil {
			respondError(ctx, err)
			return
		}
		ctx.Data["GroupTab"], ctx.Data["Work"] = tab, state
		pager := context.NewPagination(state.Count, state.Query.PageSize, state.Query.Page, 5)
		pager.AddParamFromRequest(ctx.Req)
		ctx.Data["Page"] = pager
		ctx.HTML(http.StatusOK, "governance/group_issues")
		return
	}
	if ctx.FormString("tab") == "activity" {
		if ctx.Data["Navigation"].(*governance_service.GroupNavigation).RestrictedNavigation {
			ctx.NotFound(nil)
			return
		}
		group, err := user_model.GetUserByID(ctx, groupID)
		if err != nil {
			respondError(ctx, err)
			return
		}
		page := max(1, ctx.FormInt("page"))
		feeds, count, err := feed_service.GetFeedsForDashboard(ctx, activities_model.GetFeedsOptions{RequestedUser: group, Actor: ctx.Doer, IncludePrivate: ctx.Doer != nil, ListOptions: db.ListOptions{Page: page, PageSize: setting.UI.FeedPagingNum}})
		if err != nil {
			respondError(ctx, err)
			return
		}
		ctx.Data["GroupTab"] = "activity"
		ctx.Data["Feeds"] = feeds
		pager := context.NewPagination(count, setting.UI.FeedPagingNum, page, 5).WithUnlimitedPaging(len(feeds), len(feeds) == setting.UI.FeedPagingNum)
		pager.AddParamFromRequest(ctx.Req)
		ctx.Data["Page"] = pager
	}
	ctx.HTML(http.StatusOK, "governance/navigation")
}

// 网页会话使用 Web 入口；REST API 继续要求令牌，不能为前端放宽全局认证。
func NavigationJSON(ctx *context.Context) {
	actorID := int64(0)
	if ctx.Doer != nil {
		actorID = ctx.Doer.ID
	}
	query := governance_service.NavigationQuery{Q: ctx.FormString("q"), State: ctx.FormString("state"), Sort: ctx.FormString("sort"), Direction: ctx.FormString("direction"), Cursor: ctx.FormString("cursor"), Limit: ctx.FormInt("limit"), GroupsOnly: ctx.FormBool("groups_only")}
	var result any
	var err error
	if id := ctx.PathParamInt64("id"); id > 0 {
		result, err = governance_service.GetGroupNavigation(ctx, actorID, id, query)
	} else {
		result, err = governance_service.ListNavigationGroups(ctx, actorID, query)
	}
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, result)
}
