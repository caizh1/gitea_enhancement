// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	"gitea.dev/models/webhook"
	"gitea.dev/modules/json"
	api "gitea.dev/modules/structs"

	"github.com/stretchr/testify/require"
)

func TestOrganizationWebhookTestDelivery(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		type receipt struct {
			Payload api.PushPayload
			Event   string
			Attempt string
			Error   error
		}
		receipts := make(chan receipt, 2)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := receipt{Event: r.Header.Get("X-Gitea-Event-ID"), Attempt: r.Header.Get("X-Gitea-Delivery")}
			got.Error = json.NewDecoder(r.Body).Decode(&got.Payload)
			receipts <- got
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		session := loginUser(t, "user2")
		testAPICreateWebhookForOrg(t, session, "org3", server.URL, "push")
		hook := unittest.AssertExistsAndLoadBean(t, &webhook.Webhook{OwnerID: 3, URL: server.URL})
		base := fmt.Sprintf("/org/org3/settings/hooks/%d", hook.ID)
		page := session.MakeRequest(t, NewRequest(t, http.MethodGet, base), http.StatusOK)
		doc := NewHTMLParser(t, page.Body)
		require.Equal(t, base+"/test", doc.doc.Find("#test-delivery").AttrOr("data-link", ""))
		session.MakeRequest(t, NewRequest(t, http.MethodPost, base+"/test"), http.StatusOK)
		var first receipt
		select {
		case first = <-receipts:
		case <-time.After(5 * time.Second):
			t.Fatal("群组测试按钮没有产生 HTTP 投递")
		}
		require.NoError(t, first.Error)
		require.NotEmpty(t, first.Event)
		require.NotEmpty(t, first.Attempt)
		require.NotNil(t, first.Payload.Repo)
		require.Zero(t, first.Payload.Repo.ID, "合成测试不读取下级仓库")
		require.Equal(t, "webhook-test", first.Payload.Repo.Name)
		require.EqualValues(t, 3, first.Payload.Repo.Owner.ID)
		session.MakeRequest(t, NewRequest(t, http.MethodPost, base+"/replay/"+first.Attempt), http.StatusSeeOther)
		select {
		case replay := <-receipts:
			require.NoError(t, replay.Error)
			require.Equal(t, first.Event, replay.Event)
			require.NotEqual(t, first.Attempt, replay.Attempt)
		case <-time.After(5 * time.Second):
			t.Fatal("群组重投没有产生 HTTP 投递")
		}
		hook.IsActive = false
		require.NoError(t, webhook.UpdateWebhook(t.Context(), hook))
		session.MakeRequest(t, NewRequest(t, http.MethodPost, base+"/test"), http.StatusConflict)
		session.MakeRequest(t, NewRequest(t, http.MethodPost, base+"/replay/"+first.Attempt), http.StatusConflict)
	})
}
