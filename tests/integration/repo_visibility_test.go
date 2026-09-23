// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	auth_model "gitea.dev/models/auth"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/test"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryVisibilityChange(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")

	t.Run("MakePrivateRequiresCorrectName", func(t *testing.T) {
		// Wrong name should be rejected with a JSON error
		req := NewRequestWithValues(t, "POST", "/user2/repo1/settings", map[string]string{
			"action":            "visibility",
			"visibility":        "2",
			"confirm_repo_name": "wrong-name",
		})
		resp := session.MakeRequest(t, req, http.StatusBadRequest)
		assert.NotEmpty(t, test.ParseJSONError(resp.Body.Bytes()).ErrorMessage)

		repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		assert.False(t, repo1.IsPrivate)

		// Correct full name (owner/repo) should succeed with a JSON redirect
		req = NewRequestWithValues(t, "POST", "/user2/repo1/settings", map[string]string{
			"action":            "visibility",
			"visibility":        "2",
			"confirm_repo_name": "user2/repo1",
		})
		resp = session.MakeRequest(t, req, http.StatusOK)
		assert.NotNil(t, test.ParseJSONRedirect(resp.Body.Bytes()).Redirect)

		repo1 = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		assert.True(t, repo1.IsPrivate)
	})

	t.Run("MakePublicDoesNotRequireName", func(t *testing.T) {
		req := NewRequestWithValues(t, "POST", "/user2/repo2/settings", map[string]string{
			"action":     "visibility",
			"visibility": "0",
		})
		resp := session.MakeRequest(t, req, http.StatusOK)
		assert.NotNil(t, test.ParseJSONRedirect(resp.Body.Bytes()).Redirect)

		repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
		assert.False(t, repo2.IsPrivate)
	})
}

func TestRepositoryVisibilityGroupSettings(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", governance_service.GroupOption{
			Name: "可见性验收", Path: "visibility-child", ParentID: 3, Visibility: 1,
		}).AddTokenAuth(token), http.StatusCreated)
		group := DecodeJSON(t, response, &governance_service.GroupState{})
		session := loginUser(t, "user2")
		for _, path := range []struct {
			id   int64
			name string
		}{{3, "org3"}, {group.ID, group.FullPath}} {
			response = session.MakeRequest(t, NewRequest(t, "GET", "/governance/groups/"+strconv.FormatInt(path.id, 10)+"?tab=settings"), http.StatusOK)
			html := NewHTMLParser(t, response.Body)
			link, exists := html.Find(`a:contains("原生群组设置")`).Attr("href")
			require.True(t, exists)
			require.Equal(t, "/org/"+path.name+"/settings", link)
			session.MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK)
			loginUser(t, "user4").MakeRequest(t, NewRequest(t, "GET", link), http.StatusNotFound)
		}
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/orgs/"+group.FullPath+"/repos", api.CreateRepoOption{Name: "visibility-check"}).AddTokenAuth(token), http.StatusCreated)
		repo := DecodeJSON(t, response, &api.Repository{})
		page := "/" + repo.FullPath + "/settings"
		response = session.MakeRequest(t, NewRequest(t, "GET", page), http.StatusOK)
		html := NewHTMLParser(t, response.Body)
		require.Equal(t, "1", html.Find(`#repo_visibility option[selected]`).AttrOr("value", ""))
		require.Equal(t, 1, html.Find(`#visibility-repo-modal input[name="confirm_repo_name"]`).Length())
		// 父群组限制生效时必须明确提示，不能伪装为设置公开成功。
		session.MakeRequest(t, NewRequestWithValues(t, "POST", page, map[string]string{"action": "visibility", "visibility": "0"}), http.StatusOK)
		response = session.MakeRequest(t, NewRequest(t, "GET", page), http.StatusOK)
		require.Contains(t, response.Body.String(), "受所属群组可见性限制")
		require.Equal(t, repo_model.VisibilityInternal, unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID}).EffectiveVisibility())
		// 子群组页面展示完整路径，名称确认必须接受同一路径。
		values := map[string]string{"action": "visibility", "visibility": "2", "confirm_repo_name": "错误路径"}
		session.MakeRequest(t, NewRequestWithValues(t, "POST", page, values), http.StatusBadRequest)
		values["confirm_repo_name"] = repo.FullPath
		session.MakeRequest(t, NewRequestWithValues(t, "POST", page, values), http.StatusOK)
		require.Equal(t, repo_model.VisibilityPrivate, unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID}).EffectiveVisibility())
		response = session.MakeRequest(t, NewRequest(t, "GET", page), http.StatusOK)
		require.Equal(t, "2", NewHTMLParser(t, response.Body).Find(`#repo_visibility option[selected]`).AttrOr("value", ""))
		values["visibility"] = "1"
		session.MakeRequest(t, NewRequestWithValues(t, "POST", page, values), http.StatusOK)
		require.Equal(t, repo_model.VisibilityInternal, unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID}).EffectiveVisibility())
	})
}
