// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"

	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
)

// 路径已在外层事务中更新；任何丢失来源都会使整次移动回滚。
func checkMovedGroupLabels(ctx context.Context, root *governance_model.Namespace) error {
	_, repos, err := groupResourceTree(ctx, root)
	if err != nil {
		return err
	}
	owners := make(map[int64][]int64)
	for _, repo := range repos {
		ids, ok := owners[repo.OwnerID]
		if !ok {
			chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
			if err != nil {
				return err
			}
			ids = namespaceIDs(chain)
			owners[repo.OwnerID] = ids
		}
		lost, err := issues_model.RepositoryHasLabelsOutsideOwners(ctx, repo.ID, ids)
		if err != nil {
			return err
		}
		if lost {
			return fmt.Errorf("%w：仓库 #%d 的标签关联或操作历史会失去祖先来源；为避免数据丢失，此移动组合暂不支持，请保持当前归属", governance_model.ErrConflict, repo.ID)
		}
	}
	return nil
}
