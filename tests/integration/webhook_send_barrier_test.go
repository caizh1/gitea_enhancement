// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	webhook_model "gitea.dev/models/webhook"
	webhook_module "gitea.dev/modules/webhook"
	webhook_service "gitea.dev/services/webhook"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestWebhookSendBarrierPostgres(t *testing.T) {
	if os.Getenv("GITEA_TEST_DATABASE") != "pgsql" {
		t.Skip("固定发送门禁只在 PostgreSQL 集成目标执行")
	}
	defer tests.PrepareTestEnv(t)()
	var engine struct{ Version string }
	found, err := db.GetEngine(t.Context()).SQL("SELECT version()").Get(&engine)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, strings.HasPrefix(engine.Version, "PostgreSQL "), "必须读取真实 PostgreSQL 引擎")
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 37})
	var sends atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sends.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	hook := &webhook_model.Webhook{
		RepoID: repo.ID, URL: receiver.URL, Type: webhook_module.GITEA,
		ContentType: webhook_model.ContentTypeJSON, Events: `{"push_only":true}`, IsActive: true,
	}
	require.NoError(t, webhook_model.CreateWebhook(t.Context(), hook))
	newTask := func() *webhook_model.HookTask {
		task := &webhook_model.HookTask{
			HookID: hook.ID, EventType: webhook_module.HookEventPush,
			PayloadVersion: 2, PayloadContent: `{"ref":"refs/heads/main"}`,
		}
		require.NoError(t, task.CaptureHook(hook, repo.ID, 0, nil))
		created, createErr := webhook_model.CreateHookTask(t.Context(), task)
		require.NoError(t, createErr)
		return created
	}

	sent := newTask()
	require.NoError(t, webhook_service.Deliver(t.Context(), sent))
	require.EqualValues(t, 1, sends.Load(), "活跃 Hook 须实际发送到同一 HTTP 接收端")
	sent, err = webhook_model.GetHookTaskByID(t.Context(), sent.ID)
	require.NoError(t, err)
	require.True(t, sent.IsSucceed)
	require.Equal(t, http.StatusOK, sent.ResponseInfo.Status)

	paused := newTask()
	hook.IsActive = false
	require.NoError(t, webhook_model.UpdateWebhook(t.Context(), hook))
	require.NoError(t, webhook_service.Deliver(t.Context(), paused))
	require.EqualValues(t, 1, sends.Load(), "停用前固定的待发任务不得发送 HTTP")
	paused, err = webhook_model.GetHookTaskByID(t.Context(), paused.ID)
	require.NoError(t, err)
	require.True(t, paused.IsDelivered)
	require.Contains(t, paused.ResponseContent, "webhook disabled")
	require.ErrorIs(t, webhook_service.ReplayHookTask(t.Context(), hook, paused.UUID), governance_model.ErrConflict)
	require.EqualValues(t, 1, sends.Load(), "停用后重投不得发送 HTTP")

	hook.IsActive = true
	require.NoError(t, webhook_model.UpdateWebhook(t.Context(), hook))
	deleted := newTask()
	require.NoError(t, webhook_model.DeleteWebhookByID(t.Context(), hook.ID))
	require.NoError(t, webhook_service.Deliver(t.Context(), deleted))
	require.EqualValues(t, 1, sends.Load(), "删除前固定的待发任务不得发送 HTTP")
	deleted, err = webhook_model.GetHookTaskByID(t.Context(), deleted.ID)
	require.NoError(t, err)
	require.True(t, deleted.IsDelivered)
	require.Empty(t, deleted.PayloadContent)
	require.Empty(t, deleted.HookSnapshotEncrypted)
	require.True(t, webhook_model.IsErrWebhookNotExist(webhook_service.ReplayHookTask(t.Context(), hook, deleted.UUID)))
	require.EqualValues(t, 1, sends.Load(), "删除后重投不得发送 HTTP")
}
