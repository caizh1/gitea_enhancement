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
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	api "gitea.dev/modules/structs"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceSudoCannotApproveAsAnotherUser(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		token := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
		response := MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/user2/repo1/pulls/3").AddTokenAuth(token), http.StatusOK)
		pull := DecodeJSON(t, response, &api.PullRequest{})
		require.Equal(t, "user1", pull.Poster.UserName)
		before, err := db.GetEngine(t.Context()).Count(new(issues_model.Review))
		require.NoError(t, err)
		evidenceBefore, err := db.GetEngine(t.Context()).Count(new(governance_model.ApprovalEvidence))
		require.NoError(t, err)
		MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo1/pulls/3/reviews?sudo=user2", api.CreatePullReviewOptions{Event: api.ReviewStateApproved, CommitID: pull.Head.Sha}).AddTokenAuth(token), http.StatusForbidden)
		after, err := db.GetEngine(t.Context()).Count(new(issues_model.Review))
		require.NoError(t, err)
		require.Equal(t, before, after, "拒绝代办不能产生原生评审")
		evidenceAfter, err := db.GetEngine(t.Context()).Count(new(governance_model.ApprovalEvidence))
		require.NoError(t, err)
		require.Equal(t, evidenceBefore, evidenceAfter, "拒绝代办不能产生批准证据")
		var events []governance_model.AuditEvent
		require.NoError(t, db.GetEngine(t.Context()).Where("type = ?", "approval.denied").Find(&events))
		require.Len(t, events, 1)
		require.Equal(t, int64(1), events[0].Actor.ID)
		require.Equal(t, "user1", events[0].Actor.Name)
		require.Equal(t, int64(2), events[0].Actor.ActingAsID)
		require.Equal(t, "user2", events[0].Actor.ActingAsName)
		require.Equal(t, "denied", events[0].Result)
	})
}

func TestGovernanceSudoTokenAuditIdentity(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/users/user2/tokens?sudo=user2", map[string]any{"name": "代办身份审计", "scopes": []string{"read:user"}}).AddBasicAuth("user1"), http.StatusCreated)
	token := DecodeJSON(t, response, &api.AccessToken{})
	MakeRequest(t, NewRequest(t, "DELETE", "/api/v1/users/user2/tokens/"+strconv.FormatInt(token.ID, 10)+"?sudo=user2").AddBasicAuth("user1"), http.StatusNoContent)
	var events []governance_model.AuditEvent
	require.NoError(t, db.GetEngine(t.Context()).Where("object_type = ? AND object_id = ?", "access_token", token.ID).Asc("id").Find(&events))
	require.Len(t, events, 2)
	require.Equal(t, "credential.access_token_created", events[0].Type)
	require.Equal(t, "credential.access_token_revoked", events[1].Type)
	for _, event := range events {
		require.Equal(t, int64(1), event.Actor.ID)
		require.Equal(t, "user1", event.Actor.Name)
		require.Equal(t, int64(2), event.Actor.ActingAsID)
		require.Equal(t, "user2", event.Actor.ActingAsName)
		require.Equal(t, "user", event.ScopeType)
		require.Equal(t, int64(2), event.ScopeID)
	}
}
