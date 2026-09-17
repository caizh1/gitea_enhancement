// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"errors"
	"strconv"
	"time"

	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
	issue_service "gitea.dev/services/issue"
)

func RepositoryMembers(ctx *context.Context) {
	state, err := governance_service.ListRepositoryAllMembers(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"), ctx.FormInt64("after_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Data["Title"] = "项目成员与来源"
	ctx.Data["AbilityNames"] = governanceAbilityNames
	ctx.Data["NativeSourceNames"] = map[string]string{"owner": "个人项目所有者", "collaborator": "原生协作者", "team": "原生团队"}
	sourceGroups := map[int64]string{}
	for _, member := range state.Members {
		for _, grant := range member.Grants {
			if grant.ScopeType != "group" {
				continue
			}
			if _, checked := sourceGroups[grant.ScopeID]; checked {
				continue
			}
			group, err := governance_service.CheckGroupAccess(ctx, ctx.Doer.ID, grant.ScopeID, governance_model.ReadGroup)
			if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
				respondError(ctx, err)
				return
			}
			sourceGroups[grant.ScopeID] = ""
			if err == nil {
				sourceGroups[grant.ScopeID] = group.FullPath
			}
		}
	}
	ctx.Data["SourceGroups"] = sourceGroups
	ctx.Data["State"] = state
	ctx.Data["InvitationPath"] = invitationPath("repository", ctx.PathParamInt64("id"))
	ctx.Data["RoleNames"] = map[governance_model.Role]string{5: "Minimal Access", 10: "Guest", 15: "Planner", 20: "Reporter", 30: "Developer", 40: "Maintainer", 50: "Owner"}
	ctx.Data["SourceNames"] = map[string]string{"direct": "直接", "inherited": "继承", "shared": "共享", "inherited_shared": "继承共享"}
	ctx.HTML(200, "governance/repository_members")
}

func SaveRepositoryMember(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	option := governance_service.GroupMemberOption{UserID: ctx.FormInt64("user_id"), Role: governance_model.Role(ctx.FormInt("role")), CustomRoleID: ctx.FormInt64("custom_role_id"), Revision: ctx.FormInt64("revision")}
	remove := ctx.FormString("action") == "remove"
	if !remove {
		user, err := user_model.GetUserByName(ctx, ctx.FormString("username"))
		if err != nil {
			respondError(ctx, governance_model.ErrNotFound)
			return
		}
		option.UserID = user.ID
		if expiry := ctx.FormString("expires"); expiry != "" {
			date, err := time.Parse("2006-01-02", expiry)
			if err != nil {
				respondError(ctx, governance_model.ErrInvalid)
				return
			}
			option.ExpiresUnix = date.Unix()
		}
	}
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	if err := governance_service.SetRepositoryMember(ctx, actor, id, option, remove); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncRepositoryGovernanceReviewRequests(ctx, id, ctx.Doer)
	ctx.Flash.Success("项目直接授权已更新；其他有效授权来源继续生效")
	ctx.Redirect(setting.AppSubURL + "/governance/repositories/" + strconv.FormatInt(id, 10) + "/members")
}

func SaveRepositoryRole(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	option := governance_service.GroupRoleOption{Name: ctx.FormString("name"), BaseRole: governance_model.Role(ctx.FormInt("base_role")), Abilities: ctx.FormStrings("abilities"), Revision: ctx.FormInt64("revision")}
	actor := governance_model.AuditActor(ctx)
	if actor.EffectiveUserID() <= 0 {
		actor = governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	}
	if _, err := governance_service.SaveRepositoryRootRole(ctx, actor, id, ctx.FormInt64("role_id"), option, ctx.FormString("action") == "remove"); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncRepositoryGovernanceReviewRequests(ctx, id, ctx.Doer)
	ctx.Flash.Success("个人命名空间自定义角色已更新")
	ctx.Redirect(setting.AppSubURL + "/governance/repositories/" + strconv.FormatInt(id, 10) + "/members")
}
