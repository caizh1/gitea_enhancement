// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"

	actions_model "gitea.dev/models/actions"
	asymkey_model "gitea.dev/models/asymkey"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
)

// prepareActionsForGroupMove retires credentials issued under the old ancestry.
// The caller holds the governance write transaction through the namespace move.
func prepareActionsForGroupMove(ctx context.Context, source *governance_model.Namespace) (int64, error) {
	namespaces, repositories, err := groupResourceTree(ctx, source)
	if err != nil {
		return 0, err
	}
	groupIDs := make([]int64, 0, len(namespaces))
	for _, namespace := range namespaces {
		groupIDs = append(groupIDs, namespace.ID)
	}
	repoIDs := make([]int64, 0, len(repositories))
	for _, repo := range repositories {
		repoIDs = append(repoIDs, repo.ID)
	}
	e := db.GetEngine(ctx)
	has, err := e.In("owner_id", groupIDs).Where("repo_id = ?", 0).Exist(new(actions_model.ActionRunner))
	if err != nil {
		return 0, err
	}
	if has {
		return 0, fmt.Errorf("%w：请先删除移动群组及后代的专属 Runner，并在移动后重新注册", governance_model.ErrConflict)
	}
	if len(repoIDs) > 0 {
		for _, blocked := range []struct {
			bean   any
			where  string
			args   []any
			remedy string
		}{
			{new(actions_model.ActionTask), "status IN (?, ?)", []any{actions_model.StatusRunning, actions_model.StatusCancelling}, "请先等待或取消正在执行的 Actions 任务"},
			{new(actions_model.ActionRunner), "repo_id != ?", []any{0}, "请先删除移动群组及后代仓库的专属 Runner，并在移动后重新注册"},
			{new(asymkey_model.DeployKey), "repo_id != ?", []any{0}, "请先移除移动群组及后代仓库的部署密钥，并在移动后重新配置"},
		} {
			has, err := e.In("repo_id", repoIDs).Where(blocked.where, blocked.args...).Exist(blocked.bean)
			if err != nil {
				return 0, err
			}
			if has {
				return 0, fmt.Errorf("%w：%s", governance_model.ErrConflict, blocked.remedy)
			}
		}
	}
	deactivated, err := e.In("owner_id", groupIDs).Where("repo_id = ? AND is_active = ?", 0, true).Cols("is_active").Update(&actions_model.ActionRunnerToken{IsActive: false})
	if err != nil {
		return 0, err
	}
	if len(repoIDs) == 0 {
		return deactivated, nil
	}
	if _, err := e.In("repo_id", repoIDs).Delete(new(actions_model.ActionScheduleSpec)); err != nil {
		return 0, err
	}
	if _, err := e.In("repo_id", repoIDs).Delete(new(actions_model.ActionSchedule)); err != nil {
		return 0, err
	}
	if _, err := e.In("id", repoIDs).Incr("actions_scope_revision").Update(new(repo_model.Repository)); err != nil {
		return 0, err
	}
	repoTokens, err := e.In("repo_id", repoIDs).Where("owner_id = ? AND is_active = ?", 0, true).Cols("is_active").Update(&actions_model.ActionRunnerToken{IsActive: false})
	if err != nil {
		return 0, err
	}
	deactivated += repoTokens
	if _, err := e.In("repo_id", repoIDs).NoVersionCheck().Cols("scope_invalidated").Update(&actions_model.ActionRun{ScopeInvalidated: true}); err != nil {
		return 0, err
	}
	var jobs []*actions_model.ActionRunJob
	if err := e.In("repo_id", repoIDs).Where("status IN (?, ?)", actions_model.StatusWaiting, actions_model.StatusBlocked).Find(&jobs); err != nil {
		return 0, err
	}
	if _, err := actions_model.CancelJobs(ctx, jobs); err != nil {
		return 0, fmt.Errorf("取消群组移动前排队的 Actions 任务：%w", err)
	}
	return deactivated, nil
}
