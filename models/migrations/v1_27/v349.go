// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"

	"xorm.io/xorm"
)

func AddScopedScheduleProvenance(engine db.EngineMigration) error {
	type ActionSchedule struct {
		WorkflowRepoID              int64            `xorm:"NOT NULL DEFAULT 0"`
		WorkflowCommitSHA           string           `xorm:"VARCHAR(64) NOT NULL DEFAULT ''"`
		WorkflowSourceScopeRevision int64            `xorm:"NOT NULL DEFAULT 0"`
		ScopedConfigRevisions       map[string]int64 `xorm:"JSON TEXT"`
		IsScopedRun                 bool             `xorm:"NOT NULL DEFAULT false"`
	}
	_, err := engine.SyncWithOptions(xorm.SyncOptions{IgnoreDropIndices: true, IgnoreConstrains: true}, new(ActionSchedule))
	return err
}
