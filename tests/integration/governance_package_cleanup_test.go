// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	packages_model "gitea.dev/models/packages"
	packages_module "gitea.dev/modules/packages"
	"gitea.dev/modules/storage"
	packages_service "gitea.dev/services/packages"
	cleanup "gitea.dev/services/packages/cleanup"
	repo_service "gitea.dev/services/repository"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

type deletionBoundaryStorage struct {
	storage.ObjectStorage
	beforeDelete func()
}

func (s *deletionBoundaryStorage) Delete(path string) error {
	if s.beforeDelete != nil {
		callback := s.beforeDelete
		s.beforeDelete = nil
		callback()
	}
	return s.ObjectStorage.Delete(path)
}

func TestGovernancePackageCleanupConcurrentUpload(t *testing.T) {
	testConcurrentPackageCleanup(t, false)
}

func TestGovernancePackageCompensationConcurrentUpload(t *testing.T) {
	testConcurrentPackageCleanup(t, true)
}

func testConcurrentPackageCleanup(t *testing.T, compensation bool) {
	t.Helper()
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	pkg, err := packages_model.TryInsertPackage(ctx, &packages_model.Package{OwnerID: 2, Type: packages_model.TypeGeneric, Name: "cleanup-race", LowerName: "cleanup-race"})
	require.NoError(t, err)
	version, err := packages_model.GetOrInsertVersion(ctx, &packages_model.PackageVersion{PackageID: pkg.ID, Version: "1", LowerVersion: "1"})
	require.NoError(t, err)
	body := "软件包清理与新上传共享正文"
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	blob := packages_model.PackageBlob{Size: int64(len(body)), HashSHA256: hash}
	old, _, err := packages_model.GetOrInsertBlob(ctx, &blob)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Exec("UPDATE package_blob SET created_unix = ? WHERE id = ?", time.Now().Add(-48*time.Hour).Unix(), old.ID)
	require.NoError(t, err)
	original := storage.Packages
	store := packages_module.NewContentStore()
	require.NoError(t, store.Save(packages_module.BlobHash256Key(hash), strings.NewReader(body), int64(len(body))))
	uploadDone := make(chan error, 1)
	storage.Packages = &deletionBoundaryStorage{ObjectStorage: original, beforeDelete: func() {
		go func() {
			uploadDone <- db.WithTx(ctx, func(ctx context.Context) error {
				next, _, err := packages_model.GetOrInsertBlob(ctx, &packages_model.PackageBlob{Size: int64(len(body)), HashSHA256: hash})
				if err != nil {
					return err
				}
				if err := store.Save(packages_module.BlobHash256Key(hash), strings.NewReader(body), int64(len(body))); err != nil {
					return err
				}
				_, err = packages_model.TryInsertFile(ctx, &packages_model.PackageFile{VersionID: version.ID, BlobID: next.ID, Name: "sample", LowerName: "sample"})
				return err
			})
		}()
		// 给真实的新上传事务进入删除交界；正确实现应按正文锁排列二者。
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case err := <-uploadDone:
			uploadDone <- err
		case <-timer.C:
		}
	}}
	t.Cleanup(func() { storage.Packages = original })
	if compensation {
		require.NoError(t, packages_model.DeleteBlobByID(ctx, old.ID))
		require.NoError(t, packages_service.RemoveUnreferencedBlobContent(ctx, hash))
	} else {
		require.NoError(t, cleanup.CleanupExpiredData(ctx, 24*time.Hour))
	}
	select {
	case err := <-uploadDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("新上传未完成")
	}
	reader, err := store.OpenBlob(packages_module.BlobHash256Key(hash))
	require.NoError(t, err, "已成功提交的新引用不能被旧清理任务删除正文")
	defer reader.Close()
	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, body, string(actual))
}

func TestGovernancePackageCleanupRecovery(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	body := "软件包清理完成审计失败后仍能可靠恢复"
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	blob, _, err := packages_model.GetOrInsertBlob(ctx, &packages_model.PackageBlob{Size: int64(len(body)), HashSHA256: hash})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Exec("UPDATE package_blob SET created_unix = ? WHERE id = ?", time.Now().Add(-48*time.Hour).Unix(), blob.ID)
	require.NoError(t, err)
	store := packages_module.NewContentStore()
	key := packages_module.BlobHash256Key(hash)
	require.NoError(t, store.Save(key, strings.NewReader(body), int64(len(body))))
	injected := errors.New("软件包清理外层事务失败")
	err = db.WithTx(ctx, func(ctx context.Context) error {
		if err := cleanup.CleanupExpiredData(ctx, 24*time.Hour); err != nil {
			return err
		}
		return injected
	})
	require.ErrorIs(t, err, injected)
	require.NoError(t, store.Has(key), "外层回滚必须保留真实正文")
	_, err = packages_model.GetBlobByID(ctx, blob.ID)
	require.NoError(t, err)
	var task governance_model.ResourceCleanup
	require.NoError(t, db.WithTx(ctx, func(ctx context.Context) error {
		if err := cleanup.CleanupExpiredData(ctx, 24*time.Hour); err != nil {
			return err
		}
		has, err := db.GetEngine(ctx).Where("kind = ? AND resource_id = ?", "package_blob", blob.ID).Get(&task)
		if err != nil {
			return err
		}
		if !has {
			return errors.New("应存在持久化正文清理任务")
		}
		broken := task
		broken.Actor.Transport = ""
		broken.NextAttemptUnix = time.Now().Add(time.Hour).Unix()
		_, err = db.GetEngine(ctx).ID(task.ID).Cols("actor", "next_attempt_unix").Update(&broken)
		return err
	}))
	require.NoError(t, store.Has(key), "提交后崩溃样本尚未执行文件清理")
	originalActor := task.Actor
	require.Error(t, repo_service.RunResourceCleanup(ctx, task.ID), "完成审计故障应保留任务")
	saved, has, err := db.GetByID[governance_model.ResourceCleanup](ctx, task.ID)
	require.NoError(t, err)
	require.True(t, has)
	require.True(t, saved.StorageCleaned)
	require.Error(t, store.Has(key))
	endpoint := "/api/packages/user2/generic/cleanup-recovery/1/file"
	MakeRequest(t, NewRequestWithBody(t, "PUT", endpoint, strings.NewReader(body)).AddBasicAuth("user2"), http.StatusCreated)
	_, err = db.GetEngine(ctx).ID(task.ID).Cols("actor").Update(&governance_model.ResourceCleanup{Actor: originalActor})
	require.NoError(t, err)
	require.NoError(t, repo_service.RunResourceCleanup(ctx, task.ID))
	require.NoError(t, repo_service.RunResourceCleanup(ctx, task.ID))
	response := MakeRequest(t, NewRequest(t, "GET", endpoint).AddBasicAuth("user2"), http.StatusOK)
	require.Equal(t, body, response.Body.String(), "完成审计重试不能重复删除后来新上传的正文")
}
