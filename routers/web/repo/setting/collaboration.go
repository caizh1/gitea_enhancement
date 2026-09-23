// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"errors"
	"net/http"
	"strings"

	gm "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/perm"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/log"
	"gitea.dev/modules/setting"
	governance_web "gitea.dev/routers/web/governance"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/services/mailer"
	repo_service "gitea.dev/services/repository"
)

// Collaboration render a repository's collaboration page
func Collaboration(ctx *context.Context) {
	ctx.Redirect(ctx.Repo.RepoLink + "/collaborators")
}

func Collaborators(ctx *context.Context) {
	viewerID := int64(0)
	if ctx.Doer != nil {
		viewerID = ctx.Doer.ID
	}
	view, err := governance_service.ListRepositoryMembersView(ctx, viewerID, ctx.Repo.Repository.ID, governance_service.RepositoryMemberQuery{Q: ctx.FormString("q"), Role: gm.Role(ctx.FormInt("role")), Source: ctx.FormString("source"), AfterID: ctx.FormInt64("after_id")})
	if err != nil {
		if errors.Is(err, gm.ErrNotFound) {
			ctx.NotFound(err)
		} else {
			ctx.ServerError("成员视图", err)
		}
		return
	}
	ctx.Data["MemberView"] = view
	ctx.Data["PageIsSettingsCollaboration"] = true
	ctx.Data["Title"] = "协作者"
	ctx.Data["MemberRoleNames"] = map[gm.Role]string{10: "Guest · 访客", 15: "Planner · 计划者", 20: "Reporter · 只读成员", 30: "Developer · 开发者", 40: "Maintainer · 维护者", 50: "Owner · 所有者"}
	if view.CanOwn && !governance_web.PrepareRepositoryShares(ctx, ctx.Repo.Repository.ID) {
		return
	}
	ctx.Data["Title"] = "协作者"
	ctx.HTML(http.StatusOK, tplCollaboration)
}

// CollaborationPost response for actions for a collaboration of a repository
func CollaborationPost(ctx *context.Context) {
	name := strings.ToLower(ctx.FormString("collaborator"))
	if len(name) == 0 || ctx.Repo.Owner.LowerName == name {
		ctx.Redirect(setting.AppSubURL + ctx.Req.URL.EscapedPath())
		return
	}

	u, err := user_model.GetUserByName(ctx, name)
	if err != nil {
		if user_model.IsErrUserNotExist(err) {
			ctx.Flash.Error(ctx.Tr("form.user_not_exist"))
			ctx.Redirect(setting.AppSubURL + ctx.Req.URL.EscapedPath())
		} else {
			ctx.ServerError("GetUserByName", err)
		}
		return
	}

	if !u.IsActive {
		ctx.Flash.Error(ctx.Tr("repo.settings.add_collaborator_inactive_user"))
		ctx.Redirect(setting.AppSubURL + ctx.Req.URL.EscapedPath())
		return
	}

	// Organization is not allowed to be added as a collaborator.
	if u.IsOrganization() {
		ctx.Flash.Error(ctx.Tr("repo.settings.org_not_allowed_to_be_collaborator"))
		ctx.Redirect(setting.AppSubURL + ctx.Req.URL.EscapedPath())
		return
	}

	if got, err := repo_model.IsCollaborator(ctx, ctx.Repo.Repository.ID, u.ID); err == nil && got {
		ctx.Flash.Error(ctx.Tr("repo.settings.add_collaborator_duplicate"))
		ctx.Redirect(ctx.Repo.RepoLink + "/settings/collaboration")
		return
	}

	// find the owner team of the organization the repo belongs too and
	// check if the user we're trying to add is an owner.
	if ctx.Repo.Repository.Owner.IsOrganization() {
		if isOwner, err := organization.IsOrganizationOwner(ctx, ctx.Repo.Repository.Owner.ID, u.ID); err != nil {
			ctx.ServerError("IsOrganizationOwner", err)
			return
		} else if isOwner {
			ctx.Flash.Error(ctx.Tr("repo.settings.add_collaborator_owner"))
			ctx.Redirect(setting.AppSubURL + ctx.Req.URL.EscapedPath())
			return
		}
	}

	if err = repo_service.AddOrUpdateCollaborator(ctx, ctx.Repo.Repository, u, perm.AccessModeWrite); err != nil {
		if errors.Is(err, user_model.ErrBlockedUser) {
			ctx.Flash.Error(ctx.Tr("repo.settings.add_collaborator.blocked_user"))
			ctx.Redirect(ctx.Repo.RepoLink + "/settings/collaboration")
		} else {
			ctx.ServerError("AddOrUpdateCollaborator", err)
		}
		return
	}

	if setting.Service.EnableNotifyMail {
		mailer.SendCollaboratorMail(u, ctx.Doer, ctx.Repo.Repository)
	}

	ctx.Flash.Success(ctx.Tr("repo.settings.add_collaborator_success"))
	ctx.Redirect(setting.AppSubURL + ctx.Req.URL.EscapedPath())
}

