// Copyright 2016 The Gogs Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package structs

import (
	"time"
)

// Branch represents a repository branch
type Branch struct {
	// Name is the branch name
	Name string `json:"name"`
	// Commit contains the latest commit information for this branch
	Commit *PayloadCommit `json:"commit"`
	// Protected indicates if the branch is protected
	Protected bool `json:"protected"`
	// RequiredApprovals is the number of required approvals for pull requests
	RequiredApprovals int64 `json:"required_approvals"`
	// EnableStatusCheck indicates if status checks are enabled
	EnableStatusCheck bool `json:"enable_status_check"`
	// StatusCheckContexts contains the list of required status check contexts
	StatusCheckContexts []string `json:"status_check_contexts"`
	// UserCanPush indicates if the current user can push to this branch
	UserCanPush bool `json:"user_can_push"`
	// UserCanMerge indicates if the current user can merge to this branch
	UserCanMerge bool `json:"user_can_merge"`
	// EffectiveBranchProtectionName is the name of the effective branch protection rule
	EffectiveBranchProtectionName string `json:"effective_branch_protection_name"`
}

// BranchProtection represents a branch protection for a repository
type BranchProtection struct {
	// 原子审批配置，结构与仓库审批批量接口一致；省略时保留现有规则。
	ApprovalConfiguration BranchApprovalPayload `json:"approval_configuration,omitempty"`
	// 启用后，目标分支仅接受原生治理最终授权的合并写入。
	RequireGovernanceApproval bool `json:"require_governance_approval"`
	// Deprecated: true
	BranchName string `json:"branch_name"`
	// RuleName is the name of the branch protection rule
	RuleName string `json:"rule_name"`
	// Priority is the priority of this branch protection rule
	Priority                      int64    `json:"priority"`
	EnablePush                    bool     `json:"enable_push"`
	EnablePushWhitelist           bool     `json:"enable_push_whitelist"`
	PushWhitelistUsernames        []string `json:"push_whitelist_usernames"`
	PushWhitelistTeams            []string `json:"push_whitelist_teams"`
	PushWhitelistDeployKeys       bool     `json:"push_whitelist_deploy_keys"`
	EnableForcePush               bool     `json:"enable_force_push"`
	EnableForcePushAllowlist      bool     `json:"enable_force_push_allowlist"`
	ForcePushAllowlistUsernames   []string `json:"force_push_allowlist_usernames"`
	ForcePushAllowlistTeams       []string `json:"force_push_allowlist_teams"`
	ForcePushAllowlistDeployKeys  bool     `json:"force_push_allowlist_deploy_keys"`
	EnableMergeWhitelist          bool     `json:"enable_merge_whitelist"`
	MergeWhitelistUsernames       []string `json:"merge_whitelist_usernames"`
	MergeWhitelistTeams           []string `json:"merge_whitelist_teams"`
	EnableBypassAllowlist         bool     `json:"enable_bypass_allowlist"`
	BypassAllowlistUsernames      []string `json:"bypass_allowlist_usernames"`
	BypassAllowlistTeams          []string `json:"bypass_allowlist_teams"`
	EnableStatusCheck             bool     `json:"enable_status_check"`
	StatusCheckContexts           []string `json:"status_check_contexts"`
	RequiredApprovals             int64    `json:"required_approvals"`
	EnableApprovalsWhitelist      bool     `json:"enable_approvals_whitelist"`
	ApprovalsWhitelistUsernames   []string `json:"approvals_whitelist_username"`
	ApprovalsWhitelistTeams       []string `json:"approvals_whitelist_teams"`
	BlockOnRejectedReviews        bool     `json:"block_on_rejected_reviews"`
	BlockOnOfficialReviewRequests bool     `json:"block_on_official_review_requests"`
	BlockOnOutdatedBranch         bool     `json:"block_on_outdated_branch"`
	DismissStaleApprovals         bool     `json:"dismiss_stale_approvals"`
	IgnoreStaleApprovals          bool     `json:"ignore_stale_approvals"`
	RequireSignedCommits          bool     `json:"require_signed_commits"`
	ProtectedFilePatterns         string   `json:"protected_file_patterns"`
	UnprotectedFilePatterns       string   `json:"unprotected_file_patterns"`
	BlockAdminMergeOverride       bool     `json:"block_admin_merge_override"`
	// swagger:strfmt date-time
	Created time.Time `json:"created_at"`
	// swagger:strfmt date-time
	Updated time.Time `json:"updated_at"`
}

