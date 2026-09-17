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
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/unittest"
	api "gitea.dev/modules/structs"
	governance_api "gitea.dev/routers/api/v1/governance"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGovernanceReviewReservationHTTP(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		ctx := t.Context()
		token := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
		pr, err := issues_model.GetPullRequestByIssueIDWithNoAttributes(ctx, 2)
		require.NoError(t, err)
		// 此样本专门验证开放 PR 的撤回冲突；已关闭 PR 会先被原生入口拒绝。
		_, err = db.GetEngine(ctx).ID(pr.IssueID).Cols("is_closed").Update(&issues_model.Issue{IsClosed: false})
		require.NoError(t, err)
		_, err = db.GetEngine(ctx).ID(pr.ID).Cols("has_merged").Update(&issues_model.PullRequest{HasMerged: false})
		require.NoError(t, err)
		operation := &governance_model.MergeAuthorization{PullID: pr.ID, RepoID: pr.BaseRepoID, Branch: pr.BaseBranch, Head: "head", OldTarget: "before", NewTarget: "after", Actor: governance_model.Actor{ID: 1, Name: "user1", Kind: "user", Transport: "api"}}
		require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, nil, func(context.Context) error { return nil }))
		endpoint := "/api/v1/repos/user2/repo1/pulls/2/reviews/1"
		resp := MakeRequest(t, NewRequest(t, "DELETE", endpoint).AddTokenAuth(token), http.StatusConflict)
		assert.Contains(t, resp.Body.String(), "相关合并尚未完成")
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint+"/dismissals", api.DismissPullReviewOptions{Message: "撤回批准"}).AddTokenAuth(token), http.StatusConflict)
		review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 1})
		assert.False(t, review.Dismissed)
		_, err = governance_model.ReconcileMerge(ctx, operation.ID, "before")
		require.NoError(t, err)
		request := NewRequestWithJSON(t, "POST", endpoint+"/dismissals", api.DismissPullReviewOptions{Message: "撤回批准"}).AddTokenAuth(token)
		request.RemoteAddr = "192.0.2.5:45000"
		MakeRequest(t, request, http.StatusOK)
		var events []governance_model.AuditEvent
		require.NoError(t, db.GetEngine(ctx).Where("object_type = ? AND object_id = ? AND type = ?", "review", review.ID, "approval.withdrawn").Find(&events))
		require.Len(t, events, 1, "被拒绝的撤回不能记为成功")
		require.Equal(t, "user1", events[0].Actor.Name)
		require.Equal(t, "api", events[0].Actor.Transport)
		require.Equal(t, "192.0.2.5", events[0].Actor.IP)
		require.NotContains(t, string(events[0].Details), "撤回批准")
	})
}

func TestGovernanceAuthorApprovalSetting(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		token := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
		response := MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/user2/repo1/pulls/3").AddTokenAuth(token), http.StatusOK)
		pull := DecodeJSON(t, response, &api.PullRequest{})
		require.Equal(t, "user1", pull.Poster.UserName)
		endpoint := "/api/v1/repos/user2/repo1/pulls/3/reviews"
		option := api.CreatePullReviewOptions{Event: api.ReviewStateApproved, CommitID: pull.Head.Sha}
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, option).AddTokenAuth(token), http.StatusUnprocessableEntity)
		session := loginUser(t, "user1")
		buttonDisabled := func() bool {
			t.Helper()
			page := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/pulls/3/files"), http.StatusOK)
			button := NewHTMLParser(t, page.Body).Find(`button[name="type"][value="approve"]`)
			require.Equal(t, 1, button.Length())
			_, disabled := button.Attr("disabled")
			return disabled
		}
		require.True(t, buttonDisabled())
		settingPath := "/api/v1/governance/repositories/1/approval-settings"
		settings := governance_api.ApprovalSettingsOption{PreventAuthor: false, ResetOnChange: true}
		response = MakeRequest(t, NewRequestWithJSON(t, "PUT", settingPath, settings).AddTokenAuth(token), http.StatusOK)
		saved := DecodeJSON(t, response, &governance_model.ApprovalSettings{})
		require.False(t, buttonDisabled(), "允许作者批准后原生按钮必须可用")
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, option).AddTokenAuth(token), http.StatusOK)
		settings.Revision, settings.PreventAuthor = saved.Revision, true
		MakeRequest(t, NewRequestWithJSON(t, "PUT", settingPath, settings).AddTokenAuth(token), http.StatusOK)
		require.True(t, buttonDisabled())
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, option).AddTokenAuth(token), http.StatusUnprocessableEntity)
	})
}
