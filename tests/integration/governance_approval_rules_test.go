// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	auth_model "gitea.dev/models/auth"
	governance_model "gitea.dev/models/governance"
	governance_api "gitea.dev/routers/api/v1/governance"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestGovernanceApprovalRulesHTTP(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		owner := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteGovernance)
		reader := getUserToken(t, "user2", auth_model.AccessTokenScopeReadGovernance)
		other := getUserToken(t, "user4", auth_model.AccessTokenScopeWriteGovernance)
		endpoint := "/api/v1/governance/repositories/1/approval-rules"
		option := governance_api.ApprovalRuleOption{Rule: governance_model.ApprovalRule{Name: "人员必批", Required: 1, UserIDs: []int64{2}, BranchMode: "all", Enabled: true}}
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, option).AddTokenAuth(reader), http.StatusForbidden)
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, option).AddTokenAuth(other), http.StatusNotFound)
		resp := MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, option).AddTokenAuth(owner), http.StatusCreated)
		rule := DecodeJSON(t, resp, &governance_model.ApprovalRule{})
		require.EqualValues(t, 1, rule.Revision)
		resp = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(reader), http.StatusOK)
		state := DecodeJSON(t, resp, &governance_service.RepositoryApprovalRules{})
		require.Len(t, state.Rules, 1)
		require.True(t, state.CanManage)
		require.False(t, state.EnforcementReady)
		target := fmt.Sprintf("%s/%d", endpoint, rule.ID)
		option.Revision = rule.Revision
		option.Rule.Required = 2
		MakeRequest(t, NewRequestWithJSON(t, "PUT", target, option).AddTokenAuth(owner), http.StatusOK)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", target, option).AddTokenAuth(owner), http.StatusConflict)
		MakeRequest(t, NewRequest(t, "DELETE", target+"?revision=1").AddTokenAuth(owner), http.StatusConflict)
		MakeRequest(t, NewRequest(t, "DELETE", target+"?revision=2").AddTokenAuth(owner), http.StatusNoContent)
	})
}

func TestGovernanceApprovalRuleNativeForm(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteGovernance)
		endpoint := "/api/v1/governance/repositories/1/approval-rules"
		option := governance_api.ApprovalRuleOption{Rule: governance_model.ApprovalRule{Name: "冲突批次 · user2", Required: 1, UserIDs: []int64{2}, BranchMode: "all", Enabled: true}}
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, option).AddTokenAuth(token), http.StatusCreated)
		session := loginUser(t, "user2")
		page := "/governance/repositories/1/approval-rules"
		resp := session.MakeRequest(t, NewRequest(t, "GET", page), http.StatusSeeOther)
		require.Equal(t, "/user2/repo1/settings/pulls", resp.Header().Get("Location"))
		resp = session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/settings/pulls"), http.StatusOK)
		require.Contains(t, resp.Body.String(), "data-approval-state")
		require.Contains(t, resp.Body.String(), "user2/repo1")
		values := url.Values{"name": {"冲突批次"}, "required": {"1"}, "users": {"1", "2"}, "branch_mode": {"all"}, "enabled": {"true"}, "every_person": {"true"}}
		session.MakeRequest(t, NewRequestWithURLValues(t, "POST", page, values), http.StatusConflict)
		resp = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(token), http.StatusOK)
		state := DecodeJSON(t, resp, &governance_service.RepositoryApprovalRules{})
		require.Len(t, state.Rules, 1, "批次最后一条失败时，前面的单人规则也必须回滚")
		values.Set("name", "指定人员")
		session.MakeRequest(t, NewRequestWithURLValues(t, "POST", page, values), http.StatusSeeOther)
		resp = MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(token), http.StatusOK)
		state = DecodeJSON(t, resp, &governance_service.RepositoryApprovalRules{})
		require.Len(t, state.Rules, 3)
		for _, rule := range state.Rules {
			require.Len(t, rule.UserIDs, 1)
			require.Equal(t, 1, rule.Required)
		}
	})
}