// CreateBranchProtectionOption options for creating a branch protection
type CreateBranchProtectionOption struct {
	// 原子审批配置，结构与仓库审批批量接口一致；省略时保留现有规则。
	ApprovalConfiguration BranchApprovalPayload `json:"approval_configuration,omitempty"`
	// 启用后，目标分支仅接受原生治理最终授权的合并写入。
	RequireGovernanceApproval bool `json:"require_governance_approval"`
	// Deprecated: true
	BranchName                    string   `json:"branch_name"`
	RuleName                      string   `json:"rule_name"`
	Priority                      int64    `json:"priority"`
	EnablePush                    bool     `json:"enable_push"`
	EnablePushWhitelist           bool     `json:"enable_push_whitelist"`
	PushWhitelistUsernames        []string `json:"push_whitelist_usernames"`
	PushWhitelistTeams            []string `json:"push_whitelist_teams"`
	PushWhitelistDeployKeys       bool     `json:"push_whitelist_deploy_keys"`
	EnableForcePush               bool     `json:"enable_force_push"`
	EnableForcePushAllowlist      bool     `json:"enable_force_push_allowlist"`
	ForcePushAllowlistUsernames   []string `json:"force_push_allowlist_usernames"`
	ForcePushAllowlistTeams       []string `json:"force_push_allowlist_teams"`
	ForcePushAllowlistDeployKeys  bool     `json:"force_push_allowlist_deploy_keys"`
	EnableMergeWhitelist          bool     `json:"enable_merge_whitelist"`
	MergeWhitelistUsernames       []string `json:"merge_whitelist_usernames"`
	MergeWhitelistTeams           []string `json:"merge_whitelist_teams"`
	EnableBypassAllowlist         bool     `json:"enable_bypass_allowlist"`
	BypassAllowlistUsernames      []string `json:"bypass_allowlist_usernames"`
	BypassAllowlistTeams          []string `json:"bypass_allowlist_teams"`
	EnableStatusCheck             bool     `json:"enable_status_check"`
	StatusCheckContexts           []string `json:"status_check_contexts"`
	RequiredApprovals             int64    `json:"required_approvals"`
	EnableApprovalsWhitelist      bool     `json:"enable_approvals_whitelist"`
	ApprovalsWhitelistUsernames   []string `json:"approvals_whitelist_username"`
	ApprovalsWhitelistTeams       []string `json:"approvals_whitelist_teams"`
	BlockOnRejectedReviews        bool     `json:"block_on_rejected_reviews"`
	BlockOnOfficialReviewRequests bool     `json:"block_on_official_review_requests"`
	BlockOnOutdatedBranch         bool     `json:"block_on_outdated_branch"`
	DismissStaleApprovals         bool     `json:"dismiss_stale_approvals"`
	IgnoreStaleApprovals          bool     `json:"ignore_stale_approvals"`
	RequireSignedCommits          bool     `json:"require_signed_commits"`
	ProtectedFilePatterns         string   `json:"protected_file_patterns"`
	UnprotectedFilePatterns       string   `json:"unprotected_file_patterns"`
	BlockAdminMergeOverride       bool     `json:"block_admin_merge_override"`
}

