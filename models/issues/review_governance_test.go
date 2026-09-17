// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package issues_test

import (
	"context"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewMutationsRespectFinalMergeReservation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 1})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: review.IssueID})
	pr, err := issues_model.GetPullRequestByIssueIDWithNoAttributes(ctx, issue.ID)
	require.NoError(t, err)
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: review.ReviewerID})
	operation := &governance_model.MergeAuthorization{PullID: pr.ID, RepoID: pr.BaseRepoID, Branch: pr.BaseBranch, Head: "head", OldTarget: "before", NewTarget: "after", Actor: governance_model.Actor{ID: user.ID, Name: user.Name, Kind: "user", Transport: "api"}}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, nil, func(context.Context) error { return nil }))
	assert.ErrorIs(t, issues_model.DismissReview(ctx, review, true), governance_model.ErrConflict)
	assert.False(t, review.Dismissed, "失败不能修改调用者手中的评审状态")
	assert.ErrorIs(t, issues_model.DeleteReview(ctx, review), governance_model.ErrConflict)
	assert.ErrorIs(t, issues_model.MarkReviewsAsStale(ctx, issue.ID), governance_model.ErrConflict)
	_, err = issues_model.CreateReview(ctx, issues_model.CreateReviewOptions{Issue: issue, Reviewer: user, Type: issues_model.ReviewTypeApprove})
	assert.ErrorIs(t, err, governance_model.ErrConflict)
	_, _, err = issues_model.SubmitReview(ctx, user, issue, issues_model.ReviewTypeReject, "修改请求", "head", false, nil)
	assert.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = issues_model.AddReviewRequest(ctx, issue, user, user, false)
	assert.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = issues_model.RemoveReviewRequest(ctx, issue, user, user)
	assert.ErrorIs(t, err, governance_model.ErrConflict)
	current := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: review.ID})
	assert.False(t, current.Dismissed)
	assert.False(t, current.Stale)
	comment, err := issues_model.CreateReview(ctx, issues_model.CreateReviewOptions{Issue: issue, Reviewer: user, Type: issues_model.ReviewTypeComment, Content: "合并期间继续讨论"})
	require.NoError(t, err)
	require.NoError(t, issues_model.DeleteReview(ctx, comment))
	state, err := governance_model.ReconcileMerge(ctx, operation.ID, "before")
	require.NoError(t, err)
	assert.Equal(t, "failed", state)
	require.NoError(t, issues_model.DismissReview(ctx, review, true))
	assert.True(t, review.Dismissed)
}

func TestReviewGovernanceEvidenceFollowsNativeReviewAndGeneration(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 1})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: review.IssueID})
	pr, err := issues_model.GetPullRequestByIssueIDWithNoAttributes(ctx, issue.ID)
	require.NoError(t, err)
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: review.ReviewerID})
	version, err := governance_model.AdvancePullVersion(ctx, governance_model.PullVersion{PullID: pr.ID, Head: "提交甲", BaseBranch: pr.BaseBranch, PatchID: "差异甲"}, "", true)
	require.NoError(t, err)
	read := func() []governance_model.ApprovalEvidence {
		t.Helper()
		evidence, err := issues_model.FindGovernanceApprovalEvidence(ctx, pr.ID)
		require.NoError(t, err)
		return evidence
	}
	require.Empty(t, read(), "不能把已有原生批准自动导入为有效票")
	opts := issues_model.CreateReviewOptions{Issue: issue, Reviewer: user, Type: issues_model.ReviewTypeApprove, CommitID: "提交甲"}
	first, err := issues_model.CreateReview(ctx, opts)
	require.NoError(t, err)
	evidence := read()
	require.Len(t, evidence, 1)
	require.Equal(t, version.Generation, evidence[0].Generation)
	require.Positive(t, evidence[0].RuleVersionID, "批准保留当时的项目规则快照依据")
	require.False(t, evidence[0].Reauthenticated, "普通批准不能虚构再次认证")
	opts.CommitID = "过期提交"
	_, err = issues_model.CreateReview(ctx, opts)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	require.Len(t, read(), 1, "过期批准失败事务不能撤销原有效票")
	require.NoError(t, issues_model.DismissReview(ctx, first, true))
	require.Empty(t, read())
	opts.CommitID = "提交甲"
	second, err := issues_model.CreateReview(ctx, opts)
	require.NoError(t, err)
	require.Len(t, read(), 1)
	next := *version
	next.Head, next.PatchID = "提交乙", "差异乙"
	version, err = governance_model.AdvancePullVersion(ctx, next, "提交甲", true)
	require.NoError(t, err)
	require.NotEqual(t, version.Generation, read()[0].Generation, "旧证据保留历史代次，不能随当前版本更新")
	opts.Type = issues_model.ReviewTypeReject
	_, err = issues_model.CreateReview(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, read(), "请求修改必须清除该用户可计票的批准")
	require.NoError(t, issues_model.DeleteReview(ctx, second))
	require.Empty(t, read(), "删除后不能使更早批准重新出现")
}

