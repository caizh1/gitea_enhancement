// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package mirror

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/git"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/json"
	"gitea.dev/modules/secret"
	"gitea.dev/modules/setting"
	repo_service "gitea.dev/services/repository"

	"github.com/google/uuid"
)

func pullMirrorAuditValues(mirror *repo_model.Mirror) map[string]any {
	return map[string]any{"interval_seconds": mirror.Interval.Seconds(), "enable_prune": mirror.EnablePrune, "lfs_enabled": mirror.LFS, "lfs_endpoint": safeMirrorURL(mirror.LFSEndpoint), "remote": safeMirrorURL(mirror.RemoteAddress)}
}

// UpdatePullMirrorConfiguration 只更新管理配置，避免网页旧对象覆盖并发同步状态。
func UpdatePullMirrorConfiguration(ctx context.Context, mirror *repo_model.Mirror) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", mirror.RepoID)}, func(ctx context.Context) error {
		fresh, err := repo_model.GetMirrorByRepoID(ctx, mirror.RepoID)
		if err != nil || fresh.ID != mirror.ID {
			return governance_model.ErrConflict
		}
		before := pullMirrorAuditValues(fresh)
		fresh.Interval, fresh.EnablePrune, fresh.LFS, fresh.LFSEndpoint, fresh.RemoteAddress = mirror.Interval, mirror.EnablePrune, mirror.LFS, mirror.LFSEndpoint, mirror.RemoteAddress
		fresh.ScheduleNextUpdate()
		affected, err := db.GetEngine(ctx).ID(fresh.ID).Cols("interval", "enable_prune", "lfs_enabled", "lfs_endpoint", "remote_address", "next_update_unix").Update(fresh)
		if err != nil || affected != 1 {
			return governance_model.ErrConflict
		}
		after := pullMirrorAuditValues(fresh)
		changed := make([]string, 0)
		for key, value := range before {
			if !reflect.DeepEqual(value, after[key]) {
				changed = append(changed, key)
			}
		}
		slices.Sort(changed)
		if len(changed) == 0 {
			return nil
		}
		repo := fresh.GetRepository(ctx)
		return repo_service.AppendRepositoryAudit(ctx, repo, repo, "repository.mirror_updated", map[string]any{"direction": "pull", "mirror_id": fresh.ID, "changed_fields": changed, "before": before, "after": after})
	})
}

func beginPullMirrorOperation(ctx context.Context, mirror *repo_model.Mirror, beforeSecret, afterSecret, beforeWikiSecret, afterWikiSecret, beforePublic, afterPublic string, hasWiki bool) (*governance_model.MirrorOperation, error) {
	beforeEncrypted, err := secret.EncryptSecret(setting.SecretKey, beforeSecret)
	if err != nil {
		return nil, err
	}
	afterEncrypted, err := secret.EncryptSecret(setting.SecretKey, afterSecret)
	if err != nil {
		return nil, err
	}
	wikiBeforeEncrypted, err := secret.EncryptSecret(setting.SecretKey, beforeWikiSecret)
	if err != nil {
		return nil, err
	}
	wikiAfterEncrypted, err := secret.EncryptSecret(setting.SecretKey, afterWikiSecret)
	if err != nil {
		return nil, err
	}
	repo := mirror.GetRepository(ctx)
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
		return nil, err
	}
	now := time.Now()
	operation := &governance_model.MirrorOperation{ID: uuid.NewString(), RepoID: repo.ID, MirrorID: mirror.ID, Direction: "pull", Action: "update", RemoteName: mirror.GetRemoteName(), State: "prepared", Actor: governance_model.AuditActor(ctx), ObjectPath: repo.FullPath(), BeforeEncrypted: beforeEncrypted, AfterEncrypted: afterEncrypted, WikiBeforeEncrypted: wikiBeforeEncrypted, WikiAfterEncrypted: wikiAfterEncrypted, HasWiki: hasWiki, BeforePublic: beforePublic, AfterPublic: afterPublic, CreatedUnix: now.Unix(), NextAttemptUnix: now.Add(10 * time.Minute).Unix()}
	for _, ancestor := range chain {
		if ancestor.Kind == "group" {
			operation.AncestorIDs = append(operation.AncestorIDs, ancestor.ID)
		}
	}
	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		pending, err := db.GetEngine(ctx).Where("repo_id = ?", repo.ID).In("state", "prepared", "unknown").Exist(new(governance_model.MirrorOperation))
		if err != nil || pending {
			return governance_model.ErrConflict
		}
		fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
		if err != nil || fresh.OriginalURL != beforePublic {
			return governance_model.ErrConflict
		}
		return db.Insert(ctx, operation)
	}); err != nil {
		return nil, err
	}
	return operation, nil
}

