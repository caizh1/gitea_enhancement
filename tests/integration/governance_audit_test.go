// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/test"
	governance_api "gitea.dev/routers/api/v1/governance"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGovernanceAuditHTTP(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	event := &governance_model.AuditEvent{Type: "member.updated", ScopeType: "group", ScopeID: 3, Actor: governance_model.Actor{ID: 2, Name: "<script>测试</script>", Kind: "user", Transport: "api"}, ObjectType: "user", ObjectID: 4, Result: "success", ObjectPath: "私有群组/历史路径", Details: []byte(`{"after":{"role":"Reporter"}}`)}
	require.NoError(t, governance_model.AppendAudit(ctx, event))
	readToken := getUserToken(t, "user2", auth_model.AccessTokenScopeReadGovernance)
	writeToken := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteGovernance)
	otherToken := getUserToken(t, "user4", auth_model.AccessTokenScopeReadGovernance)
	publicToken := getUserToken(t, "user2", auth_model.AccessTokenScopeReadGovernance, auth_model.AccessTokenScopePublicOnly)
	repoToken := getUserToken(t, "user2", auth_model.AccessTokenScopeReadRepository)
	endpoint := "/api/v1/governance/audit-events?scope_type=group&scope_id=3"
	MakeRequest(t, NewRequest(t, "GET", endpoint), http.StatusUnauthorized)
	MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(otherToken), http.StatusNotFound)
	MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(publicToken), http.StatusForbidden)
	MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(repoToken), http.StatusForbidden)
	resp := MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(readToken), http.StatusOK)
	page := DecodeJSON(t, resp, &governance_model.AuditPage{})
	require.Len(t, page.Events, 1)
	assert.Equal(t, event.EventID, page.Events[0].EventID)
	assert.Equal(t, "no-store", resp.Header().Get("Cache-Control"))

	options := governance_api.AuditExportOption{Filter: governance_model.AuditFilter{ScopeType: "group", ScopeID: 3, From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Second)}, Format: "json"}
	MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/audit-exports", options).AddTokenAuth(readToken), http.StatusForbidden)
	resp = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/audit-exports", options).AddTokenAuth(writeToken), http.StatusAccepted)
	job := DecodeJSON(t, resp, &governance_model.AuditExport{})
	require.NoError(t, governance_service.RunAuditExports(ctx))
	resp = MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/audit-exports/"+job.ID+"/download").AddTokenAuth(readToken), http.StatusOK)
	var events []*governance_model.AuditEvent
	DecodeJSON(t, resp, &events)
	require.Len(t, events, 1)
	assert.Equal(t, event.EventID, events[0].EventID)
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/audit-exports/"+job.ID+"/download").AddTokenAuth(otherToken), http.StatusNotFound)

	session := loginUser(t, "user2")
	resp = session.MakeRequest(t, NewRequest(t, "GET", "/governance/audit?scope_type=group&scope_id=3").SetHeader("Accept", "text/html"), http.StatusOK)
	require.Contains(t, resp.Header().Get("Content-Type"), "text/html")
	require.True(t, test.IsNormalPageCompleted(resp.Body.String()))
	assert.Contains(t, resp.Body.String(), "私有群组/历史路径")
	assert.NotContains(t, resp.Body.String(), "<script>测试</script>")
	assert.Contains(t, resp.Body.String(), "&lt;script&gt;测试&lt;/script&gt;")
	assert.Contains(t, resp.Body.String(), "relative-time")
	assert.Contains(t, resp.Body.String(), "after")
	resp = session.MakeRequest(t, NewRequest(t, "GET", "/governance/audit?scope_type=group&scope_id=3&from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z"), http.StatusBadRequest)
	assert.Contains(t, resp.Body.String(), `role="alert"`)
	assert.Contains(t, resp.Body.String(), "单次查询最多三十天")
	assert.NotContains(t, resp.Body.String(), "这个时间范围内没有可见的治理事件")
	assert.NotContains(t, resp.Body.String(), "私有群组/历史路径")
	adminToken := getUserToken(t, "user1", auth_model.AccessTokenScopeWriteGovernance)
	resp = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/audit-exports?sudo=user2", options).AddTokenAuth(adminToken), http.StatusAccepted)
	delegated := DecodeJSON(t, resp, &governance_model.AuditExport{})
	assert.EqualValues(t, 2, delegated.UserID)
	var recorded governance_model.AuditEvent
	_, err := db.GetEngine(ctx).Where("type = ?", "audit.export_requested").Desc("id").Get(&recorded)
	require.NoError(t, err)
	assert.EqualValues(t, 1, recorded.Actor.ID, "真实管理员身份必须保留")
	assert.EqualValues(t, 2, recorded.Actor.ActingAsID, "代办目标单独记录")
	streamOption := governance_service.AuditStreamOption{ScopeType: "group", ScopeID: 3, Name: "原生外送验收", Kind: "http", Endpoint: "http://127.0.0.1:1/events", Enabled: false}
	MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/audit-streams", streamOption).AddTokenAuth(readToken), http.StatusForbidden)
	resp = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/audit-streams", streamOption).AddTokenAuth(writeToken), http.StatusCreated)
	stream := DecodeJSON(t, resp, &governance_service.AuditStreamSaved{})
	assert.NotEmpty(t, stream.VerificationToken)
	resp = MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/audit-streams?scope_type=group&scope_id=3").AddTokenAuth(readToken), http.StatusOK)
	assert.NotContains(t, resp.Body.String(), stream.VerificationToken, "普通列表不得返回验证秘密")
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/audit-streams?scope_type=group&scope_id=3").AddTokenAuth(otherToken), http.StatusNotFound)
	resp = session.MakeRequest(t, NewRequest(t, "GET", "/governance/audit/streams?scope_type=group&scope_id=3"), http.StatusOK)
	assert.Contains(t, resp.Body.String(), "原生外送验收")
	assert.Contains(t, resp.Body.String(), "测试连通性")
	assert.NotContains(t, resp.Body.String(), stream.VerificationToken)
	loginUser(t, "user4").MakeRequest(t, NewRequest(t, "GET", "/governance/audit/streams?scope_type=group&scope_id=3"), http.StatusNotFound)
	denied := loginUser(t, "user4").MakeRequest(t, NewRequest(t, "GET", "/governance/audit?scope_type=group&scope_id=3").SetHeader("Accept", "text/html"), http.StatusNotFound)
	require.Contains(t, denied.Header().Get("Content-Type"), "text/html")
	require.True(t, test.IsNormalPageCompleted(denied.Body.String()))
	require.NotContains(t, denied.Body.String(), "私有群组/历史路径")
	_, err = db.GetEngine(ctx).ID(2).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: true})
	require.NoError(t, err)
	assert.ErrorIs(t, governance_service.CheckAuditAccess(ctx, 2, "group", 3), governance_model.ErrNotFound)
}
