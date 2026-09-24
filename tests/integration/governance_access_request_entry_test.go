// Copyright 2026 Gitea. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/setting"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceGroupAccessRequestEntry(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	applicant := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "web"}
	group, err := governance_service.CreateGroup(ctx, owner, governance_service.GroupOption{Path: "request-entry-internal", Visibility: 1})
	require.NoError(t, err)
	groupURL := setting.AppSubURL + "/" + group.FullPath
	memberURL := setting.AppSubURL + "/governance/groups/" + strconv.FormatInt(group.ID, 10) + "?tab=members"
	requestURL := setting.AppSubURL + "/governance/groups/" + strconv.FormatInt(group.ID, 10) + "/access-requests"
	requestLink := `href="` + requestURL + `"`

	visitor := loginUser(t, "user4")
	for _, page := range []string{groupURL, memberURL} {
		response := visitor.MakeRequest(t, NewRequest(t, http.MethodGet, page), http.StatusOK)
		require.Equal(t, 1, strings.Count(response.Body.String(), requestLink))
	}
	response := visitor.MakeRequest(t, NewRequest(t, http.MethodGet, requestURL), http.StatusOK)
	require.Contains(t, response.Body.String(), "申请访问")
	request, err := governance_service.RequestAccess(ctx, applicant, "group", group.ID)
	require.NoError(t, err)
	response = visitor.MakeRequest(t, NewRequest(t, http.MethodGet, memberURL), http.StatusOK)
	require.Contains(t, response.Body.String(), requestLink)
	response = visitor.MakeRequest(t, NewRequest(t, http.MethodGet, requestURL), http.StatusOK)
	require.Contains(t, response.Body.String(), "你的申请正在等待处理")

	manager := loginUser(t, "user2")
	response = manager.MakeRequest(t, NewRequest(t, http.MethodGet, memberURL), http.StatusOK)
	require.Equal(t, 1, strings.Count(response.Body.String(), requestLink))
	response = manager.MakeRequest(t, NewRequest(t, http.MethodGet, requestURL), http.StatusOK)
	require.Contains(t, response.Body.String(), "待处理申请")
	require.NoError(t, governance_service.DecideAccessRequest(ctx, owner, "group", group.ID, request.ID, governance_service.GroupMemberOption{Role: governance_model.Reporter, Revision: group.Revision}, true))
	response = visitor.MakeRequest(t, NewRequest(t, http.MethodGet, memberURL), http.StatusOK)
	require.Contains(t, response.Body.String(), requestLink)
	require.NotContains(t, response.Body.String(), `>申请访问</a>`)
	response = visitor.MakeRequest(t, NewRequest(t, http.MethodGet, requestURL), http.StatusOK)
	require.Contains(t, response.Body.String(), "当前无需申请")

	privateGroup, err := governance_service.CreateGroup(ctx, owner, governance_service.GroupOption{Path: "request-entry-private", Visibility: 2})
	require.NoError(t, err)
	privateURL := setting.AppSubURL + "/governance/groups/" + strconv.FormatInt(privateGroup.ID, 10)
	visitor.MakeRequest(t, NewRequest(t, http.MethodGet, privateURL+"?tab=members"), http.StatusNotFound)
	visitor.MakeRequest(t, NewRequest(t, http.MethodGet, privateURL+"/access-requests"), http.StatusNotFound)

	publicGroup, err := governance_service.CreateGroup(ctx, owner, governance_service.GroupOption{Path: "request-entry-public", Visibility: 0})
	require.NoError(t, err)
	publicRequestURL := setting.AppSubURL + "/governance/groups/" + strconv.FormatInt(publicGroup.ID, 10) + "/access-requests"
	response = MakeRequest(t, NewRequest(t, http.MethodGet, setting.AppSubURL+"/"+publicGroup.FullPath), http.StatusOK)
	require.NotContains(t, response.Body.String(), `href="`+publicRequestURL+`"`)
	response = MakeRequest(t, NewRequest(t, http.MethodGet, publicRequestURL), http.StatusSeeOther)
	require.Contains(t, response.Header().Get("Location"), "/user/login")
}
