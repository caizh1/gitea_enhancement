// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package issues

import (
	"context"
	"errors"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
)

// lockReviewGovernance 必须在评审变更的数据库事务内调用，锁保持到外层事务结束。
// 最终合并已经取得授权时，批准、撤回和失效均返回冲突，不能虚假报告撤回成功。
func lockReviewGovernance(ctx context.Context, issueID int64) error {
	pr, err := GetPullRequestByIssueIDWithNoAttributes(ctx, issueID)
	if IsErrPullRequestNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("pull", pr.ID)}, func(context.Context) error { return nil })
}

// recordReviewGovernance 只给本次真实批准绑定代次；历史批准不会在启用或重算时补票。
func recordReviewGovernance(ctx context.Context, review *Review) error {
	if review.Type != ReviewTypeApprove && review.Type != ReviewTypeReject {
		return nil
	}
	if err := recordReviewEvidence(ctx, review); err != nil {
		return err
	}
	event := "approval.approved"
	if review.Type == ReviewTypeReject {
		event = "review.changes_requested"
	}
	return appendReviewAudit(ctx, review, event, "submitted")
}

func recordReviewEvidence(ctx context.Context, review *Review) error {
	if review.Type != ReviewTypeApprove && review.Type != ReviewTypeReject {
		return nil
	}
	pr, err := GetPullRequestByIssueIDWithNoAttributes(ctx, review.IssueID)
	if err != nil {
		return err
	}
	version, has, err := db.GetByID[governance_model.PullVersion](ctx, pr.ID)
	if err != nil || !has {
		return err
	}
	if review.Type == ReviewTypeApprove && (review.CommitID != version.Head || review.Stale) {
		return governance_model.ErrConflict
	}
	if _, err := db.GetEngine(ctx).Where("pull_id = ? AND user_id = ?", pr.ID, review.ReviewerID).Delete(new(governance_model.ApprovalEvidence)); err != nil {
		return err
	}
	if review.Type != ReviewTypeApprove {
		return nil
	}
	rules, err := governance_model.SnapshotProjectPullRules(ctx, pr.ID, pr.BaseRepoID, false)
	if err != nil {
		return err
	}
	return db.Insert(ctx, &governance_model.ApprovalEvidence{ReviewID: review.ID, RuleVersionID: rules.ID, PullID: pr.ID, UserID: review.ReviewerID, Generation: version.Generation, Head: version.Head, Reauthenticated: governance_model.IsApprovalReauthenticated(ctx)})
}

// FindGovernanceApprovalEvidence 同时核对原生撤回状态，不能读取独立且过期的批准副本。
func FindGovernanceApprovalEvidence(ctx context.Context, pullID int64) ([]governance_model.ApprovalEvidence, error) {
	evidence := make([]governance_model.ApprovalEvidence, 0)
	err := db.GetEngine(ctx).Table(new(governance_model.ApprovalEvidence)).
		Join("INNER", "review", "review.id = governance_approval_evidence.review_id").
		Where("governance_approval_evidence.pull_id = ? AND review.type = ? AND review.dismissed = ?", pullID, ReviewTypeApprove, false).
		Select("governance_approval_evidence.*").Find(&evidence)
	return evidence, err
}

// appendReviewAudit 与原生评审共用事务，不记录评论正文和再次认证口令。
func appendReviewAudit(ctx context.Context, review *Review, event, action string) error {
	issue, err := GetIssueByID(ctx, review.IssueID)
	if err != nil {
		return err
	}
	if err = issue.LoadRepo(ctx); err != nil {
		return err
	}
	chain, err := governance_model.Ancestors(ctx, issue.Repo.OwnerID)
	var ancestors []int64
	if errors.Is(err, governance_model.ErrNotFound) {
		// 原生旧数据尚无层级记录时，只保留其真实单层组织归属。
		if err = issue.Repo.LoadOwner(ctx); err != nil {
			return err
		}
		if issue.Repo.Owner.IsOrganization() {
			ancestors = append(ancestors, issue.Repo.OwnerID)
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
	values := map[string]any{"review_id": review.ID, "issue_id": review.IssueID, "reviewer_id": review.ReviewerID, "commit": review.CommitID, "review_type": review.Type, "action": action, "stale": review.Stale, "dismissed": review.Dismissed}
	evidence, has, err := db.GetByID[governance_model.ApprovalEvidence](ctx, review.ID)
	if err != nil {
		return err
	}
	if has {
		values["approval_generation"] = evidence.Generation
		values["rule_version_id"] = evidence.RuleVersionID
		values["reauthenticated"] = evidence.Reauthenticated
	}
	details, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: event, Actor: governance_model.AuditActor(ctx), ScopeType: "repository", ScopeID: issue.RepoID, AncestorIDs: ancestors, ObjectType: "review", ObjectID: review.ID, ObjectPath: issue.Repo.FullPath(), Result: "success", Details: details})
}
