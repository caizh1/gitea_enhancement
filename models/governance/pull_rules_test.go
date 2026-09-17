// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/db"

	"github.com/stretchr/testify/require"
)

func TestPullRuleSnapshotsPreservePriorVersions(t *testing.T) {
	ctx := testDatabase(t)
	rule := &ApprovalRule{ScopeType: "repository", ScopeID: 12, Name: "指定人", Required: 1, UserIDs: []int64{3}, Enabled: true, BranchMode: "all", Revision: 1}
	require.NoError(t, db.Insert(ctx, rule))
	initial, err := SnapshotProjectPullRules(ctx, 9, 12, false)
	require.NoError(t, err)
	require.Len(t, initial.Rules, 1)
	require.EqualValues(t, 1, initial.Revision)
	rule.Required, rule.Revision = 2, 2
	_, err = db.GetEngine(ctx).ID(rule.ID).AllCols().Update(rule)
	require.NoError(t, err)
	kept, err := SnapshotProjectPullRules(ctx, 9, 12, false)
	require.NoError(t, err)
	require.Equal(t, initial.ID, kept.ID)
	require.Equal(t, 1, kept.Rules[0].Required)
	updated, err := SnapshotProjectPullRules(ctx, 9, 12, true)
	require.NoError(t, err)
	require.EqualValues(t, 2, updated.Revision)
	require.Equal(t, 2, updated.Rules[0].Required)
	original, has, err := db.GetByID[PullRuleVersion](ctx, initial.ID)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, 1, original.Rules[0].Required, "原始规则依据不可被模板更新覆盖")
	latest, has, err := LatestPullRuleVersion(ctx, 9)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, updated.ID, latest.ID)
}
