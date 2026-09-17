// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
	issue_service "gitea.dev/services/issue"
	org_service "gitea.dev/services/org"
)

var governanceAbilityNames = map[string]string{
	"create_group": "创建子群组", "create_project": "创建项目", "read_group": "查看群组", "read_issues": "查看工单", "write_issues": "管理工单", "read_code": "读取代码", "read_pulls": "查看合并请求", "write_pulls": "管理合并请求", "push_code": "推送代码", "merge_code": "合并代码", "approve_code": "批准代码", "read_wiki": "读取 Wiki", "write_wiki": "编辑 Wiki", "read_actions": "查看工作流", "write_actions": "管理工作流", "read_packages": "读取软件包", "write_packages": "发布软件包", "read_releases": "查看发布", "write_releases": "管理发布", "manage_project": "管理项目", "manage_group": "管理群组", "manage_members": "管理项目成员", "manage_group_members": "管理群组成员", "manage_approvals": "管理审批规则", "read_audit": "查看审计", "manage_audit": "管理审计外送",
}

func Groups(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	tab := ctx.FormString("tab")
	if tab == "" {
		tab = "settings"
	}
	ctx.Data["GroupTab"] = tab
	ctx.Data["ShowCreate"] = ctx.FormBool("create") || (ctx.Req.Method == http.MethodPost && ctx.FormString("action") != "move")
	ctx.Data["CreateName"] = ctx.FormString("name")
	ctx.Data["CreatePath"] = ctx.FormString("path")
	ctx.Data["CreateVisibility"] = ctx.FormInt("visibility")
	ctx.Data["MemberSearch"] = ctx.FormString("q")
	ctx.Data["MemberFilter"] = ctx.FormString("source")
	ctx.Data["Title"] = "群组与权限"
	ctx.Data["CanManageInstancePolicies"] = ctx.Doer.IsAdmin
	ctx.Data["AbilityNames"] = governanceAbilityNames
	ctx.Data["RoleNames"] = map[governance_model.Role]string{5: "Minimal Access", 10: "Guest", 15: "Planner", 20: "Reporter", 30: "Developer", 40: "Maintainer", 50: "Owner"}
	ctx.Data["SourceNames"] = map[string]string{"direct": "直接", "inherited": "继承", "shared": "共享", "inherited_shared": "继承共享"}
	ctx.Data["VisibilityNames"] = map[int]string{0: "公开", 1: "内部", 2: "私有"}
	if id > 0 {
		group, err := governance_service.CheckGroupAccess(ctx, ctx.Doer.ID, id, governance_model.ReadGroup)
		if err != nil {
			respondError(ctx, err)
			return
		}
		ctx.Data["Group"] = group
		if !prepareNavigation(ctx, id) {
			return
		}
		if ctx.Req.Method == http.MethodGet && ctx.FormString("tab") == "" && !ctx.FormBool("create") && !ctx.FormBool("move_preview") {
			ctx.Redirect(setting.AppSubURL + "/" + group.FullPath)
			return
		}
		if group.ParentID != 0 {
			parent, err := governance_service.CheckGroupAccess(ctx, ctx.Doer.ID, group.ParentID, governance_model.ReadGroup)
			if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
				respondError(ctx, err)
				return
			}
			if parent != nil {
				ctx.Data["MoveParentPath"] = parent.FullPath
			}
		}
		impact, err := governance_service.PreviewGroupArchive(ctx, ctx.Doer.ID, id)
		if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
			respondError(ctx, err)
			return
		}
		ctx.Data["ArchiveImpact"] = impact
		ctx.Data["DeletionRetentionDays"] = setting.Governance.DeletionRetentionDays
		if ctx.FormBool("move_preview") && group.Abilities[governance_model.ManageGroup] {
			parentID, err := resolveGroupPath(ctx, ctx.FormString("move_parent_path"))
			if err != nil {
				respondError(ctx, err)
				return
			}
			option := governance_service.GroupOption{Path: ctx.FormString("move_path"), ParentID: parentID, Revision: group.Revision}
			impact, err := governance_service.PreviewGroupMove(ctx, ctx.Doer.ID, id, option)
			if err != nil {
				respondError(ctx, err)
				return
			}
			ctx.Data["MoveImpact"], ctx.Data["MoveOption"] = impact, option
			ctx.Data["MoveParentPath"] = strings.Trim(strings.TrimSpace(ctx.FormString("move_parent_path")), "/")
		}

		if group.Abilities[governance_model.ManageGroup] {
			shares, err := governance_service.ListGroupShares(ctx, ctx.Doer.ID, id, ctx.FormInt64("shares_after"))
			if err != nil {
				respondError(ctx, err)
				return
			}
			ctx.Data["Shares"] = shares
			shareNames := make(map[int64]string, len(shares))
			for _, share := range shares {
				invited, err := governance_service.CheckGroupAccess(ctx, ctx.Doer.ID, share.GroupID, governance_model.ReadGroup)
				if errors.Is(err, governance_model.ErrNotFound) {
					shareNames[share.GroupID] = "不可见群组"
					continue
				}
				if err != nil {
					respondError(ctx, err)
					return
				}
				shareNames[share.GroupID] = invited.FullPath
			}
			ctx.Data["ShareNames"] = shareNames
			if len(shares) == 100 {
				ctx.Data["SharesNext"] = shares[len(shares)-1].ID
			}
		}
		if group.Abilities[governance_model.ManageGroupMembers] {
			roles, err := governance_service.ListGroupRoles(ctx, ctx.Doer.ID, id, ctx.FormInt64("roles_after"))
			if err != nil {
				respondError(ctx, err)
				return
			}
			ctx.Data["CustomRoles"] = roles
			if len(roles) == 100 {
				ctx.Data["RolesNext"] = roles[len(roles)-1].ID
			}
		}
		if tab == "members" || group.Abilities[governance_model.ManageGroupMembers] || ctx.Doer.IsAuditor {
			members, err := governance_service.ListGroupMembers(ctx, ctx.Doer.ID, id, ctx.FormInt64("members_after"), 100, governance_service.GroupMemberQuery{Q: ctx.FormString("q"), Source: ctx.FormString("source"), Own: !group.Abilities[governance_model.ManageGroupMembers] && !ctx.Doer.IsAuditor})
			if err != nil {
				respondError(ctx, err)
				return
			}
			ctx.Data["Members"] = members
			sourceGroups := map[int64]string{}
			for _, member := range members {
				for _, grant := range member.Grants {
					if grant.ScopeType != "group" {
						continue
					}
					if _, checked := sourceGroups[grant.ScopeID]; checked {
						continue
					}
					sourceGroups[grant.ScopeID] = ""
					source, accessErr := governance_service.CheckGroupAccess(ctx, ctx.Doer.ID, grant.ScopeID, governance_model.ReadGroup)
					if accessErr != nil && !errors.Is(accessErr, governance_model.ErrNotFound) {
						respondError(ctx, accessErr)
						return
					}
					if accessErr == nil {
						sourceGroups[grant.ScopeID] = source.FullPath
					}
				}
			}
			ctx.Data["MemberSourceGroups"] = sourceGroups
			if len(members) == 100 {
				ctx.Data["MembersNext"] = members[len(members)-1].UserID
			}
		}
	}
	groups, err := governance_service.ListGroups(ctx, ctx.Doer.ID, id, ctx.FormInt64("after_id"), 100)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Data["Groups"] = groups
	if len(groups) == 100 {
		ctx.Data["NextID"] = groups[len(groups)-1].ID
	}
	ctx.HTML(http.StatusOK, "governance/groups")
}

