// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import "gitea.dev/models/db"

// CleanupObject 保存由服务端生成的存储定位信息，不接受客户端路径。
type CleanupObject struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	InStorage bool   `json:"in_storage,omitempty"`
}

// ResourceCleanup 与业务删除共用事务，只有提交后后台任务才能看到它。
type ResourceCleanup struct {
	StorageCleaned  bool            `xorm:"NOT NULL DEFAULT false"`
	ID              int64           `xorm:"pk autoincr"`
	Kind            string          `xorm:"VARCHAR(20) UNIQUE(target) NOT NULL"`
	ResourceID      int64           `xorm:"UNIQUE(target) NOT NULL"`
	Objects         []CleanupObject `xorm:"JSON TEXT"`
	NextAttemptUnix int64           `xorm:"INDEX NOT NULL DEFAULT 0"`
	Failures        int             `xorm:"NOT NULL DEFAULT 0"`
	RewriteKeys     bool
	Actor           Actor   `xorm:"JSON TEXT"`
	ScopeType       string  `xorm:"VARCHAR(16) NOT NULL DEFAULT ''"`
	ScopeID         int64   `xorm:"NOT NULL DEFAULT 0"`
	ObjectPath      string  `xorm:"VARCHAR(2048)"`
	AncestorIDs     []int64 `xorm:"JSON TEXT"`
}

func (*ResourceCleanup) TableName() string { return "governance_resource_cleanup" }

func AddResourceCleanup(engine db.EngineMigration) error {
	return engine.Sync(new(ResourceCleanup), new(LFSContentLock))
}
