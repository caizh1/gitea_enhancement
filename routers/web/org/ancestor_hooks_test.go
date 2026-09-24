// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/models/webhook"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestAncestorHookSourcesPermission(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	child, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "webhook-child", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, actor, child.ID, governance_service.GroupMemberOption{UserID: 5, Role: governance_model.Owner, Revision: 1}, false))
	hook := &webhook.Webhook{OwnerID: 3, URL: "https://secret.example/hook", Name: "sensitive", Events: `{"push_only":true}`, IsActive: true}
	require.NoError(t, webhook.CreateWebhook(ctx, hook))
	sources, err := ancestorHookSources(ctx, child.ID, 5)
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.True(t, sources[0].CanRead)
	require.False(t, sources[0].CanManage)
	require.Empty(t, sources[0].URL)
	require.Equal(t, "org3", sources[0].Path)
	sources, err = ancestorHookSources(ctx, child.ID, 2)
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.True(t, sources[0].CanManage)
	require.Contains(t, sources[0].URL, "/org/org3/settings/hooks")
	_, err = db.GetEngine(ctx).ID(3).Cols("visibility").Update(&user_model.User{Visibility: 2})
	require.NoError(t, err)
	sources, err = ancestorHookSources(ctx, child.ID, 5)
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.False(t, sources[0].CanRead)
	require.Empty(t, sources[0].Path)
	require.Empty(t, sources[0].URL)
	require.Zero(t, sources[0].Count)
}