func beginPushMirrorOperation(ctx context.Context, mirror *repo_model.PushMirror, action, beforeSecret, afterSecret, wikiBeforeSecret, wikiAfterSecret string, hasWiki bool) (*governance_model.MirrorOperation, error) {
	repo := mirror.GetRepository(ctx)
	beforeEncrypted, err := secret.EncryptSecret(setting.SecretKey, beforeSecret)
	if err != nil {
		return nil, err
	}
	afterEncrypted, err := secret.EncryptSecret(setting.SecretKey, afterSecret)
	if err != nil {
		return nil, err
	}
	wikiBeforeEncrypted, err := secret.EncryptSecret(setting.SecretKey, wikiBeforeSecret)
	if err != nil {
		return nil, err
	}
	wikiAfterEncrypted, err := secret.EncryptSecret(setting.SecretKey, wikiAfterSecret)
	if err != nil {
		return nil, err
	}
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
		return nil, err
	}
	now := time.Now()
	operation := &governance_model.MirrorOperation{ID: uuid.NewString(), RepoID: repo.ID, MirrorID: mirror.ID, Direction: "push", Action: action, RemoteName: mirror.RemoteName, State: "prepared", Actor: governance_model.AuditActor(ctx), ObjectPath: repo.FullPath(), BeforeEncrypted: beforeEncrypted, AfterEncrypted: afterEncrypted, WikiBeforeEncrypted: wikiBeforeEncrypted, WikiAfterEncrypted: wikiAfterEncrypted, HasWiki: hasWiki, BeforePublic: safeMirrorURL(mirror.RemoteAddress), AfterPublic: safeMirrorURL(mirror.RemoteAddress), CreatedUnix: now.Unix(), NextAttemptUnix: now.Add(10 * time.Minute).Unix()}
	for _, ancestor := range chain {
		if ancestor.Kind == "group" {
			operation.AncestorIDs = append(operation.AncestorIDs, ancestor.ID)
		}
	}
	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		pending, err := db.GetEngine(ctx).Where("repo_id = ?", repo.ID).In("state", "prepared", "unknown").Exist(new(governance_model.MirrorOperation))
		if err != nil || pending {
			return governance_model.ErrConflict
		}
		return db.Insert(ctx, operation)
	}); err != nil {
		return nil, err
	}
	return operation, nil
}

func completePushMirrorOperation(ctx context.Context, operation *governance_model.MirrorOperation, mirror *repo_model.PushMirror, eventType string, details map[string]any) error {
	repo := mirror.GetRepository(ctx)
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		fresh := new(governance_model.MirrorOperation)
		has, err := db.GetEngine(ctx).ID(operation.ID).Get(fresh)
		if err != nil || !has {
			return governance_model.ErrConflict
		}
		if fresh.State == "succeeded" {
			return nil
		}
		if fresh.State != "prepared" && fresh.State != "unknown" {
			return governance_model.ErrConflict
		}
		ctx = governance_model.WithAuditActor(ctx, fresh.Actor)
		details["operation_id"], details["direction"], details["mirror_id"] = operation.ID, "push", mirror.ID
		if err := appendMirrorOperationAudit(ctx, fresh, eventType, details); err != nil {
			return err
		}
		affected, err := db.GetEngine(ctx).ID(operation.ID).In("state", "prepared", "unknown").Cols("state", "next_attempt_unix", "last_reason_code").Update(&governance_model.MirrorOperation{State: "succeeded", NextAttemptUnix: 0, LastReasonCode: ""})
		if err != nil || affected != 1 {
			return governance_model.ErrConflict
		}
		return nil
	})
}

