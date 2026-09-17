// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	org_model "gitea.dev/models/organization"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/lfs"
	org_service "gitea.dev/services/org"
	repo_service "gitea.dev/services/repository"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceRepositoryDeletionOuterRollback(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	repo, err := repo_model.GetRepositoryByID(t.Context(), 1)
	require.NoError(t, err)
	exists, err := gitrepo.IsRepositoryExist(t.Context(), repo)
	require.NoError(t, err)
	require.True(t, exists)
	injected := errors.New("群组外层删除事务故障样本")
	err = db.WithTx(t.Context(), func(ctx context.Context) error {
		if err := repo_service.DeleteRepositoryDirectly(ctx, repo.ID); err != nil {
			return err
		}
		return injected
	})
	require.ErrorIs(t, err, injected)
	_, err = repo_model.GetRepositoryByID(t.Context(), repo.ID)
	require.NoError(t, err, "外层事务已恢复仓库数据库记录")
	exists, err = gitrepo.IsRepositoryExist(t.Context(), repo)
	require.NoError(t, err)
	require.True(t, exists, "数据库回滚后真实仓库文件必须仍然存在")
}

func TestGovernanceRepositoryCleanupRecovery(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	repo, err := repo_model.GetRepositoryByID(t.Context(), 1)
	require.NoError(t, err)
	require.NoError(t, db.WithTx(t.Context(), func(ctx context.Context) error { return repo_service.DeleteRepositoryDirectly(ctx, repo.ID) }))
	var task governance_model.ResourceCleanup
	has, err := db.GetEngine(t.Context()).Where("kind = ? AND resource_id = ?", "repository", repo.ID).Get(&task)
	require.NoError(t, err)
	require.True(t, has, "外层提交后保留清理快照，模拟进程在清理前退出")
	exists, err := gitrepo.IsRepositoryExist(t.Context(), repo)
	require.NoError(t, err)
	require.True(t, exists)
	_, err = governance_model.RegisterNativeRepository(t.Context(), 99999, repo.OwnerID, repo.Name)
	require.Error(t, err, "清理完成前不能重用路径")
	_, err = db.GetEngine(t.Context()).Exec("CREATE TRIGGER governance_cleanup_audit_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'resource.cleanup_completed' BEGIN SELECT RAISE(ABORT, '清理完成审计故障样本'); END")
	require.NoError(t, err)
	defer func() {
		_, err := db.GetEngine(context.WithoutCancel(t.Context())).Exec("DROP TRIGGER IF EXISTS governance_cleanup_audit_fail")
		require.NoError(t, err)
	}()
	require.Error(t, repo_service.RunResourceCleanup(t.Context(), task.ID))
	count, err := db.GetEngine(t.Context()).ID(task.ID).Count(new(governance_model.ResourceCleanup))
	require.NoError(t, err)
	require.EqualValues(t, 1, count, "结果落盘失败保留清理任务")
	_, err = db.GetEngine(t.Context()).Exec("DROP TRIGGER governance_cleanup_audit_fail")
	require.NoError(t, err)
	require.NoError(t, repo_service.RunResourceCleanup(t.Context(), task.ID))
	require.NoError(t, repo_service.RunResourceCleanup(t.Context(), task.ID), "重复恢复不能删除新资源")
	exists, err = gitrepo.IsRepositoryExist(t.Context(), repo)
	require.NoError(t, err)
	require.False(t, exists)
	count, err = db.GetEngine(t.Context()).ID(task.ID).Count(new(governance_model.ResourceCleanup))
	require.NoError(t, err)
	require.Zero(t, count)
	_, err = governance_model.RegisterNativeRepository(t.Context(), 99999, repo.OwnerID, repo.Name)
	require.NoError(t, err, "清理完成后才释放规范路径")
}