func resolveGroupPath(ctx *context.Context, path string) (int64, error) {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return 0, nil
	}
	resolved, err := governance_model.ResolvePath(ctx, path)
	if err != nil {
		return 0, err
	}
	if resolved.Kind != "group" {
		return 0, governance_model.ErrNotFound
	}
	return resolved.ResourceID, nil
}

func SaveGroup(ctx *context.Context) {
	option := governance_service.GroupOption{Name: ctx.FormString("name"), Path: ctx.FormString("path"), ParentID: ctx.FormInt64("parent_id"), Visibility: ctx.FormInt("visibility"), Revision: ctx.FormInt64("revision")}
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	var group *governance_service.GroupState
	var err error
	if ctx.FormString("action") == "move" {
		group, err = governance_service.MoveGroup(ctx, actor, ctx.PathParamInt64("id"), option)
	} else {
		group, err = governance_service.CreateGroup(ctx, actor, option)
	}
	if err != nil {
		if ctx.FormString("action") != "move" && (errors.Is(err, governance_model.ErrInvalid) || errors.Is(err, governance_model.ErrConflict)) {
			ctx.Data["CreateError"] = err.Error()
			ctx.SetPathParam("id", strconv.FormatInt(option.ParentID, 10))
			Groups(ctx)
			return
		}
		respondError(ctx, err)
		return
	}
	if !group.Abilities[governance_model.ReadGroup] {
		ctx.Flash.Success("移动已完成；你的原继承权限已撤销")
		ctx.Redirect(setting.AppSubURL + "/governance/groups")
		return
	}
	ctx.Flash.Success("群组已保存")
	ctx.Redirect(setting.AppSubURL + "/governance/groups/" + strconv.FormatInt(group.ID, 10))
}

