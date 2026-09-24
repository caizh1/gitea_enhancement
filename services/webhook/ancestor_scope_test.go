// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	webhook_model "gitea.dev/models/webhook"
	"gitea.dev/modules/json"
	api "gitea.dev/modules/structs"
	webhook_module "gitea.dev/modules/webhook"

	"github.com/stretchr/testify/require"
)

func seedWebhookAncestorScope(t *testing.T) *repo_model.Repository {
	t.Helper()
	require.NoError(t, unittest.PrepareTestDatabase())
	for _, ns := range []*governance_model.Namespace{
		{ID: 3, Slug: "org3", LowerSlug: "org3", FullPath: "org3", LowerPath: "org3", Kind: "group"},
		{ID: 6, ParentID: 3, Slug: "org6", LowerSlug: "org6", FullPath: "org3/org6", LowerPath: "org3/org6", Kind: "group"},
		{ID: 7, ParentID: 6, Slug: "org7", LowerSlug: "org7", FullPath: "org3/org6/org7", LowerPath: "org3/org6/org7", Kind: "group"},
		{ID: 17, ParentID: 3, Slug: "org17", LowerSlug: "org17", FullPath: "org3/org17", LowerPath: "org3/org17", Kind: "group"},
		{ID: 19, Slug: "org19", LowerSlug: "org19", FullPath: "org19", LowerPath: "org19", Kind: "group"},
	} {
		require.NoError(t, db.Insert(t.Context(), ns))
	}
	require.NoError(t, db.Insert(t.Context(), &governance_model.Share{ScopeType: "group", ScopeID: 7, GroupID: 19, MaxRole: governance_model.Reporter}))
	_, err := db.GetEngine(t.Context()).ID(3).Cols("owner_id", "actions_scope_revision").Update(&repo_model.Repository{OwnerID: 7, ActionsScopeRevision: 1})
	require.NoError(t, err)
	repo, err := repo_model.GetRepositoryByID(t.Context(), 3)
	require.NoError(t, err)
	return repo
}

func createAncestorTestHook(t *testing.T, repoID, ownerID int64, url string) *webhook_model.Webhook {
	t.Helper()
	w := &webhook_model.Webhook{RepoID: repoID, OwnerID: ownerID, URL: url, Type: webhook_module.GITEA, ContentType: webhook_model.ContentTypeJSON, Events: `{"push_only":true}`, IsActive: true}
	require.NoError(t, webhook_model.CreateWebhook(t.Context(), w))
	w, err := webhook_model.GetWebhookByID(t.Context(), w.ID)
	require.NoError(t, err)
	return w
}

