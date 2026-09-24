// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"testing"

	"gitea.dev/models/migrations/migrationtest"

	"github.com/stretchr/testify/require"
)

type legacyHookEvent struct {
	ID   int64 `xorm:"pk autoincr"`
	UUID string
}

func (legacyHookEvent) TableName() string { return "hook_task" }

func TestAddWebhookEventID(t *testing.T) {
	x, deferable := migrationtest.PrepareTestEnv(t, 0, new(legacyHookEvent))
	defer deferable()
	if x == nil || t.Failed() {
		return
	}
	_, err := x.Insert(&legacyHookEvent{ID: 1, UUID: "original-delivery"})
	require.NoError(t, err)
	require.NoError(t, AddWebhookEventID(x))
	_, err = x.Exec("UPDATE hook_task SET event_uuid = ? WHERE id = ?", "stable-event", 1)
	require.NoError(t, err)
	require.NoError(t, AddWebhookEventID(x))
	var event string
	has, err := x.SQL("SELECT event_uuid FROM hook_task WHERE id = ?", 1).Get(&event)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, "stable-event", event)
}
