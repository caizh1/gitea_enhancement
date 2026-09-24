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
	user_model "gitea.dev/models/user"
	packages_module "gitea.dev/modules/packages"
	"gitea.dev/modules/storage"
	governance_service "gitea.dev/services/governance"
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

type blockedPackageSaveStorage struct {
	storage.ObjectStorage
	entered chan struct{}
	release chan struct{}
}

func (s *blockedPackageSaveStorage) Save(path string, body io.Reader, size int64) (int64, error) {
	s.entered <- struct{}{}
	<-s.release
	return s.ObjectStorage.Save(path, body, size)
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

func TestPackageFailedStageRefreshSurvivesConcurrentCleanup(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	body := "失败暂存重新上传期间，清理不可删除正文"
	data, err := packages_module.CreateHashedBufferFromReader(strings.NewReader(body))
	require.NoError(t, err)
	defer data.Close()
	blob, _, err := packages_model.GetOrInsertBlob(ctx, packages_service.NewPackageBlob(data))
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Exec("UPDATE package_blob SET created_unix = ? WHERE id = ?", time.Now().Add(-48*time.Hour).Unix(), blob.ID)
	require.NoError(t, err)
	key := packages_module.BlobHash256Key(blob.HashSHA256)
	store := packages_module.NewContentStore()
	require.Error(t, store.Has(key), "模拟上次对象存储失败，只有旧暂存数据库行")
	original := storage.Packages
	blocked := &blockedPackageSaveStorage{ObjectStorage: original, entered: make(chan struct{}), release: make(chan struct{})}
	storage.Packages = blocked
	t.Cleanup(func() { storage.Packages = original })
	defer func() {
		select {
		case <-blocked.release:
		default:
			close(blocked.release)
		}
	}()
	uploadDone := make(chan error, 1)
	go func() {
		_, err := packages_service.StagePackageBlob(ctx, data)
		uploadDone <- err
	}()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("MinIO 写入未进入阻断点")
	}
	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- cleanup.CleanupExpiredData(ctx, 0) }()
	select {
	case err := <-cleanupDone:
		require.NoError(t, err, "对象存储 Save 不得持有治理写锁")
	case <-time.After(5 * time.Second):
		t.Fatal("对象存储 Save 阻塞治理清理事务")
	}
	_, err = packages_model.GetBlobByID(ctx, blob.ID)
	require.NoError(t, err, "刷新后的暂存行不能被过期清理删除")
	close(blocked.release)
	require.NoError(t, <-uploadDone)
	reader, err := store.OpenBlob(key)
	require.NoError(t, err)
	defer reader.Close()
	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, body, string(actual))
}

func TestPackageConcurrentIdenticalStage(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	body := "两个上传者同时持久化同一哈希"
	original := storage.Packages
	blocked := &blockedPackageSaveStorage{ObjectStorage: original, entered: make(chan struct{}, 2), release: make(chan struct{})}
	storage.Packages = blocked
	t.Cleanup(func() { storage.Packages = original })
	defer func() {
		select {
		case <-blocked.release:
		default:
			close(blocked.release)
		}
	}()
	type stageResult struct {
		blob *packages_model.PackageBlob
		err  error
	}
	results := make(chan stageResult, 2)
	for range 2 {
		data, err := packages_module.CreateHashedBufferFromReader(strings.NewReader(body))
		require.NoError(t, err)
		defer data.Close()
		go func() {
			blob, err := packages_service.StagePackageBlob(ctx, data)
			results <- stageResult{blob: blob, err: err}
		}()
	}
	for range 2 {
		select {
		case <-blocked.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("两个同哈希上传未同时进入 MinIO Save")
		}
	}
	close(blocked.release)
	first := <-results
	second := <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.Equal(t, first.blob.ID, second.blob.ID)
	store := packages_module.NewContentStore()
	reader, err := store.OpenBlob(packages_module.BlobHash256Key(first.blob.HashSHA256))
	require.NoError(t, err)
	defer reader.Close()
	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, body, string(actual))
}

