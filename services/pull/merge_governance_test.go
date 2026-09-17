// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package pull

import (
	"strings"
	"testing"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/commitstatus"

	"github.com/stretchr/testify/require"
)

func TestFinalMergeChecksBindExactCommitAndNativeProtection(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: 2})
	protection := &git_model.ProtectedBranch{RepoID: pr.BaseRepoID, RuleName: pr.BaseBranch, EnableStatusCheck: true, StatusCheckContexts: []string{"ci/test"}}
	require.NoError(t, db.Insert(ctx, protection))
	head, other := strings.Repeat("a", 40), strings.Repeat("b", 40)
	require.NoError(t, db.Insert(ctx, &git_model.CommitStatus{RepoID: pr.BaseRepoID, SHA: other, Context: "ci/test", ContextHash: git_model.HashCommitStatusContext("ci/test"), State: commitstatus.CommitStatusSuccess, Index: 1, CreatorID: 2}))
	require.ErrorIs(t, CheckPullBranchProtectionsAtHead(ctx, pr, head), ErrNotReadyToMerge, "其他提交的 CI 成功不能批准当前候选提交")
	require.NoError(t, db.Insert(ctx, &git_model.CommitStatus{RepoID: pr.BaseRepoID, SHA: head, Context: "ci/test", ContextHash: git_model.HashCommitStatusContext("ci/test"), State: commitstatus.CommitStatusSuccess, Index: 1, CreatorID: 2}))
	require.NoError(t, CheckPullBranchProtectionsAtHead(ctx, pr, head))
	require.NoError(t, db.Insert(ctx, &git_model.CommitStatus{RepoID: pr.BaseRepoID, SHA: head, Context: "ci/test", ContextHash: git_model.HashCommitStatusContext("ci/test"), State: commitstatus.CommitStatusFailure, Index: 2, CreatorID: 2}))
	require.ErrorIs(t, CheckPullBranchProtectionsAtHead(ctx, pr, head), ErrNotReadyToMerge, "最终授权读取同一提交的最新状态")
	_, err := db.GetEngine(ctx).ID(protection.ID).Cols("enable_status_check", "required_approvals").Update(&git_model.ProtectedBranch{EnableStatusCheck: false, RequiredApprovals: 100})
	require.NoError(t, err)
	require.ErrorIs(t, CheckPullBranchProtectionsAtHead(ctx, pr, head), ErrNotReadyToMerge, "治理批准不能替代原生必需评审")
}
