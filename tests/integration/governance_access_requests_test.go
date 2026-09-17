// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceAccessRequestHTTP(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, owner, governance_service.GroupOption{Path: "http-request", Visibility: 0})
	require.NoError(t, err)
	applicant := getUserToken(t, "user4", auth_model.AccessTokenScopeWriteGovernance)
	reader := getUserToken(t, "user4", auth_model.AccessTokenScopeReadGovernance)
	manager := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteGovernance)
	endpoint := fmt.Sprintf("/api/v1/governance/groups/%d/access-requests", group.ID)
	MakeRequest(t, NewRequest(t, "GET", endpoint), http.StatusUnauthorized)
	MakeRequest(t, NewRequest(t, "POST", endpoint).AddTokenAuth(reader), http.StatusForbidden)
	response := MakeRequest(t, NewRequest(t, "POST", endpoint).AddTokenAuth(applicant), http.StatusCreated)
	request := DecodeJSON(t, response, &governance_model.AccessRequest{})
	require.EqualValues(t, 4, request.UserID)
	response = MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/access-requests").AddTokenAuth(reader), http.StatusOK)
	var own []*governance_model.AccessRequest
	DecodeJSON(t, response, &own)
	require.Len(t, own, 1)
	require.Equal(t, request.ID, own[0].ID)
	MakeRequest(t, NewRequest(t, "DELETE", fmt.Sprintf("/api/v1/governance/access-requests/%d", request.ID)).AddTokenAuth(manager), http.StatusNotFound)

	MakeRequest(t, NewRequest(t, "POST", endpoint).AddTokenAuth(applicant), http.StatusConflict)
	response = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(reader), http.StatusOK)
	state := DecodeJSON(t, response, &governance_service.AccessRequestState{})
	require.Empty(t, state.Requests)
	require.Equal(t, request.ID, state.OwnRequest.ID)
	response = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(manager), http.StatusOK)
	state = DecodeJSON(t, response, &governance_service.AccessRequestState{})
	require.Len(t, state.Requests, 1)
	page := fmt.Sprintf("/governance/groups/%d/access-requests", group.ID)
	session := loginUser(t, "user2")
	response = session.MakeRequest(t, NewRequest(t, "GET", page), http.StatusOK)
	require.Contains(t, response.Body.String(), "批准并添加直接授权")
	require.Contains(t, response.Body.String(), "user4")
	applicantSession := loginUser(t, "user4")
	response = applicantSession.MakeRequest(t, NewRequest(t, "GET", "/governance/access-requests"), http.StatusOK)
	require.Contains(t, response.Body.String(), "http-request")
	require.Contains(t, response.Body.String(), "撤回申请")
	otherSession := loginUser(t, "user5")
	response = otherSession.MakeRequest(t, NewRequest(t, "GET", "/governance/access-requests"), http.StatusOK)
	require.NotContains(t, response.Body.String(), "http-request")
	approve := fmt.Sprintf("%s/%d/approve", endpoint, request.ID)
	MakeRequest(t, NewRequestWithJSON(t, "POST", approve, map[string]any{"revision": state.Revision}).AddTokenAuth(applicant), http.StatusNotFound)
	MakeRequest(t, NewRequestWithJSON(t, "POST", approve, map[string]any{"revision": state.Revision}).AddTokenAuth(manager), http.StatusNoContent)
	unittest.AssertExistsAndLoadBean(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4, Role: governance_model.Developer})
	MakeRequest(t, NewRequestWithJSON(t, "POST", approve, map[string]any{"revision": state.Revision}).AddTokenAuth(manager), http.StatusNotFound)
	// 项目和群组的路径均经过真实路由，不能只覆盖服务函数。
	projectEndpoint := "/api/v1/governance/repositories/1/access-requests"
	MakeRequest(t, NewRequest(t, "GET", projectEndpoint).AddTokenAuth(manager), http.StatusOK)
	session.MakeRequest(t, NewRequest(t, "GET", "/governance/repositories/1/access-requests"), http.StatusOK)
}

