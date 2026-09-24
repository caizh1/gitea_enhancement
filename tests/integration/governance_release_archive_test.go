// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	lfs_module "gitea.dev/modules/lfs"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/storage"
	api "gitea.dev/modules/structs"
	governance_service "gitea.dev/services/governance"
	repo_service "gitea.dev/services/repository"
	"gitea.dev/tests"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type lfsStatBoundaryStorage struct {
	storage.ObjectStorage
	beforeStat func()
	stats      atomic.Int64
}

type blockedLFSBody struct {
	reader  io.Reader
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockedLFSBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return b.reader.Read(p)
}

func (s *lfsStatBoundaryStorage) Stat(path string) (os.FileInfo, error) {
	if s.stats.Add(1) == 2 && s.beforeStat != nil {
		s.beforeStat()
	}
	return s.ObjectStorage.Stat(path)
}

func TestArchivedGroupDeveloperCannotMutateReleaseAPI(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		ownerToken := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", governance_service.GroupOption{Name: "归档发布组", Path: "release-archive-guard", Visibility: 2}).AddTokenAuth(ownerToken), http.StatusCreated)
		group := DecodeJSON(t, response, &governance_service.GroupState{})
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/orgs/release-archive-guard/repos", api.CreateRepoOption{Name: "app", Private: true}).AddTokenAuth(ownerToken), http.StatusCreated)
		created := DecodeJSON(t, response, &api.Repository{})
		memberURL := fmt.Sprintf("/api/v1/governance/groups/%d/members/4", group.ID)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", memberURL, governance_service.GroupMemberOption{Role: governance_model.Developer, Revision: group.Revision}).AddTokenAuth(ownerToken), http.StatusNoContent)

		doer, err := user_model.GetUserByID(t.Context(), 4)
		require.NoError(t, err)
		repository, err := repo_model.GetRepositoryByID(t.Context(), created.ID)
		require.NoError(t, err)
		permission, err := access_model.GetDoerRepoPermission(t.Context(), repository, doer)
		require.NoError(t, err)
		require.True(t, permission.CanWrite(unit.TypeReleases), "反证主体须实际拥有发布写权")

		release := &repo_model.Release{RepoID: created.ID, PublisherID: 2, TagName: "v1", LowerTagName: "v1", Title: "原发布", IsDraft: true}
		require.NoError(t, db.Insert(t.Context(), release))
		attach := &repo_model.Attachment{RepoID: created.ID, ReleaseID: release.ID, UploaderID: 2, UUID: uuid.NewString(), Name: "original.txt"}
		require.NoError(t, db.Insert(t.Context(), attach))

		archiveURL := fmt.Sprintf("/api/v1/governance/groups/%d/archive", group.ID)
		response = MakeRequest(t, NewRequest(t, "GET", archiveURL).AddTokenAuth(ownerToken), http.StatusOK)
		impact := DecodeJSON(t, response, &governance_service.GroupArchiveImpact{})
		MakeRequest(t, NewRequestWithJSON(t, "PUT", archiveURL, governance_service.GroupArchiveOption{Archived: true, Revision: impact.Revision}).AddTokenAuth(ownerToken), http.StatusOK)
		content := "归档项目不能接收 LFS 对象"
		pointer, err := lfs_module.GeneratePointer(strings.NewReader(content))
		require.NoError(t, err)
		lfsURL := fmt.Sprintf("/release-archive-guard/app.git/info/lfs/objects/%s/%d", pointer.Oid, pointer.Size)
		MakeRequest(t, NewRequestWithBody(t, "PUT", lfsURL, strings.NewReader(content)).AddBasicAuth("user4"), http.StatusLocked)
		developerToken := getUserToken(t, "user4", auth_model.AccessTokenScopeAll)
		base := "/api/v1/repos/release-archive-guard/app/releases"
		asset := fmt.Sprintf("%s/%d/assets/%d", base, release.ID, attach.ID)
		for _, request := range []*RequestWrapper{
			NewRequestWithJSON(t, "POST", base, api.CreateReleaseOption{TagName: "v2", Title: "越权发布"}),
			NewRequestWithJSON(t, "PATCH", fmt.Sprintf("%s/%d", base, release.ID), api.EditReleaseOption{Title: "越权修改"}),
			NewRequest(t, "DELETE", fmt.Sprintf("%s/%d", base, release.ID)),
			NewRequest(t, "POST", fmt.Sprintf("%s/%d/assets?name=unauthorized.txt", base, release.ID)),
			NewRequestWithJSON(t, "PATCH", asset, api.EditAttachmentOptions{Name: "unauthorized.txt"}),
			NewRequest(t, "DELETE", asset),
			NewRequest(t, "DELETE", base+"/tags/v1"),
		} {
			MakeRequest(t, request.AddTokenAuth(developerToken), http.StatusLocked)
		}
		current, err := repo_model.GetReleaseByID(t.Context(), release.ID)
		require.NoError(t, err)
		require.Equal(t, "原发布", current.Title)
		currentAttach, err := repo_model.GetAttachmentByID(t.Context(), attach.ID)
		require.NoError(t, err)
		require.Equal(t, "original.txt", currentAttach.Name)
	})
}

