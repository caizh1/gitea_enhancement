// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"

	"xorm.io/xorm"
)

func AddTrustedScopedWorkflowRevisions(engine db.EngineMigration) error {
	type ActionRun struct {
		ScopedConfigRevisions map[string]int64 `xorm:"JSON TEXT"`
	}
	type ActionScopedWorkflowSource struct {
		ConfigRevision int64 `xorm:"NOT NULL DEFAULT 1"`
	}
	_, err := engine.SyncWithOptions(xorm.SyncOptions{IgnoreDropIndices: true, IgnoreConstrains: true}, new(ActionRun), new(ActionScopedWorkflowSource))
	return err
}