func TestGovernanceAccessRequestAuditRollback(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	applicant := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, owner, governance_service.GroupOption{Path: "request-rollback", Visibility: 0})
	require.NoError(t, err)
	request, err := governance_service.RequestAccess(ctx, applicant, "group", group.ID)
	require.NoError(t, err)
	create := "CREATE TRIGGER governance_request_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'access_request.approved' BEGIN SELECT RAISE(ABORT, '访问申请审计故障'); END"
	drop := "DROP TRIGGER IF EXISTS governance_request_fail"
	if setting.Database.Type.IsPostgreSQL() {
		_, err := db.GetEngine(ctx).Exec("CREATE FUNCTION governance_request_fail_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type = 'access_request.approved' THEN RAISE EXCEPTION '访问申请审计故障'; END IF; RETURN NEW; END $$")
		require.NoError(t, err)
		defer func() {
			_, err := db.GetEngine(context.WithoutCancel(ctx)).Exec("DROP FUNCTION IF EXISTS governance_request_fail_fn() CASCADE")
			require.NoError(t, err)
		}()
		create = "CREATE TRIGGER governance_request_fail BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION governance_request_fail_fn()"
		drop += " ON governance_audit_event"
	}
	_, err = db.GetEngine(ctx).Exec(create)
	require.NoError(t, err)
	defer func() { _, err := db.GetEngine(context.WithoutCancel(ctx)).Exec(drop); require.NoError(t, err) }()
	option := governance_service.GroupMemberOption{Role: governance_model.Developer, Revision: group.Revision}
	require.Error(t, governance_service.DecideAccessRequest(ctx, owner, "group", group.ID, request.ID, option, true))
	unittest.AssertExistsAndLoadBean(t, &governance_model.AccessRequest{ID: request.ID})
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 0)
	current, err := governance_model.GetNamespace(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, group.Revision, current.Revision)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "access_request.approved"}, 0)
	_, err = db.GetEngine(ctx).Exec(drop)
	require.NoError(t, err)
	require.NoError(t, governance_service.DecideAccessRequest(ctx, owner, "group", group.ID, request.ID, option, true))
	unittest.AssertCount(t, &governance_model.AccessRequest{ID: request.ID}, 0)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "access_request.approved"}, 1)
}

func TestGovernanceRepositoryDirectMembersHTTP(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteGovernance)
	other := getUserToken(t, "user4", auth_model.AccessTokenScopeWriteGovernance)
	endpoint := "/api/v1/governance/repositories/2/members"
	response := MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(token), http.StatusOK)
	state := DecodeJSON(t, response, &governance_service.RepositoryMembersState{})
	MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(other), http.StatusNotFound)
	MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint+"/4", map[string]any{"role": 20, "revision": state.Revision}).AddTokenAuth(token), http.StatusNoContent)
	response = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(token), http.StatusOK)
	state = DecodeJSON(t, response, &governance_service.RepositoryMembersState{})
	require.Len(t, state.Members, 1)
	require.EqualValues(t, 4, state.Members[0].UserID)
	session := loginUser(t, "user2")
	response = session.MakeRequest(t, NewRequest(t, "GET", "/governance/repositories/2/members"), http.StatusOK)
	require.Contains(t, response.Body.String(), "user4")
	require.Contains(t, response.Body.String(), "撤销项目直接授权")
	MakeRequest(t, NewRequest(t, "DELETE", endpoint+"/4?revision=0").AddTokenAuth(token), http.StatusConflict)
	MakeRequest(t, NewRequest(t, "DELETE", fmt.Sprintf("%s/4?revision=%d", endpoint, state.Revision)).AddTokenAuth(other), http.StatusNotFound)
	MakeRequest(t, NewRequest(t, "DELETE", fmt.Sprintf("%s/4?revision=%d", endpoint, state.Revision)).AddTokenAuth(token), http.StatusNoContent)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "repository", ScopeID: 2, UserID: 4}, 0)
}

func TestGovernanceRepositoryAllMembersHTTP(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	group, err := governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, actor, 3, governance_service.GroupMemberOption{UserID: 4, Role: governance_model.Reporter, Revision: group.Revision}, false))
	manager := getUserToken(t, "user2", auth_model.AccessTokenScopeReadGovernance)
	endpoint := "/api/v1/governance/repositories/3/members"
	response := MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(manager), http.StatusOK)
	direct := DecodeJSON(t, response, &governance_service.RepositoryMembersState{})
	require.Empty(t, direct.Members)
	response = MakeRequest(t, NewRequest(t, "GET", endpoint+"?include_inherited=true").AddTokenAuth(manager), http.StatusOK)
	all := DecodeJSON(t, response, &governance_service.RepositoryMembersState{})
	require.True(t, all.AllSources)
	inherited := false
	for _, member := range all.Members {
		if member.UserID == 4 {
			inherited = true
			require.Nil(t, member.Direct)
			require.Equal(t, "inherited", member.Grants[0].Source)
		}
	}
	require.True(t, inherited, "没有项目直接授权的继承成员必须出现")
	session := loginUser(t, "user2")
	response = session.MakeRequest(t, NewRequest(t, "GET", "/governance/repositories/3/members"), http.StatusOK)
	require.Contains(t, response.Body.String(), "user4")
	require.Contains(t, response.Body.String(), "本用户没有项目直接授权")
	require.Contains(t, response.Body.String(), "查看来源 org3")
}