func TestLFSSettingsDeletePreservesOtherRepositoryReference(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	content := "共享 LFS 对象保留"
	pointer, err := lfs_module.GeneratePointer(strings.NewReader(content))
	require.NoError(t, err)
	store := lfs_module.NewContentStore()
	require.NoError(t, store.Put(pointer, strings.NewReader(content)))
	for _, repoID := range []int64{1, 2} {
		_, err := git_model.NewLFSMetaObject(t.Context(), repoID, pointer)
		require.NoError(t, err)
	}
	session := loginUser(t, "user2")
	for _, repoName := range []string{"repo1", "repo2"} {
		path := fmt.Sprintf("/user2/%s/settings/lfs/delete/%s", repoName, pointer.Oid)
		session.MakeRequest(t, NewRequest(t, "POST", path), http.StatusSeeOther)
		if repoName == "repo1" {
			exists, err := store.Exists(pointer)
			require.NoError(t, err)
			require.True(t, exists)
		}
	}
	exists, err := store.Exists(pointer)
	require.NoError(t, err)
	require.False(t, exists)
}

func TestLFSConcurrentSameObjectStagingIsNotCleanedByRejectedUpload(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	content := "同一对象并发暂存"
	pointer, err := lfs_module.GeneratePointer(strings.NewReader(content))
	require.NoError(t, err)
	original := storage.LFS
	staged := &lfsStatBoundaryStorage{ObjectStorage: original}
	storage.LFS = staged
	defer func() { storage.LFS = original }()
	interleaved := false
	staged.beforeStat = func() {
		interleaved = true
		require.NoError(t, governance_model.WithLFSContentLocks(t.Context(), []string{pointer.Oid}, func(context.Context) error {
			_, err := original.Save(pointer.RelativePath(), strings.NewReader(content), pointer.Size)
			return err
		}))
	}
	lfsURL := fmt.Sprintf("/user2/repo1.git/info/lfs/objects/%s/%d", pointer.Oid, pointer.Size)
	MakeRequest(t, NewRequestWithBody(t, "PUT", lfsURL, strings.NewReader(content)).AddBasicAuth("user2"), http.StatusConflict)
	require.True(t, interleaved)
	_, err = git_model.GetLFSMetaObjectByOid(t.Context(), 1, pointer.Oid)
	require.ErrorIs(t, err, git_model.ErrLFSObjectNotExist)
	exists, err := lfs_module.NewContentStore().Exists(pointer)
	require.NoError(t, err)
	require.True(t, exists, "较早请求拒绝后不能删掉另一请求暂存的同 OID 对象")
	require.NoError(t, original.Delete(pointer.RelativePath()))
}