func completePullMirrorOperation(ctx context.Context, operation *governance_model.MirrorOperation, repo *repo_model.Repository) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		freshOperation := new(governance_model.MirrorOperation)
		has, err := db.GetEngine(ctx).ID(operation.ID).Get(freshOperation)
		if err != nil || !has {
			return governance_model.ErrConflict
		}
		if freshOperation.State == "succeeded" {
			fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
			if err == nil && fresh.OriginalURL == freshOperation.AfterPublic {
				repo.OriginalURL = fresh.OriginalURL
				return nil
			}
			return governance_model.ErrConflict
		}
		if freshOperation.State != "prepared" && freshOperation.State != "unknown" {
			return governance_model.ErrConflict
		}
		ctx = governance_model.WithAuditActor(ctx, freshOperation.Actor)
		fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
		if err != nil || fresh.OriginalURL != freshOperation.BeforePublic {
			return governance_model.ErrConflict
		}
		fresh.OriginalURL = freshOperation.AfterPublic
		if err := repo_model.UpdateRepositoryColsNoAutoTime(ctx, fresh, "original_url"); err != nil {
			return err
		}
		if err := appendMirrorOperationAudit(ctx, freshOperation, "repository.mirror_updated", map[string]any{"operation_id": operation.ID, "direction": "pull", "changed_fields": []string{"remote_address"}, "before_remote": safeMirrorURL(freshOperation.BeforePublic), "after_remote": safeMirrorURL(freshOperation.AfterPublic), "current_path": fresh.FullPath()}); err != nil {
			return err
		}
		affected, err := db.GetEngine(ctx).ID(operation.ID).In("state", "prepared", "unknown").Cols("state", "next_attempt_unix", "last_reason_code").Update(&governance_model.MirrorOperation{State: "succeeded", NextAttemptUnix: 0, LastReasonCode: ""})
		if err != nil || affected != 1 {
			return governance_model.ErrConflict
		}
		repo.OriginalURL = fresh.OriginalURL
		return nil
	})
}

func appendMirrorOperationAudit(ctx context.Context, operation *governance_model.MirrorOperation, eventType string, details map[string]any) error {
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: eventType, Actor: operation.Actor, ScopeType: "repository", ScopeID: operation.RepoID, AncestorIDs: operation.AncestorIDs, ObjectType: "repository", ObjectID: operation.RepoID, ObjectPath: operation.ObjectPath, Result: "success", Details: raw})
}

// RunMirrorOperationRecovery 核对远端实际值与数据库快照；不会重新应用未知配置。
func RunMirrorOperationRecovery(ctx context.Context) error {
	var operations []*governance_model.MirrorOperation
	if err := db.GetEngine(ctx).In("state", "prepared", "unknown").Where("next_attempt_unix <= ?", time.Now().Unix()).Asc("next_attempt_unix", "created_unix").Limit(100).Find(&operations); err != nil {
		return err
	}
	for _, operation := range operations {
		if err := reconcileMirrorOperation(ctx, operation); err != nil {
			return err
		}
	}
	return nil
}

// RecoverMirrorOperation 按操作 ID 只读核对真实数据库与 Git 配置，不重新执行镜像写入。
func RecoverMirrorOperation(ctx context.Context, id string) (string, error) {
	operation := new(governance_model.MirrorOperation)
	has, err := db.GetEngine(ctx).ID(id).Get(operation)
	if err != nil {
		return "", err
	}
	if !has {
		return "", governance_model.ErrNotFound
	}
	if operation.State == "prepared" || operation.State == "unknown" {
		if err := reconcileMirrorOperation(ctx, operation); err != nil {
			return "", err
		}
		fresh := new(governance_model.MirrorOperation)
		if _, err := db.GetEngine(ctx).ID(id).Get(fresh); err != nil {
			return "", err
		}
		operation = fresh
	}
	return operation.State, nil
}