func TestAncestorWebhookFanoutAndStaleSource(t *testing.T) {
	repo := seedWebhookAncestorScope(t)
	root := createAncestorTestHook(t, 0, 3, "http://localhost/hook")
	child := createAncestorTestHook(t, 0, 6, "http://localhost/hook")
	grandchild := createAncestorTestHook(t, 0, 7, "http://localhost/grandchild")
	repository := createAncestorTestHook(t, repo.ID, 0, "http://localhost/repository")
	sibling := createAncestorTestHook(t, 0, 17, "http://localhost/sibling")
	shared := createAncestorTestHook(t, 0, 19, "http://localhost/shared")
	payload := &api.PushPayload{Ref: "refs/heads/main", Commits: []*api.PayloadCommit{{}}}
	require.NoError(t, PrepareWebhooks(t.Context(), EventSource{Repository: repo}, webhook_module.HookEventPush, payload))
	for _, hook := range []*webhook_model.Webhook{root, child, grandchild, repository} {
		tasks, err := webhook_model.HookTasks(t.Context(), hook.ID, 1)
		require.NoError(t, err)
		require.Len(t, tasks, 1)
		require.Equal(t, repo.ID, tasks[0].RepoID)
		require.Equal(t, repo.ActionsScopeRevision, tasks[0].ScopeRevision)
		var ownerIDs []int64
		require.NoError(t, json.Unmarshal([]byte(tasks[0].ScopeOwnerIDs), &ownerIDs))
		require.Equal(t, []int64{7, 6, 3}, ownerIDs)
		snapshot, err := tasks[0].HookSnapshot()
		require.NoError(t, err)
		require.Equal(t, hook.ID, snapshot.ID)
	}
	for _, hook := range []*webhook_model.Webhook{sibling, shared} {
		tasks, err := webhook_model.HookTasks(t.Context(), hook.ID, 1)
		require.NoError(t, err)
		require.Empty(t, tasks)
	}
	rootTasks, err := webhook_model.HookTasks(t.Context(), root.ID, 1)
	require.NoError(t, err)
	require.NoError(t, ReplayHookTask(t.Context(), root, rootTasks[0].UUID))
	rootTasks, err = webhook_model.HookTasks(t.Context(), root.ID, 1)
	require.NoError(t, err)
	require.Len(t, rootTasks, 2)
	require.NotEqual(t, rootTasks[0].UUID, rootTasks[1].UUID, "每次尝试保留独立历史")
	require.NotEmpty(t, rootTasks[0].EventUUID)
	require.Equal(t, rootTasks[0].EventUUID, rootTasks[1].EventUUID, "重投保留同一事件标识")
	legacy, err := webhook_model.CreateHookTask(t.Context(), &webhook_model.HookTask{
		HookID: root.ID, PayloadVersion: 2, EventType: webhook_module.HookEventPush,
		PayloadContent: `{"repository":{"id":3}}`,
	})
	require.NoError(t, err)
	require.NoError(t, ReplayHookTask(t.Context(), root, legacy.UUID))
	legacyUnknown, err := webhook_model.CreateHookTask(t.Context(), &webhook_model.HookTask{HookID: root.ID, PayloadVersion: 1, EventType: webhook_module.HookEventPush, PayloadContent: `{}`})
	require.NoError(t, err)
	require.ErrorIs(t, ReplayHookTask(t.Context(), root, legacyUnknown.UUID), governance_model.ErrConflict)
	rootTasks, err = webhook_model.HookTasks(t.Context(), root.ID, 1)
	require.NoError(t, err)
	require.Len(t, rootTasks, 5)
	root.Events = `{"send_everything":true}`
	require.NoError(t, webhook_model.UpdateWebhook(t.Context(), root))
	require.NoError(t, PrepareWebhooks(t.Context(), EventSource{Repository: repo}, webhook_module.HookEventRelease, &api.ReleasePayload{}))
	rootTasks, err = webhook_model.HookTasks(t.Context(), root.ID, 1)
	require.NoError(t, err)
	require.Len(t, rootTasks, 5, "未纳入一期的事件不向祖先扩散")
	_, err = db.GetEngine(t.Context()).ID(repo.ID).Incr("actions_scope_revision").Update(new(repo_model.Repository))
	require.NoError(t, err)
	require.ErrorIs(t, PrepareWebhooks(t.Context(), EventSource{Repository: repo}, webhook_module.HookEventPush, payload), governance_model.ErrConflict)
	tasks, err := webhook_model.HookTasks(t.Context(), root.ID, 1)
	require.NoError(t, err)
	require.Len(t, tasks, 5)
	_, err = db.GetEngine(t.Context()).ID(repo.ID).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 19})
	require.NoError(t, err)
	require.ErrorIs(t, ReplayHookTask(t.Context(), root, tasks[0].UUID), governance_model.ErrConflict)
}

