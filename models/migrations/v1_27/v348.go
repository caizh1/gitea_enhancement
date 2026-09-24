// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import "gitea.dev/models/db"

func AddWebhookEventID(engine db.EngineMigration) error {
	type HookTask struct {
		EventUUID string
	}
	if err := engine.Sync(new(HookTask)); err != nil {
		return err
	}
	_, err := engine.Exec("UPDATE hook_task SET event_uuid = uuid WHERE event_uuid IS NULL OR event_uuid = ?", "")
	return err
}
