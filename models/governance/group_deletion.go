// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import "gitea.dev/models/db"

type DeletionGroupState struct {
	ID            int64
	Archived      bool
	DeleteAfter   int64
	DeleteActorID int64
}

type DeletionRepositoryState struct {
	ID           int64
	Archived     bool
	ArchivedUnix int64
}

// GroupDeletion 保存进入待删除之前的状态，恢复不会覆盖原有独立归档或子组删除计划。
type GroupDeletion struct {
	NextAttemptUnix int64                     `xorm:"INDEX NOT NULL DEFAULT 0"`
	Failures        int                       `xorm:"NOT NULL DEFAULT 0"`
	GroupID         int64                     `xorm:"pk"`
	DueUnix         int64                     `xorm:"INDEX NOT NULL"`
	Actor           Actor                     `xorm:"JSON TEXT"`
	OriginalSlug    string                    `xorm:"VARCHAR(100)"`
	OriginalName    string                    `xorm:"VARCHAR(255)"`
	Groups          []DeletionGroupState      `xorm:"JSON TEXT"`
	Repositories    []DeletionRepositoryState `xorm:"JSON TEXT"`
}

func (*GroupDeletion) TableName() string { return "governance_group_deletion" }

func AddGroupDeletion(engine db.EngineMigration) error {
	return engine.Sync(new(GroupDeletion))
}
