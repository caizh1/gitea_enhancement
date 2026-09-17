// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package setting

import "errors"

var Governance = struct {
	DeletionRetentionDays int
}{DeletionRetentionDays: 30}

func loadGovernanceFrom(cfg ConfigProvider) error {
	Governance.DeletionRetentionDays = cfg.Section("governance").Key("DELETION_RETENTION_DAYS").MustInt(30)
	if Governance.DeletionRetentionDays < 1 || Governance.DeletionRetentionDays > 90 {
		return errors.New("群组删除保留期必须在 1 到 90 天之间")
	}
	return nil
}
