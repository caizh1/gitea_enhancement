// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	api "gitea.dev/modules/structs"

	"github.com/stretchr/testify/require"
)

func TestGovernanceNativeArchiveAudit(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/user/repos", api.CreateRepoOption{Name: "archive-audit", Private: true, AutoInit: true}).AddTokenAuth(token), http.StatusCreated)
		created := DecodeJSON(t, response, &api.Repository{})
		session := loginUser(t, "user2")
		request := NewRequestWithValues(t, "POST", "/user2/archive-audit/settings", map[string]string{"repo_name": "archive-audit", "action": "archive"})
		request.RemoteAddr = "192.0.2.5:45000"
		session.MakeRequest(t, request, http.StatusSeeOther)
		require.NotEmpty(t, session.GetCookieFlashMessage().SuccessMsg)
		repository, err := repo_model.GetRepositoryByID(t.Context(), created.ID)
		require.NoError(t, err)
		require.True(t, repository.IsArchived)
		var event governance_model.AuditEvent
		found, err := db.GetEngine(t.Context()).Where("type = ? AND object_id = ?", "repository.archived", created.ID).Get(&event)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "user2", event.Actor.Name)
		require.Equal(t, "web", event.Actor.Transport)
		require.Equal(t, "192.0.2.5", event.Actor.IP)
		require.Equal(t, "user2/archive-audit", event.ObjectPath)
		require.JSONEq(t, `{"before":false,"after":true}`, string(event.Details))
		archived := false
		request = NewRequestWithJSON(t, "PATCH", "/api/v1/repos/user2/archive-audit", api.EditRepoOption{Archived: &archived}).AddTokenAuth(token)
		request.RemoteAddr = "192.0.2.6:46000"
		MakeRequest(t, request, http.StatusOK)
		event = governance_model.AuditEvent{}
		found, err = db.GetEngine(t.Context()).Where("type = ? AND object_id = ?", "repository.unarchived", created.ID).Get(&event)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "user2", event.Actor.Name)
		require.Equal(t, "api", event.Actor.Transport)
		require.Equal(t, "192.0.2.6", event.Actor.IP)
		require.JSONEq(t, `{"before":true,"after":false}`, string(event.Details))
		repository, err = repo_model.GetRepositoryByID(t.Context(), created.ID)
		require.NoError(t, err)
		require.False(t, repository.IsArchived)
	})
}
