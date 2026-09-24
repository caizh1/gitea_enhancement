// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"
)

// AddGroupProtectedBranches stores top-level group rules without copying them into repositories.
func AddGroupProtectedBranches(engine db.EngineMigration) error {
	type GroupProtectedBranch struct {
		ID             int64  `xorm:"pk autoincr"`
		GroupID        int64  `xorm:"UNIQUE(group_rule) INDEX NOT NULL"`
		RuleName       string `xorm:"UNIQUE(group_rule) NOT NULL"`
		PushRole       int    `xorm:"NOT NULL DEFAULT 0"`
		MergeRole      int    `xorm:"NOT NULL DEFAULT 0"`
		AllowForcePush bool   `xorm:"NOT NULL DEFAULT false"`
	}
	return engine.Sync(new(GroupProtectedBranch))
}
