// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	auth_model "gitea.dev/models/auth"
	governance_model "gitea.dev/models/governance"
	governance_api "gitea.dev/routers/api/v1/governance"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestApprovalPolicyNativeAPIAndPage(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		owner := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteGovernance)
		reader := getUserToken(t, "user2", auth_model.AccessTokenScopeReadGovernance)
		member := getUserToken(t, "user4", auth_model.AccessTokenScopeReadGovernance)
		endpoint := "/api/v1/governance/approval-policies/group/3"
		option := governance_api.ApprovalRuleOption{Rule: governance_model.ApprovalRule{Name: "组织必须批准", Required: 1, UserIDs: []int64{2}, BranchMode: "all", Enabled: true}}
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, option).AddTokenAuth(reader), http.StatusForbidden)
		MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(member), http.StatusNotFound)
		MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/approval-policies/instance/0", option).AddTokenAuth(owner), http.StatusNotFound)
		response := MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, option).AddTokenAuth(owner), http.StatusCreated)
		rule := DecodeJSON(t, response, &governance_model.ApprovalRule{})
		require.True(t, rule.Locked)
		session := loginUser(t, "user2")
		response = session.MakeRequest(t, NewRequest(t, "GET", "/governance/groups/3?tab=settings"), http.StatusOK)
		require.Contains(t, response.Body.String(), "/org/org3/settings/approvals")
		page := "/org/org3/settings/approvals"
		response = session.MakeRequest(t, NewRequest(t, "GET", page+"?edit="+strconv.FormatInt(rule.ID, 10)), http.StatusOK)
		require.Contains(t, response.Body.String(), "强制审批策略")
		require.Contains(t, response.Body.String(), "保存审批设置")
		values := url.Values{"rule_id": {strconv.FormatInt(rule.ID, 10)}, "revision": {strconv.FormatInt(rule.Revision, 10)}, "name": {"组织必须批准"}, "required": {"2"}, "users": {"2"}, "branch_mode": {"all"}, "enabled": {"true"}}
		session.MakeRequest(t, NewRequestWithURLValues(t, "POST", page, values), http.StatusSeeOther)
		response = MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/repositories/3/approval-rules").AddTokenAuth(reader), http.StatusOK)
		state := DecodeJSON(t, response, &governance_service.RepositoryApprovalRules{})
		require.Len(t, state.Rules, 1)
		require.Equal(t, 2, state.Rules[0].Required)
		MakeRequest(t, NewRequest(t, "DELETE", "/api/v1/governance/repositories/3/approval-rules/"+strconv.FormatInt(rule.ID, 10)+"?revision=2").AddTokenAuth(owner), http.StatusNotFound)
		MakeRequest(t, NewRequest(t, "DELETE", endpoint+"/"+strconv.FormatInt(rule.ID, 10)+"?revision=1").AddTokenAuth(owner), http.StatusConflict)
		MakeRequest(t, NewRequest(t, "DELETE", endpoint+"/"+strconv.FormatInt(rule.ID, 10)+"?revision=2").AddTokenAuth(owner), http.StatusNoContent)
	})
}

func TestScopeApprovalSettingsNativeAPIAndPage(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		owner := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteGovernance)
		reader := getUserToken(t, "user2", auth_model.AccessTokenScopeReadGovernance)
		admin := getUserToken(t, "user1", auth_model.AccessTokenScopeWriteGovernance)
		endpoint := "/api/v1/governance/approval-settings/group/3"
		option := governance_api.ScopeApprovalSettingsOption{ApprovalSettingsOption: governance_api.ApprovalSettingsOption{PreventAuthor: true, PreventCommitter: true, ResetOnChange: true}}
		MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint, option).AddTokenAuth(reader), http.StatusForbidden)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", "/api/v1/governance/approval-settings/instance/0", option).AddTokenAuth(owner), http.StatusNotFound)
		response := MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint, option).AddTokenAuth(owner), http.StatusOK)
		settings := DecodeJSON(t, response, &governance_model.ApprovalSettings{})
		require.EqualValues(t, 1, settings.Revision)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint, option).AddTokenAuth(owner), http.StatusConflict)
		response = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(reader), http.StatusOK)
		state := DecodeJSON(t, response, &governance_service.ScopeApprovalSettings{})
		require.True(t, state.Effective.Sources["prevent_committer"].Locked)
		session := loginUser(t, "user2")
		page := "/org/org3/settings/approvals"
		response = session.MakeRequest(t, NewRequest(t, "GET", page), http.StatusOK)
		require.NotContains(t, response.Body.String(), `name="prevent_committer" value="true" checked disabled`)
		values := url.Values{"action": {"settings"}, "revision": {"1"}, "prevent_author": {"true"}, "reset_on_change": {"true"}, "locked": {"true"}}
		session.MakeRequest(t, NewRequestWithURLValues(t, "POST", page, values), http.StatusSeeOther)
		response = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(reader), http.StatusOK)
		state = DecodeJSON(t, response, &governance_service.ScopeApprovalSettings{})
		require.False(t, state.Local.PreventCommitter, "本级所有者可修改自己下发的限制")
		require.True(t, state.Local.Locked)
		require.EqualValues(t, 2, state.Local.Revision)
		MakeRequest(t, NewRequest(t, "DELETE", endpoint+"?revision=1").AddTokenAuth(owner), http.StatusConflict)
		MakeRequest(t, NewRequest(t, "DELETE", endpoint+"?revision=2").AddTokenAuth(owner), http.StatusNoContent)
		response = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(reader), http.StatusOK)
		state = DecodeJSON(t, response, &governance_service.ScopeApprovalSettings{})
		require.True(t, state.Local.Inherit)
		require.EqualValues(t, 3, state.Local.Revision)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint, option).AddTokenAuth(owner), http.StatusConflict)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", "/api/v1/governance/approval-settings/instance/0", option).AddTokenAuth(admin), http.StatusOK)
		response = session.MakeRequest(t, NewRequest(t, "GET", page), http.StatusOK)
		require.Contains(t, response.Body.String(), `name="prevent_committer" value="true" checked disabled`)
		values.Set("revision", "3")
		// 原生治理页面对越权统一隐藏为 404，拒绝后核对设置未改变。
		session.MakeRequest(t, NewRequestWithURLValues(t, "POST", page, values), http.StatusNotFound)
		response = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(reader), http.StatusOK)
		state = DecodeJSON(t, response, &governance_service.ScopeApprovalSettings{})
		require.True(t, state.Local.Inherit)
		require.EqualValues(t, 3, state.Local.Revision)
		require.True(t, state.Effective.Settings.PreventCommitter)
	})
}
