// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"

	"xorm.io/xorm"
)

// AddWebhookDeliverySnapshots stores event scope and encrypted hook configuration at enqueue time.
func AddWebhookDeliverySnapshots(engine db.EngineMigration) error {
	type Webhook struct {
		ConfigRevision int64 `xorm:"NOT NULL DEFAULT 1"`
	}
	type HookTask struct {
		RepoID                int64 `xorm:"INDEX"`
		ScopeRevision         int64
		ScopeOwnerIDs         string `xorm:"TEXT"`
		HookRevision          int64
		HookSnapshotEncrypted string `xorm:"TEXT"`
	}
	if _, err := engine.SyncWithOptions(xorm.SyncOptions{IgnoreDropIndices: true, IgnoreConstrains: true}, new(Webhook), new(HookTask)); err != nil {
		return err
	}
	// Legacy pending rows have no event-time scope or configuration; sending them would be unsafe.
	_, err := engine.Exec("UPDATE hook_task SET is_delivered = ?, delivered = ?, response_content = ? WHERE is_delivered = ?", true, timeutil.TimeStampNanoNow(), `{"body":"canceled during webhook scope upgrade"}`, false)
	return err
}
