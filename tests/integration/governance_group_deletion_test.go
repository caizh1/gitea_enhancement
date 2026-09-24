// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	packages_model "gitea.dev/models/packages"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/git"
	"gitea.dev/modules/git/gitcmd"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	governance_service "gitea.dev/services/governance"
	repo_service "gitea.dev/services/repository"

	"github.com/stretchr/testify/require"
)

func TestGovernanceGroupDeletionRealGit(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		ctx := t.Context()
		require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", governance_service.GroupOption{Name: "延迟删除", Path: "deletion-real", Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
		root := DecodeJSON(t, response, &governance_service.GroupState{})
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", governance_service.GroupOption{Name: "子组", Path: "child", ParentID: root.ID, Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
		child := DecodeJSON(t, response, &governance_service.GroupState{})
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/orgs/deletion-real/child/repos", api.CreateRepoOption{Name: "firmware", Private: true, AutoInit: true}).AddTokenAuth(token), http.StatusCreated)
		created := DecodeJSON(t, response, &api.Repository{})
		repo, err := repo_model.GetRepositoryByID(ctx, created.ID)
		require.NoError(t, err)
		pkg, err := packages_model.TryInsertPackage(ctx, &packages_model.Package{OwnerID: child.ID, Type: packages_model.TypeGeneric, Name: "sample", LowerName: "sample"})
		require.NoError(t, err)
		_, err = packages_model.GetOrInsertVersion(ctx, &packages_model.PackageVersion{PackageID: pkg.ID, Version: "1", LowerVersion: "1"})
		require.NoError(t, err)
		cloneURL := *baseURL
		cloneURL.User, cloneURL.Path = url.UserPassword("user2", "password"), "/deletion-real/child/firmware.git"
		local := filepath.Join(t.TempDir(), "待删除读取")
		doGitClone(local, &cloneURL)(t)
		before, err := git.GetFullCommitID(ctx, local, "HEAD")
		require.NoError(t, err)
		nativeResponse := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", governance_service.GroupOption{Name: "原生删除", Path: "native-deletion-real", Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
		nativeGroup := DecodeJSON(t, nativeResponse, &governance_service.GroupState{})
		MakeRequest(t, NewRequest(t, "DELETE", "/api/v1/orgs/native-deletion-real").AddTokenAuth(token), http.StatusNoContent)
		nativeState, err := governance_model.GetNamespace(ctx, nativeGroup.ID)
		require.NoError(t, err, "原生删除入口同样进入保留期")
		require.NotZero(t, nativeState.DeleteAfter)
		endpoint := fmt.Sprintf("/api/v1/governance/groups/%d", root.ID)
		option := governance_service.GroupDeletionOption{Revision: root.Revision, ConfirmationPath: root.FullPath}
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint+"/delete-permanently", option).AddTokenAuth(token), http.StatusNotFound)
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint+"/deletion", option).AddTokenAuth(token), http.StatusOK)
		root = DecodeJSON(t, response, &governance_service.GroupState{})
		_, err = packages_model.GetOrInsertVersion(ctx, &packages_model.PackageVersion{PackageID: pkg.ID, Version: "2", LowerVersion: "2"})
		require.ErrorIs(t, err, governance_model.ErrConflict, "排队的软件包写入不能越过待删除状态")
		page := loginUser(t, "user2").MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/governance/groups/%d?tab=settings", root.ID)), http.StatusOK)
		require.Contains(t, page.Body.String(), "恢复或永久删除群组")
		doGitCheckoutWriteFileCommit(localGitAddCommitOptions{LocalRepoPath: local, CheckoutBranch: created.DefaultBranch, TreeFilePath: "pending.txt", TreeFileContent: "保留期不能写入\n"})(t)
		require.Error(t, gitcmd.NewCommand("push", "origin").AddDynamicArguments(created.DefaultBranch).WithDir(local).RunWithStderr(ctx))
		actual, err := git.GetFullCommitID(ctx, repo.RepoPath(), "HEAD")
		require.NoError(t, err)
		require.Equal(t, before, actual)
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint+"/restore", governance_service.GroupDeletionOption{Revision: root.Revision, ConfirmationPath: root.FullPath}).AddTokenAuth(token), http.StatusOK)
		root = DecodeJSON(t, response, &governance_service.GroupState{})
		require.Equal(t, "deletion-real", root.FullPath)
		doGitPushTestRepository(local, "origin", created.DefaultBranch)(t)
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint+"/deletion", governance_service.GroupDeletionOption{Revision: root.Revision, ConfirmationPath: root.FullPath}).AddTokenAuth(token), http.StatusOK)
		root = DecodeJSON(t, response, &governance_service.GroupState{})
		option = governance_service.GroupDeletionOption{Revision: root.Revision, ConfirmationPath: root.FullPath}
		createTrigger := "CREATE TRIGGER governance_group_delete_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'resource.deletion_committed' BEGIN SELECT RAISE(ABORT, '群组删除事务审计故障'); END"
		dropTrigger := "DROP TRIGGER IF EXISTS governance_group_delete_fail"
		if setting.Database.Type.IsPostgreSQL() {
			_, err = db.GetEngine(ctx).Exec("CREATE FUNCTION governance_group_delete_fail_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type = 'resource.deletion_committed' THEN RAISE EXCEPTION '群组删除事务审计故障'; END IF; RETURN NEW; END $$")
			require.NoError(t, err)
			defer func() {
				_, err := db.GetEngine(context.WithoutCancel(ctx)).Exec("DROP FUNCTION IF EXISTS governance_group_delete_fail_fn() CASCADE")
				require.NoError(t, err)
			}()
			createTrigger = "CREATE TRIGGER governance_group_delete_fail BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION governance_group_delete_fail_fn()"
			dropTrigger += " ON governance_audit_event"
		}
		_, err = db.GetEngine(ctx).Exec(createTrigger)
		require.NoError(t, err)
		defer func() {
			_, err := db.GetEngine(context.WithoutCancel(ctx)).Exec(dropTrigger)
			require.NoError(t, err)
		}()
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint+"/delete-permanently", option).AddTokenAuth(token), http.StatusInternalServerError)
		_, err = repo_model.GetRepositoryByID(ctx, repo.ID)
		require.NoError(t, err)
		exists, err := gitrepo.IsRepositoryExist(ctx, repo)
		require.NoError(t, err)
		require.True(t, exists)
		_, err = packages_model.GetPackageByID(ctx, pkg.ID)
		require.NoError(t, err, "删除审计失败也要恢复软件包")
		_, err = db.GetEngine(ctx).Exec(dropTrigger)
		require.NoError(t, err)
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint+"/delete-permanently", option).AddTokenAuth(token), http.StatusNoContent)
		MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(token), http.StatusNotFound)
		require.NoError(t, repo_service.RunResourceCleanups(ctx))
		exists, err = gitrepo.IsRepositoryExist(ctx, repo)
		require.NoError(t, err)
		require.False(t, exists)
		_, err = packages_model.GetPackageByID(ctx, pkg.ID)
		require.Error(t, err)
	})
}