// ChangeCollaborationAccessMode response for changing access of a collaboration
func ChangeCollaborationAccessMode(ctx *context.Context) {
	// the frontend initRepoSettingsCollaboration logic: it only checks "resp.ok"
	u, err := user_model.GetUserByID(ctx, ctx.FormInt64("uid"))
	if err != nil {
		ctx.Status(http.StatusBadRequest)
		return
	}
	mode := perm.AccessMode(ctx.FormInt("mode"))
	if err := repo_service.AddOrUpdateCollaborator(ctx, ctx.Repo.Repository, u, mode); err != nil {
		ctx.Status(http.StatusBadRequest)
		log.Error("AddOrUpdateCollaborator: %v", err)
		return
	}
	ctx.JSONOK()
}

// DeleteCollaboration delete a collaboration for a repository
func DeleteCollaboration(ctx *context.Context) {
	if collaborator, err := user_model.GetUserByID(ctx, ctx.FormInt64("id")); err != nil {
		if user_model.IsErrUserNotExist(err) {
			ctx.Flash.Error(ctx.Tr("form.user_not_exist"))
		} else {
			ctx.ServerError("GetUserByName", err)
			return
		}
	} else {
		if err := repo_service.DeleteCollaboration(ctx, ctx.Repo.Repository, collaborator); err != nil {
			ctx.Flash.Error("DeleteCollaboration: " + err.Error())
		} else {
			ctx.Flash.Success(ctx.Tr("repo.settings.remove_collaborator_success"))
		}
	}

	ctx.JSONRedirect(ctx.Repo.RepoLink + "/settings/collaboration")
}

// AddTeamPost response for adding a team to a repository
func AddTeamPost(ctx *context.Context) {
	if !canChangeRepoTeamAccess(ctx) {
		return
	}

	name := strings.ToLower(ctx.FormString("team"))
	if len(name) == 0 {
		ctx.Redirect(ctx.Repo.RepoLink + "/settings/collaboration")
		return
	}

	team, err := organization.OrgFromUser(ctx.Repo.Owner).GetTeam(ctx, name)
	if err != nil {
		if organization.IsErrTeamNotExist(err) {
			ctx.Flash.Error(ctx.Tr("form.team_not_exist"))
			ctx.Redirect(ctx.Repo.RepoLink + "/settings/collaboration")
		} else {
			ctx.ServerError("GetTeam", err)
		}
		return
	}

	if team.OrgID != ctx.Repo.Repository.OwnerID {
		ctx.Flash.Error(ctx.Tr("repo.settings.team_not_in_organization"))
		ctx.Redirect(ctx.Repo.RepoLink + "/settings/collaboration")
		return
	}

	if organization.HasTeamRepo(ctx, ctx.Repo.Repository.OwnerID, team.ID, ctx.Repo.Repository.ID) {
		ctx.Flash.Error(ctx.Tr("repo.settings.add_team_duplicate"))
		ctx.Redirect(ctx.Repo.RepoLink + "/settings/collaboration")
		return
	}

	if err = repo_service.TeamAddRepository(ctx, team, ctx.Repo.Repository); err != nil {
		ctx.ServerError("TeamAddRepository", err)
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.settings.add_team_success"))
	ctx.Redirect(ctx.Repo.RepoLink + "/settings/collaboration")
}

// DeleteTeam response for deleting a team from a repository
func DeleteTeam(ctx *context.Context) {
	if !canChangeRepoTeamAccess(ctx) {
		return
	}

	team, err := organization.GetTeamByID(ctx, ctx.FormInt64("id"))
	if err != nil {
		ctx.ServerError("GetTeamByID", err)
		return
	}

	if err = repo_service.RemoveRepositoryFromTeam(ctx, team, ctx.Repo.Repository.ID); err != nil {
		ctx.ServerError("team.RemoveRepositories", err)
		return
	}

	ctx.Flash.Success(ctx.Tr("repo.settings.remove_team_success"))
	ctx.JSONRedirect(ctx.Repo.RepoLink + "/settings/collaboration")
}

func canChangeRepoTeamAccess(ctx *context.Context) bool {
	canChange, err := organization.OrgFromUser(ctx.Repo.Owner).CanChangeRepoTeamAccess(ctx, ctx.Doer)
	if err != nil {
		ctx.ServerError("CanChangeRepoTeamAccess", err)
		return false
	}
	if !canChange {
		ctx.Flash.Error(ctx.Tr("repo.settings.change_team_access_not_allowed"))
		ctx.Redirect(ctx.Repo.RepoLink + "/settings/collaboration")
	}
	return canChange
}
