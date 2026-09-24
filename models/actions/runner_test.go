// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAncestorRunnerScopeAndNotification(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := db.GetEngine(ctx).ID(6).Cols("parent_id", "full_path", "lower_path").Update(&governance_model.Namespace{
		ParentID: 3, FullPath: "org3/org6", LowerPath: "org3/org6",
	})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(1).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 6})
	require.NoError(t, err)

	parent := &ActionRunner{OwnerID: 3}
	allowed, err := runnerCanUseRepo(ctx, parent, 1, 6)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = runnerCanUseRepo(ctx, &ActionRunner{OwnerID: 7}, 1, 6)
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, err = runnerCanUseRepo(ctx, &ActionRunner{RepoID: 2}, 1, 6)
	require.NoError(t, err)
	require.False(t, allowed)

	ids, err := AvailableRunnerOwnerIDs(ctx, 6)
	require.NoError(t, err)
	require.Equal(t, []int64{6, 3}, ids)
	require.NoError(t, IncreaseTaskVersion(ctx, 6, 1))
	for _, scope := range [][2]int64{{0, 0}, {6, 0}, {3, 0}, {0, 1}} {
		version, err := GetTasksVersionByScope(ctx, scope[0], scope[1])
		require.NoError(t, err)
		require.EqualValues(t, 1, version)
	}
	version, err := GetTasksVersionByScope(ctx, 7, 0)
	require.NoError(t, err)
	require.Zero(t, version)
}

func TestVariablesPreferNearestNamespaceAndRepo(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := db.GetEngine(ctx).ID(6).Cols("parent_id", "full_path", "lower_path").Update(&governance_model.Namespace{
		ParentID: 3, FullPath: "org3/org6", LowerPath: "org3/org6",
	})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(1).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 6})
	require.NoError(t, err)
	for _, entry := range []struct {
		ownerID, repoID int64
		name, value     string
	}{
		{0, 0, "CI_LEVEL", "global"},
		{3, 0, "CI_LEVEL", "parent"},
		{6, 0, "CI_LEVEL", "child"},
		{0, 1, "CI_LEVEL", "repo"},
		{3, 0, "CHILD_WINS", "parent"},
		{6, 0, "CHILD_WINS", "child"},
		{3, 0, "PARENT_ONLY", "inherited"},
		{7, 0, "UNRELATED", "excluded"},
	} {
		_, err := InsertVariable(ctx, entry.ownerID, entry.repoID, entry.name, entry.value, "")
		require.NoError(t, err)
	}
	vars, err := GetVariablesOfRun(ctx, &ActionRun{RepoID: 1})
	require.NoError(t, err)
	require.Equal(t, "repo", vars["CI_LEVEL"])
	require.Equal(t, "child", vars["CHILD_WINS"])
	require.Equal(t, "inherited", vars["PARENT_ONLY"])
	require.NotContains(t, vars, "UNRELATED")
}

func TestShouldPersistLastOnline(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		last timeutil.TimeStamp
		want bool
	}{
		{
			name: "fresh, skip write",
			last: timeutil.TimeStamp(now.Add(-5 * time.Second).Unix()),
			want: false,
		},
		{
			name: "exactly at interval, write",
			last: timeutil.TimeStamp(now.Add(-RunnerHeartbeatInterval).Unix()),
			want: true,
		},
		{
			name: "stale, write",
			last: timeutil.TimeStamp(now.Add(-2 * RunnerHeartbeatInterval).Unix()),
			want: true,
		},
		{
			name: "zero (never seen), write",
			last: 0,
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ShouldPersistLastOnline(tt.last, now))
		})
	}
}

func TestShouldPersistLastActive(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		last timeutil.TimeStamp
		want bool
	}{
		{
			name: "fresh, skip write",
			last: timeutil.TimeStamp(now.Add(-1 * time.Second).Unix()),
			want: false,
		},
		{
			name: "exactly at interval, write",
			last: timeutil.TimeStamp(now.Add(-RunnerActiveInterval).Unix()),
			want: true,
		},
		{
			name: "stale, write",
			last: timeutil.TimeStamp(now.Add(-2 * RunnerActiveInterval).Unix()),
			want: true,
		},
		{
			name: "zero (never seen), write",
			last: 0,
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ShouldPersistLastActive(tt.last, now))
		})
	}
}