func TestPackageArchiveDuringStageRejectsFinalReference(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "package-stage-archive", Visibility: 2})
	require.NoError(t, err)
	owner, err := user_model.GetUserByID(ctx, group.ID)
	require.NoError(t, err)
	doer, err := user_model.GetUserByID(ctx, actor.ID)
	require.NoError(t, err)
	data, err := packages_module.CreateHashedBufferFromReader(strings.NewReader("归档在正文上传期间提交"))
	require.NoError(t, err)
	defer data.Close()
	original := storage.Packages
	blocked := &blockedPackageSaveStorage{ObjectStorage: original, entered: make(chan struct{}), release: make(chan struct{})}
	storage.Packages = blocked
	t.Cleanup(func() { storage.Packages = original })
	defer func() {
		select {
		case <-blocked.release:
		default:
			close(blocked.release)
		}
	}()
	uploadDone := make(chan error, 1)
	go func() {
		_, _, err := packages_service.CreatePackageAndAddFile(ctx,
			&packages_service.PackageCreationInfo{PackageInfo: packages_service.PackageInfo{Owner: owner, PackageType: packages_model.TypeGeneric, Name: "archive-race", Version: "1"}, Creator: doer},
			&packages_service.PackageFileCreationInfo{PackageFileInfo: packages_service.PackageFileInfo{Filename: "sample"}, Creator: doer, Data: data})
		uploadDone <- err
	}()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("MinIO 写入未进入阻断点")
	}
	_, err = governance_service.SetGroupArchiveState(ctx, actor, group.ID, governance_service.GroupArchiveOption{Archived: true, Revision: group.Revision})
	require.NoError(t, err, "归档不能被正在运行的 MinIO Save 阻塞")
	close(blocked.release)
	require.ErrorIs(t, <-uploadDone, governance_model.ErrConflict)
	_, err = packages_model.GetPackageByName(ctx, group.ID, packages_model.TypeGeneric, "archive-race")
	require.ErrorIs(t, err, packages_model.ErrPackageNotExist, "归档后不得留下可读取的包引用")
}

func TestPackageQueuedCleanupSkipsRestagedReference(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	body := "排队清理后同哈希重新引用"
	data, err := packages_module.CreateHashedBufferFromReader(strings.NewReader(body))
	require.NoError(t, err)
	defer data.Close()
	old, _, err := packages_model.GetOrInsertBlob(ctx, packages_service.NewPackageBlob(data))
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Exec("UPDATE package_blob SET created_unix = ? WHERE id = ?", time.Now().Add(-48*time.Hour).Unix(), old.ID)
	require.NoError(t, err)
	store := packages_module.NewContentStore()
	key := packages_module.BlobHash256Key(old.HashSHA256)
	require.NoError(t, store.Save(key, strings.NewReader(body), int64(len(body))))
	var queued governance_model.ResourceCleanup
	require.NoError(t, db.WithTx(ctx, func(ctx context.Context) error {
		if err := cleanup.CleanupExpiredData(ctx, 0); err != nil {
			return err
		}
		found, err := db.GetEngine(ctx).Where("kind = ? AND resource_id = ?", "package_blob", old.ID).Get(&queued)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("旧 blob 清理任务未排队")
		}
		return nil
	}))
	next, err := packages_service.StagePackageBlob(ctx, data)
	require.NoError(t, err)
	require.NotEqual(t, old.ID, next.ID)
	pkg, err := packages_model.TryInsertPackage(ctx, &packages_model.Package{OwnerID: 2, Type: packages_model.TypeGeneric, Name: "restaged-cleanup", LowerName: "restaged-cleanup"})
	require.NoError(t, err)
	version, err := packages_model.GetOrInsertVersion(ctx, &packages_model.PackageVersion{PackageID: pkg.ID, Version: "1", LowerVersion: "1"})
	require.NoError(t, err)
	_, err = packages_model.TryInsertFile(ctx, &packages_model.PackageFile{VersionID: version.ID, BlobID: next.ID, Name: "sample", LowerName: "sample"})
	require.NoError(t, err)
	require.NoError(t, repo_service.RunResourceCleanup(ctx, queued.ID))
	reader, err := store.OpenBlob(key)
	require.NoError(t, err, "旧任务不得删除新引用的对象")
	defer reader.Close()
	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, body, string(actual))
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
