// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import "gitea.dev/models/db"

// AccessRequest 只保存待处理申请；完成后的证据保存在不可变审计中。
type AccessRequest struct {
	CurrentUsername string `xorm:"-" json:"current_username,omitempty"`
	UserState       string `xorm:"-" json:"user_state,omitempty"`
	ID              int64  `xorm:"pk autoincr" json:"id"`
	ScopeType       string `xorm:"VARCHAR(16) UNIQUE(scope_user) NOT NULL" json:"scope_type"`
	ScopeID         int64  `xorm:"UNIQUE(scope_user) NOT NULL" json:"scope_id"`
	UserID          int64  `xorm:"UNIQUE(scope_user) INDEX NOT NULL" json:"user_id"`
	Username        string `xorm:"VARCHAR(255) NOT NULL" json:"username"`
	ScopePath       string `xorm:"VARCHAR(2048) NOT NULL" json:"scope_path"`
	CreatedUnix     int64  `xorm:"INDEX NOT NULL" json:"created_unix"`
}

func (*AccessRequest) TableName() string { return "governance_access_request" }

type AccessRequestSetting struct {
	ScopeType string `xorm:"VARCHAR(16) pk" json:"scope_type"`
	ScopeID   int64  `xorm:"pk" json:"scope_id"`
	Disabled  bool   `xorm:"NOT NULL DEFAULT false" json:"disabled"`
	Revision  int64  `xorm:"NOT NULL DEFAULT 0" json:"revision"`
}

func (*AccessRequestSetting) TableName() string { return "governance_access_request_setting" }

func AddAccessRequests(engine db.EngineMigration) error {
	return engine.Sync(new(AccessRequest), new(AccessRequestSetting))
}
