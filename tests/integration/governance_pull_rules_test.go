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

func TestGovernancePullRuleNativeHTTPAndForm(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		owner := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteGovernance)
		reader := getUserToken(t, "user2", auth_model.AccessTokenScopeReadGovernance)
		endpoint := "/api/v1/governance/pulls/2/approval-rules"
		response := MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(reader), http.StatusOK)
		state := DecodeJSON(t, response, &governance_service.PullApprovalRules{})
		option := governance_api.PullApprovalRuleOption{Revision: state.Version.Revision, Rule: governance_model.ApprovalRule{Name: "本 PR 专用", Required: 1, UserIDs: []int64{2}, BranchMode: "all", Enabled: true}}
		MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint, option).AddTokenAuth(reader), http.StatusForbidden)
		response = MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint, option).AddTokenAuth(owner), http.StatusOK)
		version := DecodeJSON(t, response, &governance_model.PullRuleVersion{})
		require.Len(t, version.Rules, 1)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", endpoint, option).AddTokenAuth(owner), http.StatusConflict)
		session := loginUser(t, "user2")
		legacy := session.MakeRequest(t, NewRequest(t, "GET", "/governance/pulls/2/approval-rules"), http.StatusSeeOther)
		require.Equal(t, "/user2/repo1/pulls/3/approvals", legacy.Header().Get("Location"))
		page := "/user2/repo1/pulls/3/approvals"
		response = session.MakeRequest(t, NewRequest(t, "GET", page+"?edit="+strconv.FormatInt(version.Rules[0].ID, 10)), http.StatusOK)
		require.Contains(t, response.Body.String(), "PR 独立审批规则")
		require.NotContains(t, response.Body.String(), "保存审批设置")
		values := url.Values{"name": {"当前 PR 已编辑"}, "rule_id": {strconv.FormatInt(version.Rules[0].ID, 10)}, "revision": {strconv.FormatInt(version.Revision, 10)}, "required": {"2"}, "users": {"2"}, "branch_mode": {"all"}, "enabled": {"true"}}
		session.MakeRequest(t, NewRequestWithURLValues(t, "POST", page, values), http.StatusSeeOther)
		response = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(reader), http.StatusOK)
		state = DecodeJSON(t, response, &governance_service.PullApprovalRules{})
		require.Equal(t, 2, state.Version.Rules[0].Required)
		response = MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/repositories/1/approval-rules").AddTokenAuth(reader), http.StatusOK)
		project := DecodeJSON(t, response, &governance_service.RepositoryApprovalRules{})
		require.Empty(t, project.Rules)
		response = session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/pulls/3"), http.StatusOK)
		require.Contains(t, response.Body.String(), "/user2/repo1/pulls/3/approvals")
		MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/repositories/1/approval-rules", governance_api.ApprovalRuleOption{Rule: governance_model.ApprovalRule{Name: "锁定项目规则", Required: 1, UserIDs: []int64{2}, BranchMode: "all", Enabled: true}}).AddTokenAuth(owner), http.StatusCreated)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", "/api/v1/governance/repositories/1/approval-settings", governance_service.ApprovalSettingsOption{PreventOverrides: true, PreventAuthor: true, ResetOnChange: true}).AddTokenAuth(owner), http.StatusOK)
		response = session.MakeRequest(t, NewRequest(t, "GET", page), http.StatusOK)
		require.Contains(t, response.Body.String(), "锁定项目规则")
		require.Contains(t, response.Body.String(), "项目规则")
		require.NotContains(t, response.Body.String(), "PR 独立规则", "禁止覆盖后准确显示项目来源")
	})
}
