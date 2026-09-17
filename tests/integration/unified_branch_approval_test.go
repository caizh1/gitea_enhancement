// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"testing"

	auth_model "gitea.dev/models/auth"
	git_model "gitea.dev/models/git"
	gm "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/json"
	api "gitea.dev/modules/structs"
	gs "gitea.dev/services/governance"
	"gitea.dev/tests"
	"github.com/stretchr/testify/require"
)

func TestUnifiedBranchApprovalNativeAPIAndPages(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, gm.InitializeLegacyNamespaces(t.Context()))
	token := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
	endpoint := "/api/v1/repos/org3/repo3/branch_protections"
	response := MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, map[string]any{"rule_name": "unified-api", "required_approvals": 3, "enable_push": true}).AddTokenAuth(token), http.StatusCreated)
	var protection api.BranchProtection
	DecodeJSON(t, response, &protection)
	var config gs.BranchApprovalConfiguration
	require.NoError(t, json.Unmarshal(protection.ApprovalConfiguration, &config))
	require.NotEmpty(t, config.Version)
	var native *gm.ApprovalRule
	for _, rule := range config.Rules {
		if rule.NativeProtectionID == config.ProtectionID {
			native = rule
		}
	}
	require.NotNil(t, native)
	update := *native
	update.Required = 2
	response = MakeRequest(t, NewRequestWithJSON(t, "PATCH", endpoint+"/unified-api", map[string]any{"approval_configuration": gs.BranchApprovalUpdate{Version: config.Version, ProtectionID: config.ProtectionID, Rules: []gs.ApprovalRuleChange{{Rule: update, Revision: native.Revision}}}}).AddTokenAuth(token), http.StatusOK)
	DecodeJSON(t, response, &protection)
	require.EqualValues(t, 2, protection.RequiredApprovals)
	require.True(t, protection.EnablePush)
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", endpoint+"/unified-api", map[string]any{"enable_push": false, "approval_configuration": gs.BranchApprovalUpdate{Version: config.Version, ProtectionID: config.ProtectionID}}).AddTokenAuth(token), http.StatusConflict)
	saved := unittest.AssertExistsAndLoadBean(t, &git_model.ProtectedBranch{RepoID: 3, RuleName: "unified-api"})
	require.True(t, saved.CanPush)
	response = MakeRequest(t, NewRequest(t, "GET", endpoint+"/unified-api").AddTokenAuth(token), http.StatusOK)
	DecodeJSON(t, response, &protection)
	require.NoError(t, json.Unmarshal(protection.ApprovalConfiguration, &config))
	for _, rule := range config.Rules {
		if rule.NativeProtectionID == config.ProtectionID {
			native = rule
		}
	}
	update = *native
	update.Required = 4
	response = MakeRequest(t, NewRequestWithJSON(t, "PATCH", endpoint+"/unified-api", map[string]any{"required_approvals": 4, "approval_configuration": gs.BranchApprovalUpdate{Version: config.Version, ProtectionID: config.ProtectionID, Rules: []gs.ApprovalRuleChange{{Rule: update, Revision: native.Revision}}}}).AddTokenAuth(token), http.StatusOK)
	DecodeJSON(t, response, &protection)
	require.EqualValues(t, 4, protection.RequiredApprovals, "一致的新旧字段只更新同一条规则")
	require.NoError(t, json.Unmarshal(protection.ApprovalConfiguration, &config))
	for _, rule := range config.Rules {
		if rule.NativeProtectionID == config.ProtectionID {
			native = rule
		}
	}
	update = *native
	update.Required, update.NativeProtectionID = 5, 0
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", endpoint+"/unified-api", map[string]any{"required_approvals": 4, "approval_configuration": gs.BranchApprovalUpdate{Version: config.Version, ProtectionID: config.ProtectionID, Rules: []gs.ApprovalRuleChange{{Rule: update, Revision: native.Revision}}}}).AddTokenAuth(token), http.StatusUnprocessableEntity)
	response = MakeRequest(t, NewRequestWithJSON(t, "PATCH", endpoint+"/unified-api", map[string]any{"enable_status_check": false}).AddTokenAuth(token), http.StatusOK)
	DecodeJSON(t, response, &protection)
	require.EqualValues(t, 4, protection.RequiredApprovals, "旧客户端省略审批字段不能重置新规则")
	session := loginUser(t, "user1")
	page := session.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/org3/repo3/settings/branches/edit?rule_id=%d", saved.ID)), http.StatusOK)
	require.Contains(t, page.Body.String(), "data-approval-state")
	require.NotContains(t, page.Body.String(), "data-rule-teams", "审批规则不再提供原生团队选择器")
	require.Contains(t, page.Body.String(), "data-rule-groups")
	require.Contains(t, page.Body.String(), "保存分支保护")
	require.NotContains(t, page.Body.String(), "name=\"required_approvals\"")
	session.MakeRequest(t, NewRequest(t, "GET", "/org3/repo3/settings/pulls"), http.StatusOK)
	legacy := session.MakeRequest(t, NewRequest(t, "GET", "/governance/repositories/3/approval-rules"), http.StatusSeeOther)
	require.Equal(t, "/org3/repo3/settings/pulls", legacy.Header().Get("Location"))
	session.MakeRequest(t, NewRequest(t, "GET", "/-/admin/approvals"), http.StatusOK)
	session.MakeRequest(t, NewRequest(t, "GET", "/org/org3/settings/approvals"), http.StatusOK)
	childResponse := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups", gs.GroupOption{Name: "审批子群组", Path: "approval-child", ParentID: 3, Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
	var child gs.GroupState
	DecodeJSON(t, childResponse, &child)
	session.MakeRequest(t, NewRequest(t, "GET", "/org/org3/approval-child/settings/approvals"), http.StatusOK)
	oldPolicy := session.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/governance/approval-policies/group/%d", child.ID)), http.StatusSeeOther)
	require.Equal(t, "/org/org3/approval-child/settings/approvals", oldPolicy.Header().Get("Location"))
}
