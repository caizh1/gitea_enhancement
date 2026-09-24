// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package packages_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	packages_model "gitea.dev/models/packages"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	packages_module "gitea.dev/modules/packages"
	"gitea.dev/modules/storage"
	governance_service "gitea.dev/services/governance"
	packages_service "gitea.dev/services/packages"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { unittest.MainTest(m) }

type blockingPackageSaveStorage struct {
	storage.ObjectStorage
	entered chan struct{}
	release chan struct{}
}

func (s *blockingPackageSaveStorage) Save(path string, content io.Reader, size int64) (int64, error) {
	close(s.entered)
	<-s.release
	return s.ObjectStorage.Save(path, content, size)
}

func TestStagePackageBlobDoesNotHoldGovernanceWriteLock(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	data, err := packages_module.CreateHashedBufferFromReader(strings.NewReader("正文写入时不占用治理写锁"))
	require.NoError(t, err)
	defer data.Close()
	original := storage.Packages
	require.NotNil(t, original)
	blocked := &blockingPackageSaveStorage{ObjectStorage: original, entered: make(chan struct{}), release: make(chan struct{})}
	storage.Packages = blocked
	t.Cleanup(func() { storage.Packages = original })
	defer func() {
		select {
		case <-blocked.release:
		default:
			close(blocked.release)
		}
	}()
	staged := make(chan error, 1)
	go func() {
		_, err := packages_service.StagePackageBlob(ctx, data)
		staged <- err
	}()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("对象存储写入未开始")
	}
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- governance_model.WithWrite(ctx, nil, func(context.Context) error { return nil })
	}()
	select {
	case err := <-writeDone:
		require.NoError(t, err, "对象存储写入期间治理写入仍应继续")
	case <-time.After(2 * time.Second):
		t.Fatal("对象存储写入阻塞了治理写锁")
	}
	close(blocked.release)
	require.NoError(t, <-staged)
}

func TestPackageUploadRevokedDuringContentSaveLeavesNoReference(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	parent := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	child, err := governance_service.CreateGroup(ctx, parent, governance_service.GroupOption{Path: "package-upload-child", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, parent, child.ID, governance_service.GroupMemberOption{UserID: 5, Role: governance_model.Owner, Revision: 1}, false))
	owner, err := user_model.GetUserByID(ctx, child.ID)
	require.NoError(t, err)
	doer, err := user_model.GetUserByID(ctx, 5)
	require.NoError(t, err)
	data, err := packages_module.CreateHashedBufferFromReader(strings.NewReader("上传时撤销子组 Owner"))
	require.NoError(t, err)
	defer data.Close()
	original := storage.Packages
	blocked := &blockingPackageSaveStorage{ObjectStorage: original, entered: make(chan struct{}), release: make(chan struct{})}
	storage.Packages = blocked
	t.Cleanup(func() { storage.Packages = original })
	defer func() {
		select {
		case <-blocked.release:
		default:
			close(blocked.release)
		}
	}()
	result := make(chan error, 1)
	go func() {
		_, _, err := packages_service.CreatePackageAndAddFile(ctx,
			&packages_service.PackageCreationInfo{PackageInfo: packages_service.PackageInfo{Owner: owner, PackageType: packages_model.TypeGeneric, Name: "revoked-upload", Version: "1"}, Creator: doer},
			&packages_service.PackageFileCreationInfo{PackageFileInfo: packages_service.PackageFileInfo{Filename: "sample"}, Creator: doer, Data: data, IsLead: true})
		result <- err
	}()
	select {
	case <-blocked.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("对象存储写入未开始")
	}
	require.NoError(t, governance_service.SetGroupMember(ctx, parent, child.ID, governance_service.GroupMemberOption{UserID: 5, Revision: 2}, true))
	close(blocked.release)
	require.ErrorIs(t, <-result, governance_model.ErrForbidden)
	_, err = packages_model.GetPackageByName(ctx, child.ID, packages_model.TypeGeneric, "revoked-upload")
	require.ErrorIs(t, err, packages_model.ErrPackageNotExist, "撤权后不得提交包元数据引用")
	blob := packages_service.NewPackageBlob(data)
	exists, err := packages_model.ExistPackageBlobWithSHA(ctx, blob.HashSHA256)
	require.NoError(t, err)
	require.True(t, exists, "失败上传的孤立正文由延迟清理回收")
}

