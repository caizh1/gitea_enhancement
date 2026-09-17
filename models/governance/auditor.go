// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import "gitea.dev/models/db"

func AddAuditor(engine db.EngineMigration) error {
	type User struct {
		IsAuditor bool `xorm:"NOT NULL DEFAULT false"`
	}
	return engine.Sync(new(User))
}

// IncludeAuditorAbilities 只增加读取能力，已有直接或继承写权限保持独立。
func IncludeAuditorAbilities(abilities Abilities) {
	for _, ability := range []string{ReadGroup, ReadIssues, ReadCode, ReadPulls, ReadWiki, ReadActions, ReadPackages, ReadReleases, ReadAudit} {
		abilities[ability] = true
	}
}
