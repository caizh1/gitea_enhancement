// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package git

import (
	"cmp"
	"context"
	"errors"
	"reflect"
	"slices"

	governance_model "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
)

func requireProtectionManager(ctx context.Context, repo *repo_model.Repository) error {
	actor := governance_model.AuditActor(ctx)
	if actor.Kind == "system" {
		return nil
	}
	if actor.ActingAsID > 0 {
		admin, err := user_model.GetUserByID(ctx, actor.ID)
		if err != nil {
			return err
		}
		if !admin.IsAdmin || !admin.IsActive || admin.ProhibitLogin {
			return governance_model.ErrForbidden
		}
	}
	user, err := user_model.GetUserByID(ctx, actor.EffectiveUserID())
	if err != nil {
		return err
	}
	if !user.IsActive || user.ProhibitLogin {
		return governance_model.ErrForbidden
	}
	permission, err := access_model.GetDoerRepoPermission(ctx, repo, user)
	if err != nil {
		return err
	}
	permission = permission.ForMutation()
	if !permission.IsAdmin() {
		return governance_model.ErrForbidden
	}
	return nil
}

func normalizedProtectionList[T cmp.Ordered](values []T) []T {
	result := append([]T{}, values...)
	slices.Sort(result)
	return result
}

func protectionAuditValues(rule *ProtectedBranch) map[string]any {
	if rule == nil {
		return nil
	}
	// 只记录持久化治理字段，不包含运行期仓库对象和缓存。
	return map[string]any{
		"require_governance_approval":       rule.RequireGovernanceApproval,
		"rule_name":                         rule.RuleName,
		"priority":                          rule.Priority,
		"enable_whitelist":                  rule.EnableWhitelist,
		"can_push":                          rule.CanPush,
		"whitelist_user_ids":                normalizedProtectionList(rule.WhitelistUserIDs),
		"whitelist_team_ids":                normalizedProtectionList(rule.WhitelistTeamIDs),
		"enable_merge_whitelist":            rule.EnableMergeWhitelist,
		"whitelist_deploy_keys":             rule.WhitelistDeployKeys,
		"merge_whitelist_user_ids":          normalizedProtectionList(rule.MergeWhitelistUserIDs),
		"merge_whitelist_team_ids":          normalizedProtectionList(rule.MergeWhitelistTeamIDs),
		"enable_bypass_allowlist":           rule.EnableBypassAllowlist,
		"bypass_allowlist_user_ids":         normalizedProtectionList(rule.BypassAllowlistUserIDs),
		"bypass_allowlist_team_ids":         normalizedProtectionList(rule.BypassAllowlistTeamIDs),
		"can_force_push":                    rule.CanForcePush,
		"enable_force_push_allowlist":       rule.EnableForcePushAllowlist,
		"force_push_allowlist_user_ids":     normalizedProtectionList(rule.ForcePushAllowlistUserIDs),
		"force_push_allowlist_team_ids":     normalizedProtectionList(rule.ForcePushAllowlistTeamIDs),
		"force_push_allowlist_deploy_keys":  rule.ForcePushAllowlistDeployKeys,
		"enable_status_check":               rule.EnableStatusCheck,
		"status_check_contexts":             normalizedProtectionList(rule.StatusCheckContexts),
		"enable_approvals_whitelist":        rule.EnableApprovalsWhitelist,
		"approvals_whitelist_user_ids":      normalizedProtectionList(rule.ApprovalsWhitelistUserIDs),
		"approvals_whitelist_team_ids":      normalizedProtectionList(rule.ApprovalsWhitelistTeamIDs),
		"required_approvals":                rule.RequiredApprovals,
		"block_on_rejected_reviews":         rule.BlockOnRejectedReviews,
		"block_on_official_review_requests": rule.BlockOnOfficialReviewRequests,
		"block_on_outdated_branch":          rule.BlockOnOutdatedBranch,
		"dismiss_stale_approvals":           rule.DismissStaleApprovals,
		"ignore_stale_approvals":            rule.IgnoreStaleApprovals,
		"require_signed_commits":            rule.RequireSignedCommits,
		"protected_file_patterns":           rule.ProtectedFilePatterns,
		"unprotected_file_patterns":         rule.UnprotectedFilePatterns,
		"block_admin_merge_override":        rule.BlockAdminMergeOverride,
	}
}

func appendProtectionAudit(ctx context.Context, repo *repo_model.Repository, before, after *ProtectedBranch) error {
	oldValues, newValues := protectionAuditValues(before), protectionAuditValues(after)
	if reflect.DeepEqual(oldValues, newValues) {
		return nil
	}
	rule, kind := after, "repository.branch_protection_updated"
	if before == nil {
		kind = "repository.branch_protection_created"
	}
	if after == nil {
		rule, kind = before, "repository.branch_protection_deleted"
	}
	return appendRepositoryProtectionAudit(ctx, repo, kind, "branch_protection", rule.ID, rule.RuleName, oldValues, newValues)
}

func appendRepositoryProtectionAudit(ctx context.Context, repo *repo_model.Repository, kind, objectType string, id int64, name string, oldValues, newValues map[string]any) error {
	if err := repo.LoadOwner(ctx); err != nil {
		return err
	}
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	var ancestors []int64
	if errors.Is(err, governance_model.ErrNotFound) {
		if err := repo.LoadOwner(ctx); err != nil {
			return err
		}
		if repo.Owner.IsOrganization() {
			ancestors = append(ancestors, repo.OwnerID)
		}
	} else if err != nil {
		return err
	} else {
		for _, group := range chain {
			if group.Kind == "group" {
				ancestors = append(ancestors, group.ID)
			}
		}
	}
	details, err := json.Marshal(map[string]any{"before": oldValues, "after": newValues})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{
		Type: kind, Actor: governance_model.AuditActor(ctx), ScopeType: "repository", ScopeID: repo.ID,
		AncestorIDs: ancestors, ObjectType: objectType, ObjectID: id,
		ObjectPath: repo.FullPath() + ":" + name, Result: "success", Details: details,
	})
}
