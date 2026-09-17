// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"

	"gitea.dev/models/db"
)

// ReferenceRevision 在引用准备及结果核对时推进，避免中间写入后恢复原值形成 ABA。
type ReferenceRevision struct {
	RepoID   int64 `xorm:"pk"`
	Revision int64 `xorm:"NOT NULL"`
}

func (*ReferenceRevision) TableName() string { return "governance_reference_revision" }

func ReadReferenceRevision(ctx context.Context, repoID int64) (int64, error) {
	row, has, err := db.GetByID[ReferenceRevision](ctx, repoID)
	if err != nil || !has {
		return 0, err
	}
	return row.Revision, nil
}

// 调用者持有治理写事务；递增与引用日志在同一事务提交。
func advanceReferenceRevision(ctx context.Context, repoID int64) error {
	n, err := db.GetEngine(ctx).ID(repoID).Incr("revision").Update(new(ReferenceRevision))
	if err != nil || n != 0 {
		return err
	}
	return db.Insert(ctx, &ReferenceRevision{RepoID: repoID, Revision: 1})
}
