// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"testing"

	"gitea.dev/models/migrations/migrationtest"

	"github.com/stretchr/testify/require"
)

func TestAddGroupProtectedBranches(t *testing.T) {
	x, deferable := migrationtest.PrepareTestEnv(t, 0)
	defer deferable()
	if x == nil || t.Failed() {
		return
	}
	require.NoError(t, AddGroupProtectedBranches(x))
	require.NoError(t, AddGroupProtectedBranches(x))
	_, err := x.Exec("INSERT INTO group_protected_branch (group_id, rule_name, push_role, merge_role, allow_force_push) VALUES (?, ?, ?, ?, ?)", 3, "main", 40, 30, false)
	require.NoError(t, err)
	_, err = x.Exec("INSERT INTO group_protected_branch (group_id, rule_name, push_role, merge_role, allow_force_push) VALUES (?, ?, ?, ?, ?)", 3, "main", 40, 30, false)
	require.Error(t, err)
}
