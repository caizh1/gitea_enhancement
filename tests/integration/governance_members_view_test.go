// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	api "gitea.dev/modules/structs"
	gs "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestUnifiedMembersHTTPAndSSH(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		withKeyFile(t, "成员撤权验收", func(keyFile string) {
			require.NoError(t, gm.InitializeLegacyNamespaces(t.Context()))
			token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
			ownerToken := getUserToken(t, "user4", auth_model.AccessTokenScopeAll)
			maintainerToken := getUserToken(t, "user5", auth_model.AccessTokenScopeAll)
			response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/user/repos", api.CreateRepoOption{Name: "unified-members", Private: true, AutoInit: true}).AddTokenAuth(token), http.StatusCreated)
			repo := DecodeJSON(t, response, &api.Repository{})
			endpoint := "/api/v1/governance/repositories/" + strconv.FormatInt(repo.ID, 10)
			revision := func() int64 { n, e := gm.GetNamespace(t.Context(), 2); require.NoError(t, e); return n.Revision }
			MakeRequest(t, NewRequest(t, "GET", endpoint+"/members/view"), http.StatusNotFound)
			MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint+"/members/4", gs.GroupMemberOption{Role: gm.Owner, Revision: revision()}).AddTokenAuth(token), http.StatusNoContent)
			MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint+"/members/5", gs.GroupMemberOption{Role: gm.Maintainer, Revision: revision()}).AddTokenAuth(token), http.StatusNoContent)
			response = MakeRequest(t, NewRequest(t, "GET", endpoint+"/members/view?role=50").AddTokenAuth(ownerToken), http.StatusOK)
			view := DecodeJSON(t, response, &gs.RepositoryMembersView{})
			require.Len(t, view.Members, 2)
			session := loginUser(t, "user5")
			response = session.MakeRequest(t, NewRequest(t, "GET", "/user2/unified-members/collaborators"), http.StatusOK)
			require.Contains(t, response.Body.String(), "Owner · 所有者")
			require.NotContains(t, response.Body.String(), "id=\"repo-share-modal\"")
			MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint+"/members/4", gs.GroupMemberOption{Role: gm.Developer, Revision: revision()}).AddTokenAuth(maintainerToken), http.StatusNotFound)
			MakeRequest(t, NewRequest(t, "DELETE", endpoint+"/members/4?revision="+strconv.FormatInt(revision(), 10)).AddTokenAuth(maintainerToken), http.StatusNotFound)
			readPermission := api.RepoWritePermission("read")
			denied := MakeRequest(t, NewRequestWithJSON(t, "PUT", "/api/v1/repos/user2/unified-members/collaborators/user4", api.AddCollaboratorOption{Permission: &readPermission}).AddTokenAuth(maintainerToken), http.StatusNotFound)
			require.NotEmpty(t, denied.Body.String())
			archived := true
			MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/user2/unified-members", api.EditRepoOption{Archived: &archived}).AddTokenAuth(maintainerToken), http.StatusNotFound)
			MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/user2/unified-members", api.EditRepoOption{Archived: &archived}).AddTokenAuth(ownerToken), http.StatusOK)
			archived = false
			MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/user2/unified-members", api.EditRepoOption{Archived: &archived}).AddTokenAuth(ownerToken), http.StatusOK)
			key, err := os.ReadFile(keyFile + ".pub")
			require.NoError(t, err)
			MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/user/keys", api.CreateKeyOption{Title: "成员验收", Key: string(key)}).AddTokenAuth(ownerToken), http.StatusCreated)
			cloneURL := *baseURL
			cloneURL.User = url.UserPassword("user4", "password")
			cloneURL.Path = "/user2/unified-members.git"
			sshURL := createSSHUrl("user2/unified-members.git", baseURL)
			httpClone, sshClone := filepath.Join(t.TempDir(), "http-owner"), filepath.Join(t.TempDir(), "ssh-owner")
			t.Run("Owner_HTTP实际克隆", doGitClone(httpClone, &cloneURL))
			t.Run("Owner_SSH实际克隆", doGitClone(sshClone, sshURL))
			t.Run("Owner_HTTP实际推送", doGitPushTestRepository(httpClone, "origin", "HEAD:refs/heads/owner-http"))
			t.Run("Owner_SSH实际推送", doGitPushTestRepository(sshClone, "origin", "HEAD:refs/heads/owner-ssh"))
			change := gs.RepositoryMemberChange{Kind: "member", Remove: true, Member: gs.GroupMemberOption{UserID: 4, Revision: revision()}}
			response = MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint+"/members/preview", change).AddTokenAuth(token), http.StatusOK)
			preview := DecodeJSON(t, response, &gs.RepositoryChangePreview{})
			require.NotEmpty(t, preview.Token)
			MakeRequest(t, NewRequest(t, "DELETE", endpoint+"/members/4?revision="+strconv.FormatInt(revision(), 10)+"&preview_token="+preview.Token).AddTokenAuth(token), http.StatusNoContent)
			MakeRequest(t, NewRequest(t, "GET", endpoint+"/members/view").AddTokenAuth(ownerToken), http.StatusNotFound)
			t.Run("撤权_HTTP推送拒绝", doGitPushTestRepositoryFail(httpClone, "origin", "HEAD:refs/heads/revoked-http"))
			t.Run("撤权_SSH推送拒绝", doGitPushTestRepositoryFail(sshClone, "origin", "HEAD:refs/heads/revoked-ssh"))
			t.Run("撤权_HTTP拒绝", doGitCloneFail(&cloneURL))
			t.Run("撤权_SSH拒绝", doGitCloneFail(sshURL))
			actor := gm.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
			group, err := gs.CreateGroup(t.Context(), actor, gs.GroupOption{Path: "unified-shared-owner", Visibility: 2})
			require.NoError(t, err)
			require.NoError(t, gs.SetGroupMember(t.Context(), actor, group.ID, gs.GroupMemberOption{UserID: 4, Role: gm.Owner, Revision: group.Revision}, false))
			require.NoError(t, gs.SetRepositoryShare(t.Context(), actor, repo.ID, gs.GroupShareOption{GroupID: group.ID, MaxRole: gm.Owner, Revision: revision()}, false))
			response = MakeRequest(t, NewRequest(t, "GET", endpoint+"/members/view").AddTokenAuth(ownerToken), http.StatusOK)
			require.True(t, DecodeJSON(t, response, &gs.RepositoryMembersView{}).CanOwn)
			t.Run("共享Owner_HTTP推送", doGitPushTestRepository(httpClone, "origin", "HEAD:refs/heads/shared-http"))
			t.Run("共享Owner_SSH推送", doGitPushTestRepository(sshClone, "origin", "HEAD:refs/heads/shared-ssh"))
			require.NoError(t, gs.SetRepositoryShare(t.Context(), actor, repo.ID, gs.GroupShareOption{GroupID: group.ID, MaxRole: gm.Developer, Revision: revision()}, false))
			archived = true
			MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/user2/unified-members", api.EditRepoOption{Archived: &archived}).AddTokenAuth(ownerToken), http.StatusForbidden)
			t.Run("共享降为Developer_HTTP仍可推送", doGitPushTestRepository(httpClone, "origin", "HEAD:refs/heads/capped-http"))
			_, err = db.GetEngine(t.Context()).Where("scope_type = ? AND scope_id = ? AND group_id = ?", "repository", repo.ID, group.ID).Cols("expires_unix").Update(&gm.Share{ExpiresUnix: time.Now().Unix() - 1})
			require.NoError(t, err)
			MakeRequest(t, NewRequest(t, "GET", endpoint+"/members/view").AddTokenAuth(ownerToken), http.StatusNotFound)
			t.Run("共享到期_HTTP推送拒绝", doGitPushTestRepositoryFail(httpClone, "origin", "HEAD:refs/heads/expired-http"))
			t.Run("共享到期_SSH推送拒绝", doGitPushTestRepositoryFail(sshClone, "origin", "HEAD:refs/heads/expired-ssh"))
			t.Run("共享到期_HTTP读取拒绝", doGitCloneFail(&cloneURL))
			t.Run("共享到期_SSH读取拒绝", doGitCloneFail(sshURL))
			MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint+"/members/5", gs.GroupMemberOption{Role: gm.Guest, Revision: revision()}).AddTokenAuth(token), http.StatusNoContent)
			_, err = db.GetEngine(t.Context()).Where("repo_id = ? AND type <> ?", repo.ID, unit.TypeCode).Delete(new(repo_model.RepoUnit))
			require.NoError(t, err)
			session.MakeRequest(t, NewRequest(t, "GET", "/user2/unified-members/collaborators"), http.StatusOK)
			MakeRequest(t, NewRequest(t, "GET", endpoint+"/members/view").AddTokenAuth(maintainerToken), http.StatusOK)
			ownerSession := loginUser(t, "user2")
			csrf := NewRequestWithJSON(t, "POST", "/user2/unified-members/collaborators/preview", change)
			csrf.Header.Set("Origin", "https://untrusted.invalid")
			csrf.Header.Set("Sec-Fetch-Site", "cross-site")
			ownerSession.MakeRequest(t, csrf, http.StatusForbidden)
		})
	})
}
