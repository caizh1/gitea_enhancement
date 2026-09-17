// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/web"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
	issue_service "gitea.dev/services/issue"
	org_service "gitea.dev/services/org"
)

type (
	GroupDeletionOption = governance_service.GroupDeletionOption
	GroupArchiveOption  = governance_service.GroupArchiveOption
	GroupRoleOption     = governance_service.GroupRoleOption
	GroupShareOption    = governance_service.GroupShareOption
	GroupOption         = governance_service.GroupOption
	GroupMemberOption   = governance_service.GroupMemberOption
)

func Groups(ctx *context.APIContext) {
	// swagger:operation GET /governance/groups governance governanceGroups
	// ---
	// summary: 按父级列出有权查看的群组
	// produces:
	// - application/json
	// parameters:
	// - name: parent_id
	//   in: query
	//   type: integer
	//   format: int64
	// - name: after_id
	//   in: query
	//   type: integer
	//   format: int64
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceGroups"
	groups, err := governance_service.ListGroups(ctx, ctx.Doer.ID, ctx.FormInt64("parent_id"), ctx.FormInt64("after_id"), 100)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, groups)
}

func Group(ctx *context.APIContext) {
	// swagger:operation GET /governance/groups/{id} governance governanceGroup
	// ---
	// summary: 查看群组及当前用户的有效权限来源
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   type: integer
	//   format: int64
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceGroup"
	group, err := governance_service.CheckGroupAccess(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"), governance_model.ReadGroup)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, group)
}

func CreateGroup(ctx *context.APIContext) {
	// swagger:operation POST /governance/groups governance governanceCreateGroup
	// ---
	// summary: 创建顶级群组或子群组
	// consumes:
	// - application/json
	// produces:
	// - application/json
	// parameters:
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/GroupOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/GovernanceGroup"
	group, err := governance_service.CreateGroup(ctx, requestActor(ctx), *web.GetForm(ctx).(*GroupOption))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, group)
}

func MoveGroup(ctx *context.APIContext) {
	// swagger:operation POST /governance/groups/{id}/move governance governanceMoveGroup
	// ---
	// summary: 按修订号原子移动或改名，保留旧地址别名
	// consumes:
	// - application/json
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
	//     "$ref": "#/definitions/GroupOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceGroup"
	group, err := governance_service.MoveGroup(ctx, requestActor(ctx), ctx.PathParamInt64("id"), *web.GetForm(ctx).(*GroupOption))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, group)
}

func SetGroupMember(ctx *context.APIContext) {
	// swagger:operation PUT /governance/groups/{id}/members/{user_id} governance governanceSetGroupMember
	// ---
	// summary: 设置当前群组的直接成员来源，不覆盖继承或团队授权
	// consumes:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: user_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/GroupMemberOption"
	// responses:
	//   "204":
	//     description: 直接授权已更新
	option := *web.GetForm(ctx).(*GroupMemberOption)
	option.UserID = ctx.PathParamInt64("user_id")
	if err := governance_service.SetGroupMember(ctx, requestActor(ctx), ctx.PathParamInt64("id"), option, false); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncScopeGovernanceReviewRequests(ctx, "group", ctx.PathParamInt64("id"), ctx.Doer)
	ctx.Status(http.StatusNoContent)
}

func RemoveGroupMember(ctx *context.APIContext) {
	// swagger:operation DELETE /governance/groups/{id}/members/{user_id} governance governanceRemoveGroupMember
	// ---
	// summary: 移除当前群组的一条直接授权来源
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: user_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: revision
	//   in: query
	//   required: true
	//   type: integer
	//   format: int64
	// responses:
	//   "204":
	//     description: 指定来源已移除，其他来源继续生效
	option := GroupMemberOption{UserID: ctx.PathParamInt64("user_id"), Revision: ctx.FormInt64("revision")}
	if err := governance_service.SetGroupMember(ctx, requestActor(ctx), ctx.PathParamInt64("id"), option, true); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncScopeGovernanceReviewRequests(ctx, "group", ctx.PathParamInt64("id"), ctx.Doer)
	ctx.Status(http.StatusNoContent)
}

func GroupMembers(ctx *context.APIContext) {
	// swagger:operation GET /governance/groups/{id}/members governance governanceGroupMembers
	// ---
	// summary: 查看群组成员的直接、继承、共享和原生团队来源
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: after_id
	//   in: query
	//   type: integer
	//   format: int64
	// - name: q
	//   in: query
	//   type: string
	//   description: 用户名或姓名
	// - name: source
	//   in: query
	//   type: string
	//   enum: [direct, inherited, shared, inherited_shared, native_team]
	// - name: own
	//   in: query
	//   type: boolean
	//   description: 仅查看自己的授权来源
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceGroupMembers"
	members, err := governance_service.ListGroupMembers(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"), ctx.FormInt64("after_id"), 100, governance_service.GroupMemberQuery{Q: ctx.FormString("q"), Source: ctx.FormString("source"), Own: ctx.FormBool("own")})
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, members)
}

