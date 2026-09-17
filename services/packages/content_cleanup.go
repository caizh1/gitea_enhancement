// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package packages

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	packages_model "gitea.dev/models/packages"
	packages_module "gitea.dev/modules/packages"
)

// RemoveUnreferencedBlobContent 仅在失败事务结束后补偿；提交结果不明时以数据库中的实际引用为准。
func RemoveUnreferencedBlobContent(ctx context.Context, hash string) error {
	if db.InTransaction(ctx) {
		return errors.New("软件包补偿清理必须在业务事务结束后执行")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return governance_model.WithPackageContentLocks(ctx, []string{hash}, func(ctx context.Context) error {
		exists, err := packages_model.ExistPackageBlobWithSHA(ctx, hash)
		if err != nil || exists {
			return err
		}
		err = packages_module.NewContentStore().Delete(packages_module.BlobHash256Key(hash))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	})
}
