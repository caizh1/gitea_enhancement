// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package shared

import (
	"net/http"
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/services/contexttest"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
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
