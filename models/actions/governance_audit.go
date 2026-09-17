// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"

	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
)

// AppendConfigurationAudit 记录 Actions 管理配置；details 必须由调用者显式白名单构造。
func AppendConfigurationAudit(ctx context.Context, ownerID, repoID int64, eventType, objectType string, objectID int64, objectName string, details map[string]any) error {
	scopeType, scopeID, ancestors, resourcePath, err := actionsAuditScope(ctx, ownerID, repoID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{
		Type: eventType, Actor: governance_model.AuditActor(ctx),
		ScopeType: scopeType, ScopeID: scopeID, AncestorIDs: ancestors,
		ObjectType: objectType, ObjectID: objectID, ObjectPath: path.Join(resourcePath, fmt.Sprintf("%s-%d-%s", objectType, objectID, objectName)),
		Result: "success", Details: raw,
	})
}

func actionsAuditScope(ctx context.Context, ownerID, repoID int64) (string, int64, []int64, string, error) {
	if repoID > 0 {
		repo, err := repo_model.GetRepositoryByID(ctx, repoID)
		if err != nil {
			return "", 0, nil, "", err
		}
		if err := repo.LoadOwner(ctx); err != nil {
			return "", 0, nil, "", err
		}
		chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
		if err != nil {
			if !errors.Is(err, governance_model.ErrNotFound) {
				return "", 0, nil, "", err
			}
			return "repository", repoID, nil, repo.FullPath(), nil
		}
		ancestorIDs := make([]int64, 0, len(chain))
		for _, namespace := range chain {
			if namespace.Kind == "group" {
				ancestorIDs = append(ancestorIDs, namespace.ID)
			}
		}
		return "repository", repoID, ancestorIDs, repo.FullPath(), nil
	}
	if ownerID > 0 {
		namespace, err := governance_model.GetNamespace(ctx, ownerID)
		if err != nil {
			if !errors.Is(err, governance_model.ErrNotFound) {
				return "", 0, nil, "", err
			}
			owner, userErr := user_model.GetUserByID(ctx, ownerID)
			if userErr != nil {
				return "", 0, nil, "", userErr
			}
			return "user", ownerID, nil, owner.Name, nil
		}
		if namespace.Kind == "group" {
			chain, err := governance_model.Ancestors(ctx, ownerID)
			if err != nil {
				return "", 0, nil, "", err
			}
			ancestorIDs := make([]int64, 0, len(chain))
			for _, ancestor := range chain[1:] {
				if ancestor.Kind == "group" {
					ancestorIDs = append(ancestorIDs, ancestor.ID)
				}
			}
			return "group", ownerID, ancestorIDs, namespace.FullPath, nil
		}
		return "user", ownerID, nil, namespace.FullPath, nil
	}
	return "instance", 0, nil, "instance", nil
}

func changedFields(before, after map[string]any) []string {
	fields := make([]string, 0, len(before)+len(after))
	for key, beforeValue := range before {
		if afterValue, ok := after[key]; !ok || fmt.Sprint(beforeValue) != fmt.Sprint(afterValue) {
			fields = append(fields, key)
		}
	}
	for key := range after {
		if _, ok := before[key]; !ok {
			fields = append(fields, key)
		}
	}
	slices.Sort(fields)
	return slices.Compact(fields)
}

// AppendRunAudit 记录工作流运行管理，不保存输入、事件正文、日志或秘密。
func AppendRunAudit(ctx context.Context, run *ActionRun, eventType string) error {
	details := map[string]any{"workflow_id": run.WorkflowID, "trigger_event": run.TriggerEvent, "ref": run.Ref, "commit_sha": run.CommitSHA, "status": run.Status}
	return AppendConfigurationAudit(ctx, run.OwnerID, run.RepoID, eventType, "actions_run", run.ID, run.WorkflowID, details)
}
