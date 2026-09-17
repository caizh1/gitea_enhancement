// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package git

import (
	"context"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
)

// DeleteRepositoryProtectionRules 由已授权的项目删除链路调用，必须在删除项目记录之前保存范围快照。
func DeleteRepositoryProtectionRules(ctx context.Context, repoID int64) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repoID)}, func(ctx context.Context) error {
		repo, err := repo_model.GetRepositoryByID(ctx, repoID)
		if err != nil {
			return err
		}
		for {
			var rules []*ProtectedBranch
			if err := db.GetEngine(ctx).Where("repo_id = ?", repoID).Limit(100).Find(&rules); err != nil {
				return err
			}
			if len(rules) == 0 {
				break
			}
			for _, rule := range rules {
				if err := appendProtectionAudit(ctx, repo, rule, nil); err != nil {
					return err
				}
				if _, err := db.DeleteByID[ProtectedBranch](ctx, rule.ID); err != nil {
					return err
				}
			}
		}
		for {
			var rules []*ProtectedTag
			if err := db.GetEngine(ctx).Where("repo_id = ?", repoID).Limit(100).Find(&rules); err != nil {
				return err
			}
			if len(rules) == 0 {
				break
			}
			for _, rule := range rules {
				if err := appendRepositoryProtectionAudit(ctx, repo, "repository.tag_protection_deleted", "tag_protection", rule.ID, rule.NamePattern, protectedTagAuditValues(rule), nil); err != nil {
					return err
				}
				if _, err := db.DeleteByID[ProtectedTag](ctx, rule.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
