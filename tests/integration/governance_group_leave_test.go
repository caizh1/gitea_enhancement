// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"testing"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGroupMemberLeavesOwnDirectSource(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	group, err := governance_service.CreateGroup(ctx, owner, governance_service.GroupOption{Path: "member-leave", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, owner, group.ID, governance_service.GroupMemberOption{UserID: 4, Role: governance_model.Reporter, Revision: group.Revision}, false))
	path := fmt.Sprintf("/governance/groups/%d", group.ID)
	session := loginUser(t, "user4")
	page := session.MakeRequest(t, NewRequest(t, "GET", path+"?tab=members"), http.StatusOK)
	require.Contains(t, page.Body.String(), "退出本群组直接授权")
	csrf := NewHTMLParser(t, page.Body).GetInputValueByName("_csrf")
	loginUser(t, "user5").MakeRequest(t, NewRequestWithValues(t, "POST", path+"/leave", map[string]string{"_csrf": csrf}), http.StatusNotFound)
	result := session.MakeRequest(t, NewRequestWithValues(t, "POST", path+"/leave", map[string]string{"_csrf": csrf}), http.StatusSeeOther)
	require.Equal(t, "/governance/groups", result.Header().Get("Location"))
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 0)
	session.MakeRequest(t, NewRequest(t, "GET", path+"?tab=members"), http.StatusNotFound)
	session.MakeRequest(t, NewRequestWithValues(t, "POST", path+"/leave", map[string]string{"_csrf": csrf}), http.StatusNotFound)
}
