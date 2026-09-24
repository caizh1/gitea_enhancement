// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"testing"

	auth_model "gitea.dev/models/auth"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	api "gitea.dev/modules/structs"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestNativeOrgSettingsAncestorLifecycle(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	root, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "native-settings-root", Visibility: 2})
	require.NoError(t, err)
	child, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "child", ParentID: root.ID, Visibility: 2})
	require.NoError(t, err)
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: child.ID})
	session := loginUser(t, "user2")
	base := "/org/" + child.FullPath + "/settings/applications"
	values := map[string]string{
		"application_name": "原生设置测试应用", "redirect_uris": "http://127.0.0.1/callback", "confidential_client": "true",
	}
	session.MakeRequest(t, NewRequestWithValues(t, "POST", base+"/oauth2", values), http.StatusOK)
	app := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{UID: child.ID, Name: values["application_name"]})
	require.NotEmpty(t, app.ClientSecret)
	audit := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "oauth.application_created", ObjectID: app.ID})
	require.Equal(t, "group", audit.ScopeType)
	require.Equal(t, child.ID, audit.ScopeID)
	unittest.AssertExistsAndLoadBean(t, &governance_model.AuditScope{EventSequence: audit.ID, ScopeType: "group", ScopeID: root.ID})
	require.NotContains(t, string(audit.Details), app.ClientSecret)
	edit := fmt.Sprintf("%s/oauth2/%d", base, app.ID)
	values["application_name"] = "归档前修改"
	resp := session.MakeRequest(t, NewRequestWithValues(t, "POST", edit, values), http.StatusSeeOther)
	require.Equal(t, base, resp.Header().Get("Location"))

	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteOrganization)
	blockPath := "/api/v1/orgs/" + owner.Name + "/blocks/user4"
	MakeRequest(t, NewRequest(t, "PUT", blockPath).AddTokenAuth(token), http.StatusNoContent)
	assertBlockAudit := func(eventType string) {
		t.Helper()
		event := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: eventType, ScopeType: "group", ScopeID: child.ID, ObjectID: 4})
		require.Equal(t, int64(2), event.Actor.ID)
		unittest.AssertExistsAndLoadBean(t, &governance_model.AuditScope{EventSequence: event.ID, ScopeType: "group", ScopeID: root.ID})
		require.NotContains(t, string(event.Details), "不应进入审计的私有备注")
		unittest.AssertCount(t, &governance_model.AuditEvent{Type: eventType, ScopeID: child.ID}, 1)
	}
	assertBlockAudit("user.blocked")
	MakeRequest(t, NewRequest(t, "PUT", blockPath).AddTokenAuth(token), http.StatusBadRequest)
	assertBlockAudit("user.blocked")
	notePath := "/org/" + child.FullPath + "/settings/blocked_users"
	for range 2 {
		session.MakeRequest(t, NewRequestWithValues(t, "POST", notePath, map[string]string{
			"action": "note", "blockee": "user4", "note": "不应进入审计的私有备注",
		}), http.StatusOK)
	}
	assertBlockAudit("user.block_note_updated")
	root, err = governance_service.CheckGroupAccess(ctx, actor.ID, root.ID, governance_model.ManageGroup)
	require.NoError(t, err)
	_, err = governance_service.SetGroupArchiveState(ctx, actor, root.ID, governance_service.GroupArchiveOption{Archived: true, Revision: root.Revision})
	require.NoError(t, err)

	values["application_name"] = "归档后不应写入"
	session.MakeRequest(t, NewRequestWithValues(t, "POST", edit, values), http.StatusSeeOther)
	session.MakeRequest(t, NewRequest(t, "POST", edit+"/regenerate_secret"), http.StatusSeeOther)
	session.MakeRequest(t, NewRequestWithValues(t, "POST", base+"/oauth2", values), http.StatusSeeOther)
	stored, err := auth_model.GetOAuth2ApplicationByID(ctx, app.ID)
	require.NoError(t, err)
	require.Equal(t, "归档前修改", stored.Name)
	require.Equal(t, app.ClientSecret, stored.ClientSecret)
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Application{UID: child.ID, Name: values["application_name"]})
	MakeRequest(t, NewRequest(t, "DELETE", blockPath).AddTokenAuth(token), http.StatusConflict)
	unittest.AssertExistsAndLoadBean(t, &user_model.Blocking{BlockerID: child.ID, BlockeeID: 4})
	unittest.AssertNotExistsBean(t, &governance_model.AuditEvent{Type: "user.unblocked", ScopeID: child.ID})
	// 原生个人 API 的 sudo 不能将组织应用当作个人应用修改。
	apiBody := api.CreateOAuth2ApplicationOptions{Name: "不应通过 sudo 写入", RedirectURIs: []string{"http://127.0.0.1/callback"}}
	apiPath := "/api/v1/user/applications/oauth2"
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		path := apiPath
		if method != "POST" {
			path += fmt.Sprintf("/%d", app.ID)
		}
		request := NewRequestWithJSON(t, method, path, &apiBody).AddBasicAuth("user1")
		request.Header.Set("Sudo", owner.Name)
		MakeRequest(t, request, http.StatusNotFound)
	}
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: app.ID, Name: "归档前修改", ClientSecret: app.ClientSecret})

	// 归档仍允许收回授权，不允许重新开放配置。
	session.MakeRequest(t, NewRequest(t, "POST", edit+"/delete"), http.StatusOK)
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Application{ID: app.ID})
	root, err = governance_service.CheckGroupAccess(ctx, actor.ID, root.ID, governance_model.ManageGroup)
	require.NoError(t, err)
	_, err = governance_service.SetGroupArchiveState(ctx, actor, root.ID, governance_service.GroupArchiveOption{Archived: false, Revision: root.Revision})
	require.NoError(t, err)
	MakeRequest(t, NewRequest(t, "DELETE", blockPath).AddTokenAuth(token), http.StatusNoContent)
	unittest.AssertNotExistsBean(t, &user_model.Blocking{BlockerID: child.ID, BlockeeID: 4})
	assertBlockAudit("user.unblocked")
	session.MakeRequest(t, NewRequestWithValues(t, "POST", base+"/oauth2", values), http.StatusOK)
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{UID: child.ID, Name: values["application_name"]})

	outsider := loginUser(t, "user4")
	outsider.MakeRequest(t, NewRequest(t, "GET", base), http.StatusNotFound)
	outsider.MakeRequest(t, NewRequestWithValues(t, "POST", base+"/oauth2", values), http.StatusNotFound)
}
