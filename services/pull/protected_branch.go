// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pull

import (
	"context"
	governance_model "gitea.dev/models/governance"
	governance_service "gitea.dev/services/governance"

	git_model "gitea.dev/models/git"
	repo_model "gitea.dev/models/repo"
)

func CreateOrUpdateProtectedBranch(ctx context.Context, repo *repo_model.Repository,
	protectBranch *git_model.ProtectedBranch, whitelistOptions git_model.WhitelistOptions, approvals ...governance_service.BranchApprovalUpdate,
) error {
	var err error
	if len(approvals) > 0 {
		err = governance_service.SaveBranchApprovals(ctx, governance_model.AuditActor(ctx), repo.ID, approvals[0], func(tx context.Context) (int64, error) {
			err := git_model.UpdateProtectBranch(tx, repo, protectBranch, whitelistOptions)
			return protectBranch.ID, err
		})
	} else {
		err = git_model.UpdateProtectBranch(ctx, repo, protectBranch, whitelistOptions)
	}
	if err != nil {
		return err
	}

	if err := RefreshProtectedBranch(ctx, repo, protectBranch); err != nil {
		return &ProtectionSavedRefreshError{Err: err}
	}
	return nil
}

// ProtectionSavedRefreshError 表示权威配置已经提交，不能把刷新失败报告为保存回滚。
type ProtectionSavedRefreshError struct{ Err error }

func (e *ProtectionSavedRefreshError) Error() string {
	return "配置已保存，PR 状态刷新尚未完成；最终合并仍读取已保存配置"
}
func (e *ProtectionSavedRefreshError) Unwrap() error { return e.Err }

// RefreshProtectedBranch 只调度派生状态，失败不撤销已提交的权威配置。
func RefreshProtectedBranch(ctx context.Context, repo *repo_model.Repository, protectBranch *git_model.ProtectedBranch) error {
	isPlainRule := !git_model.IsRuleNameSpecial(protectBranch.RuleName)
	var isBranchExist bool
	if isPlainRule {
		isBranchExist, _ = git_model.IsBranchExist(ctx, repo.ID, protectBranch.RuleName)
	}

	if isBranchExist {
		if err := CheckPRsForBaseBranch(ctx, repo, protectBranch.RuleName); err != nil {
			return err
		}
	} else {
		if !isPlainRule {
			// FIXME: since we only need to recheck files protected rules, we could improve this
			matchedBranches, err := git_model.FindAllMatchedBranches(ctx, repo.ID, protectBranch.RuleName)
			if err != nil {
				return err
			}
			for _, branchName := range matchedBranches {
				if err = CheckPRsForBaseBranch(ctx, repo, branchName); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
