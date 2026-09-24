// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"gitea.dev/models/db"
	"gitea.dev/modules/setting"
)

// LFSContentLock 将引用建立、上传与孤立对象清理按内容 ID 排列，锁持有到最外层事务提交。
type LFSContentLock struct {
	OID      string `xorm:"VARCHAR(64) pk"`
	Revision int64  `xorm:"NOT NULL DEFAULT 0"`
}

func (*LFSContentLock) TableName() string { return "governance_lfs_content_lock" }

func WithLFSContentLocks(ctx context.Context, oids []string, apply func(context.Context) error) error {
	return withContentLocks(ctx, oids, new(LFSContentLock), true, apply)
}

func WithLFSContentLocksWithoutRevision(ctx context.Context, oids []string, apply func(context.Context) error) error {
	return withContentLocks(ctx, oids, new(LFSContentLock), false, apply)
}

func withContentLocks(ctx context.Context, oids []string, bean any, increment bool, apply func(context.Context) error) error {
	oids = slices.Clone(oids)
	slices.Sort(oids)
	oids = slices.Compact(oids)
	return db.WithTx(ctx, func(ctx context.Context) error {
		for _, oid := range oids {
			if len(oid) != 64 || strings.ToLower(oid) != oid {
				return ErrInvalid
			}
			if _, err := hex.DecodeString(oid); err != nil {
				return ErrInvalid
			}
			next := "revision+1"
			initial := 1
			if !increment {
				next, initial = "revision", 0
			}
			query := fmt.Sprintf("INSERT INTO governance_lfs_content_lock (oid, revision) VALUES (?,%d) ON CONFLICT (oid) DO UPDATE SET revision = governance_lfs_content_lock.%s", initial, next)
			if setting.Database.Type.IsMySQL() {
				query = fmt.Sprintf("INSERT INTO governance_lfs_content_lock (oid, revision) VALUES (?,%d) ON DUPLICATE KEY UPDATE revision = %s", initial, next)
			}
			if setting.Database.Type.IsMSSQL() {
				query = fmt.Sprintf("MERGE INTO governance_lfs_content_lock WITH (HOLDLOCK) AS target USING (SELECT ? AS oid) AS source ON target.oid = source.oid WHEN MATCHED THEN UPDATE SET revision = %s WHEN NOT MATCHED THEN INSERT (oid, revision) VALUES (source.oid, %d);", next, initial)
			}
			engine := db.GetEngine(ctx).Context(ctx).Engine()
			table := engine.Quote(engine.TableName(bean, true))
			query = strings.ReplaceAll(query, "governance_lfs_content_lock", table)
			if _, err := db.GetEngine(ctx).Exec(query, oid); err != nil {
				return err
			}
		}
		return apply(ctx)
	})
}