// EditBranchProtectionOption options for editing a branch protection
type EditBranchProtectionOption struct {
	// 原子审批配置，结构与仓库审批批量接口一致；省略时保留现有规则。
	ApprovalConfiguration BranchApprovalPayload `json:"approval_configuration,omitempty"`
	// 启用后，目标分支仅接受原生治理最终授权的合并写入。
	RequireGovernanceApproval     *bool    `json:"require_governance_approval"`
	Priority                      *int64   `json:"priority"`
	EnablePush                    *bool    `json:"enable_push"`
	EnablePushWhitelist           *bool    `json:"enable_push_whitelist"`
	PushWhitelistUsernames        []string `json:"push_whitelist_usernames"`
	PushWhitelistTeams            []string `json:"push_whitelist_teams"`
	PushWhitelistDeployKeys       *bool    `json:"push_whitelist_deploy_keys"`
	EnableForcePush               *bool    `json:"enable_force_push"`
	EnableForcePushAllowlist      *bool    `json:"enable_force_push_allowlist"`
	ForcePushAllowlistUsernames   []string `json:"force_push_allowlist_usernames"`
	ForcePushAllowlistTeams       []string `json:"force_push_allowlist_teams"`
	ForcePushAllowlistDeployKeys  *bool    `json:"force_push_allowlist_deploy_keys"`
	EnableMergeWhitelist          *bool    `json:"enable_merge_whitelist"`
	MergeWhitelistUsernames       []string `json:"merge_whitelist_usernames"`
	MergeWhitelistTeams           []string `json:"merge_whitelist_teams"`
	EnableBypassAllowlist         *bool    `json:"enable_bypass_allowlist"`
	BypassAllowlistUsernames      []string `json:"bypass_allowlist_usernames"`
	BypassAllowlistTeams          []string `json:"bypass_allowlist_teams"`
	EnableStatusCheck             *bool    `json:"enable_status_check"`
	StatusCheckContexts           []string `json:"status_check_contexts"`
	RequiredApprovals             *int64   `json:"required_approvals"`
	EnableApprovalsWhitelist      *bool    `json:"enable_approvals_whitelist"`
	ApprovalsWhitelistUsernames   []string `json:"approvals_whitelist_username"`
	ApprovalsWhitelistTeams       []string `json:"approvals_whitelist_teams"`
	BlockOnRejectedReviews        *bool    `json:"block_on_rejected_reviews"`
	BlockOnOfficialReviewRequests *bool    `json:"block_on_official_review_requests"`
	BlockOnOutdatedBranch         *bool    `json:"block_on_outdated_branch"`
	DismissStaleApprovals         *bool    `json:"dismiss_stale_approvals"`
	IgnoreStaleApprovals          *bool    `json:"ignore_stale_approvals"`
	RequireSignedCommits          *bool    `json:"require_signed_commits"`
	ProtectedFilePatterns         *string  `json:"protected_file_patterns"`
	UnprotectedFilePatterns       *string  `json:"unprotected_file_patterns"`
	BlockAdminMergeOverride       *bool    `json:"block_admin_merge_override"`
}

// UpdateBranchProtectionPriories a list to update the branch protection rule priorities
type UpdateBranchProtectionPriories struct {
	IDs []int64 `json:"ids"`
}

type MergeUpstreamRequest struct {
	Branch string `json:"branch"`
	FfOnly bool   `json:"ff_only"`
}

type MergeUpstreamResponse struct {
	MergeStyle string `json:"merge_type"`
}

// BranchApprovalPayload 是统一审批配置的 JSON 对象；具体字段见 BranchApprovalUpdate 与 BranchApprovalConfiguration。
// swagger:type object
type BranchApprovalPayload []byte

func (p BranchApprovalPayload) MarshalJSON() ([]byte, error) {
	if len(p) == 0 {
		return []byte("null"), nil
	}
	return p, nil
}

func (p *BranchApprovalPayload) UnmarshalJSON(data []byte) error {
	*p = append((*p)[:0], data...)
	return nil
}
