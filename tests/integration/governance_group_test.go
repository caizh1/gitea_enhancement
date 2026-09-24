// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	auth_model "gitea.dev/models/auth"
	governance_model "gitea.dev/models/governance"
	api "gitea.dev/modules/structs"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGovernanceGroupHTTP(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		withKeyFile(t, "群组验收密钥", func(keyFile string) {
			require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
			token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
			reader := getUserToken(t, "user4", auth_model.AccessTokenScopeAll)
			readOnly := getUserToken(t, "user2", auth_model.AccessTokenScopeReadGovernance)
			option := governance_service.GroupOption{Name: "原生层级验收", Path: "rd", ParentID: 3, Visibility: 2}
			MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", option).AddTokenAuth(readOnly), http.StatusForbidden)
			resp := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", option).AddTokenAuth(token), http.StatusCreated)
			group := DecodeJSON(t, resp, &governance_service.GroupState{})
			assert.Equal(t, "org3/rd", group.FullPath)
			repoOption := api.CreateRepoOption{Name: "firmware", Private: true, AutoInit: true}
			resp = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/orgs/org3/rd/repos", repoOption).AddTokenAuth(token), http.StatusCreated)
			repo := DecodeJSON(t, resp, &api.Repository{})
			assert.Equal(t, "org3/rd/firmware", repo.FullPath)
			assert.Contains(t, repo.CloneURL, "/org3/rd/firmware.git")
			MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/org3/rd/firmware").AddTokenAuth(reader), http.StatusNotFound)
			member := governance_service.GroupMemberOption{Role: governance_model.Reporter, Revision: 1}
			MakeRequest(t, NewRequestWithJSON(t, "PUT", "/api/v1/governance/groups/3/members/4", member).AddTokenAuth(token), http.StatusNoContent)
			MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/org3/rd/firmware").AddTokenAuth(reader), http.StatusOK)
			session := loginUser(t, "user4")
			resp = session.MakeRequest(t, NewRequest(t, "GET", "/org3/rd/firmware"), http.StatusOK)
			assert.Contains(t, resp.Body.String(), "<title>org3/rd/firmware")
			session.MakeRequest(t, NewRequest(t, "GET", "/org3/rd"), http.StatusOK)
			req := NewRequest(t, "GET", "/org3/rd/firmware.git/info/refs?service=git-upload-pack")
			req.SetBasicAuth("user4", "password")
			resp = MakeRequest(t, req, http.StatusOK)
			assert.Contains(t, resp.Header().Get("Content-Type"), "application/x-git-upload-pack-advertisement")
			cloneURL := *baseURL
			cloneURL.User = url.UserPassword("user4", "password")
			cloneURL.Path = "/org3/rd/firmware.git"
			t.Run("完整路径克隆", doGitClone(filepath.Join(t.TempDir(), "仓库"), &cloneURL))
			publicKey, err := os.ReadFile(keyFile + ".pub")
			require.NoError(t, err)
			MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/user/keys", api.CreateKeyOption{Title: "群组验收", Key: string(publicKey)}).AddTokenAuth(reader), http.StatusCreated)
			sshURL := createSSHUrl("org3/rd/firmware.git", baseURL)
			t.Run("SSH完整路径克隆", doGitClone(filepath.Join(t.TempDir(), "SSH仓库"), sshURL))
			ownerSession := loginUser(t, "user2")
			resp = ownerSession.MakeRequest(t, NewRequest(t, "GET", "/governance/groups/"+strconv.FormatInt(group.ID, 10)+"?tab=members"), http.StatusOK)
			assert.Contains(t, resp.Body.String(), "成员与权限来源")
			assert.Contains(t, resp.Body.String(), "user4")
			assert.Contains(t, resp.Body.String(), "Reporter")

			move := governance_service.GroupOption{Path: "research", ParentID: 3, Revision: group.Revision}
			endpoint := "/api/v1/governance/groups/" + strconv.FormatInt(group.ID, 10) + "/move"
			MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, move).AddTokenAuth(token), http.StatusOK)
			MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/org3/rd/firmware").AddTokenAuth(reader), http.StatusOK)
			MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/org3/research/firmware").AddTokenAuth(reader), http.StatusOK)
			MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/orgs/org3/research/rename", api.RenameOrgOption{NewName: "science"}).AddTokenAuth(token), http.StatusNoContent)
			MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/org3/science/firmware").AddTokenAuth(reader), http.StatusOK)
			MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/org3/research/firmware").AddTokenAuth(reader), http.StatusOK)

			MakeRequest(t, NewRequest(t, "DELETE", "/api/v1/governance/groups/3/members/4?revision=2").AddTokenAuth(token), http.StatusNoContent)
			MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/org3/research/firmware").AddTokenAuth(reader), http.StatusNotFound)
			session.MakeRequest(t, NewRequest(t, "GET", "/org3/research/firmware"), http.StatusNotFound)
			req = NewRequest(t, "GET", "/org3/research/firmware.git/info/refs?service=git-upload-pack")
			req.SetBasicAuth("user4", "password")
			MakeRequest(t, req, http.StatusNotFound)
			cloneURL.Path = "/org3/research/firmware.git"
			t.Run("撤权后克隆拒绝", doGitCloneFail(&cloneURL))
			sshURL.Path = "org3/research/firmware.git"
			t.Run("SSH撤权后克隆拒绝", doGitCloneFail(sshURL))
		})
	})
}

