// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/json"
	"gitea.dev/modules/util"
)

// RunRepositoryCreationRecovery 只核对已落盘结果；不会重建 Git 数据或删除可能成功的仓库。
func RunRepositoryCreationRecovery(ctx context.Context) error {
	operations := make([]*governance_model.RepositoryCreation, 0, 100)
	if err := db.GetEngine(ctx).In("state", "prepared", "unknown").Where("next_attempt_unix <= ?", time.Now().Unix()).Asc("next_attempt_unix", "created_unix").Limit(100).Find(&operations); err != nil {
		return err
	}
	for _, operation := range operations {
		if err := reconcileRepositoryCreation(ctx, operation.ID); err != nil && !errors.Is(err, governance_model.ErrConflict) && !errors.Is(err, governance_model.ErrNotFound) {
			return err
		}
	}
	return nil
}

func reconcileRepositoryCreation(ctx context.Context, id string) error {
	operation := new(governance_model.RepositoryCreation)
	has, err := db.GetEngine(ctx).ID(id).Get(operation)
	if err != nil || !has {
		return err
	}
	repo, err := repo_model.GetRepositoryByID(ctx, operation.RepoID)
	if err != nil {
		if errors.Is(err, util.ErrNotExist) {
			return markRepositoryCreationUnknown(ctx, operation, "database_record_missing")
		}
		return err
	}
	if repo.RelativePath() != operation.ExpectedRelativePath {
		return markRepositoryCreationUnknown(ctx, operation, "repository_path_changed")
	}
	if operation.Phase != "storage_complete" {
		return markRepositoryCreationUnknown(ctx, operation, "completion_marker_missing")
	}
	exists, err := gitrepo.IsRepositoryExist(ctx, repo)
	if err != nil {
		return err
	}
	if !exists {
		return markRepositoryCreationUnknown(ctx, operation, "git_repository_missing")
	}
	installed, err := gitrepo.ReferenceTransactionHookInstalled(repo)
	if err != nil {
		return err
	}
	if !installed {
		return markRepositoryCreationUnknown(ctx, operation, "initialization_incomplete")
	}
	if operation.Origin == "import" && repo.Status != repo_model.RepositoryReady {
		return markRepositoryCreationUnknown(ctx, operation, "import_completion_unproven")
	}
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		freshOperation := new(governance_model.RepositoryCreation)
		has, err := db.GetEngine(ctx).ID(operation.ID).Get(freshOperation)
		if err != nil || !has {
			return err
		}
		if freshOperation.State == "succeeded" {
			return nil
		}
		if freshOperation.State != "prepared" && freshOperation.State != "unknown" || freshOperation.Phase != "storage_complete" {
			return governance_model.ErrConflict
		}
		fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
		if err != nil || fresh.RelativePath() != operation.ExpectedRelativePath {
			return governance_model.ErrConflict
		}
		if operation.Origin != "import" && fresh.Status != repo_model.RepositoryReady {
			fresh.Status = repo_model.RepositoryReady
			if err := repo_model.UpdateRepositoryColsWithAutoTime(ctx, fresh, "status"); err != nil {
				return err
			}
		}
		if freshOperation.Metadata == nil {
			freshOperation.Metadata = make(map[string]any)
		}
		freshOperation.Metadata["recovered"] = true
		if _, err := db.GetEngine(ctx).ID(operation.ID).Cols("metadata", "last_reason_code").Update(&governance_model.RepositoryCreation{Metadata: freshOperation.Metadata, LastReasonCode: ""}); err != nil {
			return err
		}
		return completeRepositoryCreation(ctx, fresh)
	})
}

// RecoverRepositoryCreation 供主机管理员按操作ID再次核对；不执行新的 Git 写入。
func RecoverRepositoryCreation(ctx context.Context, id string) (string, error) {
	if err := reconcileRepositoryCreation(ctx, id); err != nil {
		return "", err
	}
	var operation governance_model.RepositoryCreation
	has, err := db.GetEngine(ctx).ID(id).Get(&operation)
	if err != nil {
		return "", err
	}
	if !has {
		return "", governance_model.ErrNotFound
	}
	return operation.State, nil
}

func markRepositoryCreationUnknown(ctx context.Context, operation *governance_model.RepositoryCreation, reason string) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", operation.RepoID)}, func(ctx context.Context) error {
		fresh := new(governance_model.RepositoryCreation)
		has, err := db.GetEngine(ctx).ID(operation.ID).Get(fresh)
		if err != nil || !has {
			return err
		}
		if fresh.State == "succeeded" {
			return nil
		}
		if fresh.State != "prepared" && fresh.State != "unknown" {
			return governance_model.ErrConflict
		}
		fresh.State, fresh.LastReasonCode, fresh.NextAttemptUnix = "unknown", reason, time.Now().Add(time.Hour).Unix()
		affected, err := db.GetEngine(ctx).ID(fresh.ID).In("state", "prepared", "unknown").Cols("state", "last_reason_code", "next_attempt_unix").Update(fresh)
		if err != nil {
			return err
		}
		if affected != 1 {
			return governance_model.ErrConflict
		}
		details, err := json.Marshal(map[string]any{"operation_id": fresh.ID, "origin": fresh.Origin, "reason_code": reason})
		if err != nil {
			return err
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "repository.creation_recovery_required", Actor: governance_model.Actor{Kind: "system", Name: "项目创建恢复核对任务", Transport: "background"}, ScopeType: "repository", ScopeID: fresh.RepoID, AncestorIDs: fresh.AncestorIDs, ObjectType: "repository", ObjectID: fresh.RepoID, ObjectPath: fresh.ObjectPath, Result: "unknown", Details: details})
	})
}
