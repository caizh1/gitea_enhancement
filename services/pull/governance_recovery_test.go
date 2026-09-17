// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package pull

import (
	"context"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestAuthorizedMergeRecoversNativeStateOnce(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	operation := &governance_model.MergeAuthorization{RepoID: 1, PullID: 2, Branch: "master", Head: "已批准提交", OldTarget: "旧目标", NewTarget: "已核对合并提交", Actor: governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, nil, func(context.Context) error { return nil }))
	require.ErrorIs(t, FinalizeAuthorizedMerge(ctx, operation.ID), governance_model.ErrConflict, "未核对引用不能关闭 PR")
	_, err := governance_model.ReconcileMerge(ctx, operation.ID, operation.NewTarget)
	require.NoError(t, err)
	require.NoError(t, FinalizeAuthorizedMerge(ctx, operation.ID))
	require.NoError(t, FinalizeAuthorizedMerge(ctx, operation.ID), "重复恢复不能重复关闭或创建审计")
	pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: 2})
	require.True(t, pr.HasMerged)
	require.Equal(t, operation.NewTarget, pr.MergedCommitID)
	require.EqualValues(t, 2, pr.MergerID)
	require.NoError(t, pr.LoadIssue(ctx))
	require.True(t, pr.Issue.IsClosed)
	count, err := db.GetEngine(ctx).Where("type = ?", "merge.native_state_recovered").Count(new(governance_model.AuditEvent))
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}