func TestWebhookSnapshotPauseAndDelete(t *testing.T) {
	repo := seedWebhookAncestorScope(t)
	var oldCalls, newCalls atomic.Int64
	oldServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { oldCalls.Add(1); w.WriteHeader(http.StatusOK) }))
	defer oldServer.Close()
	newServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { newCalls.Add(1); w.WriteHeader(http.StatusOK) }))
	defer newServer.Close()
	hook := createAncestorTestHook(t, repo.ID, 0, oldServer.URL)
	payload := &api.PushPayload{Ref: "refs/heads/main", Commits: []*api.PayloadCommit{{}}}
	data, err := payload.JSONPayload()
	require.NoError(t, err)
	scope, err := hookScopeForRepository(t.Context(), repo)
	require.NoError(t, err)
	var taskID int64
	require.NoError(t, governance_model.WithWrite(t.Context(), nil, func(ctx context.Context) error {
		var err error
		taskID, err = createScopedHookTask(ctx, hook, scope, webhook_module.HookEventPush, payload, string(data), false)
		return err
	}))
	hook.URL = newServer.URL
	require.NoError(t, webhook_model.UpdateWebhook(t.Context(), hook))
	task, err := webhook_model.GetHookTaskByID(t.Context(), taskID)
	require.NoError(t, err)
	require.NoError(t, Deliver(t.Context(), task))
	require.EqualValues(t, 1, oldCalls.Load())
	require.Zero(t, newCalls.Load())
	var tamperedID int64
	require.NoError(t, governance_model.WithWrite(t.Context(), nil, func(ctx context.Context) error {
		var err error
		tamperedID, err = createScopedHookTask(ctx, hook, scope, webhook_module.HookEventPush, payload, string(data), false)
		return err
	}))
	_, err = db.GetEngine(t.Context()).ID(tamperedID).Cols("payload_content").Update(&webhook_model.HookTask{PayloadContent: `{"ref":"tampered"}`})
	require.NoError(t, err)
	tampered, err := webhook_model.GetHookTaskByID(t.Context(), tamperedID)
	require.NoError(t, err)
	require.Error(t, Deliver(t.Context(), tampered))
	tampered, err = webhook_model.GetHookTaskByID(t.Context(), tamperedID)
	require.NoError(t, err)
	require.True(t, tampered.IsDelivered)
	require.Contains(t, tampered.ResponseContent, "invalid webhook snapshot")
	require.EqualValues(t, 1, oldCalls.Load())
	require.Zero(t, newCalls.Load())
	var pendingID int64
	require.NoError(t, governance_model.WithWrite(t.Context(), nil, func(ctx context.Context) error {
		var err error
		pendingID, err = createScopedHookTask(ctx, hook, scope, webhook_module.HookEventPush, payload, string(data), false)
		return err
	}))
	var authorizedID int64
	require.NoError(t, governance_model.WithWrite(t.Context(), nil, func(ctx context.Context) error {
		var err error
		authorizedID, err = createScopedHookTask(ctx, hook, scope, webhook_module.HookEventPush, payload, string(data), false)
		return err
	}))
	authorizedTask, err := webhook_model.GetHookTaskByID(t.Context(), authorizedID)
	require.NoError(t, err)
	_, authorized, err := authorizeHookDelivery(t.Context(), authorizedTask)
	require.NoError(t, err)
	require.True(t, authorized)
	hook.IsActive = false
	require.NoError(t, webhook_model.UpdateWebhook(t.Context(), hook))
	authorizedTask, err = webhook_model.GetHookTaskByID(t.Context(), authorizedID)
	require.NoError(t, err)
	require.True(t, authorizedTask.IsDelivered)
	require.Empty(t, authorizedTask.ResponseContent, "停用不得追溯撤销已提交的发送授权")
	pending, err := webhook_model.GetHookTaskByID(t.Context(), pendingID)
	require.NoError(t, err)
	require.True(t, pending.IsDelivered)
	require.Contains(t, pending.ResponseContent, "webhook disabled")
	require.Contains(t, pending.ResponseInfo.Body, "webhook disabled")
	_, authorized, err = authorizeHookDelivery(t.Context(), pending)
	require.NoError(t, err)
	require.False(t, authorized)
	hook.IsActive = true
	require.NoError(t, webhook_model.UpdateWebhook(t.Context(), hook))
	require.NoError(t, webhook_model.DeleteWebhookByID(t.Context(), hook.ID))
	pending, err = webhook_model.GetHookTaskByID(t.Context(), pendingID)
	require.NoError(t, err)
	require.Empty(t, pending.PayloadContent)
	require.Empty(t, pending.HookSnapshotEncrypted)
	require.Contains(t, pending.ResponseContent, "redacted")
}