func GroupShares(ctx *context.APIContext) {
	// swagger:operation GET /governance/groups/{id}/shares governance governanceGroupShares
	// ---
	// summary: 查看群组直接共享关系
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: after_id
	//   in: query
	//   type: integer
	//   format: int64
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceGroupShares"
	shares, err := governance_service.ListGroupShares(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"), ctx.FormInt64("after_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, shares)
}

func SetGroupShare(ctx *context.APIContext) {
	// swagger:operation PUT /governance/groups/{id}/shares/{group_id} governance governanceSetGroupShare
	// ---
	// summary: 设置群组共享、角色上限和到期时间
	// consumes:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: group_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/GroupShareOption"
	// responses:
	//   "204":
	//     description: 共享已生效
	option := *web.GetForm(ctx).(*GroupShareOption)
	option.GroupID = ctx.PathParamInt64("group_id")
	if err := governance_service.SetGroupShare(ctx, requestActor(ctx), ctx.PathParamInt64("id"), option, false); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncScopeGovernanceReviewRequests(ctx, "group", ctx.PathParamInt64("id"), ctx.Doer)
	ctx.Status(http.StatusNoContent)
}

func RemoveGroupShare(ctx *context.APIContext) {
	// swagger:operation DELETE /governance/groups/{id}/shares/{group_id} governance governanceRemoveGroupShare
	// ---
	// summary: 撤销当前群组的指定共享来源
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: group_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: revision
	//   in: query
	//   required: true
	//   type: integer
	//   format: int64
	// responses:
	//   "204":
	//     description: 共享已撤销
	option := GroupShareOption{GroupID: ctx.PathParamInt64("group_id"), Revision: ctx.FormInt64("revision")}
	if err := governance_service.SetGroupShare(ctx, requestActor(ctx), ctx.PathParamInt64("id"), option, true); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncScopeGovernanceReviewRequests(ctx, "group", ctx.PathParamInt64("id"), ctx.Doer)
	ctx.Status(http.StatusNoContent)
}

func GroupRoles(ctx *context.APIContext) {
	// swagger:operation GET /governance/groups/{id}/roles governance governanceGroupRoles
	// ---
	// summary: 查看当前层级可用的自定义角色
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: after_id
	//   in: query
	//   type: integer
	//   format: int64
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceGroupRoles"
	roles, err := governance_service.ListGroupRoles(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"), ctx.FormInt64("after_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, roles)
}

func SaveGroupRole(ctx *context.APIContext) {
	// swagger:operation POST /governance/groups/{id}/roles governance governanceCreateGroupRole
	// ---
	// summary: 在顶级群组创建自定义角色
	// consumes:
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
	//     "$ref": "#/definitions/GroupRoleOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceGroupRole"
	role, err := governance_service.SaveGroupRole(ctx, requestActor(ctx), ctx.PathParamInt64("id"), ctx.PathParamInt64("role_id"), *web.GetForm(ctx).(*GroupRoleOption), false)
	if err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncScopeGovernanceReviewRequests(ctx, "group", ctx.PathParamInt64("id"), ctx.Doer)
	ctx.JSON(http.StatusOK, role)
}

func UpdateGroupRole(ctx *context.APIContext) {
	// swagger:operation PUT /governance/groups/{id}/roles/{role_id} governance governanceUpdateGroupRole
	// ---
	// summary: 修改自定义角色并立即重算权限
	// consumes:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: role_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/GroupRoleOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceGroupRole"
	SaveGroupRole(ctx)
}

func RemoveGroupRole(ctx *context.APIContext) {
	// swagger:operation DELETE /governance/groups/{id}/roles/{role_id} governance governanceRemoveGroupRole
	// ---
	// summary: 删除未被授权引用的自定义角色
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: role_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: revision
	//   in: query
	//   required: true
	//   type: integer
	//   format: int64
	// responses:
	//   "204":
	//     description: 角色已删除
	_, err := governance_service.SaveGroupRole(ctx, requestActor(ctx), ctx.PathParamInt64("id"), ctx.PathParamInt64("role_id"), GroupRoleOption{Revision: ctx.FormInt64("revision")}, true)
	if err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncScopeGovernanceReviewRequests(ctx, "group", ctx.PathParamInt64("id"), ctx.Doer)
	ctx.Status(http.StatusNoContent)
}

