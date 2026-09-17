// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

// MirrorOperation 保存镜像远端与数据库配置之间的恢复证据；含凭据地址只保存密文。
type MirrorOperation struct {
	ID                  string  `xorm:"pk VARCHAR(36)"`
	RepoID              int64   `xorm:"INDEX NOT NULL"`
	MirrorID            int64   `xorm:"INDEX NOT NULL DEFAULT 0"`
	Direction           string  `xorm:"VARCHAR(10) NOT NULL"`
	Action              string  `xorm:"VARCHAR(20) NOT NULL"`
	RemoteName          string  `xorm:"VARCHAR(100) NOT NULL DEFAULT ''"`
	State               string  `xorm:"VARCHAR(20) INDEX NOT NULL"`
	Actor               Actor   `xorm:"JSON TEXT"`
	ObjectPath          string  `xorm:"VARCHAR(2048)"`
	AncestorIDs         []int64 `xorm:"JSON TEXT"`
	BeforeEncrypted     string  `xorm:"TEXT" json:"-"`
	AfterEncrypted      string  `xorm:"TEXT" json:"-"`
	WikiBeforeEncrypted string  `xorm:"TEXT" json:"-"`
	WikiAfterEncrypted  string  `xorm:"TEXT" json:"-"`
	HasWiki             bool    `xorm:"NOT NULL DEFAULT false"`
	BeforePublic        string  `xorm:"VARCHAR(2048)"`
	AfterPublic         string  `xorm:"VARCHAR(2048)"`
	CreatedUnix         int64   `xorm:"INDEX NOT NULL"`
	NextAttemptUnix     int64   `xorm:"INDEX NOT NULL"`
	LastReasonCode      string  `xorm:"VARCHAR(40) NOT NULL DEFAULT ''"`
}

func (*MirrorOperation) TableName() string { return "governance_mirror_operation" }