func TestPackageDeletionRechecksActorAndLifecycle(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	parent := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	child, err := governance_service.CreateGroup(ctx, parent, governance_service.GroupOption{Path: "package-delete-child", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	pkg, err := packages_model.TryInsertPackage(ctx, &packages_model.Package{OwnerID: child.ID, Type: packages_model.TypeGeneric, Name: "protected", LowerName: "protected"})
	require.NoError(t, err)
	version, err := packages_model.GetOrInsertVersion(ctx, &packages_model.PackageVersion{PackageID: pkg.ID, Version: "1", LowerVersion: "1"})
	require.NoError(t, err)
	blob, _, err := packages_model.GetOrInsertBlob(ctx, &packages_model.PackageBlob{HashSHA256: strings.Repeat("c", 64)})
	require.NoError(t, err)
	file, err := packages_model.TryInsertFile(ctx, &packages_model.PackageFile{VersionID: version.ID, BlobID: blob.ID, Name: "sample", LowerName: "sample"})
	require.NoError(t, err)
	parentUser, err := user_model.GetUserByID(ctx, 2)
	require.NoError(t, err)
	sharedUser, err := user_model.GetUserByID(ctx, 20)
	require.NoError(t, err)
	share := &governance_model.Share{ScopeType: "group", ScopeID: child.ID, GroupID: 19, MaxRole: governance_model.Reporter}
	require.NoError(t, db.Insert(ctx, share))

	called := false
	err = packages_model.WithAuthenticatedOwnerWrite(ctx, child.ID, sharedUser, func(context.Context) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, governance_model.ErrForbidden)
	require.False(t, called, "只读共享不能执行底层包写入")
	require.ErrorIs(t, packages_service.DeletePackageFile(ctx, file), governance_model.ErrForbidden, "无身份的文件删除必须拒绝")
	require.ErrorIs(t, packages_service.DeletePackageVersionAndReferences(ctx, version), governance_model.ErrForbidden, "无身份的版本删除必须拒绝")
	require.NoError(t, db.DeleteBeans(ctx, share))
	sharedCtx := context.WithValue(ctx, governance_model.AuditActorContextKey, governance_model.Actor{ID: sharedUser.ID, Kind: "user"})
	require.ErrorIs(t, packages_service.DeletePackageFile(sharedCtx, file), governance_model.ErrForbidden, "撤权后的请求不能继续删除")
	_, has, err := db.GetByID[packages_model.PackageFile](ctx, file.ID)
	require.NoError(t, err)
	require.True(t, has)

	forgedCtx := context.WithValue(ctx, governance_model.AuditActorContextKey, governance_model.Actor{ID: sharedUser.ID, Kind: "user"})
	err = packages_model.WithAuthenticatedOwnerWrite(forgedCtx, child.ID, parentUser, func(context.Context) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, governance_model.ErrForbidden, "请求身份不得冒充另一名 doer")

	_, err = db.GetEngine(ctx).ID(3).Cols("archived").Update(&governance_model.Namespace{Archived: true})
	require.NoError(t, err)
	parentCtx := context.WithValue(ctx, governance_model.AuditActorContextKey, parent)
	require.ErrorIs(t, packages_service.DeletePackageFile(parentCtx, file), governance_model.ErrConflict, "祖先归档后不可删除子组文件")
	require.ErrorIs(t, packages_service.RemovePackageVersion(parentCtx, parentUser, version), governance_model.ErrConflict, "祖先归档后不可删除版本")
	require.ErrorIs(t, packages_service.RemovePackage(parentCtx, parentUser, pkg), governance_model.ErrConflict, "祖先归档后不可删除整包")
	_, has, err = db.GetByID[packages_model.PackageFile](ctx, file.ID)
	require.NoError(t, err)
	require.True(t, has)
	_, err = packages_model.GetVersionByID(ctx, version.ID)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(3).Cols("archived").Update(&governance_model.Namespace{Archived: false})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(parentUser.ID).Cols("is_active").Update(&user_model.User{IsActive: false})
	require.NoError(t, err)
	require.ErrorIs(t, packages_service.DeletePackageFile(parentCtx, file), governance_model.ErrForbidden, "身份停用后即使持有旧请求也不能删除")
	_, err = db.GetEngine(ctx).ID(parentUser.ID).Cols("is_active").Update(&user_model.User{IsActive: true})
	require.NoError(t, err)
	require.NoError(t, packages_service.DeletePackageFile(parentCtx, file), "父组 Owner 可以删除子组包文件")
	_, has, err = db.GetByID[packages_model.PackageFile](ctx, file.ID)
	require.NoError(t, err)
	require.False(t, has)
}