func reconcileMirrorOperation(ctx context.Context, operation *governance_model.MirrorOperation) error {
	repo, err := repo_model.GetRepositoryByID(ctx, operation.RepoID)
	if err != nil {
		if err := markMirrorOperationUnknown(ctx, operation, "repository_missing"); err != nil {
			return err
		}
		return nil
	}
	after, err := secret.DecryptSecret(setting.SecretKey, operation.AfterEncrypted)
	if err != nil {
		return err
	}
	before, err := secret.DecryptSecret(setting.SecretKey, operation.BeforeEncrypted)
	if err != nil {
		return err
	}
	if operation.Direction == "pull" && operation.Action == "update" {
		mirror, err := repo_model.GetMirrorByRepoID(ctx, repo.ID)
		if err != nil {
			if err := markMirrorOperationUnknown(ctx, operation, "mirror_missing"); err != nil {
				return err
			}
			return nil
		}
		actual, exists, err := mirrorRemoteValue(ctx, repo, mirror.GetRemoteName())
		if err != nil {
			return err
		}
		wikiActual, wikiExists, wikiAfter, wikiBefore := "", !operation.HasWiki, "", ""
		if operation.HasWiki {
			wikiActual, wikiExists, err = mirrorRemoteValue(ctx, repo.WikiStorageRepo(), mirror.GetRemoteName())
			if err != nil {
				return err
			}
			wikiAfter, err = secret.DecryptSecret(setting.SecretKey, operation.WikiAfterEncrypted)
			if err != nil {
				return err
			}
			wikiBefore, err = secret.DecryptSecret(setting.SecretKey, operation.WikiBeforeEncrypted)
			if err != nil {
				return err
			}
		}
		if exists && actual == after && wikiExists && wikiActual == wikiAfter {
			if err := completePullMirrorOperation(ctx, operation, repo); err != nil {
				return err
			}
		} else if actual == before && (!operation.HasWiki || wikiActual == wikiBefore) {
			if err := finishFailedMirrorOperation(ctx, operation, true); err != nil {
				return err
			}
		} else if err := markMirrorOperationUnknown(ctx, operation, "remote_value_mismatch"); err != nil {
			return err
		}
		return nil
	}
	if operation.Direction == "push" && (operation.Action == "create" || operation.Action == "delete") {
		actual, remoteExists, err := mirrorRemoteValue(ctx, repo, operation.RemoteName)
		if err != nil {
			return err
		}
		mainConfigOK := !remoteExists
		if remoteExists {
			mainConfigOK, err = pushMirrorRemoteConfigurationMatches(ctx, repo, operation.RemoteName)
			if err != nil {
				return err
			}
		}
		mirror, mirrorExists, err := repo_model.GetPushMirrorByIDAndRepoID(ctx, operation.MirrorID, operation.RepoID)
		if err != nil {
			return err
		}
		wikiActual, wikiExists, wikiAfter, wikiBefore := "", !operation.HasWiki, "", ""
		wikiConfigOK := !operation.HasWiki
		if operation.HasWiki {
			wikiActual, wikiExists, err = mirrorRemoteValue(ctx, repo.WikiStorageRepo(), operation.RemoteName)
			if err != nil {
				return err
			}
			wikiAfter, err = secret.DecryptSecret(setting.SecretKey, operation.WikiAfterEncrypted)
			if err != nil {
				return err
			}
			wikiBefore, err = secret.DecryptSecret(setting.SecretKey, operation.WikiBeforeEncrypted)
			if err != nil {
				return err
			}
			wikiConfigOK = !wikiExists
			if wikiExists {
				wikiConfigOK, err = pushMirrorRemoteConfigurationMatches(ctx, repo.WikiStorageRepo(), operation.RemoteName)
				if err != nil {
					return err
				}
			}
		}
		if operation.Action == "create" && mirrorExists && remoteExists && mainConfigOK && actual == after && wikiExists && wikiConfigOK && wikiActual == wikiAfter {
			mirror.Repo = repo
			if err := completePushMirrorOperation(ctx, operation, mirror, "repository.mirror_configured", map[string]any{"action": "created", "remote_name": operation.RemoteName, "recovered": true}); err != nil {
				return err
			}
		} else if operation.Action == "delete" && !mirrorExists && !remoteExists && (!operation.HasWiki || !wikiExists) {
			mirror = &repo_model.PushMirror{ID: operation.MirrorID, RepoID: operation.RepoID, Repo: repo, RemoteName: operation.RemoteName}
			if err := completePushMirrorOperation(ctx, operation, mirror, "repository.mirror_updated", map[string]any{"action": "deleted", "remote_name": operation.RemoteName, "recovered": true}); err != nil {
				return err
			}
		} else if operation.Action == "create" && !mirrorExists && !remoteExists && (!operation.HasWiki || !wikiExists) || operation.Action == "delete" && mirrorExists && remoteExists && mainConfigOK && actual == before && (!operation.HasWiki || wikiExists && wikiConfigOK && wikiActual == wikiBefore) {
			if err := finishFailedMirrorOperation(ctx, operation, true); err != nil {
				return err
			}
		} else if err := markMirrorOperationUnknown(ctx, operation, "remote_value_mismatch"); err != nil {
			return err
		}
		return nil
	}
	if err := markMirrorOperationUnknown(ctx, operation, "unsupported_operation"); err != nil {
		return err
	}
	return nil
}

