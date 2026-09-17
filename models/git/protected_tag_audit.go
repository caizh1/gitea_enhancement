// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package git

import (
	"context"
	"fmt"
	"reflect"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
)

func protectedTagAuditValues(tag *ProtectedTag) map[string]any {
	if tag == nil {
		return nil
	}
	return map[string]any{
		"name_pattern":       tag.NamePattern,
		"allowlist_user_ids": normalizedProtectionList(tag.AllowlistUserIDs),
		"allowlist_team_ids": normalizedProtectionList(tag.AllowlistTeamIDs),
	}
}

func mutateProtectedTag(ctx context.Context, tag *ProtectedTag, action string) error {
	candidate := *tag
	err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", tag.RepoID)}, func(ctx context.Context) error {
		repo, err := repo_model.GetRepositoryByID(ctx, tag.RepoID)
		if err != nil {
			return err
		}
		if err := requireProtectionManager(ctx, repo); err != nil {
			return err
		}
		if err := repo.MustNotBeArchived(); err != nil {
			return err
		}
		var before *ProtectedTag
		if action == "created" {
			if tag.ID != 0 {
				return governance_model.ErrInvalid
			}
		} else {
			before, err = GetProtectedTagByID(ctx, tag.ID)
			if err != nil {
				return err
			}
			if before == nil || before.RepoID != tag.RepoID {
				return governance_model.ErrNotFound
			}
		}
		if action != "deleted" {
			// 调用者可能修改过名称，不能复用旧规则的编译缓存。
			candidate.RegexPattern, candidate.GlobPattern = nil, nil
			if err := candidate.EnsureCompiledPattern(); err != nil {
				return fmt.Errorf("%w：标签规则无效", governance_model.ErrInvalid)
			}
		}
		after := &candidate
		switch action {
		case "created":
			_, err = db.GetEngine(ctx).Insert(&candidate)
		case "updated":
			_, err = db.GetEngine(ctx).ID(tag.ID).Where("repo_id = ?", tag.RepoID).Cols("name_pattern", "allowlist_user_i_ds", "allowlist_team_i_ds").Update(&candidate)
		case "deleted":
			_, err = db.GetEngine(ctx).ID(tag.ID).Where("repo_id = ?", tag.RepoID).Delete(&ProtectedTag{})
			after = nil
		default:
			return governance_model.ErrInvalid
		}
		if err != nil {
			return err
		}
		oldValues, newValues := protectedTagAuditValues(before), protectedTagAuditValues(after)
		if reflect.DeepEqual(oldValues, newValues) {
			return nil
		}
		name := candidate.NamePattern
		if after == nil {
			name = before.NamePattern
		}
		return appendRepositoryProtectionAudit(ctx, repo, "repository.tag_protection_"+action, "tag_protection", candidate.ID, name, oldValues, newValues)
	})
	if err == nil {
		*tag = candidate
	}
	return err
}