func TestLFSSlowSameOIDUploadDoesNotHoldGovernanceWriteLock(t *testing.T) {
	if !setting.Database.Type.IsPostgreSQL() {
		t.Skip("需要 PostgreSQL 的独立行锁验证")
	}
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	content := "同 OID 慢上传不能占用全局治理锁"
	pointer, err := lfs_module.GeneratePointer(strings.NewReader(content))
	require.NoError(t, err)
	original := storage.LFS
	staged := &lfsStatBoundaryStorage{ObjectStorage: original}
	storage.LFS = staged
	defer func() { storage.LFS = original }()
	body := &blockedLFSBody{reader: strings.NewReader(content), started: make(chan struct{}), release: make(chan struct{})}
	lfsURL := fmt.Sprintf("/user2/repo1.git/info/lfs/objects/%s/%d", pointer.Oid, pointer.Size)
	secondDone := make(chan struct{})
	staged.beforeStat = func() {
		go func() {
			defer close(secondDone)
			MakeRequest(t, NewRequestWithBody(t, "PUT", lfsURL, body).AddBasicAuth("user2"), http.StatusOK)
		}()
		<-body.started
	}
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		MakeRequest(t, NewRequestWithBody(t, "PUT", lfsURL, strings.NewReader(content)).AddBasicAuth("user2"), http.StatusConflict)
	}()
	writeDone := make(chan error, 1)
	select {
	case <-body.started:
	case <-time.After(3 * time.Second):
		close(body.release)
		t.Fatal("第二个上传未进入正文屏障")
	}
	deadline := time.Now().Add(time.Second)
	for {
		err := db.WithTx(t.Context(), func(ctx context.Context) error {
			_, err := db.GetEngine(ctx).Exec("SELECT revision FROM governance_write_lock WHERE id = 1 FOR UPDATE NOWAIT")
			return err
		})
		if err != nil {
			require.ErrorContains(t, err, "could not obtain lock")
			break
		}
		if time.Now().After(deadline) {
			close(body.release)
			t.Fatal("首个上传未进入最终治理写锁")
		}
		time.Sleep(5 * time.Millisecond)
	}
	go func() {
		writeDone <- governance_model.WithWrite(t.Context(), nil, func(context.Context) error { return nil })
	}()
	select {
	case err := <-writeDone:
		require.NoError(t, err)
	case <-time.After(time.Second):
		close(body.release)
		t.Fatal("治理写锁随慢客户端正文阻塞超过 1 秒")
	}
	close(body.release)
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("首个上传未结束")
	}
	select {
	case <-secondDone:
	case <-time.After(5 * time.Second):
		t.Fatal("第二个上传未结束")
	}
	_, err = git_model.GetLFSMetaObjectByOid(t.Context(), 1, pointer.Oid)
	require.NoError(t, err)
	var cleanups []*governance_model.ResourceCleanup
	require.NoError(t, db.GetEngine(t.Context()).Where("kind = ?", "lfs_upload").Find(&cleanups))
	require.Len(t, cleanups, 1)
	require.NoError(t, repo_service.RunResourceCleanup(t.Context(), cleanups[0].ID))
	exists, err := lfs_module.NewContentStore().Exists(pointer)
	require.NoError(t, err)
	require.True(t, exists, "延迟清理不能删除已建立的全局引用")
}

func TestLFSDelayedCleanupDoesNotDeleteRestagedContent(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	content := "旧清理不能删除新一轮同哈希暂存"
	pointer, err := lfs_module.GeneratePointer(strings.NewReader(content))
	require.NoError(t, err)
	store := lfs_module.NewContentStore()
	require.NoError(t, store.Put(pointer, strings.NewReader(content)))
	var firstRevision int64
	require.NoError(t, governance_model.WithLFSContentLocks(t.Context(), []string{pointer.Oid}, func(ctx context.Context) error {
		var stamp governance_model.LFSContentLock
		_, err := db.GetEngine(ctx).Where("oid = ?", pointer.Oid).Get(&stamp)
		firstRevision = stamp.Revision
		return err
	}))
	cleanup := &governance_model.ResourceCleanup{Kind: "lfs_upload", ResourceID: 1, Objects: []governance_model.CleanupObject{{Kind: "lfs", Path: pointer.RelativePath(), Revision: firstRevision}}, ScopeType: "instance", Actor: governance_model.AuditActor(t.Context())}
	require.NoError(t, db.Insert(t.Context(), cleanup))
	require.NoError(t, governance_model.WithLFSContentLocks(t.Context(), []string{pointer.Oid}, func(context.Context) error { return nil }))
	require.NoError(t, repo_service.RunResourceCleanup(t.Context(), cleanup.ID))
	exists, err := store.Exists(pointer)
	require.NoError(t, err)
	require.True(t, exists)
	require.NoError(t, storage.LFS.Delete(pointer.RelativePath()))
}

