// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"

	"xorm.io/xorm"
)

func AddActionsScopeSafety(engine db.EngineMigration) error {
	type ActionRun struct {
		ScopeInvalidated            bool  `xorm:"NOT NULL DEFAULT false"`
		WorkflowSourceScopeRevision int64 `xorm:"NOT NULL DEFAULT 0"`
	}
	type Repository struct {
		ActionsScopeRevision int64 `xorm:"NOT NULL DEFAULT 0"`
	}
	type ActionSchedule struct {
		ScopeRevision int64 `xorm:"NOT NULL DEFAULT 0"`
	}
	type ActionScopedWorkflowSource struct {
		SourceScopeRevision int64 `xorm:"NOT NULL DEFAULT 0"`
	}
	_, err := engine.SyncWithOptions(xorm.SyncOptions{
		IgnoreDropIndices: true,
		IgnoreConstrains:  true,
	}, new(ActionRun), new(Repository), new(ActionSchedule), new(ActionScopedWorkflowSource))
	return err
}