func TestTopLevelGroupCreationDeniedWeb(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		admin := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
		allowed := false
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user4", api.EditUserOption{AllowCreateOrganization: &allowed}).AddTokenAuth(admin), http.StatusOK)
		user := loginUser(t, "user4")
		page := user.MakeRequest(t, NewRequest(t, "GET", "/governance/groups?create=1"), http.StatusOK)
		assert.NotContains(t, page.Body.String(), "500 Internal Server Error")
		assert.NotContains(t, page.Body.String(), "创建顶级群组</summary>")
		user.MakeRequest(t, NewRequest(t, "GET", "/org/create"), http.StatusNotFound)
		user.MakeRequest(t, NewRequestWithValues(t, "POST", "/org/create", map[string]string{"org_name": "m01-denied-native"}), http.StatusNotFound)
		user.MakeRequest(t, NewRequestWithValues(t, "POST", "/governance/groups", map[string]string{"name": "拒绝创建", "path": "m01-denied-group", "visibility": "2"}), http.StatusNotFound)
	})
}

func TestGovernanceGroupShareHTTP(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		reader := getUserToken(t, "user4", auth_model.AccessTokenScopeAll)
		create := func(path string) *governance_service.GroupState {
			resp := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", governance_service.GroupOption{Path: path, Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
			return DecodeJSON(t, resp, &governance_service.GroupState{})
		}
		source, invited := create("http-source"), create("http-invited")
		endpoint := "/api/v1/governance/groups/" + strconv.FormatInt(source.ID, 10)
		invitedEndpoint := "/api/v1/governance/groups/" + strconv.FormatInt(invited.ID, 10)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", invitedEndpoint+"/members/4", governance_service.GroupMemberOption{Role: governance_model.Maintainer, Revision: 1}).AddTokenAuth(token), http.StatusNoContent)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", invitedEndpoint+"/members/5", governance_service.GroupMemberOption{Role: governance_model.Reporter, Revision: 2}).AddTokenAuth(reader), http.StatusNotFound)
		MakeRequest(t, NewRequest(t, "GET", invitedEndpoint+"/members").AddTokenAuth(reader), http.StatusNotFound)
		resp := MakeRequest(t, NewRequest(t, "GET", invitedEndpoint+"/members").AddTokenAuth(token), http.StatusOK)
		assert.Contains(t, resp.Body.String(), "user4")
		MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(reader), http.StatusNotFound)
		shareEndpoint := endpoint + "/shares/" + strconv.FormatInt(invited.ID, 10)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", shareEndpoint, governance_service.GroupShareOption{MaxRole: governance_model.Reporter, Revision: 1}).AddTokenAuth(token), http.StatusNoContent)
		resp = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(reader), http.StatusOK)
		state := DecodeJSON(t, resp, &governance_service.GroupState{})
		assert.True(t, state.Abilities[governance_model.ReadCode])
		assert.False(t, state.Abilities[governance_model.PushCode])
		resp = MakeRequest(t, NewRequest(t, "GET", endpoint+"/shares").AddTokenAuth(token), http.StatusOK)
		shares := DecodeJSON(t, resp, &[]*governance_model.Share{})
		require.Len(t, *shares, 1)
		assert.Equal(t, invited.ID, (*shares)[0].GroupID)
		session := loginUser(t, "user2")
		resp = session.MakeRequest(t, NewRequest(t, "GET", "/governance/groups/"+strconv.FormatInt(source.ID, 10)+"?tab=shares"), http.StatusOK)
		assert.Contains(t, resp.Body.String(), "撤销共享")
		MakeRequest(t, NewRequest(t, "DELETE", shareEndpoint+"?revision=2").AddTokenAuth(token), http.StatusNoContent)
		MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(reader), http.StatusNotFound)
	})
}