func SaveGroupMember(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	// 先验证范围，再解析用户名，避免无权用户探测成员候选。
	if _, err := governance_service.CheckGroupAccess(ctx, ctx.Doer.ID, id, governance_model.ManageGroupMembers); err != nil {
		respondError(ctx, err)
		return
	}
	userID := ctx.FormInt64("user_id")
	if userID == 0 {
		user, err := user_model.GetUserByName(ctx, ctx.FormString("username"))
		if err != nil {
			respondError(ctx, governance_model.ErrInvalid)
			return
		}
		userID = user.ID
	}
	option := governance_service.GroupMemberOption{UserID: userID, Role: governance_model.Role(ctx.FormInt("role")), CustomRoleID: ctx.FormInt64("custom_role_id"), Revision: ctx.FormInt64("revision")}
	if expiry := ctx.FormString("expires"); expiry != "" {
		date, err := time.Parse("2006-01-02", expiry)
		if err != nil {
			respondError(ctx, governance_model.ErrInvalid)
			return
		}
		option.ExpiresUnix = date.Add(24 * time.Hour).Unix()
	}
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	if err := governance_service.SetGroupMember(ctx, actor, id, option, ctx.FormString("action") == "remove"); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncScopeGovernanceReviewRequests(ctx, "group", id, ctx.Doer)
	ctx.Flash.Success("当前群组的直接授权已更新；其他来源继续按实际权限生效")
	ctx.Redirect(setting.AppSubURL + "/governance/groups/" + strconv.FormatInt(id, 10) + "?tab=members")
}

func SaveGroupShare(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	remove := ctx.FormString("action") == "remove"
	option := governance_service.GroupShareOption{GroupID: ctx.FormInt64("group_id"), MaxRole: governance_model.Role(ctx.FormInt("role")), Revision: ctx.FormInt64("revision")}
	if !remove {
		groupID, err := resolveGroupPath(ctx, ctx.FormString("group_path"))
		if err != nil || groupID == 0 {
			if err == nil {
				err = governance_model.ErrInvalid
			}
			respondError(ctx, err)
			return
		}
		option.GroupID = groupID
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
	if err := governance_service.SetGroupShare(ctx, actor, id, option, remove); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncScopeGovernanceReviewRequests(ctx, "group", id, ctx.Doer)
	ctx.Flash.Success("群组共享已更新")
	ctx.Redirect(setting.AppSubURL + "/governance/groups/" + strconv.FormatInt(id, 10))
}

func SaveExternalShareRestriction(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	if err := governance_service.SetExternalShareRestriction(ctx, actor, id, ctx.FormInt64("revision"), ctx.FormBool("enabled")); err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Flash.Success("跨顶级群组共享限制已更新；已有共享关系保留")
	ctx.Redirect(setting.AppSubURL + "/governance/groups/" + strconv.FormatInt(id, 10))
}

func SaveGroupRole(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	option := governance_service.GroupRoleOption{Name: ctx.FormString("name"), BaseRole: governance_model.Role(ctx.FormInt("base_role")), Abilities: ctx.FormStrings("abilities"), Revision: ctx.FormInt64("revision")}
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	if _, err := governance_service.SaveGroupRole(ctx, actor, id, ctx.FormInt64("role_id"), option, ctx.FormString("action") == "remove"); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncScopeGovernanceReviewRequests(ctx, "group", id, ctx.Doer)
	ctx.Flash.Success("自定义角色已保存，现有成员权限已重新计算")
	ctx.Redirect(setting.AppSubURL + "/governance/groups/" + strconv.FormatInt(id, 10))
}

func ArchiveGroup(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	option := governance_service.GroupArchiveOption{Archived: ctx.FormBool("archived"), Revision: ctx.FormInt64("revision")}
	_, err := governance_service.SetGroupArchiveState(ctx, governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web"), id, option)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Flash.Success("群组及后代归档状态已更新")
	ctx.Redirect(setting.AppSubURL + "/governance/groups/" + strconv.FormatInt(id, 10))
}

func GroupDeletion(ctx *context.Context) {
	id := ctx.PathParamInt64("id")
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	option := governance_service.GroupDeletionOption{Revision: ctx.FormInt64("revision"), ConfirmationPath: ctx.FormString("confirmation_path")}
	var err error
	switch ctx.FormString("action") {
	case "schedule":
		_, err = governance_service.ScheduleGroupDeletion(ctx, actor, id, option)
	case "restore":
		_, err = governance_service.RestoreGroupDeletion(ctx, actor, id, option)
	case "delete":
		err = org_service.DeleteScheduledGroup(ctx, id, &actor, option)
	default:
		err = governance_model.ErrInvalid
	}
	if err != nil {
		respondError(ctx, err)
		return
	}
	if ctx.FormString("action") == "delete" {
		ctx.Flash.Success("群组已删除，存储清理由后台可靠完成")
		ctx.Redirect(setting.AppSubURL + "/governance/groups")
		return
	}
	ctx.Flash.Success("群组删除状态已更新")
	ctx.Redirect(setting.AppSubURL + "/governance/groups/" + strconv.FormatInt(id, 10))
}
