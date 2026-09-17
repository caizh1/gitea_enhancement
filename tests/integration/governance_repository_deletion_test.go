// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/git"
	"gitea.dev/modules/git/gitcmd"
	"gitea.dev/modules/gitrepo"
	api "gitea.dev/modules/structs"
	governance_service "gitea.dev/services/governance"
	repo_service "gitea.dev/services/repository"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceRepositoryDeletionRealGit(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		ctx := t.Context()
		require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/user/repos", api.CreateRepoOption{Name: "repository-deletion-real", Private: true, AutoInit: true}).AddTokenAuth(token), http.StatusCreated)
		created := DecodeJSON(t, response, &api.Repository{})
		repo, err := repo_model.GetRepositoryByID(ctx, created.ID)
		require.NoError(t, err)
		originalPath := repo.RepoPath()
		cloneURL := *baseURL
		cloneURL.User = url.UserPassword("user2", "password")
		cloneURL.Path = "/user2/repository-deletion-real.git"
		local := filepath.Join(t.TempDir(), "待删除项目")
		doGitClone(local, &cloneURL)(t)
		before, err := git.GetFullCommitID(ctx, local, "HEAD")
		require.NoError(t, err)
		endpoint := fmt.Sprintf("/api/v1/governance/repositories/%d", repo.ID)
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint+"/delete-permanently", repo_service.DeletionOption{}).AddTokenAuth(token), http.StatusNotFound)
		native := MakeRequest(t, NewRequest(t, "DELETE", "/api/v1/repos/user2/repository-deletion-real").AddTokenAuth(token), http.StatusNoContent)
		require.Equal(t, "scheduled", native.Header().Get("X-Gitea-Deletion-State"))
		response = MakeRequest(t, NewRequest(t, "GET", endpoint+"/deletion").AddTokenAuth(token), http.StatusOK)
		state := DecodeJSON(t, response, &repo_service.DeletionState{})
		require.NotNil(t, state.Pending)
		require.True(t, state.Archived)
		fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
		require.NoError(t, err)
		require.Equal(t, originalPath, fresh.RepoPath())
		doGitCheckoutWriteFileCommit(localGitAddCommitOptions{LocalRepoPath: local, CheckoutBranch: created.DefaultBranch, TreeFilePath: "pending.txt", TreeFileContent: "保留期不能写入\n"})(t)
		require.Error(t, gitcmd.NewCommand("push", "origin").AddDynamicArguments(created.DefaultBranch).WithDir(local).RunWithStderr(ctx))
		actual, err := git.GetFullCommitID(ctx, originalPath, "HEAD")
		require.NoError(t, err)
		require.Equal(t, before, actual)
		page := loginUser(t, "user2").MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/governance/repositories/%d/deletion", repo.ID)), http.StatusOK)
		require.Contains(t, page.Body.String(), "恢复项目")
		option := repo_service.DeletionOption{ConfirmationPath: state.FullPath, DueUnix: state.Pending.DueUnix}
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint+"/restore", option).AddTokenAuth(token), http.StatusOK)
		doGitPushTestRepository(local, "origin", created.DefaultBranch)(t)
		// 恢复后普通改名仍使用同一正文，后续转移再由原生流程移动到新所有者目录。
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/user2/repository-deletion-real", api.EditRepoOption{Name: new("repository-deletion-renamed")}).AddTokenAuth(token), http.StatusOK)
		fresh, err = repo_model.GetRepositoryByID(ctx, repo.ID)
		require.NoError(t, err)
		require.Equal(t, originalPath, fresh.RepoPath())
		renamedURL := cloneURL
		renamedURL.Path = "/user2/repository-deletion-renamed.git"
		doGitClone(filepath.Join(t.TempDir(), "改名后读取"), &renamedURL)(t)
		adminToken := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
		MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repository-deletion-renamed/transfer", api.TransferRepoOption{NewOwner: "user1"}).AddTokenAuth(adminToken), http.StatusAccepted)
		fresh, err = repo_model.GetRepositoryByID(ctx, repo.ID)
		require.NoError(t, err)
		require.EqualValues(t, 1, fresh.OwnerID)
		require.Empty(t, fresh.GovernanceStorageName)
		renamedURL.User = url.UserPassword("user1", "password")
		renamedURL.Path = "/user1/repository-deletion-renamed.git"
		doGitClone(filepath.Join(t.TempDir(), "转移后读取"), &renamedURL)(t)
		token = adminToken
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint+"/deletion", repo_service.DeletionOption{ConfirmationPath: fresh.FullPath()}).AddTokenAuth(token), http.StatusOK)
		state = DecodeJSON(t, response, &repo_service.DeletionState{})
		option = repo_service.DeletionOption{ConfirmationPath: state.FullPath, DueUnix: state.Pending.DueUnix}
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint+"/delete-permanently", option).AddTokenAuth(token), http.StatusNoContent)
		require.NoError(t, repo_service.RunResourceCleanups(ctx))
		exists, err := gitrepo.IsRepositoryExist(ctx, fresh)
		require.NoError(t, err)
		require.False(t, exists)
	})
}

func TestGovernanceRepositoryBulkDeletion(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
	response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", governance_service.GroupOption{Name: "批量删除", Path: "bulk-deletion", Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
	group := DecodeJSON(t, response, &governance_service.GroupState{})
	var ids []int64
	for _, name := range []string{"one", "two"} {
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/orgs/bulk-deletion/repos", api.CreateRepoOption{Name: name, Private: true}).AddTokenAuth(token), http.StatusCreated)
		repo := DecodeJSON(t, response, &api.Repository{})
		ids = append(ids, repo.ID)
	}
	for range 2 {
		response = MakeRequest(t, NewRequest(t, "DELETE", "/api/v1/orgs/bulk-deletion/repos").AddTokenAuth(token), http.StatusAccepted)
		require.Equal(t, "scheduled", response.Header().Get("X-Gitea-Deletion-State"))
	}
	for _, id := range ids {
		schedule, has, err := db.GetByID[governance_model.RepositoryDeletion](ctx, id)
		require.NoError(t, err)
		require.True(t, has)
		require.Equal(t, group.ID, schedule.OwnerID)
		repo, err := repo_model.GetRepositoryByID(ctx, id)
		require.NoError(t, err)
		require.True(t, repo.IsArchived)
	}
	current, err := governance_model.GetNamespace(ctx, group.ID)
	require.NoError(t, err)
	require.Zero(t, current.DeleteAfter, "批量项目删除不删除其群组")
}