func mirrorRemoteValue(ctx context.Context, repo gitrepo.Repository, name string) (string, bool, error) {
	remote, err := gitrepo.GitRemoteGetURL(ctx, repo, name)
	if git.IsRemoteNotExistError(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return remote.String(), true, nil
}

func markMirrorOperationUnknown(ctx context.Context, operation *governance_model.MirrorOperation, reason string) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", operation.RepoID)}, func(ctx context.Context) error {
		fresh := new(governance_model.MirrorOperation)
		has, err := db.GetEngine(ctx).ID(operation.ID).Get(fresh)
		if err != nil || !has {
			return err
		}
		if fresh.State == "succeeded" || fresh.State == "failed" {
			return nil
		}
		if fresh.State == "unknown" {
			return nil
		}
		if fresh.State != "prepared" && fresh.State != "unknown" {
			return governance_model.ErrConflict
		}
		affected, err := db.GetEngine(ctx).ID(operation.ID).In("state", "prepared", "unknown").Cols("state", "last_reason_code", "next_attempt_unix").Update(&governance_model.MirrorOperation{State: "unknown", LastReasonCode: reason, NextAttemptUnix: time.Now().Add(time.Hour).Unix()})
		if err != nil {
			return err
		}
		if affected != 1 {
			return governance_model.ErrConflict
		}
		details, err := json.Marshal(map[string]any{"operation_id": fresh.ID, "direction": fresh.Direction, "action": fresh.Action, "reason_code": reason, "original_actor_id": fresh.Actor.ID})
		if err != nil {
			return err
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "repository.mirror_recovery_required", Actor: governance_model.Actor{Kind: "system", Name: "镜像配置恢复核对任务", Transport: "background"}, ScopeType: "repository", ScopeID: fresh.RepoID, AncestorIDs: fresh.AncestorIDs, ObjectType: "repository", ObjectID: fresh.RepoID, ObjectPath: fresh.ObjectPath, Result: "unknown", Details: details})
	})
}

func finishFailedMirrorOperation(ctx context.Context, operation *governance_model.MirrorOperation, compensationSucceeded bool) error {
	state, reason := "unknown", "compensation_failed"
	if compensationSucceeded {
		state, reason = "failed", "operation_rolled_back"
	}
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return governance_model.WithWrite(recoveryCtx, []string{governance_model.Resource("repository", operation.RepoID)}, func(tx context.Context) error {
		fresh := new(governance_model.MirrorOperation)
		has, err := db.GetEngine(tx).ID(operation.ID).Get(fresh)
		if err != nil {
			return err
		}
		if !has {
			return governance_model.ErrNotFound
		}
		if fresh.State == "succeeded" || fresh.State == "failed" {
			return nil
		}
		affected, err := db.GetEngine(tx).ID(fresh.ID).In("state", "prepared", "unknown").Cols("state", "last_reason_code", "next_attempt_unix").Update(&governance_model.MirrorOperation{State: state, LastReasonCode: reason, NextAttemptUnix: time.Now().Add(time.Hour).Unix()})
		if err != nil || affected != 1 {
			return governance_model.ErrConflict
		}
		return appendMirrorFailureAudit(tx, fresh, state, reason)
	})
}

func compensateMirrorOperation(ctx context.Context, operation *governance_model.MirrorOperation, compensate func(context.Context) bool) {
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = governance_model.WithWrite(recoveryCtx, []string{governance_model.Resource("repository", operation.RepoID)}, func(tx context.Context) error {
		fresh := new(governance_model.MirrorOperation)
		has, err := db.GetEngine(tx).ID(operation.ID).Get(fresh)
		if err != nil || !has || fresh.State == "succeeded" || fresh.State == "failed" {
			return err
		}
		state, reason := "unknown", "compensation_failed"
		if compensate(tx) {
			state, reason = "failed", "operation_rolled_back"
		}
		affected, err := db.GetEngine(tx).ID(operation.ID).In("state", "prepared", "unknown").Cols("state", "last_reason_code", "next_attempt_unix").Update(&governance_model.MirrorOperation{State: state, LastReasonCode: reason, NextAttemptUnix: time.Now().Add(time.Hour).Unix()})
		if err != nil || affected != 1 {
			return governance_model.ErrConflict
		}
		return appendMirrorFailureAudit(tx, fresh, state, reason)
	})
}

func appendMirrorFailureAudit(ctx context.Context, operation *governance_model.MirrorOperation, state, reason string) error {
	details, err := json.Marshal(map[string]any{"operation_id": operation.ID, "direction": operation.Direction, "action": operation.Action, "reason_code": reason})
	if err != nil {
		return err
	}
	result := "failure"
	if state == "unknown" {
		result = "unknown"
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "repository.mirror_updated", Actor: operation.Actor, ScopeType: "repository", ScopeID: operation.RepoID, AncestorIDs: operation.AncestorIDs, ObjectType: "repository", ObjectID: operation.RepoID, ObjectPath: operation.ObjectPath, Result: result, Details: details})
}
