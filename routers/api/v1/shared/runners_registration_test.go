// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package shared

import (
	"net/http"
	"testing"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	actions_service "gitea.dev/services/actions"
	"gitea.dev/services/contexttest"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestRegistrationTokenReadRejectsOwnerRevokedAfterRequestContext(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	manager := governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, manager, governance_service.GroupOption{Path: "registration-read-revoked", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, manager, group.ID, governance_service.GroupMemberOption{
		UserID: 4, Role: governance_model.Owner, ExpiresUnix: time.Now().Add(time.Hour).Unix(), Revision: group.Revision,
	}, false))
	member := governance_model.Actor{ID: 4, Kind: "user", Name: "user4", Transport: "api"}
	access, err := governance_service.CheckGroupAccess(ctx, member.ID, group.ID, governance_model.ManageGroup)
	require.NoError(t, err)
	require.True(t, access.Abilities[governance_model.ManageGroup])

	request, response := contexttest.MockAPIContext(t, "/api/v1/orgs/registration-read-revoked/actions/runners/registration-token")
	contexttest.LoadUser(t, request, member.ID)
	initial, err := actions_service.GetRunnerRegistrationToken(ctx, member, group.ID, 0, false)
	require.NoError(t, err)
	updated, err := governance_model.GetNamespace(ctx, group.ID)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, manager, group.ID, governance_service.GroupMemberOption{
		UserID: member.ID, Revision: updated.Revision,
	}, true))

	GetRegistrationToken(request, group.ID, 0)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.NotContains(t, response.Body.String(), initial.Token)
	stored := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunnerToken{ID: initial.ID})
	require.True(t, stored.IsActive)
	count, err := db.GetEngine(ctx).Where("owner_id = ? AND repo_id = ?", group.ID, 0).Count(new(actions_model.ActionRunnerToken))
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	_, err = governance_service.CheckGroupAccess(ctx, member.ID, group.ID, governance_model.ManageGroup)
	require.Error(t, err)
}

func TestRegistrationTokenRejectsOwnerChangedAfterAuthorization(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	ctx, response := contexttest.MockAPIContext(t, "/api/v1/repos/user2/repo1/actions/runners/registration-token")
	contexttest.LoadUser(t, ctx, 2)
	contexttest.LoadRepo(t, ctx, 1)
	require.True(t, ctx.Repo.Permission.IsAdmin())

	// 固定中间件通过后、令牌披露前发生转移的顺序，不依赖调度或延时。
	_, err := db.GetEngine(t.Context()).ID(1).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 5})
	require.NoError(t, err)
	_, err = db.GetEngine(t.Context()).Where("repo_id = ?", 1).Delete(new(actions_model.ActionRunnerToken))
	require.NoError(t, err)

	GetRegistrationToken(ctx, 0, 1)

	require.Equal(t, http.StatusForbidden, response.Code)
	count, err := db.GetEngine(t.Context()).Where("repo_id = ?", 1).Count(new(actions_model.ActionRunnerToken))
	require.NoError(t, err)
	require.Zero(t, count)
}
