// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/setting"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceLoginAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	bad := NewRequestWithValues(t, "POST", "/user/login", map[string]string{"user_name": "user2", "password": "错误密码"})
	bad.RemoteAddr = "192.0.2.9:12345"
	MakeRequest(t, bad, http.StatusOK)
	loginUser(t, "user2")
	var events []*governance_model.AuditEvent
	require.NoError(t, db.GetEngine(t.Context()).Where("type LIKE ?", "authentication.login_%").Asc("id").Find(&events))
	require.Len(t, events, 3)
	require.Equal(t, "authentication.login_failed", events[0].Type)
	require.Equal(t, "anonymous", events[0].Actor.Kind)
	require.Zero(t, events[0].Actor.ID, "失败的用户名声明不能冒充实际认证用户")
	require.Equal(t, "192.0.2.9", events[0].Actor.IP)
	require.Equal(t, "authentication.login_authorized", events[1].Type)
	require.Equal(t, "pending", events[1].Result)
	require.Equal(t, "authentication.login_succeeded", events[2].Type)
	require.Equal(t, events[1].RequestID, events[2].RequestID)
	require.Equal(t, int64(2), events[2].Actor.ID)
}

func TestGovernanceLoginAuditFailureNoRememberCookie(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip("此故障注入使用 SQLite 触发器")
	}
	defer tests.PrepareTestEnv(t)()
	_, err := db.GetEngine(t.Context()).Exec("CREATE TRIGGER governance_login_audit_failure BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'authentication.login_succeeded' BEGIN SELECT RAISE(ABORT, '登录审计故障注入'); END")
	require.NoError(t, err)
	defer func() {
		_, err := db.GetEngine(context.WithoutCancel(t.Context())).Exec("DROP TRIGGER governance_login_audit_failure")
		require.NoError(t, err)
	}()
	response := MakeRequest(t, NewRequestWithValues(t, "POST", "/user/login", map[string]string{"user_name": "user2", "password": "password", "remember": "on"}), http.StatusInternalServerError)
	// 使用实际写出的响应头快照，不能用已发送后仍可修改的 Header 掩盖 Cookie 泄露。
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == setting.CookieRememberName {
			require.True(t, cookie.Value == "" || cookie.MaxAge < 0, "登录审计失败不能向客户端发送有效记住登录凭据")
		}
	}
}

func TestGovernanceRememberLoginAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	response := MakeRequest(t, NewRequestWithValues(t, "POST", "/user/login", map[string]string{"user_name": "user2", "password": "password", "remember": "on"}), http.StatusSeeOther)
	restored := emptyTestSession(t)
	baseURL, err := url.Parse(setting.AppURL)
	require.NoError(t, err)
	found := false
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == setting.CookieRememberName && cookie.Value != "" {
			restored.jar.SetCookies(baseURL, []*http.Cookie{cookie})
			found = true
		}
	}
	require.True(t, found)
	restored.MakeRequest(t, NewRequest(t, "GET", "/user/login"), http.StatusSeeOther)
	restored.MakeRequest(t, NewRequest(t, "GET", "/user/settings"), http.StatusOK)
	var events []*governance_model.AuditEvent
	require.NoError(t, db.GetEngine(t.Context()).Where("type LIKE ?", "authentication.login_%").Asc("id").Find(&events))
	require.Len(t, events, 4)
	require.Equal(t, "authentication.login_authorized", events[2].Type)
	require.Equal(t, "authentication.login_succeeded", events[3].Type)
	require.Equal(t, events[2].RequestID, events[3].RequestID)
	require.Contains(t, string(events[3].Details), "remember_cookie")
}

func TestGovernanceSecondFactorFailureAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	factor := &auth_model.TwoFactor{UID: 2}
	require.NoError(t, factor.SetSecret("JBSWY3DPEHPK3PXP"))
	_, err := factor.GenerateScratchToken()
	require.NoError(t, err)
	require.NoError(t, auth_model.NewTwoFactor(t.Context(), factor))
	session := emptyTestSession(t)
	response := session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user/login", map[string]string{"user_name": "user2", "password": "password"}), http.StatusSeeOther)
	require.Contains(t, response.Header().Get("Location"), "/user/two_factor")
	session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user/two_factor", map[string]string{"passcode": "错误验证码"}), http.StatusOK)
	session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user/two_factor/scratch", map[string]string{"token": "错误恢复码"}), http.StatusOK)
	session.MakeRequest(t, NewRequest(t, "GET", "/user/settings"), http.StatusSeeOther)
	var events []*governance_model.AuditEvent
	require.NoError(t, db.GetEngine(t.Context()).Where("type = ?", "authentication.login_failed").Asc("id").Find(&events))
	require.Len(t, events, 2)
	require.Contains(t, string(events[0].Details), "totp")
	require.Contains(t, string(events[1].Details), "recovery_code")
	for _, event := range events {
		require.Equal(t, "anonymous", event.Actor.Kind)
		require.Zero(t, event.Actor.ID)
		require.Equal(t, int64(2), event.ObjectID)
		require.Equal(t, "user2", event.ObjectPath)
		require.Equal(t, "user", event.ScopeType)
	}
}

func TestGovernanceWebAuthnFailureAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	second := loginUserWithPassword(t, "user32", "notpassword")
	second.MakeRequest(t, NewRequest(t, "GET", "/user/webauthn/assertion"), http.StatusOK)
	second.MakeRequest(t, NewRequestWithJSON(t, "POST", "/user/webauthn/assertion", map[string]string{"bogus": "1"}), http.StatusForbidden)
	passkey := emptyTestSession(t)
	passkey.MakeRequest(t, NewRequest(t, "GET", "/user/webauthn/passkey/assertion"), http.StatusOK)
	passkey.MakeRequest(t, NewRequestWithJSON(t, "POST", "/user/webauthn/passkey/login", map[string]string{"bogus": "1"}), http.StatusForbidden)
	var events []*governance_model.AuditEvent
	require.NoError(t, db.GetEngine(t.Context()).Where("type = ?", "authentication.login_failed").Asc("id").Find(&events))
	require.Len(t, events, 2)
	require.Equal(t, int64(32), events[0].ObjectID)
	require.Zero(t, events[1].ObjectID)
	for _, event := range events {
		require.Zero(t, event.Actor.ID)
		require.Equal(t, "anonymous", event.Actor.Kind)
		require.Contains(t, string(event.Details), "webauthn")
	}
}
