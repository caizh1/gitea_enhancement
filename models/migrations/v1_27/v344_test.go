// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"testing"

	"gitea.dev/models/migrations/migrationtest"

	"github.com/stretchr/testify/require"
)

func TestAddTrustedScopedWorkflowRevisions(t *testing.T) {
	x, cleanup := migrationtest.PrepareTestEnv(t, 0, new(legacyScopeActionRun), new(legacyScopeScopedSource))
	defer cleanup()
	if x == nil || t.Failed() {
		return
	}
	_, err := x.Insert(&legacyScopeActionRun{ID: 1})
	require.NoError(t, err)
	_, err = x.Insert(&legacyScopeScopedSource{ID: 1})
	require.NoError(t, err)
	require.NoError(t, AddTrustedScopedWorkflowRevisions(x))
	var revision int64
	has, err := x.SQL("SELECT config_revision FROM action_scoped_workflow_source WHERE id = ?", 1).Get(&revision)
	require.NoError(t, err)
	require.True(t, has)
	require.EqualValues(t, 1, revision)
}