func TestStaleReviewDeleteCannotBypassMergeReservation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	current := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 1})
	pr, err := issues_model.GetPullRequestByIssueIDWithNoAttributes(ctx, current.IssueID)
	require.NoError(t, err)
	stale := *current
	stale.Type = issues_model.ReviewTypePending
	operation := &governance_model.MergeAuthorization{PullID: pr.ID, RepoID: pr.BaseRepoID, Head: "甲", OldTarget: "旧", NewTarget: "新", Branch: pr.BaseBranch, Actor: governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, nil, func(context.Context) error { return nil }))
	require.ErrorIs(t, issues_model.DeleteReview(ctx, &stale), governance_model.ErrConflict, "待定评审在读取后提交为批准，删除入口仍必须遵守最终授权占用")
	unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: current.ID})
}

func TestReviewAuditAtomicityAndRealActor(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	original := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 1})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: original.IssueID})
	reviewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: original.ReviewerID})
	actor := governance_model.Actor{ID: 1, Name: "管理操作者", Kind: "user", Transport: "api", IP: "192.0.2.7", RequestID: "评审审计事务验收"}
	ctx = governance_model.WithAuditActor(ctx, actor)
	opts := issues_model.CreateReviewOptions{Issue: issue, Reviewer: reviewer, Type: issues_model.ReviewTypeApprove, CommitID: "固定提交", Content: "不能进入审计的评论正文"}
	invalid := actor
	invalid.Transport = ""
	_, err := issues_model.CreateReview(governance_model.WithAuditActor(ctx, invalid), opts)
	require.ErrorIs(t, err, governance_model.ErrInvalid)
	current := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: original.ID})
	require.Equal(t, original.Dismissed, current.Dismissed, "审计失败必须回滚旧批准撤销")
	review, err := issues_model.CreateReview(ctx, opts)
	require.NoError(t, err)
	require.NoError(t, issues_model.DismissReview(ctx, review, true))
	require.NoError(t, issues_model.DismissReview(ctx, review, true))
	var events []governance_model.AuditEvent
	require.NoError(t, db.GetEngine(ctx).Where("object_type = ? AND object_id = ?", "review", review.ID).OrderBy("id").Find(&events))
	require.Len(t, events, 2, "重复撤回不重复产生已完成变更")
	require.Equal(t, "approval.approved", events[0].Type)
	require.Equal(t, "approval.withdrawn", events[1].Type)
	for _, event := range events {
		require.Equal(t, actor, event.Actor)
		require.Equal(t, issue.RepoID, event.ScopeID)
		require.NotContains(t, string(event.Details), opts.Content)
	}
	require.NoError(t, issues_model.DeleteReview(ctx, review))
	var deleted governance_model.AuditEvent
	found, err := db.GetEngine(ctx).Where("object_type = ? AND object_id = ? AND type = ?", "review", review.ID, "review.deleted").Get(&deleted)
	require.NoError(t, err)
	require.True(t, found, "删除原生评审后审计仍永久保留")
}
