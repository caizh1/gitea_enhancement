// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	actions_module "gitea.dev/modules/actions"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/json"
	packages_module "gitea.dev/modules/packages"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/storage"
	asymkey_service "gitea.dev/services/asymkey"
)

type cleanupGitPath string

func (p cleanupGitPath) RelativePath() string { return string(p) }

// RunResourceCleanups 只处理已提交的删除任务；失败保留定位信息供下次恢复。
func RunResourceCleanups(ctx context.Context) error {
	if db.InTransaction(ctx) {
		return errors.New("不能在业务事务中执行存储清理")
	}
	var tasks []*governance_model.ResourceCleanup
	if err := db.GetEngine(ctx).Where("next_attempt_unix <= ?", time.Now().Unix()).Asc("id").Limit(100).Find(&tasks); err != nil {
		return err
	}
	var failures []error
	for _, task := range tasks {
		if err := RunResourceCleanup(ctx, task.ID); err != nil {
			failures = append(failures, err)
			if _, updateErr := db.GetEngine(ctx).ID(task.ID).Cols("next_attempt_unix").Incr("failures").Update(&governance_model.ResourceCleanup{NextAttemptUnix: time.Now().Add(time.Minute).Unix()}); updateErr != nil {
				failures = append(failures, updateErr)
			}
		}
	}
	return errors.Join(failures...)
}

func RunResourceCleanup(ctx context.Context, id int64) error {
	if db.InTransaction(ctx) {
		return errors.New("不能在业务事务中执行存储清理")
	}
	if err := db.WithTx(ctx, func(ctx context.Context) error {
		affected, err := db.GetEngine(ctx).ID(id).Incr("resource_id", 0).Update(new(governance_model.ResourceCleanup))
		if err != nil || affected == 0 {
			return err
		}
		// 仅锁当前清理任务；耗时存储操作不占用全局治理写锁。
		task := &governance_model.ResourceCleanup{}
		found, err := db.GetEngine(ctx).ID(id).Get(task)
		if err != nil || !found {
			return err
		}
		if task.StorageCleaned {
			return nil
		}
		table := "repository"
		if task.Kind == "package_blob" {
			table = "package_blob"
		} else if task.Kind == "actions_run" {
			table = "action_run"
		} else if task.Kind == "attachment" {
			table = "attachment"
		} else if task.Kind == "group" {
			table = "user"
		} else if task.Kind != "repository" {
			return governance_model.ErrInvalid
		}
		exists, err := db.GetEngine(ctx).Table(table).Where("id = ?", task.ResourceID).Exist()
		if err != nil {
			return err
		}
		if exists {
			return governance_model.ErrConflict
		}
		for _, object := range task.Objects {
			var err error
			if task.Kind == "group" && object.Kind == "git" {
				err = removeEmptyGroupGitDirectory(object)
			} else {
				err = removeCleanupObject(ctx, object)
			}
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("资源清理任务 %d：%w", id, err)
			}
		}
		if task.RewriteKeys {
			if err := asymkey_service.RewriteAllPublicKeys(ctx); err != nil {
				return err
			}
		}
		_, err = db.GetEngine(ctx).ID(id).Cols("storage_cleaned").Update(&governance_model.ResourceCleanup{StorageCleaned: true})
		return err
	}); err != nil {
		return err
	}
	// 正文锁先提交释放，再写完成审计；避免上传与清理形成相反锁序。
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		affected, err := db.GetEngine(ctx).ID(id).Incr("resource_id", 0).Update(new(governance_model.ResourceCleanup))
		if err != nil || affected == 0 {
			return err
		}
		task, has, err := db.GetByID[governance_model.ResourceCleanup](ctx, id)
		if err != nil || !has {
			return err
		}
		if !task.StorageCleaned {
			return governance_model.ErrConflict
		}
		if _, err := db.GetEngine(ctx).Where("kind = ? AND resource_id = ? AND alias = ?", task.Kind, task.ResourceID, false).Delete(new(governance_model.ResourcePath)); err != nil {
			return err
		}
		scope, scopeID := cleanupAuditScope(task)
		if err := governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "resource.cleanup_completed", Actor: task.Actor, ScopeType: scope, ScopeID: scopeID, AncestorIDs: task.AncestorIDs, ObjectType: task.Kind, ObjectID: task.ResourceID, ObjectPath: task.ObjectPath, Result: "success"}); err != nil {
			return err
		}
		_, err = db.GetEngine(ctx).ID(id).Delete(new(governance_model.ResourceCleanup))
		return err
	})
}

