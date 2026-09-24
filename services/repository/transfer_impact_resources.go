// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package repository

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

// TransferRuntimeImpact describes current blockers and state retired by the existing transfer transaction.
type TransferRuntimeImpact struct {
	ActiveTasks       int      `json:"active_tasks"`
	RepositoryRunners int      `json:"repository_runners"`
	DeployKeys        int      `json:"deploy_keys"`
	QueuedJobs        int      `json:"queued_jobs"`
	RunnerTokens      int      `json:"runner_tokens"`
	Schedules         int      `json:"schedules"`
	LostLabelHistory  bool     `json:"lost_label_history"`
	BlockingReasons   []string `json:"blocking_reasons"`
	Fingerprint       string   `json:"-"`
}

func repositoryTransferRuntimeImpact(ctx context.Context, repoID int64, targetChain []*governance_model.Namespace) (TransferRuntimeImpact, error) {
	result := TransferRuntimeImpact{BlockingReasons: []string{}}
	loadIDs := func(table any, where string, args ...any) ([]int64, error) {
		ids := []int64{}
		err := db.GetEngine(ctx).Table(table).Cols("id").Where(where, args...).Asc("id").Find(&ids)
		return ids, err
	}
	tasks, err := loadIDs(new(actions_model.ActionTask), "repo_id = ? AND status IN (?, ?)", repoID, actions_model.StatusRunning, actions_model.StatusCancelling)
	if err != nil {
		return result, err
	}
	runners, err := loadIDs(new(actions_model.ActionRunner), "repo_id = ?", repoID)
	if err != nil {
		return result, err
	}
	keys, err := loadIDs(new(asymkey_model.DeployKey), "repo_id = ?", repoID)
	if err != nil {
		return result, err
	}
	queued, err := loadIDs(new(actions_model.ActionRunJob), "repo_id = ? AND status IN (?, ?)", repoID, actions_model.StatusWaiting, actions_model.StatusBlocked)
	if err != nil {
		return result, err
	}
	tokens, err := loadIDs(new(actions_model.ActionRunnerToken), "repo_id = ? AND is_active = ?", repoID, true)
	if err != nil {
		return result, err
	}
	schedules, err := loadIDs(new(actions_model.ActionSchedule), "repo_id = ?", repoID)
	if err != nil {
		return result, err
	}
	owners := make([]int64, 0, len(targetChain))
	for _, source := range targetChain {
		owners = append(owners, source.ID)
	}
	result.LostLabelHistory, err = issues_model.RepositoryHasLabelsOutsideOwners(ctx, repoID, owners)
	if err != nil {
		return result, err
	}
	result.ActiveTasks, result.RepositoryRunners, result.DeployKeys = len(tasks), len(runners), len(keys)
	result.QueuedJobs, result.RunnerTokens, result.Schedules = len(queued), len(tokens), len(schedules)
	if result.ActiveTasks > 0 {
		result.BlockingReasons = append(result.BlockingReasons, "正在执行或取消中的任务：请等待或取消")
	}
	if result.RepositoryRunners > 0 {
		result.BlockingReasons = append(result.BlockingReasons, "仓库专属 Runner：请删除并在转移后重新注册")
	}
	if result.DeployKeys > 0 {
		result.BlockingReasons = append(result.BlockingReasons, "仓库部署密钥：请移除并在转移后重新配置")
	}
	if result.LostLabelHistory {
		result.BlockingReasons = append(result.BlockingReasons, "来源组织标签关联或历史无法保留：当前转移组合被拒绝")
	}
	encoded, err := json.Marshal(struct {
		Tasks, Runners, Keys, Queued, Tokens, Schedules []int64
		LabelsOutside                                   bool
	}{tasks, runners, keys, queued, tokens, schedules, result.LostLabelHistory})
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(encoded)
	result.Fingerprint = hex.EncodeToString(sum[:])
	return result, nil
}
