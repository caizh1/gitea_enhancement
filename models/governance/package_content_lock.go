// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"

	"gitea.dev/models/db"
)

// PackageContentLock 与 LFS 分开计锁，避免不同存储中相同哈希产生无关等待。
type PackageContentLock LFSContentLock

func (*PackageContentLock) TableName() string { return "governance_package_content_lock" }

func WithPackageContentLocks(ctx context.Context, hashes []string, apply func(context.Context) error) error {
	return withContentLocks(ctx, hashes, new(PackageContentLock), true, apply)
}

func AddPackageContentRecovery(engine db.EngineMigration) error {
	return engine.Sync(new(PackageContentLock), new(ResourceCleanup))
}
