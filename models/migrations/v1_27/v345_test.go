// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"testing"

	"gitea.dev/models/migrations/migrationtest"

	"github.com/stretchr/testify/require"
)

type legacyWebhookSnapshotHook struct {
	ID int64 `xorm:"pk autoincr"`
}

func (legacyWebhookSnapshotHook) TableName() string { return "webhook" }

type legacyWebhookSnapshotTask struct {
	ID              int64 `xorm:"pk autoincr"`
	IsDelivered     bool
	Delivered       int64
	ResponseContent string `xorm:"LONGTEXT"`
}

func (legacyWebhookSnapshotTask) TableName() string { return "hook_task" }

func TestAddWebhookDeliverySnapshots(t *testing.T) {
	x, cleanup := migrationtest.PrepareTestEnv(t, 0, new(legacyWebhookSnapshotHook), new(legacyWebhookSnapshotTask))
	defer cleanup()
	if x == nil || t.Failed() {
		return
	}
	_, err := x.Insert(&legacyWebhookSnapshotHook{ID: 1})
	require.NoError(t, err)
	_, err = x.Insert(&legacyWebhookSnapshotTask{ID: 1})
	require.NoError(t, err)
	_, err = x.Insert(&legacyWebhookSnapshotTask{ID: 2, IsDelivered: true})
	require.NoError(t, err)
	require.NoError(t, AddWebhookDeliverySnapshots(x))
	var revision int64
	has, err := x.SQL("SELECT config_revision FROM webhook WHERE id = ?", 1).Get(&revision)
	require.NoError(t, err)
	require.True(t, has)
	require.EqualValues(t, 1, revision)
	var task legacyWebhookSnapshotTask
	has, err = x.ID(1).Get(&task)
	require.NoError(t, err)
	require.True(t, has)
	require.True(t, task.IsDelivered)
	require.Contains(t, task.ResponseContent, "canceled during webhook scope upgrade")
	task = legacyWebhookSnapshotTask{}
	has, err = x.ID(2).Get(&task)
	require.NoError(t, err)
	require.True(t, has)
	require.Empty(t, task.ResponseContent)
}