func TestLFSDelayedCleanupDeletesUnreferencedContentOnce(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	content := "失败暂存最终删除且重复执行安全"
	pointer, err := lfs_module.GeneratePointer(strings.NewReader(content))
	require.NoError(t, err)
	store := lfs_module.NewContentStore()
	require.NoError(t, store.Put(pointer, strings.NewReader(content)))
	var revision int64
	require.NoError(t, governance_model.WithLFSContentLocks(t.Context(), []string{pointer.Oid}, func(ctx context.Context) error {
		var stamp governance_model.LFSContentLock
		_, err := db.GetEngine(ctx).Where("oid = ?", pointer.Oid).Get(&stamp)
		revision = stamp.Revision
		return err
	}))
	cleanup := &governance_model.ResourceCleanup{Kind: "lfs_upload", ResourceID: 2, Objects: []governance_model.CleanupObject{{Kind: "lfs", Path: pointer.RelativePath(), Revision: revision}}, ScopeType: "instance", Actor: governance_model.AuditActor(t.Context())}
	require.NoError(t, db.Insert(t.Context(), cleanup))
	require.NoError(t, repo_service.RunResourceCleanup(t.Context(), cleanup.ID))
	require.NoError(t, repo_service.RunResourceCleanup(t.Context(), cleanup.ID))
	exists, err := store.Exists(pointer)
	require.NoError(t, err)
	require.False(t, exists)
}

func TestLFSBothRejectedStagesEventuallyCleanOrphan(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	repo, err := repo_model.GetRepositoryByID(t.Context(), 1)
	require.NoError(t, err)
	content := "首个创建和第二个复用均遭归档拒绝"
	pointer, err := lfs_module.GeneratePointer(strings.NewReader(content))
	require.NoError(t, err)
	original := storage.LFS
	staged := &lfsStatBoundaryStorage{ObjectStorage: original}
	storage.LFS = staged
	defer func() { storage.LFS = original }()
	staged.beforeStat = func() { require.NoError(t, repo_model.SetArchiveRepoState(t.Context(), repo, true)) }
	lfsURL := fmt.Sprintf("/user2/repo1.git/info/lfs/objects/%s/%d", pointer.Oid, pointer.Size)
	for attempt := range 2 {
		if attempt > 0 {
			require.NoError(t, repo_model.SetArchiveRepoState(t.Context(), repo, false))
			staged.stats.Store(0)
		}
		MakeRequest(t, NewRequestWithBody(t, "PUT", lfsURL, strings.NewReader(content)).AddBasicAuth("user2"), http.StatusLocked)
	}
	var cleanups []*governance_model.ResourceCleanup
	require.NoError(t, db.GetEngine(t.Context()).Where("kind = ?", "lfs_upload").Asc("id").Find(&cleanups))
	require.Len(t, cleanups, 2)
	require.Less(t, cleanups[0].Objects[0].Revision, cleanups[1].Objects[0].Revision)
	for _, cleanup := range cleanups {
		require.NoError(t, repo_service.RunResourceCleanup(t.Context(), cleanup.ID))
	}
	exists, err := lfs_module.NewContentStore().Exists(pointer)
	require.NoError(t, err)
	require.False(t, exists, "两次拒绝后最后版本须由延迟任务清理")
}
