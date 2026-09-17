// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"

	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

// SSHKeyFileSync 与密钥变更同事务提交；只清理已完成快照中的记录。
type SSHKeyFileSync struct {
	ID          int64              `xorm:"pk autoincr"`
	Principal   bool               `xorm:"NOT NULL"`
	CreatedUnix timeutil.TimeStamp `xorm:"created"`
}

func (*SSHKeyFileSync) TableName() string               { return "governance_ssh_key_file_sync" }
func AddSSHKeyFileSync(engine db.EngineMigration) error { return engine.Sync(new(SSHKeyFileSync)) }
func QueueSSHKeyFileSync(ctx context.Context, principal bool) error {
	return db.Insert(ctx, &SSHKeyFileSync{Principal: principal})
}
