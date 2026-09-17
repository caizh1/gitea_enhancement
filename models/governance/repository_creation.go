// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"gitea.dev/models/db"
	"gitea.dev/modules/json"
)

// RepositoryCreation 保存跨数据库与 Git 存储创建操作的最小恢复证据。
type RepositoryCreation struct {
	ID                   string         `xorm:"pk VARCHAR(36)"`
	RepoID               int64          `xorm:"UNIQUE NOT NULL"`
	State                string         `xorm:"VARCHAR(20) INDEX NOT NULL"`
	Phase                string         `xorm:"VARCHAR(30) INDEX NOT NULL"`
	Origin               string         `xorm:"VARCHAR(20) NOT NULL"`
	Actor                Actor          `xorm:"JSON TEXT"`
	ObjectPath           string         `xorm:"VARCHAR(2048)"`
	ExpectedRelativePath string         `xorm:"VARCHAR(2048) NOT NULL"`
	AncestorIDs          []int64        `xorm:"JSON TEXT"`
	Metadata             map[string]any `xorm:"JSON TEXT"`
	CreatedUnix          int64          `xorm:"INDEX NOT NULL"`
	NextAttemptUnix      int64          `xorm:"INDEX NOT NULL"`
	LastReasonCode       string         `xorm:"VARCHAR(40) NOT NULL DEFAULT ''"`
}

func (*RepositoryCreation) TableName() string { return "governance_repository_creation" }

func AddRepositoryCreationRecovery(engine db.EngineMigration) error {
	return engine.Sync(new(RepositoryCreation), new(MirrorOperation), new(ResourceCleanup))
}

func (operation *RepositoryCreation) AuditDetails() (AuditDetails, error) {
	details := map[string]any{"operation_id": operation.ID, "origin": operation.Origin}
	for key, value := range operation.Metadata {
		details[key] = value
	}
	raw, err := json.Marshal(details)
	return raw, err
}
