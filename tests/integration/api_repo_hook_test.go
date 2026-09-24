// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"testing"

	auth_model "gitea.dev/models/auth"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/models/webhook"
	api "gitea.dev/modules/structs"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
)

func TestAPICreateHook(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	assert.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 37})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	// user1 is an admin user
	session := loginUser(t, "user1")
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository)
	req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/%s/%s", owner.Name, repo.Name, "hooks"), api.CreateHookOption{
		Type: "gitea",
		Config: api.CreateHookOptionConfig{
			"content_type": "json",
			"url":          "http://example.com/",
			"secret":       "isolated-signing-key",
		},
		Events:              []string{"push", "issues"},
		BranchFilter:        "release/*",
		AuthorizationHeader: "Bearer s3cr3t",
		Name:                "  CI notifications  ",
	}).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusCreated)

	apiHook := DecodeJSON(t, resp, &api.Hook{})
	assert.Equal(t, "http://example.com/", apiHook.Config["url"])
	// the stored authorization header is a secret and must never be returned by the API
	assert.Empty(t, apiHook.AuthorizationHeader)
	assert.Equal(t, "CI notifications", apiHook.Name)
	assert.Contains(t, apiHook.Events, "push")
	assert.Contains(t, apiHook.Events, "issues")
	stored := unittest.AssertExistsAndLoadBean(t, &webhook.Webhook{ID: apiHook.ID})
	initialHeader := stored.HeaderAuthorizationEncrypted
	assert.NotEmpty(t, initialHeader)
	assert.Equal(t, "isolated-signing-key", stored.Secret)

	// a read-scoped token must not be able to read back the authorization header
	readToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadRepository)
	getReq := NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/%s/hooks/%d", owner.Name, repo.Name, apiHook.ID)).
		AddTokenAuth(readToken)
	getResp := MakeRequest(t, getReq, http.StatusOK)
	assert.NotContains(t, getResp.Body.String(), "s3cr3t")

	newName := "Deploy hook"
	patchReq := NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/%s/%s/hooks/%d", owner.Name, repo.Name, apiHook.ID), api.EditHookOption{
		Name: &newName,
	}).AddTokenAuth(token)
	patchResp := MakeRequest(t, patchReq, http.StatusOK)
	patched := DecodeJSON(t, patchResp, &api.Hook{})
	assert.Equal(t, newName, patched.Name)
	assert.ElementsMatch(t, apiHook.Events, patched.Events)
	assert.Equal(t, "release/*", patched.BranchFilter)
	stored = unittest.AssertExistsAndLoadBean(t, &webhook.Webhook{ID: apiHook.ID})
	assert.Equal(t, initialHeader, stored.HeaderAuthorizationEncrypted)
	assert.Equal(t, "isolated-signing-key", stored.Secret)
	assert.Equal(t, "http://example.com/", stored.URL)
	assert.Equal(t, webhook.ContentTypeJSON, stored.ContentType)
	invalidReq := NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/%s/%s/hooks/%d", owner.Name, repo.Name, apiHook.ID), api.EditHookOption{
		BranchFilter: new("[a-"),
	}).AddTokenAuth(token)
	MakeRequest(t, invalidReq, http.StatusUnprocessableEntity)
	stored = unittest.AssertExistsAndLoadBean(t, &webhook.Webhook{ID: apiHook.ID})
	assert.Equal(t, "release/*", stored.BranchFilter)
	assert.Equal(t, initialHeader, stored.HeaderAuthorizationEncrypted)
	changeReq := NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/%s/%s/hooks/%d", owner.Name, repo.Name, apiHook.ID), api.EditHookOption{
		Events:              []string{"pull_request"},
		BranchFilter:        new("feature/*"),
		AuthorizationHeader: new("Bearer replacement"),
	}).AddTokenAuth(token)
	changed := DecodeJSON(t, MakeRequest(t, changeReq, http.StatusOK), &api.Hook{})
	assert.Contains(t, changed.Events, "pull_request")
	assert.NotContains(t, changed.Events, "push")
	assert.Equal(t, "feature/*", changed.BranchFilter)
	stored = unittest.AssertExistsAndLoadBean(t, &webhook.Webhook{ID: apiHook.ID})
	value, err := stored.HeaderAuthorization()
	assert.NoError(t, err)
	assert.Equal(t, "Bearer replacement", value)
	clearOptionsReq := NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/%s/%s/hooks/%d", owner.Name, repo.Name, apiHook.ID), api.EditHookOption{
		Config:              map[string]string{},
		Events:              []string{},
		BranchFilter:        new(""),
		AuthorizationHeader: new(""),
	}).AddTokenAuth(token)
	clearedFields := DecodeJSON(t, MakeRequest(t, clearOptionsReq, http.StatusOK), &api.Hook{})
	assert.Equal(t, []string{"push"}, clearedFields.Events)
	assert.Empty(t, clearedFields.BranchFilter)
	stored = unittest.AssertExistsAndLoadBean(t, &webhook.Webhook{ID: apiHook.ID})
	assert.Empty(t, stored.HeaderAuthorizationEncrypted)
	assert.Equal(t, "isolated-signing-key", stored.Secret)
	assert.Equal(t, "http://example.com/", stored.URL)
	assert.Equal(t, webhook.ContentTypeJSON, stored.ContentType)

	hooksURL := fmt.Sprintf("/api/v1/repos/%s/%s/hooks", owner.Name, repo.Name)

	// Create with Name field omitted: Name should be ""
	req2 := NewRequestWithJSON(t, "POST", hooksURL, api.CreateHookOption{
		Type: "gitea",
		Config: api.CreateHookOptionConfig{
			"content_type": "json",
			"url":          "http://example.com/",
		},
	}).AddTokenAuth(token)
	resp2 := MakeRequest(t, req2, http.StatusCreated)
	created := DecodeJSON(t, resp2, &api.Hook{})
	assert.Empty(t, created.Name)

	hookURL := fmt.Sprintf("/api/v1/repos/%s/%s/hooks/%d", owner.Name, repo.Name, created.ID)

	// PATCH with Name set: existing Name must be updated
	setName := "original"
	setReq := NewRequestWithJSON(t, "PATCH", hookURL, api.EditHookOption{
		Name: &setName,
	}).AddTokenAuth(token)
	MakeRequest(t, setReq, http.StatusOK)

	// PATCH without Name field: name must remain "original"
	patchReq2 := NewRequestWithJSON(t, "PATCH", hookURL, api.EditHookOption{}).AddTokenAuth(token)
	patchResp2 := MakeRequest(t, patchReq2, http.StatusOK)
	notCleared := DecodeJSON(t, patchResp2, &api.Hook{})
	assert.Equal(t, "original", notCleared.Name)

	// PATCH with Name: "" explicitly: Name should be cleared to ""
	clearReq := NewRequestWithJSON(t, "PATCH", hookURL, api.EditHookOption{
		Name: new(""),
	}).AddTokenAuth(token)
	clearResp := MakeRequest(t, clearReq, http.StatusOK)
	cleared := DecodeJSON(t, clearResp, &api.Hook{})
	assert.Empty(t, cleared.Name)
}
