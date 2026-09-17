// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import "gitea.dev/models/db"

type repositoryVisibility struct {
	ID         int64 `xorm:"pk"`
	Visibility int   `xorm:"INDEX NOT NULL DEFAULT 0"`
	IsPrivate  bool  `xorm:"INDEX"`
}

func (*repositoryVisibility) TableName() string { return "repository" }

// AddRepositoryVisibility 从旧 private 布尔值无损派生：旧私有为2，旧公开为0。
func AddRepositoryVisibility(engine db.EngineMigration) error {
	if err := engine.Sync(new(repositoryVisibility)); err != nil {
		return err
	}
	_, err := engine.Where("is_private = ?", true).Cols("visibility").Update(&repositoryVisibility{Visibility: 2})
	return err
}