func RepositoryShares(ctx *context.APIContext) {
	// swagger:operation GET /governance/repositories/{id}/shares governance governanceRepositoryShares
	// ---
	// summary: 查看项目直接共享关系和修订号
	// produces:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: after_id
	//   in: query
	//   type: integer
	//   format: int64
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceRepositoryShares"
	shares, err := governance_service.ListRepositoryShares(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"), ctx.FormInt64("after_id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, shares)
}

func SetRepositoryShare(ctx *context.APIContext) {
	// swagger:operation PUT /governance/repositories/{id}/shares/{group_id} governance governanceSetRepositoryShare
	// ---
	// summary: 设置项目共享、角色上限和到期时间
	// consumes:
	// - application/json
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: group_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/GroupShareOption"
	// responses:
	//   "204":
	//     description: 共享已生效
	option := *web.GetForm(ctx).(*GroupShareOption)
	option.GroupID = ctx.PathParamInt64("group_id")
	if err := governance_service.SetRepositoryShare(ctx, requestActor(ctx), ctx.PathParamInt64("id"), option, false); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncRepositoryGovernanceReviewRequests(ctx, ctx.PathParamInt64("id"), ctx.Doer)
	ctx.Status(http.StatusNoContent)
}

func RemoveRepositoryShare(ctx *context.APIContext) {
	// swagger:operation DELETE /governance/repositories/{id}/shares/{group_id} governance governanceRemoveRepositoryShare
	// ---
	// summary: 撤销当前项目的指定共享来源
	// parameters:
	// - name: id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: group_id
	//   in: path
	//   required: true
	//   type: integer
	//   format: int64
	// - name: revision
	//   in: query
	//   required: true
	//   type: integer
	//   format: int64
	// responses:
	//   "204":
	//     description: 共享已撤销
	option := GroupShareOption{GroupID: ctx.PathParamInt64("group_id"), Revision: ctx.FormInt64("revision")}
	if err := governance_service.SetRepositoryShare(ctx, requestActor(ctx), ctx.PathParamInt64("id"), option, true); err != nil {
		respondError(ctx, err)
		return
	}
	issue_service.SyncRepositoryGovernanceReviewRequests(ctx, ctx.PathParamInt64("id"), ctx.Doer)
	ctx.Status(http.StatusNoContent)
}

func GroupArchiveImpact(ctx *context.APIContext) {
	// swagger:operation GET /governance/groups/{id}/archive governance governanceGroupArchiveImpact
	// ---
	// summary: 查看归档影响范围和阻断父级
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
	//     "$ref": "#/responses/GovernanceGroupArchiveImpact"
	impact, err := governance_service.PreviewGroupArchive(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, impact)
}

func ArchiveGroup(ctx *context.APIContext) {
	// swagger:operation PUT /governance/groups/{id}/archive governance governanceArchiveGroup
	// ---
	// summary: 按修订号统一归档或恢复群组及后代
	// consumes:
	// - application/json
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
	//     "$ref": "#/definitions/GroupArchiveOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceGroup"
	group, err := governance_service.SetGroupArchiveState(ctx, requestActor(ctx), ctx.PathParamInt64("id"), *web.GetForm(ctx).(*GroupArchiveOption))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, group)
}

func ScheduleGroupDeletion(ctx *context.APIContext) {
	// swagger:operation POST /governance/groups/{id}/deletion governance governanceScheduleGroupDeletion
	// ---
	// summary: 安排可恢复的群组删除
	// consumes:
	// - application/json
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
	//     "$ref": "#/definitions/GroupDeletionOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceGroup"
	group, err := governance_service.ScheduleGroupDeletion(ctx, requestActor(ctx), ctx.PathParamInt64("id"), *web.GetForm(ctx).(*GroupDeletionOption))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, group)
}

func RestoreGroupDeletion(ctx *context.APIContext) {
	// swagger:operation POST /governance/groups/{id}/restore governance governanceRestoreGroupDeletion
	// ---
	// summary: 恢复待删除群组及原有归档状态
	// consumes:
	// - application/json
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
	//     "$ref": "#/definitions/GroupDeletionOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/GovernanceGroup"
	group, err := governance_service.RestoreGroupDeletion(ctx, requestActor(ctx), ctx.PathParamInt64("id"), *web.GetForm(ctx).(*GroupDeletionOption))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, group)
}

func DeletePendingGroup(ctx *context.APIContext) {
	// swagger:operation POST /governance/groups/{id}/delete-permanently governance governanceDeletePendingGroup
	// ---
	// summary: 确认路径并永久删除已经处于待删除状态的群组
	// consumes:
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
	//     "$ref": "#/definitions/GroupDeletionOption"
	// responses:
	//   "204":
	//     description: 数据库删除已经提交，文件按持久化清理任务处理
	actor := requestActor(ctx)
	if err := org_service.DeleteScheduledGroup(ctx, ctx.PathParamInt64("id"), &actor, *web.GetForm(ctx).(*GroupDeletionOption)); err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
