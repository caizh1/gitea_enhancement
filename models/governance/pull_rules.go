// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"time"

	"gitea.dev/models/db"
)

// PullRuleVersion 只追加规则快照；项目模板后续变化不能覆盖已保存的 PR 历史。
type PullRuleVersion struct {
	ID        int64          `xorm:"pk autoincr" json:"id"`
	PullID    int64          `xorm:"UNIQUE(version) INDEX NOT NULL" json:"pull_id"`
	Revision  int64          `xorm:"UNIQUE(version) NOT NULL" json:"revision"`
	Rules     []ApprovalRule `xorm:"JSON TEXT" json:"rules"`
	CreatedAt time.Time      `xorm:"NOT NULL" json:"created_at"`
}

func (*PullRuleVersion) TableName() string { return "governance_pull_rule_version" }

func LatestPullRuleVersion(ctx context.Context, pullID int64) (*PullRuleVersion, bool, error) {
	result := new(PullRuleVersion)
	has, err := db.GetEngine(ctx).Where("pull_id = ?", pullID).Desc("revision").Get(result)
	return result, has, err
}

// SnapshotProjectPullRules 调用者必须在创建 PR 或修改设置的同一事务持有项目治理锁。
func SnapshotProjectPullRules(ctx context.Context, pullID, repoID int64, replace bool) (*PullRuleVersion, error) {
	if pullID <= 0 || repoID <= 0 {
		return nil, ErrInvalid
	}
	var result *PullRuleVersion
	err := WithWrite(ctx, []string{Resource("pull", pullID), Resource("repository", repoID)}, func(ctx context.Context) error {
		previous, has, err := LatestPullRuleVersion(ctx, pullID)
		if err != nil {
			return err
		}
		if has && !replace {
			result = previous
			return nil
		}
		result = &PullRuleVersion{PullID: pullID, Revision: 1, Rules: []ApprovalRule{}, CreatedAt: time.Now().UTC()}
		if has {
			result.Revision = previous.Revision + 1
		}
		if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repoID).Asc("id").Find(&result.Rules); err != nil {
			return err
		}
		return db.Insert(ctx, result)
	})
	return result, err
}
