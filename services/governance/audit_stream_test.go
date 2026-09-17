// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func streamTestActor(t *testing.T) governance_model.Actor {
	t.Helper()
	return RequestActor(unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1}), "127.0.0.1:1234", "api")
}

func TestAuditStreamRetryPauseAndCredentials(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	oldAllow := setting.Security.AllowedHostList
	setting.Security.AllowedHostList = "loopback"
	t.Cleanup(func() { setting.Security.AllowedHostList = oldAllow })
	var mu sync.Mutex
	fail := true
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		payload, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		var event governance_model.AuditEvent
		assert.NoError(t, json.Unmarshal(payload, &event))
		assert.Equal(t, event.EventID, r.Header.Get("X-Gitea-Event-Id"))
		assert.Equal(t, "验收凭据仅用于隔离测试", r.Header.Get("X-Test-Header"))
		assert.NotEmpty(t, r.Header.Get("X-Gitlab-Event-Streaming-Token"))
		received = append(received, event.EventID)
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	actor := streamTestActor(t)
	option := AuditStreamOption{ScopeType: "instance", Name: "隔离接收器", Kind: "http", Endpoint: server.URL, Enabled: true, EventTypes: []string{"member.added"}, Credentials: &AuditStreamCredentials{Headers: map[string]string{"X-Test-Header": "验收凭据仅用于隔离测试"}}}
	saved, err := SaveAuditStream(ctx, actor, 0, option)
	require.NoError(t, err)
	require.NotEmpty(t, saved.VerificationToken)
	raw, err := json.Marshal(saved)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "验收凭据仅用于隔离测试")
	assert.NotContains(t, saved.Secret, "验收凭据仅用于隔离测试")
	event := &governance_model.AuditEvent{Type: "member.added", ScopeType: "group", ScopeID: 3, Actor: actor, ObjectType: "user", ObjectID: 4, Result: "success"}
	require.NoError(t, governance_model.AppendAudit(ctx, event))
	require.NoError(t, RunAuditStreams(ctx))
	states, err := ListAuditStreams(ctx, actor.ID, "instance", 0)
	require.NoError(t, err)
	state := states[0]
	assert.EqualValues(t, 1, state.Pending)
	assert.NotEmpty(t, state.LastError)
	var pending governance_model.AuditDelivery
	has, err := db.GetEngine(ctx).Where("stream_id = ?", saved.ID).Get(&pending)
	require.NoError(t, err)
	require.True(t, has)
	assert.Equal(t, 1, pending.Attempts)
	assert.Greater(t, pending.NextAttempt, time.Now().Unix())
	require.NoError(t, RunAuditStreams(ctx))
	mu.Lock()
	assert.Len(t, received, 1)
	mu.Unlock()
	option.Revision, option.Credentials, option.Enabled = saved.Revision, nil, false
	saved, err = SaveAuditStream(ctx, actor, saved.ID, option)
	require.NoError(t, err)
	require.NoError(t, RunAuditStreams(ctx))
	state, err = streamState(ctx, saved.AuditStream)
	require.NoError(t, err)
	assert.EqualValues(t, 1, state.Pending, "停用不能删除积压正文")
	option.Revision, option.Endpoint = saved.Revision, server.URL+"/另一个接收器"
	_, err = SaveAuditStream(ctx, actor, saved.ID, option)
	assert.ErrorIs(t, err, governance_model.ErrConflict)
	option.Endpoint, option.Enabled = server.URL, true
	saved, err = SaveAuditStream(ctx, actor, saved.ID, option)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(pending.ID).Cols("next_attempt").Update(&governance_model.AuditDelivery{})
	require.NoError(t, err)
	mu.Lock()
	fail = false
	mu.Unlock()
	require.NoError(t, RunAuditStreams(ctx))
	states, err = ListAuditStreams(ctx, actor.ID, "instance", 0)
	require.NoError(t, err)
	require.Len(t, states, 1)
	assert.Zero(t, states[0].Pending)
	assert.EqualValues(t, 1, states[0].DeliveredCount)
	mu.Lock()
	assert.Equal(t, []string{event.EventID, event.EventID}, received)
	mu.Unlock()
	option.Revision = saved.Revision - 1
	_, err = SaveAuditStream(ctx, actor, saved.ID, option)
	assert.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = ListAuditStreams(ctx, 4, "instance", 0)
	assert.ErrorIs(t, err, governance_model.ErrNotFound)
}

func TestAuditStreamInFlightUpdateAndTestFilter(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := streamTestActor(t)
	option := AuditStreamOption{ScopeType: "instance", Name: "测试外送", Kind: "http", Endpoint: "https://audit.example.invalid/events", Enabled: true, EventTypes: []string{"access.code"}}
	saved, err := SaveAuditStream(ctx, actor, 0, option)
	require.NoError(t, err)
	require.NoError(t, TestAuditStream(ctx, actor, saved.ID, saved.Revision))
	_, delivery, err := leaseAuditStream(ctx, saved.ID)
	require.NoError(t, err)
	require.NotNil(t, delivery, "连通性测试需要绕过当前事件过滤，保留真实投递状态")
	option.Revision, option.Enabled = saved.Revision, false
	_, err = SaveAuditStream(ctx, actor, saved.ID, option)
	assert.ErrorIs(t, err, governance_model.ErrConflict)
	require.NoError(t, governance_model.FinishDelivery(ctx, delivery, true, time.Now()))
	_, err = SaveAuditStream(ctx, actor, saved.ID, option)
	require.NoError(t, err)
	_, delivery, err = leaseAuditStream(ctx, saved.ID)
	require.NoError(t, err)
	assert.Nil(t, delivery)
}

func TestAuditStreamRejectsRedirectAndHostBypass(t *testing.T) {
	var reached bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(http.StatusNoContent) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	stream := &governance_model.AuditStream{Kind: "http", Endpoint: redirect.URL}
	event := &governance_model.AuditEvent{EventID: "固定验收事件", OccurredAt: time.Now()}
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	delivery := &governance_model.AuditDelivery{EventID: event.EventID, Payload: string(raw)}
	err = sendAuditPayload(t.Context(), stream, &AuditStreamCredentials{VerificationToken: "只用于隔离验证的标识"}, delivery, http.DefaultTransport)
	require.Error(t, err)
	assert.False(t, reached, "不得把验证标识跟随重定向发送给其他服务")
	oldAllow := setting.Security.AllowedHostList
	setting.Security.AllowedHostList = "external"
	t.Cleanup(func() { setting.Security.AllowedHostList = oldAllow })
	base, transport, err := auditTransport(stream)
	require.NoError(t, err)
	defer base.CloseIdleConnections()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, redirect.URL, strings.NewReader("{}"))
	require.NoError(t, err)
	response, err := transport.RoundTrip(request)
	if response != nil {
		response.Body.Close()
	}
	assert.Error(t, err, "外送必须遵守原生目标网络允许列表")
	option := AuditStreamOption{Name: "目标", Kind: "http", Endpoint: "https://audit.invalid/?token=禁止内嵌"}
	assert.ErrorIs(t, validateAuditStream(&option, &AuditStreamCredentials{VerificationToken: "0123456789abcdef"}), governance_model.ErrInvalid)
	option.Endpoint = "https://audit.invalid/"
	assert.ErrorIs(t, validateAuditStream(&option, &AuditStreamCredentials{VerificationToken: "0123456789abcdef", Headers: map[string]string{"Host": "other.invalid"}}), governance_model.ErrInvalid)
}