func TestGovernanceGroupDeletionOuterRollback(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	org, err := org_model.GetOrgByID(t.Context(), 6)
	require.NoError(t, err)
	directory := user_model.UserPath(org.Name)
	require.NoError(t, os.MkdirAll(directory, 0o755))
	marker := filepath.Join(directory, "群组回滚证据")
	require.NoError(t, os.WriteFile(marker, []byte("外层回滚必须保留"), 0o600))
	injected := errors.New("群组删除外层故障")
	err = db.WithTx(t.Context(), func(ctx context.Context) error {
		if err := org_service.DeleteOrganization(ctx, org, false); err != nil {
			return err
		}
		return injected
	})
	require.ErrorIs(t, err, injected)
	_, err = org_model.GetOrgByID(t.Context(), org.ID)
	require.NoError(t, err)
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "外层回滚必须保留", string(data))
	count, err := db.GetEngine(t.Context()).Where("kind = ? AND resource_id = ?", "group", org.ID).Count(new(governance_model.ResourceCleanup))
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestGovernanceCleanupPreservesNewLFSReference(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	content := "删除等待期间新增的共享 LFS 引用必须保留正文"
	oid := storeObjectInRepo(t, 1, content)
	pointer, err := lfs.GeneratePointer(strings.NewReader(content))
	require.NoError(t, err)
	require.Equal(t, oid, pointer.Oid)
	require.NoError(t, db.WithTx(t.Context(), func(ctx context.Context) error { return repo_service.DeleteRepositoryDirectly(ctx, 1) }))
	var task governance_model.ResourceCleanup
	has, err := db.GetEngine(t.Context()).Where("kind = ? AND resource_id = ?", "repository", 1).Get(&task)
	require.NoError(t, err)
	require.True(t, has)
	_, err = git_model.NewLFSMetaObject(t.Context(), 2, pointer)
	require.NoError(t, err)
	require.NoError(t, repo_service.RunResourceCleanup(t.Context(), task.ID))
	exists, err := lfs.NewContentStore().Exists(pointer)
	require.NoError(t, err)
	require.True(t, exists, "延迟清理必须重新检查新仓库引用")
	require.NoError(t, repo_service.DeleteRepositoryDirectly(t.Context(), 2))
	exists, err = lfs.NewContentStore().Exists(pointer)
	require.NoError(t, err)
	require.False(t, exists, "最后一个引用删除后完成正文清理")
}

func TestGovernanceDeletionRechecksActorAndAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	repo, err := repo_model.GetRepositoryByID(t.Context(), 1)
	require.NoError(t, err)
	user, err := user_model.GetUserByID(t.Context(), 2)
	require.NoError(t, err)
	_, err = db.GetEngine(t.Context()).ID(user.ID).Cols("is_active").Update(&user_model.User{IsActive: false})
	require.NoError(t, err)
	require.ErrorIs(t, repo_service.DeleteRepository(t.Context(), user, repo, false), governance_model.ErrForbidden, "不能用排队前的用户状态继续删除")
	_, err = db.GetEngine(t.Context()).ID(user.ID).Cols("is_active").Update(&user_model.User{IsActive: true})
	require.NoError(t, err)
	_, err = db.GetEngine(t.Context()).Exec("CREATE TRIGGER governance_delete_audit_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'resource.deletion_committed' BEGIN SELECT RAISE(ABORT, '删除事务审计故障样本'); END")
	require.NoError(t, err)
	defer func() {
		_, err := db.GetEngine(context.WithoutCancel(t.Context())).Exec("DROP TRIGGER IF EXISTS governance_delete_audit_fail")
		require.NoError(t, err)
	}()
	actor := governance_model.Actor{ID: user.ID, Name: user.Name, Kind: "user", Transport: "api"}
	require.Error(t, repo_service.DeleteRepositoryDirectly(governance_model.WithAuditActor(t.Context(), actor), repo.ID))
	_, err = repo_model.GetRepositoryByID(t.Context(), repo.ID)
	require.NoError(t, err)
	exists, err := gitrepo.IsRepositoryExist(t.Context(), repo)
	require.NoError(t, err)
	require.True(t, exists, "删除审计失败时数据库和真实仓库均应保留")
	count, err := db.GetEngine(t.Context()).Where("kind = ? AND resource_id = ?", "repository", repo.ID).Count(new(governance_model.ResourceCleanup))
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestGovernanceLFSUploadAndSharedCleanup(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	content := "真实上传共享对象，删除仓库不能误删其他引用"
	pointer, err := lfs.GeneratePointer(strings.NewReader(content))
	require.NoError(t, err)
	upload := func(repoName, body string, status int) {
		path := fmt.Sprintf("/user2/%s.git/info/lfs/objects/%s/%d", repoName, pointer.Oid, pointer.Size)
		MakeRequest(t, NewRequestWithBody(t, "PUT", path, strings.NewReader(body)).AddBasicAuth("user2"), status)
	}
	upload("repo1", content, http.StatusOK)
	upload("repo2", strings.Repeat("x", len(content)), http.StatusUnprocessableEntity)
	_, err = git_model.GetLFSMetaObjectByOid(t.Context(), 2, pointer.Oid)
	require.ErrorIs(t, err, git_model.ErrLFSObjectNotExist, "跨仓库错误正文不能建立引用")
	upload("repo2", content, http.StatusOK)
	_, err = git_model.GetLFSMetaObjectByOid(t.Context(), 2, pointer.Oid)
	require.NoError(t, err)
	require.NoError(t, repo_service.DeleteRepositoryDirectly(t.Context(), 1))
	response := MakeRequest(t, NewRequest(t, "GET", "/user2/repo2.git/info/lfs/objects/"+pointer.Oid+"/共享文件").AddBasicAuth("user2"), http.StatusOK)
	require.Equal(t, content, response.Body.String())
	require.NoError(t, repo_service.DeleteRepositoryDirectly(t.Context(), 2))
	exists, err := lfs.NewContentStore().Exists(pointer)
	require.NoError(t, err)
	require.False(t, exists)
}
