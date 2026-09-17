// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package swagger

import (
	"gitea.dev/models/governance"
	governance_api "gitea.dev/routers/api/v1/governance"
	governance_service "gitea.dev/services/governance"
)

// 原生分支保护与仓库审批共用的配置读取结果。
// swagger:response BranchApprovalConfiguration
type swaggerBranchApprovalConfiguration struct {
	// in:body
	Body governance_service.BranchApprovalConfiguration
}

// 授权范围内的审计事件页。
// swagger:response GovernanceAuditPage
type swaggerGovernanceAuditPage struct {
	// in:body
	Body governance.AuditPage `json:"body"`
}

// 当前用户的后台导出任务。
// swagger:response GovernanceAuditExport
type swaggerGovernanceAuditExport struct {
	// in:body
	Body governance.AuditExport `json:"body"`
}

// 已授权的外送目标与投递状态。
// swagger:response GovernanceAuditStreams
type swaggerGovernanceAuditStreams struct {
	// in:body
	Body []*governance_service.AuditStreamState `json:"body"`
}

// 外送目标保存回执。
// swagger:response GovernanceAuditStreamSaved
type swaggerGovernanceAuditStreamSaved struct {
	// in:body
	Body governance_service.AuditStreamSaved `json:"body"`
}

// 群组及当前调用者的权限来源。
// swagger:response GovernanceGroup
type swaggerGovernanceGroup struct {
	// in:body
	Body governance_service.GroupState
}

// 经过可见性过滤的群组列表。
// swagger:response GovernanceGroups
type swaggerGovernanceGroups struct {
	// in:body
	Body []governance_service.GroupState
}

// 群组成员及各自的授权来源。
// swagger:response GovernanceGroupMembers
type swaggerGovernanceGroupMembers struct {
	// in:body
	Body []*governance_service.GroupMemberState
}

// 群组的直接共享来源。
// swagger:response GovernanceGroupShares
type swaggerGovernanceGroupShares struct {
	// in:body
	Body []*governance.Share
}

// 可用于当前层级的自定义角色。
// swagger:response GovernanceGroupRoles
type swaggerGovernanceGroupRoles struct {
	// in:body
	Body []*governance.CustomRole
}

// 自定义角色保存回执。
// swagger:response GovernanceGroupRole
type swaggerGovernanceGroupRole struct {
	// in:body
	Body *governance.CustomRole
}

// 项目共享关系及当前预览修订号。
// swagger:response GovernanceRepositoryShares
type swaggerGovernanceRepositoryShares struct {
	// in:body
	Body governance_service.RepositorySharesState
}

// 项目审批规则、设置来源和门禁就绪状态。
// swagger:response GovernanceRepositoryApprovalRules
type swaggerGovernanceRepositoryApprovalRules struct {
	// in:body
	Body governance_service.RepositoryApprovalRules
}

// 项目审批规则保存回执。
// swagger:response GovernanceApprovalRule
type swaggerGovernanceApprovalRule struct {
	// in:body
	Body governance.ApprovalRule
}

// 当前 PR 审批证据判定，不替代最终合并授权。
// swagger:response GovernancePullApprovalState
type swaggerGovernancePullApprovalState struct {
	// in:body
	Body governance_service.PullApprovalResult
}

// 项目本级审批设置及编辑修订号。
// swagger:response GovernanceApprovalSettings
type swaggerGovernanceApprovalSettings struct {
	// in:body
	Body governance.ApprovalSettings
}

// PR 独立规则及不可覆盖的策略来源。
// swagger:response GovernancePullApprovalRules
type swaggerGovernancePullApprovalRules struct {
	// in:body
	Body governance_service.PullApprovalRules
}

// 已保存的不可变 PR 规则版本。
// swagger:response GovernancePullRuleVersion
type swaggerGovernancePullRuleVersion struct {
	// in:body
	Body governance.PullRuleVersion
}

// 实例或顶级群组强制审批策略。
// swagger:response GovernanceApprovalPolicies
type swaggerGovernanceApprovalPolicies struct {
	// in:body
	Body governance_service.ApprovalPolicies
}

// swagger:response GovernanceScopeApprovalSettings
type swaggerGovernanceScopeApprovalSettings struct {
	// in:body
	Body governance_service.ScopeApprovalSettings
}

// swagger:response GovernanceGroupArchiveImpact
type swaggerGovernanceGroupArchiveImpact struct {
	// in:body
	Body governance_service.GroupArchiveImpact
}

// 访问申请与当前调用者可见的处理队列。
// swagger:response GovernanceAccessRequestState
type swaggerGovernanceAccessRequestState struct {
	// in:body
	Body governance_service.AccessRequestState `json:"body"`
}

// 已创建的待处理访问申请。
// swagger:response GovernanceAccessRequest
type swaggerGovernanceAccessRequest struct {
	// in:body
	Body governance.AccessRequest `json:"body"`
}

// 访问申请开关与修订号。
// swagger:response GovernanceAccessRequestSetting
type swaggerGovernanceAccessRequestSetting struct {
	// in:body
	Body governance.AccessRequestSetting `json:"body"`
}

// swagger:response GovernanceRepositoryMembers
type swaggerGovernanceRepositoryMembers struct {
	// in:body
	Body governance_service.RepositoryMembersState
}

// swagger:response GovernanceInvitations
type swaggerGovernanceInvitations struct {
	// in:body
	Body governance_service.InvitationPage `json:"body"`
}

// swagger:response GovernanceInvitation
type swaggerGovernanceInvitation struct {
	// in:body
	Body governance.Invitation `json:"body"`
}

// swagger:response GovernanceInvitationPreview
type swaggerGovernanceInvitationPreview struct {
	// in:body
	Body governance_service.InvitationPreview `json:"body"`
}

// 已鉴权的群组导航分页。
// swagger:response GovernanceNavigationPage
type swaggerGovernanceNavigationPage struct {
	// in:body
	Body governance_service.NavigationPage
}

// 群组导航及页面允许操作。
// swagger:response GovernanceGroupNavigation
type swaggerGovernanceGroupNavigation struct {
	// in:body
	Body governance_service.GroupNavigation
}

// 原生能力与验收状态。
// swagger:response GovernanceCapabilitiesResponse
type swaggerGovernanceCapabilities struct {
	// in:body
	Body governance_api.GovernanceCapabilities
}
