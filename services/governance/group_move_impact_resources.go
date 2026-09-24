// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	actions_model "gitea.dev/models/actions"
	asymkey_model "gitea.dev/models/asymkey"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/modules/json"
)

type GroupMoveRuntimeImpact struct {
	ActiveTasks      int      `json:"active_tasks"`
	DedicatedRunners int      `json:"dedicated_runners"`
	DeployKeys       int      `json:"deploy_keys"`
	QueuedJobs       int      `json:"queued_jobs"`
	RunnerTokens     int      `json:"runner_tokens"`
	Schedules        int      `json:"schedules"`
	LostLabelHistory bool     `json:"lost_label_history"`
	BlockingReasons  []string `json:"blocking_reasons"`
	Fingerprint      string   `json:"-"`
}

func groupMoveRuntimeImpact(ctx context.Context, source *governance_model.Namespace, targetChain []*governance_model.Namespace) (GroupMoveRuntimeImpact, error) {
	result := GroupMoveRuntimeImpact{BlockingReasons: []string{}}
	groups, repos, err := groupResourceTree(ctx, source)
	if err != nil {
		return result, err
	}
	groupIDs, repoIDs := make([]int64, 0, len(groups)), make([]int64, 0, len(repos))
	for _, group := range groups {
		groupIDs = append(groupIDs, group.ID)
	}
	for _, repo := range repos {
		repoIDs = append(repoIDs, repo.ID)
	}
	loadIDs := func(table any, column string, ids []int64, where string, args ...any) ([]int64, error) {
		if len(ids) == 0 {
			return []int64{}, nil
		}
		var result []int64
		err := db.GetEngine(ctx).Table(table).Cols("id").In(column, ids).Where(where, args...).Asc("id").Find(&result)
		return result, err
	}
	groupRunners, err := loadIDs(new(actions_model.ActionRunner), "owner_id", groupIDs, "repo_id = ?", 0)
	if err != nil {
		return result, err
	}
	repoRunners, err := loadIDs(new(actions_model.ActionRunner), "repo_id", repoIDs, "repo_id != ?", 0)
	if err != nil {
		return result, err
	}
	tasks, err := loadIDs(new(actions_model.ActionTask), "repo_id", repoIDs, "status IN (?, ?)", actions_model.StatusRunning, actions_model.StatusCancelling)
	if err != nil {
		return result, err
	}
	keys, err := loadIDs(new(asymkey_model.DeployKey), "repo_id", repoIDs, "repo_id != ?", 0)
	if err != nil {
		return result, err
	}
	queued, err := loadIDs(new(actions_model.ActionRunJob), "repo_id", repoIDs, "status IN (?, ?)", actions_model.StatusWaiting, actions_model.StatusBlocked)
	if err != nil {
		return result, err
	}
	groupTokens, err := loadIDs(new(actions_model.ActionRunnerToken), "owner_id", groupIDs, "repo_id = ? AND is_active = ?", 0, true)
	if err != nil {
		return result, err
	}
	repoTokens, err := loadIDs(new(actions_model.ActionRunnerToken), "repo_id", repoIDs, "owner_id = ? AND is_active = ?", 0, true)
	if err != nil {
		return result, err
	}
	schedules, err := loadIDs(new(actions_model.ActionSchedule), "repo_id", repoIDs, "repo_id > ?", 0)
	if err != nil {
		return result, err
	}
	for _, repo := range repos {
		chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
		if err != nil {
			return result, err
		}
		futureOwnerIDs := make([]int64, 0, len(chain)+len(targetChain))
		for _, current := range chain {
			futureOwnerIDs = append(futureOwnerIDs, current.ID)
			if current.ID == source.ID {
				break
			}
		}
		for _, future := range targetChain {
			futureOwnerIDs = append(futureOwnerIDs, future.ID)
		}
		lost, err := issues_model.RepositoryHasLabelsOutsideOwners(ctx, repo.ID, futureOwnerIDs)
		if err != nil {
			return result, err
		}
		result.LostLabelHistory = result.LostLabelHistory || lost
	}
	result.ActiveTasks, result.DedicatedRunners, result.DeployKeys = len(tasks), len(groupRunners)+len(repoRunners), len(keys)
	result.QueuedJobs, result.RunnerTokens, result.Schedules = len(queued), len(groupTokens)+len(repoTokens), len(schedules)
	if result.ActiveTasks > 0 {
		result.BlockingReasons = append(result.BlockingReasons, "正在执行或取消中的任务：请等待或取消")
	}
	if result.DedicatedRunners > 0 {
		result.BlockingReasons = append(result.BlockingReasons, "专属 Runner：请删除并在移动后重新注册")
	}
	if result.DeployKeys > 0 {
		result.BlockingReasons = append(result.BlockingReasons, "部署密钥：请移除并在移动后重新配置")
	}
	if result.LostLabelHistory {
		result.BlockingReasons = append(result.BlockingReasons, "来源标签关联或历史无法保留：当前移动组合被拒绝")
	}
	encoded, err := json.Marshal(struct {
		GroupRunners, RepoRunners, Tasks, Keys, Queued, GroupTokens, RepoTokens, Schedules []int64
		LabelsOutside                                                                      bool
	}{groupRunners, repoRunners, tasks, keys, queued, groupTokens, repoTokens, schedules, result.LostLabelHistory})
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(encoded)
	result.Fingerprint = hex.EncodeToString(sum[:])
	return result, nil
}
