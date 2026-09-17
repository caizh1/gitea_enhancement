// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"slices"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/gitrepo"
)

type PullApprovalResult struct {
	FullPath                  string                                      `json:"full_path"`
	VersionReady              bool                                        `json:"version_ready"`
	EnforcementReady          bool                                        `json:"enforcement_ready"`
	MergeEnforcementReady     bool                                        `json:"merge_enforcement_ready"`
	ReferenceEnforcementReady bool                                        `json:"reference_enforcement_ready"`
	Reason                    string                                      `json:"reason,omitempty"`
	RuleRevision              int64                                       `json:"rule_revision"`
	State                     governance_model.ApprovalState              `json:"state"`
	EligibleRuleIDs           []int64                                     `json:"eligible_rule_ids"`
	Settings                  *governance_model.EffectiveApprovalSettings `json:"settings"`
	requestUserIDs            []int64
}

// ReadPullApprovalState 只查询当前可核对证据；最终合并仍须在授权事务重新判定。
func ReadPullApprovalState(ctx context.Context, actorID, pullID int64) (*PullApprovalResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pr, err := issues_model.GetPullRequestByID(ctx, pullID)
	if issues_model.IsErrPullRequestNotExist(err) {
		return nil, governance_model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	repo, _, err := checkApprovalRuleAccess(ctx, actorID, pr.BaseRepoID, false)
	if err != nil {
		return nil, err
	}
	headRepo, err := repo_model.GetRepositoryByID(ctx, pr.HeadRepoID)
	if err != nil {
		return nil, err
	}
	baseInstalled, err := gitrepo.ReferenceTransactionHookInstalled(repo)
	if err != nil {
		return nil, err
	}
	headInstalled, err := gitrepo.ReferenceTransactionHookInstalled(headRepo)
	if err != nil {
		return nil, err
	}
	result := &PullApprovalResult{FullPath: repo.FullPath(), ReferenceEnforcementReady: baseInstalled && headInstalled, EligibleRuleIDs: []int64{}, State: governance_model.ApprovalState{Rules: []governance_model.RuleResult{}}}
	if !baseInstalled {
		result.Reason = "目标项目 " + repo.FullPath() + " 尚未安装引用事务入口，请由该项目审批管理员从审批配置页完成接入"
	} else if !headInstalled {
		result.Reason = "源项目 " + headRepo.FullPath() + " 尚未安装引用事务入口，请由该源项目的审批管理员完成接入"
	}
	snapshot, err := CapturePullApprovalSnapshot(ctx, pr, pr.BaseBranch)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		if result.Reason == "" {
			result.Reason = "审批差异版本尚未接入"
		}
		return result, nil
	}
	if snapshot.Previous.BaseBranch != pr.BaseBranch || snapshot.Previous.PatchID != snapshot.Version.PatchID {
		return nil, fmt.Errorf("%w：当前差异与已记录的审批版本不一致", governance_model.ErrConflict)
	}

	emails, err := gitrepo.ApprovalCommitterEmails(ctx, repo, headRepo, snapshot.BaseHead, snapshot.Version.Head)
	if err != nil {
		return nil, err
	}
	err = governance_model.WithStableRead(ctx, func(ctx context.Context) error {
		current, has, err := db.GetByID[governance_model.PullVersion](ctx, pullID)
		if err != nil {
			return err
		}
		if !has || *current != *snapshot.Previous {
			return fmt.Errorf("%w：当前 PR 的审批版本已变化", governance_model.ErrConflict)
		}
		fresh, err := issues_model.GetPullRequestByID(ctx, pullID)
		if err != nil {
			return err
		}
		if fresh.BaseRepoID != pr.BaseRepoID || fresh.HeadRepoID != pr.HeadRepoID || fresh.BaseBranch != pr.BaseBranch || fresh.HeadBranch != pr.HeadBranch {
			return governance_model.ErrConflict
		}
		pending, err := db.GetEngine(ctx).In("resource", []string{governance_model.Resource("pull", pullID), governance_model.Resource("repository", pr.BaseRepoID), governance_model.Resource("repository", pr.HeadRepoID)}).Exist(new(governance_model.ReferenceReservation))
		if err != nil {
			return err
		}
		if pending {
			result.Reason = "代码写入结果尚待核对"
			return nil
		}
		return evaluatePullApprovalState(ctx, actorID, pr, repo, *snapshot.Previous, emails, result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// evaluatePullApprovalState 在调用者持有治理锁时只查询数据库，查询页面和最终授权共用计票。
func evaluatePullApprovalState(ctx context.Context, actorID int64, pr *issues_model.PullRequest, repo *repo_model.Repository, version governance_model.PullVersion, emails []string, result *PullApprovalResult) error {
	configuration, err := ListRepositoryApprovalRules(ctx, actorID, repo.ID)
	if err != nil {
		return err
	}
	result.Settings = configuration.Settings
	result.FullPath = configuration.FullPath
	rules := make([]governance_model.ApprovalRule, 0, len(configuration.Rules))
	for _, rule := range configuration.Rules {
		if rule.ScopeType != "repository" || result.Settings.Settings.PreventOverrides || rule.NativeProtectionID > 0 {
			rules = append(rules, *rule)
		}
	}
	if !result.Settings.Settings.PreventOverrides {
		saved, has, err := governance_model.LatestPullRuleVersion(ctx, pr.ID)
		if err != nil {
			return err
		}
		if !has {
			result.Reason = "此 PR 的独立审批规则尚未初始化"
			return nil
		}
		result.RuleRevision = saved.Revision
		for _, rule := range saved.Rules {
			if rule.NativeProtectionID == 0 {
				rules = append(rules, rule)
			}
		}
	}
	var committers []int64
	if result.Settings.Settings.PreventCommitter {
		committers, err = ApprovalCommitterIDs(ctx, emails)
		if err != nil {
			return err
		}
	}
	if err := pr.LoadIssue(ctx); err != nil {
		return err
	}
	candidates, err := ApplicableApprovalCandidates(ctx, repo, pr.BaseBranch, pr.Issue.PosterID, committers, result.Settings.Settings, rules)
	if err != nil {
		return err
	}
	for i := range candidates {
		if candidates[i].Rule.NativeProtectionID == 0 {
			continue
		}
		var reviews []issues_model.Review
		query := db.GetEngine(ctx).Where("issue_id = ? AND type = ? AND official = ? AND dismissed = ?", pr.IssueID, issues_model.ReviewTypeApprove, true, false)
		if candidates[i].Rule.NativeIgnoreStale {
			query = query.And("stale = ?", false)
		}
		if err := query.Find(&reviews); err != nil {
			return err
		}
		for _, review := range reviews {
			candidates[i].NativeApprovedIDs = append(candidates[i].NativeApprovedIDs, review.ReviewerID)
		}
	}
	evidence, err := issues_model.FindGovernanceApprovalEvidence(ctx, pr.ID)
	if err != nil {
		return err
	}
	result.State, err = governance_model.CountApprovalRules(version, candidates, evidence, result.Settings.Settings.RequireReauthentication)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if candidate.Rule.Enabled && candidate.Rule.Required > 0 {
			result.requestUserIDs = append(result.requestUserIDs, candidate.UserIDs...)
		}
		if slices.Contains(candidate.UserIDs, actorID) {
			result.EligibleRuleIDs = append(result.EligibleRuleIDs, candidate.Rule.ID)
		}
	}

	result.VersionReady = true
	result.MergeEnforcementReady = result.ReferenceEnforcementReady
	slices.Sort(result.requestUserIDs)
	result.requestUserIDs = slices.Compact(result.requestUserIDs)
	return nil
}

// PullApprovalRequestUserIDs 返回当前必需规则的合格审批人，用于创建非阻断性的原生评审待办。
func PullApprovalRequestUserIDs(ctx context.Context, actorID, pullID int64) ([]int64, error) {
	pr, err := issues_model.GetPullRequestByID(ctx, pullID)
	if err != nil {
		return nil, err
	}
	snapshot, err := CaptureInitialPullApprovalSnapshot(ctx, pr)
	if err != nil || snapshot == nil {
		return nil, err
	}
	if err := ApplyPullApprovalSnapshot(ctx, snapshot); err != nil {
		return nil, err
	}
	result, err := ReadPullApprovalState(ctx, actorID, pullID)
	if err != nil {
		return nil, err
	}
	return slices.Clone(result.requestUserIDs), nil
}

// ApprovalCommitterIDs 与计票共用当前邮箱归属；不使用提交作者或推送者替代提交者。
func ApprovalCommitterIDs(ctx context.Context, emails []string) ([]int64, error) {
	var ids []int64
	for _, email := range emails {
		user, err := user_model.GetUserByEmail(ctx, email)
		if user_model.IsErrUserNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !slices.Contains(ids, user.ID) {
			ids = append(ids, user.ID)
		}
	}
	return ids, nil
}

func PullSnapshotCommitterEmails(ctx context.Context, snapshot *PullApprovalSnapshot) ([]string, error) {
	if snapshot == nil {
		return nil, governance_model.ErrConflict
	}
	base, err := repo_model.GetRepositoryByID(ctx, snapshot.BaseRepoID)
	if err != nil {
		return nil, err
	}
	head, err := repo_model.GetRepositoryByID(ctx, snapshot.HeadRepoID)
	if err != nil {
		return nil, err
	}
	return gitrepo.ApprovalCommitterEmails(ctx, base, head, snapshot.BaseHead, snapshot.Version.Head)
}
