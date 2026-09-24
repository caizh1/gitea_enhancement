// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"

	"xorm.io/xorm"
)

// AddProtectedActionsSecrets keeps existing secrets unrestricted unless explicitly protected.
func AddProtectedActionsSecrets(engine db.EngineMigration) error {
	type Secret struct {
		Protected bool `xorm:"NOT NULL DEFAULT false"`
	}
	_, err := engine.SyncWithOptions(xorm.SyncOptions{IgnoreDropIndices: true, IgnoreConstrains: true}, new(Secret))
	return err
}
