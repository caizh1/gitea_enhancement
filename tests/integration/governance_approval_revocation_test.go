// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	auth_model "gitea.dev/models/auth"
	governance_model "gitea.dev/models/governance"
	api "gitea.dev/modules/structs"
	governance_api "gitea.dev/routers/api/v1/governance"
	"gitea.dev/services/forms"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestGovernanceApprovalCurrentQualificationAndRule(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		ownerToken := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		reviewerToken := getUserToken(t, "user4", auth_model.AccessTokenScopeAll)
		groupEndpoint := "/api/v1/governance/groups"
		response := MakeRequest(t, NewRequestWithJSON(t, "POST", groupEndpoint,
			governance_service.GroupOption{Path: "approval-live-root", Visibility: 2}).AddTokenAuth(ownerToken), http.StatusCreated)
		root := DecodeJSON(t, response, &governance_service.GroupState{})
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", groupEndpoint,
			governance_service.GroupOption{Path: "approval-live-child", ParentID: root.ID, Visibility: 2}).AddTokenAuth(ownerToken), http.StatusCreated)
		child := DecodeJSON(t, response, &governance_service.GroupState{})
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/orgs/approval-live-root/approval-live-child/repos",
			api.CreateRepoOption{Name: "approval-live", Private: true, AutoInit: true}).AddTokenAuth(ownerToken), http.StatusCreated)
		repo := DecodeJSON(t, response, &api.Repository{})
		repoEndpoint := "/api/v1/repos/approval-live-root/approval-live-child/approval-live"
		memberEndpoint := fmt.Sprintf("%s/%d/members/4", groupEndpoint, child.ID)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", memberEndpoint,
			governance_service.GroupMemberOption{Role: governance_model.Developer, Revision: child.Revision}).AddTokenAuth(ownerToken), http.StatusNoContent)

		ruleEndpoint := fmt.Sprintf("/api/v1/governance/repositories/%d/approval-rules", repo.ID)
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", ruleEndpoint,
			governance_api.ApprovalRuleOption{Rule: governance_model.ApprovalRule{
				Name: "指定当前成员", Required: 1, UserIDs: []int64{4}, BranchMode: "all", Enabled: true,
			}}).AddTokenAuth(ownerToken), http.StatusCreated)
		rule := DecodeJSON(t, response, &governance_model.ApprovalRule{})
		MakeRequest(t, NewRequestWithJSON(t, "PUT", fmt.Sprintf("/api/v1/governance/repositories/%d/approval-settings", repo.ID),
			governance_service.ApprovalSettingsOption{PreventAuthor: true, PreventOverrides: true, ResetOnChange: true}).AddTokenAuth(ownerToken), http.StatusOK)
		createFile := func(path, content, branch, newBranch string) {
			options := api.CreateFileOptions{
				FileOptions:   api.FileOptions{BranchName: branch, NewBranchName: newBranch, Message: "审批版本变化"},
				ContentBase64: base64.StdEncoding.EncodeToString([]byte(content)),
			}
			MakeRequest(t, NewRequestWithJSON(t, "POST", repoEndpoint+"/contents/"+path, options).AddTokenAuth(ownerToken), http.StatusCreated)
		}
		createFile("review.txt", "版本一\n", repo.DefaultBranch, "review-change")
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", repoEndpoint+"/pulls",
			api.CreatePullRequestOption{Title: "审批人当前资格", Head: "review-change", Base: repo.DefaultBranch}).AddTokenAuth(ownerToken), http.StatusCreated)
		pr := DecodeJSON(t, response, &api.PullRequest{})
		reviewEndpoint := fmt.Sprintf("%s/pulls/%d/reviews", repoEndpoint, pr.Index)
		mergeEndpoint := fmt.Sprintf("%s/pulls/%d/merge", repoEndpoint, pr.Index)
		stateEndpoint := fmt.Sprintf("/api/v1/governance/pulls/%d/approval-state", pr.ID)
		approvalState := func() *governance_service.PullApprovalResult {
			response := MakeRequest(t, NewRequest(t, "GET", stateEndpoint).AddTokenAuth(ownerToken), http.StatusOK)
			return DecodeJSON(t, response, &governance_service.PullApprovalResult{})
		}
		approve := func(sha string) {
			MakeRequest(t, NewRequestWithJSON(t, "POST", reviewEndpoint,
				api.CreatePullReviewOptions{Event: api.ReviewStateApproved, CommitID: sha}).AddTokenAuth(reviewerToken), http.StatusOK)
		}
		denyMerge := func() {
			MakeRequest(t, NewRequestWithJSON(t, "POST", mergeEndpoint,
				forms.MergePullRequestForm{Do: "merge", HeadCommitID: pr.Head.Sha}).AddTokenAuth(ownerToken), http.StatusForbidden)
			response := MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("%s/pulls/%d", repoEndpoint, pr.Index)).AddTokenAuth(ownerToken), http.StatusOK)
			current := DecodeJSON(t, response, &api.PullRequest{})
			require.False(t, current.HasMerged)
		}

		approve(pr.Head.Sha)
		require.True(t, approvalState().State.Satisfied, "指定人批准应先被计入")
		response = MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("%s/%d", groupEndpoint, child.ID)).AddTokenAuth(ownerToken), http.StatusOK)
		child = DecodeJSON(t, response, &governance_service.GroupState{})
		MakeRequest(t, NewRequest(t, "DELETE", memberEndpoint+"?revision="+strconv.FormatInt(child.Revision, 10)).AddTokenAuth(ownerToken), http.StatusNoContent)
		require.False(t, approvalState().State.Satisfied, "批准后撤销审查资格，旧票不得满足当前规则")
		denyMerge()

		response = MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("%s/%d", groupEndpoint, child.ID)).AddTokenAuth(ownerToken), http.StatusOK)
		child = DecodeJSON(t, response, &governance_service.GroupState{})
		MakeRequest(t, NewRequestWithJSON(t, "PUT", memberEndpoint,
			governance_service.GroupMemberOption{Role: governance_model.Developer, Revision: child.Revision}).AddTokenAuth(ownerToken), http.StatusNoContent)
		createFile("second.txt", "版本二\n", "review-change", "")
		response = MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("%s/pulls/%d", repoEndpoint, pr.Index)).AddTokenAuth(ownerToken), http.StatusOK)
		pr = DecodeJSON(t, response, &api.PullRequest{})
		require.False(t, approvalState().State.Satisfied, "新提交必须使旧代次批准失效")
		denyMerge()
		approve(pr.Head.Sha)
		require.True(t, approvalState().State.Satisfied, "新代次重新批准后应恢复就绪")

		rule.Required = 2
		MakeRequest(t, NewRequestWithJSON(t, "PUT", fmt.Sprintf("%s/%d", ruleEndpoint, rule.ID),
			governance_api.ApprovalRuleOption{Revision: rule.Revision, Rule: *rule}).AddTokenAuth(ownerToken), http.StatusOK)
		require.False(t, approvalState().State.Satisfied, "规则修订为两票后不得沿用一票就绪状态")
		denyMerge()
	})
}
