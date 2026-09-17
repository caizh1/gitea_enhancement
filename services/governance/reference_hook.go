// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/json"
)

func ensureRequiredRuleReferenceHooks(ctx context.Context, actor governance_model.Actor, repoID int64, rule *governance_model.ApprovalRule) error {
	if rule == nil || !rule.Enabled || rule.Required <= 0 {
		return nil
	}
	if err := InstallRepositoryReferenceHook(ctx, actor, repoID); err != nil {
		return err
	}
	normalized := *rule
	normalized.ScopeType, normalized.ScopeID = "repository", repoID
	normalized.UserIDs = compactInt64s(rule.UserIDs)
	normalized.GroupIDs = compactInt64s(rule.GroupIDs)
	normalized.TeamIDs = compactInt64s(rule.TeamIDs)
	rule = &normalized
	var pulls []*issues_model.PullRequest
	if err := db.GetEngine(ctx).Where("base_repo_id = ? AND has_merged = ?", repoID, false).Find(&pulls); err != nil {
		return err
	}
	for _, pr := range pulls {
		if err := pr.LoadIssue(ctx); err != nil {
			return err
		}
		if pr.Issue.IsClosed {
			continue
		}
		applies, err := ApprovalRuleApplies(ctx, repoID, pr.BaseBranch, rule)
		if err != nil {
			return err
		}
		if !applies {
			continue
		}
		head, err := repo_model.GetRepositoryByID(ctx, pr.HeadRepoID)
		if err != nil {
			return err
		}
		installed, err := gitrepo.ReferenceTransactionHookInstalled(head)
		if err != nil {
			return err
		}
		if !installed {
			return fmt.Errorf("%w：开放 PR #%d 的源项目 %s 尚未接入引用事务，请由源项目审批管理员先完成接入", governance_model.ErrConflict, pr.Index, head.FullPath())
		}
	}
	return nil
}

func compactInt64s(values []int64) []int64 {
	result := slices.Clone(values)
	slices.Sort(result)
	return slices.Compact(result)
}

// InstallRepositoryReferenceHook 为有审批管理权的项目安装并回读引用事务入口。
func InstallRepositoryReferenceHook(ctx context.Context, actor governance_model.Actor, repoID int64) error {
	repo, _, err := checkApprovalRuleAccess(ctx, actor.EffectiveUserID(), repoID, true)
	if err != nil {
		return err
	}
	if err := gitrepo.InstallReferenceTransactionHook(ctx, repo); err != nil {
		cause := fmt.Errorf("安装引用事务入口失败（已有第三方入口时需先完成兼容迁移）：%w", err)
		return errors.Join(cause, recordReferenceHookAudit(ctx, actor, repoID, "repository.reference_hook_install_failed", "failure", "install_rejected"))
	}
	installed, err := gitrepo.ReferenceTransactionHookInstalled(repo)
	if err != nil {
		return errors.Join(err, recordReferenceHookAudit(ctx, actor, repoID, "repository.reference_hook_install_failed", "failure", "verification_error"))
	}
	if !installed {
		cause := fmt.Errorf("%w：引用事务入口安装后回读不一致", governance_model.ErrConflict)
		return errors.Join(cause, recordReferenceHookAudit(ctx, actor, repoID, "repository.reference_hook_install_failed", "failure", "verification_mismatch"))
	}
	return recordReferenceHookAudit(ctx, actor, repoID, "repository.reference_hook_installed", "success", "verified")
}

func recordReferenceHookAudit(ctx context.Context, actor governance_model.Actor, repoID int64, event, result, reason string) error {
	return withActorWrite(ctx, actor, []string{governance_model.Resource("repository", repoID)}, func(ctx context.Context) error {
		repo, err := repo_model.GetRepositoryByID(ctx, repoID)
		if err != nil {
			return err
		}
		chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
		if err != nil {
			return err
		}
		ancestors := make([]int64, 0, len(chain))
		for _, namespace := range chain {
			if namespace.Kind == "group" {
				ancestors = append(ancestors, namespace.ID)
			}
		}
		details, err := json.Marshal(map[string]any{"reason_code": reason})
		if err != nil {
			return err
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: event, Actor: actor, ScopeType: "repository", ScopeID: repoID, AncestorIDs: ancestors, ObjectType: "repository", ObjectID: repoID, ObjectPath: repo.FullPath(), Result: result, Details: details})
	})
}
