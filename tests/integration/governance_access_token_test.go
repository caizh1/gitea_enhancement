// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
	api "gitea.dev/modules/structs"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceNativeAccessTokenAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	req := NewRequestWithJSON(t, "POST", "/api/v1/users/user2/tokens", map[string]any{"name": "原生凭据审计", "scopes": []string{"read:user"}}).AddBasicAuth("user2")
	req.RemoteAddr = "192.0.2.5:12345"
	response := MakeRequest(t, req, http.StatusCreated)
	token := DecodeJSON(t, response, &api.AccessToken{})
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/user").AddTokenAuth(token.Token), http.StatusOK)
	req = NewRequest(t, "DELETE", "/api/v1/token").AddTokenAuth(token.Token)
	req.RemoteAddr = "192.0.2.6:12345"
	MakeRequest(t, req, http.StatusNoContent)
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/user").AddTokenAuth(token.Token), http.StatusUnauthorized)
	var events []*governance_model.AuditEvent
	require.NoError(t, db.GetEngine(t.Context()).Where("object_type = ? AND object_id = ?", "access_token", token.ID).Asc("id").Find(&events))
	require.Len(t, events, 2)
	require.Equal(t, "credential.access_token_created", events[0].Type)
	require.Equal(t, "credential.access_token_revoked", events[1].Type)
	require.Equal(t, "192.0.2.5", events[0].Actor.IP)
	require.Equal(t, "192.0.2.6", events[1].Actor.IP)
	for _, event := range events {
		require.Equal(t, int64(2), event.Actor.ID)
		require.Equal(t, "api", event.Actor.Transport)
		raw, err := json.Marshal(event)
		require.NoError(t, err)
		require.False(t, strings.Contains(string(raw), token.Token), "审计不能记录令牌正文") //nolint:testifylint // 失败日志不能打印秘密值。
	}
}

func TestGovernanceWebAccessTokenAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user2")
	req := NewRequestWithValues(t, "POST", "/user/settings/applications", map[string]string{"name": "网页凭据审计", "scope-user": "read:user"})
	req.RemoteAddr = "192.0.2.7:12345"
	session.MakeRequest(t, req, http.StatusSeeOther)
	var token auth_model.AccessToken
	has, err := db.GetEngine(t.Context()).Where("uid = ? AND name = ?", 2, "网页凭据审计").Get(&token)
	require.NoError(t, err)
	require.True(t, has)
	req = NewRequestWithValues(t, "POST", "/user/settings/applications/delete", map[string]string{"id": strconv.FormatInt(token.ID, 10)})
	req.RemoteAddr = "192.0.2.8:12345"
	session.MakeRequest(t, req, http.StatusOK)
	var events []*governance_model.AuditEvent
	require.NoError(t, db.GetEngine(t.Context()).Where("object_type = ? AND object_id = ?", "access_token", token.ID).Asc("id").Find(&events))
	require.Len(t, events, 2)
	require.Equal(t, "credential.access_token_created", events[0].Type)
	require.Equal(t, "credential.access_token_revoked", events[1].Type)
	require.Equal(t, "192.0.2.7", events[0].Actor.IP)
	require.Equal(t, "192.0.2.8", events[1].Actor.IP)
	for _, event := range events {
		require.Equal(t, int64(2), event.Actor.ID)
		require.Equal(t, "web", event.Actor.Transport)
	}
}
