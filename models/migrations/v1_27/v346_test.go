// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"testing"

	"gitea.dev/models/migrations/migrationtest"

	"github.com/stretchr/testify/require"
)

type legacyActionsSecret struct {
	ID int64 `xorm:"pk autoincr"`
}

func (legacyActionsSecret) TableName() string { return "secret" }

func TestAddProtectedActionsSecrets(t *testing.T) {
	x, deferable := migrationtest.PrepareTestEnv(t, 0, new(legacyActionsSecret))
	defer deferable()
	if x == nil || t.Failed() {
		return
	}
	_, err := x.Insert(&legacyActionsSecret{ID: 1})
	require.NoError(t, err)
	require.NoError(t, AddProtectedActionsSecrets(x))
	require.NoError(t, AddProtectedActionsSecrets(x))
	var protected bool
	has, err := x.SQL("SELECT protected FROM secret WHERE id = ?", 1).Get(&protected)
	require.NoError(t, err)
	require.True(t, has)
	require.False(t, protected)
}