func TestGovernanceCustomRoleHTTP(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		option := governance_service.GroupRoleOption{Name: "审计只读", BaseRole: governance_model.Reporter, Abilities: []string{governance_model.ReadAudit}, Revision: 1}
		resp := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups/3/roles", option).AddTokenAuth(token), http.StatusOK)
		role := DecodeJSON(t, resp, &governance_model.CustomRole{})
		MakeRequest(t, NewRequestWithJSON(t, "PUT", "/api/v1/governance/groups/3/members/4", governance_service.GroupMemberOption{Role: governance_model.Reporter, CustomRoleID: role.ID, Revision: 2}).AddTokenAuth(token), http.StatusNoContent)
		session := loginUser(t, "user2")
		resp = session.MakeRequest(t, NewRequest(t, "GET", "/governance/groups/3?tab=members"), http.StatusOK)
		assert.Contains(t, resp.Body.String(), "审计只读")
		assert.Contains(t, resp.Body.String(), "查看审计")
		reader := getUserToken(t, "user4", auth_model.AccessTokenScopeAll)
		resp = MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/groups/3").AddTokenAuth(reader), http.StatusOK)
		state := DecodeJSON(t, resp, &governance_service.GroupState{})
		assert.True(t, state.Abilities[governance_model.ReadAudit])
		roleEndpoint := "/api/v1/governance/groups/3/roles/" + strconv.FormatInt(role.ID, 10)
		MakeRequest(t, NewRequest(t, "DELETE", roleEndpoint+"?revision=3").AddTokenAuth(token), http.StatusConflict)
		option.Revision, option.Abilities = 3, []string{}
		MakeRequest(t, NewRequestWithJSON(t, "PUT", roleEndpoint, option).AddTokenAuth(token), http.StatusOK)
		resp = MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/groups/3").AddTokenAuth(reader), http.StatusOK)
		state = DecodeJSON(t, resp, &governance_service.GroupState{})
		assert.False(t, state.Abilities[governance_model.ReadAudit])
	})
}

