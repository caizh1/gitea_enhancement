// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package asymkey

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	asymkey_model "gitea.dev/models/asymkey"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/util"
)

func AddPublicKey(ctx context.Context, ownerID int64, name, content string, authSourceID int64, verified bool) (*asymkey_model.PublicKey, error) {
	key, err := asymkey_model.AddPublicKey(ctx, ownerID, name, content, authSourceID, verified)
	if err != nil {
		return nil, err
	}
	return key, SyncSSHKeyFiles(ctx)
}

func AddDeployKey(ctx context.Context, repoID int64, name, content string, readOnly bool) (*asymkey_model.DeployKey, error) {
	key, err := asymkey_model.AddDeployKey(ctx, repoID, name, content, readOnly)
	if err != nil {
		return nil, err
	}
	return key, SyncSSHKeyFiles(ctx)
}

// SyncSSHKeyFiles 仅在外层事务提交后写文件。单节点文件锁覆盖读取、替换和回执清理。
func SyncSSHKeyFiles(ctx context.Context) error {
	if db.InTransaction(ctx) {
		return nil
	}
	err := asymkey_model.WithSSHOpLocker(func() error {
		var pending []*governance_model.SSHKeyFileSync
		if err := db.GetEngine(ctx).Asc("id").Limit(1000).Find(&pending); err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		var public, principal bool
		ids := make([]int64, 0, len(pending))
		for _, row := range pending {
			ids = append(ids, row.ID)
			if row.Principal {
				principal = true
			} else {
				public = true
			}
		}
		if !setting.SSH.StartBuiltinServer {
			if public && setting.SSH.CreateAuthorizedKeysFile {
				if err := rewriteAllPublicKeys(ctx); err != nil {
					return err
				}
			}
			if principal && setting.SSH.CreateAuthorizedPrincipalsFile {
				if err := rewriteAllPrincipalKeys(ctx); err != nil {
					return err
				}
			}
		}
		// 后续事务的记录不能随本次快照清除，崩溃后可重复重建。
		_, err := db.GetEngine(ctx).In("id", ids).Delete(new(governance_model.SSHKeyFileSync))
		return err
	})
	if err != nil {
		return fmt.Errorf("密钥数据库变更已保存，SSH 授权文件同步待恢复：%w", err)
	}
	return nil
}

// replaceSSHKeyFile 文件和目录均落盘后才允许清理待同步记录。
func replaceSSHKeyFile(temporary, target string) error {
	if err := util.Rename(temporary, target); err != nil {
		return err
	}
	if setting.IsWindows {
		return nil
	}
	directory, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