// removeEmptyGroupGitDirectory 兼容历史 group 清理任务：只删空目录，绝不递归删除已转出项目的稳定正文。
func removeEmptyGroupGitDirectory(object governance_model.CleanupObject) error {
	if object.Path == "" || path.IsAbs(object.Path) || path.Clean(object.Path) != object.Path || object.Path == "." || object.Path == ".." || strings.HasPrefix(object.Path, "../") || strings.Contains(object.Path, "\\") {
		return governance_model.ErrInvalid
	}
	err := os.Remove(filepath.Join(setting.RepoRootPath, filepath.FromSlash(object.Path)))
	if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST) {
		return nil
	}
	return err
}

func cleanupAuditScope(task *governance_model.ResourceCleanup) (string, int64) {
	if task.ScopeType != "" {
		return task.ScopeType, task.ScopeID
	}
	if task.Kind == "package_blob" || task.Kind == "attachment" || task.Kind == "actions_run" {
		return "instance", 0
	}
	return task.Kind, task.ResourceID
}

func removeCleanupObject(ctx context.Context, object governance_model.CleanupObject) error {
	if object.Path == "" {
		return nil
	}
	if path.IsAbs(object.Path) || path.Clean(object.Path) != object.Path || object.Path == "." || object.Path == ".." || strings.HasPrefix(object.Path, "../") || strings.Contains(object.Path, "\\") {
		return governance_model.ErrInvalid
	}
	if object.Kind == "git" {
		return gitrepo.DeleteRepository(ctx, cleanupGitPath(object.Path))
	}
	if object.Kind == "action_log" {
		return actions_module.RemoveLogs(ctx, object.InStorage, object.Path)
	}
	stores := map[string]storage.ObjectStorage{"archive": storage.RepoArchives, "attachment": storage.Attachments, "repo_avatar": storage.RepoAvatars, "avatar": storage.Avatars, "artifact": storage.ActionsArtifacts, "lfs": storage.LFS, "package_blob": storage.Packages}
	store := stores[object.Kind]
	if store == nil {
		return governance_model.ErrInvalid
	}
	if object.Kind == "package_blob" {
		hash, err := packages_module.RelativePathToKey(object.Path)
		if err != nil {
			return err
		}
		return governance_model.WithPackageContentLocks(ctx, []string{string(hash)}, func(ctx context.Context) error {
			exists, err := db.GetEngine(ctx).Table("package_blob").Where("hash_sha256 = ?", string(hash)).Exist()
			if err != nil || exists {
				return err
			}
			return store.Delete(object.Path)
		})
	}
	if object.Kind == "lfs" {
		oid := strings.ReplaceAll(object.Path, "/", "")
		return governance_model.WithLFSContentLocks(ctx, []string{oid}, func(ctx context.Context) error {
			exists, err := db.GetEngine(ctx).Table("lfs_meta_object").Where("oid = ?", oid).Exist()
			if err != nil || exists {
				return err
			}
			return store.Delete(object.Path)
		})
	}

	return store.Delete(object.Path)
}

// QueueResourceCleanup 在删除事务内同时保存永久审计和可恢复定位信息。
func QueueResourceCleanup(ctx context.Context, task *governance_model.ResourceCleanup) error {
	if !db.InTransaction(ctx) {
		return errors.New("删除清理必须与业务事务一并保存")
	}
	if err := db.Insert(ctx, task); err != nil {
		return err
	}
	details, err := json.Marshal(map[string]any{"cleanup_id": task.ID, "storage_cleanup": "pending"})
	if err != nil {
		return err
	}
	scope, scopeID := cleanupAuditScope(task)
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "resource.deletion_committed", Actor: task.Actor, ScopeType: scope, ScopeID: scopeID, AncestorIDs: task.AncestorIDs, ObjectType: task.Kind, ObjectID: task.ResourceID, ObjectPath: task.ObjectPath, Result: "pending", Details: details})
}
