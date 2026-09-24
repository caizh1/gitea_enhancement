// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"testing"

	"gitea.dev/models/migrations/migrationtest"

	"github.com/stretchr/testify/require"
)

type legacyScopeActionRun struct {
	ID int64 `xorm:"pk autoincr"`
}

func (legacyScopeActionRun) TableName() string { return "action_run" }

type legacyScopeRepository struct {
	ID int64 `xorm:"pk autoincr"`
}

func (legacyScopeRepository) TableName() string { return "repository" }

type legacyScopeActionSchedule struct {
	ID int64 `xorm:"pk autoincr"`
}

func (legacyScopeActionSchedule) TableName() string { return "action_schedule" }

type legacyScopeScopedSource struct {
	ID int64 `xorm:"pk autoincr"`
}

func (legacyScopeScopedSource) TableName() string { return "action_scoped_workflow_source" }

func TestAddActionsScopeSafety(t *testing.T) {
	x, deferable := migrationtest.PrepareTestEnv(t, 0, new(legacyScopeActionRun), new(legacyScopeRepository), new(legacyScopeActionSchedule), new(legacyScopeScopedSource))
	defer deferable()
	if x == nil || t.Failed() {
		return
	}
	_, err := x.Insert(&legacyScopeActionRun{ID: 1})
	require.NoError(t, err)
	_, err = x.Insert(&legacyScopeRepository{ID: 1})
	require.NoError(t, err)
	_, err = x.Insert(&legacyScopeActionSchedule{ID: 1})
	require.NoError(t, err)
	_, err = x.Insert(&legacyScopeScopedSource{ID: 1})
	require.NoError(t, err)
	require.NoError(t, AddActionsScopeSafety(x))
	var invalidated bool
	has, err := x.SQL("SELECT scope_invalidated FROM action_run WHERE id = ?", 1).Get(&invalidated)
	require.NoError(t, err)
	require.True(t, has)
	require.False(t, invalidated)
	var revision int64
	has, err = x.SQL("SELECT workflow_source_scope_revision FROM action_run WHERE id = ?", 1).Get(&revision)
	require.NoError(t, err)
	require.True(t, has)
	require.Zero(t, revision)
	has, err = x.SQL("SELECT actions_scope_revision FROM repository WHERE id = ?", 1).Get(&revision)
	require.NoError(t, err)
	require.True(t, has)
	require.Zero(t, revision)
	has, err = x.SQL("SELECT scope_revision FROM action_schedule WHERE id = ?", 1).Get(&revision)
	require.NoError(t, err)
	require.True(t, has)
	require.Zero(t, revision)
	has, err = x.SQL("SELECT source_scope_revision FROM action_scoped_workflow_source WHERE id = ?", 1).Get(&revision)
	require.NoError(t, err)
	require.True(t, has)
	require.Zero(t, revision)
}