func TestGovernanceRepositorySharingHTTP(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		reader := getUserToken(t, "user5", auth_model.AccessTokenScopeAll)
		readOnly := getUserToken(t, "user2", auth_model.AccessTokenScopeReadGovernance)
		resp := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", governance_service.GroupOption{Path: "shared-audience", Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
		root := DecodeJSON(t, resp, &governance_service.GroupState{})
		resp = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", governance_service.GroupOption{Path: "child", ParentID: root.ID, Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
		child := DecodeJSON(t, resp, &governance_service.GroupState{})
		memberURL := "/api/v1/governance/groups/" + strconv.FormatInt(root.ID, 10) + "/members/5"
		MakeRequest(t, NewRequestWithJSON(t, "PUT", memberURL, governance_service.GroupMemberOption{Role: governance_model.Reporter, Revision: root.Revision}).AddTokenAuth(token), http.StatusNoContent)
		resp = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/orgs/org3/repos", api.CreateRepoOption{Name: "shared-native", Private: true, AutoInit: true}).AddTokenAuth(token), http.StatusCreated)
		repo := DecodeJSON(t, resp, &api.Repository{})
		repoURL := "/api/v1/repos/org3/shared-native"
		MakeRequest(t, NewRequest(t, "GET", repoURL).AddTokenAuth(reader), http.StatusNotFound)
		endpoint := "/api/v1/governance/repositories/" + strconv.FormatInt(repo.ID, 10) + "/shares"
		resp = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(token), http.StatusOK)
		state := DecodeJSON(t, resp, &governance_service.RepositorySharesState{})
		option := governance_service.GroupShareOption{MaxRole: governance_model.Reporter, Revision: state.Revision}
		updateURL := endpoint + "/" + strconv.FormatInt(child.ID, 10)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", updateURL, option).AddTokenAuth(readOnly), http.StatusForbidden)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", updateURL, option).AddTokenAuth(token), http.StatusNoContent)
		MakeRequest(t, NewRequest(t, "GET", repoURL).AddTokenAuth(reader), http.StatusOK)
		ownerSession := loginUser(t, "user2")
		webShares := "/governance/repositories/" + strconv.FormatInt(repo.ID, 10) + "/shares"
		collaboration := "/org3/shared-native/settings/collaboration"
		collaborators := "/org3/shared-native/collaborators"
		resp = ownerSession.MakeRequest(t, NewRequest(t, "GET", webShares), http.StatusSeeOther)
		assert.Equal(t, collaboration, resp.Header().Get("Location"))
		resp = ownerSession.MakeRequest(t, NewRequest(t, "GET", collaboration), http.StatusSeeOther)
		assert.Equal(t, collaborators, resp.Header().Get("Location"))
		resp = ownerSession.MakeRequest(t, NewRequest(t, "GET", collaborators), http.StatusOK)
		assert.Contains(t, resp.Body.String(), "shared-audience/child")
		assert.Contains(t, resp.Body.String(), "邀请群组")
		assert.NotContains(t, resp.Body.String(), "Render failed")
		assert.Contains(t, resp.Body.String(), `data-kind="share"`)
		assert.Contains(t, resp.Body.String(), `action="/org3/shared-native/collaborators/change"`)
		assert.NotContains(t, resp.Body.String(), ">项目共享</a>")
		csrf := NewHTMLParser(t, resp.Body).GetInputValueByName("_csrf")
		search := ownerSession.MakeRequest(t, NewRequest(t, "GET", webShares+"/groups?q=shared-audience"), http.StatusOK)
		assert.Contains(t, search.Body.String(), "shared-audience/child")
		loginUser(t, "user5").MakeRequest(t, NewRequest(t, "GET", webShares+"/groups?q=shared-audience"), http.StatusNotFound)
		form := map[string]string{"_csrf": csrf, "revision": strconv.FormatInt(state.Revision+1, 10), "group_path": child.FullPath, "role": "30", "expires": "2099-12-31"}
		ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", webShares, form), http.StatusSeeOther)
		resp = ownerSession.MakeRequest(t, NewRequest(t, "GET", collaborators), http.StatusOK)
		assert.Contains(t, resp.Body.String(), `data-modal-share-expiry="2099-12-31"`)
		assert.Contains(t, resp.Body.String(), `data-modal-share-role.value="30"`)
		form["revision"] = strconv.FormatInt(state.Revision+2, 10)
		form["expires_unix"] = "4102398000"
		ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", webShares, form), http.StatusSeeOther)
		precise := DecodeJSON(t, MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(token), http.StatusOK), &governance_service.RepositorySharesState{})
		require.Equal(t, int64(4102398000), precise.Shares[0].ExpiresUnix)

		ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", webShares, form), http.StatusConflict)
		crossOrigin := NewRequestWithValues(t, "POST", webShares, form)
		crossOrigin.Header.Set("Sec-Fetch-Site", "cross-site")
		ownerSession.MakeRequest(t, crossOrigin, http.StatusForbidden)
		denied := map[string]string{"action": "remove", "group_id": strconv.FormatInt(child.ID, 10), "revision": strconv.FormatInt(state.Revision+2, 10)}
		loginUser(t, "user5").MakeRequest(t, NewRequestWithValues(t, "POST", webShares, denied), http.StatusNotFound)

		cloneURL := *baseURL
		cloneURL.User, cloneURL.Path = url.UserPassword("user5", "password"), "/org3/shared-native.git"
		t.Run("继承共享真实克隆", doGitClone(filepath.Join(t.TempDir(), "共享仓库"), &cloneURL))
		resp = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(token), http.StatusOK)
		state = DecodeJSON(t, resp, &governance_service.RepositorySharesState{})
		ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", webShares, map[string]string{"_csrf": csrf, "revision": strconv.FormatInt(state.Revision, 10), "action": "remove", "group_id": strconv.FormatInt(child.ID, 10)}), http.StatusSeeOther)
		MakeRequest(t, NewRequest(t, "GET", repoURL).AddTokenAuth(reader), http.StatusNotFound)
		req := NewRequest(t, "GET", "/org3/shared-native.git/info/refs?service=git-upload-pack")
		req.SetBasicAuth("user5", "password")
		MakeRequest(t, req, http.StatusNotFound)
	})
}
