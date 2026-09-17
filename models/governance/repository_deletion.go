// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"strings"

	"gitea.dev/models/db"
)

// RepositoryDeletion 保存独立项目删除计划；群组删除不覆盖此记录。
type RepositoryDeletion struct {
	RepositoryID    int64  `xorm:"pk" json:"repository_id"`
	OwnerID         int64  `json:"owner_id"`
	DueUnix         int64  `xorm:"INDEX NOT NULL" json:"due_unix"`
	Actor           Actor  `xorm:"JSON TEXT" json:"-"`
	OriginalName    string `xorm:"VARCHAR(100)" json:"original_name"`
	Archived        bool   `json:"-"`
	ArchivedUnix    int64  `json:"-"`
	NextAttemptUnix int64  `xorm:"INDEX NOT NULL DEFAULT 0" json:"next_attempt_unix"`
	Failures        int    `xorm:"NOT NULL DEFAULT 0" json:"failures"`
}

func (*RepositoryDeletion) TableName() string { return "governance_repository_deletion" }

func AddRepositoryDeletion(engine db.EngineMigration) error {
	type Repository struct {
		GovernanceStorageName string `xorm:"VARCHAR(100) NOT NULL DEFAULT ''"`
	}
	return engine.Sync(new(RepositoryDeletion), new(Repository))
}

// ChangeRepositoryLifecyclePath 仅供已核对身份并锁定项目的删除恢复事务使用，不移动 Git 正文。
func ChangeRepositoryLifecyclePath(ctx context.Context, id, ownerID int64, name string) (string, error) {
	var ownerPath string
	err := WithWrite(ctx, []string{Resource("repository", id), Resource("group", ownerID)}, func(ctx context.Context) error {
		n, err := GetNamespace(ctx, ownerID)
		if err != nil {
			return err
		}
		if name == "" || len(name) > 100 || strings.ContainsAny(name, "/\\") {
			return ErrInvalid
		}
		if _, err := db.GetEngine(ctx).Where("kind = ? AND resource_id = ?", "repository", id).Cols("alias").Update(&ResourcePath{Alias: true}); err != nil {
			return err
		}
		if err := reservePath(ctx, n.FullPath+"/"+name, "repository", id); err != nil {
			return err
		}
		ownerPath = n.FullPath
		return nil
	})
	return ownerPath, err
}
