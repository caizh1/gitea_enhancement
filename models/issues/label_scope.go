// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	"context"

	"gitea.dev/models/db"

	"xorm.io/builder"
)

// RepositoryHasLabelsOutsideOwners 同时保留当前关联与已经解除关联的操作历史。
// 调用方提供变更后真实祖先链，并在同一生命周期事务内完成检查与变更。
func RepositoryHasLabelsOutsideOwners(ctx context.Context, repoID int64, ownerIDs []int64) (bool, error) {
	outside := builder.Eq{"issue.repo_id": repoID}.And(builder.Or(
		builder.Eq{"label.org_id": 0}.And(builder.Neq{"label.repo_id": repoID}),
		builder.Eq{"label.repo_id": 0}.And(builder.NotIn("label.org_id", ownerIDs)),
	))
	linked, err := db.GetEngine(ctx).Table("issue_label").
		Join("inner", "label", "issue_label.label_id = label.id").
		Join("inner", "issue", "issue.id = issue_label.issue_id").
		Where(outside).Exist(new(IssueLabel))
	if err != nil || linked {
		return linked, err
	}
	return db.GetEngine(ctx).Table("comment").
		Join("inner", "label", "comment.label_id = label.id").
		Join("inner", "issue", "issue.id = comment.issue_id").
		Where(builder.Eq{"comment.type": CommentTypeLabel}.And(outside)).Exist(new(Comment))
}
