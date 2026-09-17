// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	auth_model "gitea.dev/models/auth"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/git"
	"gitea.dev/modules/git/gitcmd"
	"gitea.dev/modules/gitrepo"
	repository_module "gitea.dev/modules/repository"
	api "gitea.dev/modules/structs"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestGovernanceGroupArchiveRealGit(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", governance_service.GroupOption{Name: "归档群组", Path: "archive-root", Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
		root := DecodeJSON(t, response, &governance_service.GroupState{})
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", governance_service.GroupOption{Name: "子组", Path: "child", ParentID: root.ID, Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
		child := DecodeJSON(t, response, &governance_service.GroupState{})
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/orgs/archive-root/child/repos", api.CreateRepoOption{Name: "firmware", Private: true, AutoInit: true}).AddTokenAuth(token), http.StatusCreated)
		created := DecodeJSON(t, response, &api.Repository{})
		nativeRepo, err := repo_model.GetRepositoryByID(t.Context(), created.ID)
		require.NoError(t, err)
		require.NoError(t, gitrepo.InstallReferenceTransactionHook(t.Context(), nativeRepo))

		memberEndpoint := fmt.Sprintf("/api/v1/governance/groups/%d/members/4", root.ID)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", memberEndpoint, governance_service.GroupMemberOption{Role: governance_model.Developer, Revision: root.Revision}).AddTokenAuth(token), http.StatusNoContent)
		cloneURL := *baseURL
		cloneURL.User, cloneURL.Path = url.UserPassword("user2", "password"), "/archive-root/child/firmware.git"
		local := filepath.Join(t.TempDir(), "归档读写验收")
		doGitClone(local, &cloneURL)(t)
		before, err := git.GetFullCommitID(t.Context(), local, "HEAD")
		require.NoError(t, err)
		endpoint := fmt.Sprintf("/api/v1/governance/groups/%d/archive", root.ID)
		response = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(token), http.StatusOK)
		impact := DecodeJSON(t, response, &governance_service.GroupArchiveImpact{})
		require.Equal(t, 2, impact.Groups)
		require.Equal(t, 1, impact.Repositories)
		response = MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint, governance_service.GroupArchiveOption{Archived: true, Revision: impact.Revision}).AddTokenAuth(token), http.StatusOK)
		root = DecodeJSON(t, response, &governance_service.GroupState{})
		require.True(t, root.Archived)
		response = MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/archive-root/child/firmware").AddTokenAuth(token), http.StatusOK)
		require.True(t, DecodeJSON(t, response, &api.Repository{}).Archived)
		doGitClone(filepath.Join(t.TempDir(), "归档仍可读取"), &cloneURL)(t)
		doGitCheckoutWriteFileCommit(localGitAddCommitOptions{LocalRepoPath: local, CheckoutBranch: created.DefaultBranch, TreeFilePath: "归档.txt", TreeFileContent: "归档期间不能写入\n"})(t)
		require.Error(t, gitcmd.NewCommand("push", "origin").AddDynamicArguments(created.DefaultBranch).WithDir(local).RunWithStderr(t.Context()))
		output, _, err := gitcmd.NewCommand("ls-remote", "origin").AddDynamicArguments("refs/heads/" + created.DefaultBranch).WithDir(local).RunStdString(t.Context())
		require.NoError(t, err)
		require.Contains(t, output, before)
		doer, err := user_model.GetUserByID(t.Context(), 2)
		require.NoError(t, err)
		require.Error(t, gitcmd.NewCommand("update-ref", "refs/heads/archive-bypass").AddDynamicArguments(before).WithDir(nativeRepo.RepoPath()).WithEnv(repository_module.DoerPushingEnvironment(doer, nativeRepo, false)).RunWithStderr(t.Context()), "已接入的内部引用写入不能越过归档")
		_, err = git.GetFullCommitID(t.Context(), nativeRepo.RepoPath(), "refs/heads/archive-bypass")
		require.Error(t, err)
		MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/archive-root/child/firmware/transfer", api.TransferRepoOption{NewOwner: "user2"}).AddTokenAuth(token), http.StatusConflict)
		unarchived := false
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/archive-root/child/firmware", api.EditRepoOption{Archived: &unarchived}).AddTokenAuth(token), http.StatusConflict)
		response = MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/api/v1/governance/groups/%d", child.ID)).AddTokenAuth(token), http.StatusOK)
		child = DecodeJSON(t, response, &governance_service.GroupState{})
		MakeRequest(t, NewRequestWithJSON(t, "PUT", fmt.Sprintf("/api/v1/governance/groups/%d/archive", child.ID), governance_service.GroupArchiveOption{Archived: false, Revision: child.Revision}).AddTokenAuth(token), http.StatusConflict)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", memberEndpoint, governance_service.GroupMemberOption{Role: governance_model.Reporter, Revision: root.Revision}).AddTokenAuth(token), http.StatusNoContent)
		readerToken := getUserToken(t, "user4", auth_model.AccessTokenScopeAll)
		MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/archive-root/child/firmware").AddTokenAuth(readerToken), http.StatusOK)
		readerURL := cloneURL
		readerURL.User = url.UserPassword("user4", "password")
		doGitClone(filepath.Join(t.TempDir(), "归档继承读取"), &readerURL)(t)
		MakeRequest(t, NewRequest(t, "DELETE", fmt.Sprintf("%s?revision=%d", memberEndpoint, root.Revision+1)).AddTokenAuth(token), http.StatusNoContent)
		MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/archive-root/child/firmware").AddTokenAuth(readerToken), http.StatusNotFound)
		require.Error(t, gitcmd.NewCommand("ls-remote").AddDynamicArguments(readerURL.String()).RunWithStderr(t.Context()), "归档父组撤权后真实 Git 读取必须拒绝")
		response = MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/api/v1/governance/groups/%d", root.ID)).AddTokenAuth(token), http.StatusOK)
		root = DecodeJSON(t, response, &governance_service.GroupState{})
		session := loginUser(t, "user2")
		page := session.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/governance/groups/%d?tab=settings", root.ID)), http.StatusOK)
		require.Contains(t, page.Body.String(), "恢复本组及后代")
		require.Contains(t, page.Body.String(), "原本单独归档的项目")
		require.NotContains(t, page.Body.String(), "创建自定义角色")
		memberPage := session.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/governance/groups/%d?tab=members", root.ID)), http.StatusOK)
		require.Contains(t, memberPage.Body.String(), "修改已有直接授权")
		session.MakeRequest(t, NewRequestWithValues(t, "POST", fmt.Sprintf("/governance/groups/%d/archive", root.ID), map[string]string{"archived": "false", "revision": strconv.FormatInt(root.Revision, 10)}), http.StatusSeeOther)
		require.NotEmpty(t, session.GetCookieFlashMessage().SuccessMsg)
		doGitPushTestRepository(local, "origin", created.DefaultBranch)(t)
		response = MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/archive-root/child/firmware").AddTokenAuth(token), http.StatusOK)
		require.False(t, DecodeJSON(t, response, &api.Repository{}).Archived)
	})
}
