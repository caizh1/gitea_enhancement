// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"strings"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestPullRetargetIgnoresUnrelatedGovernanceWrites(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	previous := governance_model.PullVersion{PullID: 2, Head: "固定提交", BaseBranch: "master", PatchID: "原差异", Generation: 1}
	require.NoError(t, db.Insert(ctx, &previous))
	snapshot := &PullApprovalSnapshot{Version: governance_model.PullVersion{PullID: 2, Head: previous.Head, BaseBranch: "alternate", PatchID: "目标差异"}, Previous: &previous, ReferenceRevisions: map[int64]int64{1: 0}, ExpectedBaseBranch: "master", HeadBranch: "branch2", BaseRepoID: 1, HeadRepoID: 1}
	require.NoError(t, governance_model.WithWrite(ctx, nil, func(context.Context) error { return nil }))
	require.NoError(t, ApplyPullApprovalSnapshot(ctx, snapshot), "无关治理写入不能使固定的目标分支快照失效")
	current, _, err := db.GetByID[governance_model.PullVersion](ctx, 2)
	require.NoError(t, err)
	snapshot.Previous = current
	operation := &governance_model.ReferenceTransaction{RepoID: 1, Actor: governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "git_http"}, Changes: []governance_model.ReferenceChange{{Ref: "refs/heads/alternate", Old: strings.Repeat("1", 40), New: strings.Repeat("2", 40)}}}
	require.NoError(t, governance_model.PrepareReferenceTransaction(ctx, operation, nil, func(context.Context) error { return nil }))
	_, err = governance_model.ReconcileReferenceTransaction(ctx, operation.ID, map[string]string{"refs/heads/alternate": operation.Changes[0].Old}, nil)
	require.NoError(t, err)
	revision, err := governance_model.ReadReferenceRevision(ctx, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, revision, "准备和恢复各推进一次，即使最终引用未变也不复用旧快照")
	require.ErrorIs(t, ApplyPullApprovalSnapshot(ctx, snapshot), governance_model.ErrConflict)
}