func TestAncestorWebhookDeletedEventRepository(t *testing.T) {
	repo := seedWebhookAncestorScope(t)
	hook := createAncestorTestHook(t, 0, 3, "http://localhost/ancestor")
	scope, err := hookScopeForRepository(t.Context(), repo)
	require.NoError(t, err)
	payload := &api.PushPayload{Ref: "refs/heads/main", Commits: []*api.PayloadCommit{{}}}
	data, err := payload.JSONPayload()
	require.NoError(t, err)
	var taskID int64
	require.NoError(t, governance_model.WithWrite(t.Context(), nil, func(ctx context.Context) error {
		var err error
		taskID, err = createScopedHookTask(ctx, hook, scope, webhook_module.HookEventPush, payload, string(data), false)
		return err
	}))
	_, err = db.DeleteByID[repo_model.Repository](t.Context(), repo.ID)
	require.NoError(t, err)
	task, err := webhook_model.GetHookTaskByID(t.Context(), taskID)
	require.NoError(t, err)
	_, authorized, err := authorizeHookDelivery(t.Context(), task)
	require.NoError(t, err)
	require.False(t, authorized)
	task, err = webhook_model.GetHookTaskByID(t.Context(), taskID)
	require.NoError(t, err)
	require.True(t, task.IsDelivered)
	require.Contains(t, task.ResponseContent, "event repository deleted")
}

func TestWebhookManagementRechecksRevokedActor(t *testing.T) {
	seedWebhookAncestorScope(t)
	member := &governance_model.Membership{ScopeType: "group", ScopeID: 3, UserID: 4, Role: governance_model.Owner}
	require.NoError(t, db.Insert(t.Context(), member))
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 4, Kind: "user", Transport: "web"})
	hook := &webhook_model.Webhook{OwnerID: 7, URL: "http://localhost/test", Type: webhook_module.GITEA, ContentType: webhook_model.ContentTypeJSON, Events: `{"push_only":true}`, IsActive: true}
	require.NoError(t, webhook_model.CreateWebhook(ctx, hook), "祖先 Owner 可管理下级来源")
	require.NoError(t, PrepareTestWebhook(ctx, hook, webhook_module.HookEventPush, &api.PushPayload{Ref: "refs/heads/main"}))
	tasks, err := webhook_model.HookTasks(t.Context(), hook.ID, 1)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.NoError(t, governance_model.WithWrite(t.Context(), nil, func(ctx context.Context) error {
		_, err := db.DeleteByID[governance_model.Membership](ctx, member.ID)
		return err
	}))
	hook.Name = "撤权后的修改"
	require.ErrorIs(t, webhook_model.UpdateWebhook(ctx, hook), governance_model.ErrForbidden)
	require.ErrorIs(t, webhook_model.DeleteWebhookByID(ctx, hook.ID), governance_model.ErrForbidden)
	require.ErrorIs(t, PrepareTestWebhook(ctx, hook, webhook_module.HookEventPush, &api.PushPayload{}), governance_model.ErrForbidden)
	require.ErrorIs(t, ReplayHookTask(ctx, hook, tasks[0].UUID), governance_model.ErrForbidden)
	require.ErrorIs(t, webhook_model.CreateWebhook(ctx, &webhook_model.Webhook{OwnerID: 7, URL: hook.URL}), governance_model.ErrForbidden)
	stored, err := webhook_model.GetWebhookByID(t.Context(), hook.ID)
	require.NoError(t, err)
	require.Empty(t, stored.Name)
	tasks, err = webhook_model.HookTasks(t.Context(), hook.ID, 1)
	require.NoError(t, err)
	require.Len(t, tasks, 1, "撤权后不得继续创建测试或重投任务")
}
