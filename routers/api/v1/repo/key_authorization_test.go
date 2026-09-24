// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"net/http"
	"testing"

	asymkey_model "gitea.dev/models/asymkey"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/test"
	"gitea.dev/modules/web"
	"gitea.dev/services/contexttest"

	"github.com/stretchr/testify/require"
)

func TestDeployKeyRejectsOwnerChangedAfterAuthorization(t *testing.T) {
	unittest.PrepareTestEnv(t)
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	defer test.MockVariableValue(&setting.SSH.Disabled, false)()
	defer test.MockVariableValue(&setting.SSH.CreateAuthorizedKeysFile, false)()
	ctx, response := contexttest.MockAPIContext(t, "/api/v1/repos/user2/repo1/keys")
	contexttest.LoadUser(t, ctx, 2)
	contexttest.LoadRepo(t, ctx, 1)
	require.True(t, ctx.Repo.Permission.IsAdmin())
	web.SetForm(ctx, &api.CreateKeyOption{
		Title: "stale-owner-deploy-key", ReadOnly: true,
		Key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICV0MGX/W9IvLA4FXpIuUcdDcbj5KX4syHgsTy7soVgf",
	})
	// 固定入口授权后、密钥写入前改变归属的顺序。
	_, err := db.GetEngine(t.Context()).ID(1).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 5})
	require.NoError(t, err)

	CreateDeployKey(ctx)

	require.Equal(t, http.StatusForbidden, response.Code)
	unittest.AssertCount(t, &asymkey_model.DeployKey{Name: "stale-owner-deploy-key"}, 0)
}
