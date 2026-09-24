// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package asymkey

import (
	"testing"

	asymkey_model "gitea.dev/models/asymkey"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	"gitea.dev/modules/util"

	"github.com/stretchr/testify/require"
)

func TestDeployKeyAuthorizationUsesCurrentOwner(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	defer test.MockVariableValue(&setting.SSH.CreateAuthorizedKeysFile, false)()
	owner := governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "api"}
	reader := governance_model.Actor{ID: 4, Kind: "user", Name: "user4", Transport: "api"}
	_, err := AddDeployKeyForActor(ctx, reader, 1, "denied-key", governanceTestPublicKey, true)
	require.ErrorIs(t, err, util.ErrPermissionDenied)
	key, err := AddDeployKeyForActor(ctx, owner, 1, "authorized-key", governanceTestPublicKey, true)
	require.NoError(t, err)

	_, err = db.GetEngine(ctx).ID(1).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 5})
	require.NoError(t, err)
	require.ErrorIs(t, DeleteDeployKeyForActor(ctx, owner, 1, key.ID), util.ErrPermissionDenied)
	unittest.AssertExistsAndLoadBean(t, &asymkey_model.DeployKey{ID: key.ID})
	_, err = AddDeployKeyForActor(ctx, owner, 1, "stale-key", governanceTestPublicKey, false)
	require.ErrorIs(t, err, util.ErrPermissionDenied)

	current := governance_model.Actor{ID: 5, Kind: "user", Name: "user5", Transport: "api"}
	require.NoError(t, DeleteDeployKeyForActor(ctx, current, 1, key.ID))
	unittest.AssertCount(t, &asymkey_model.DeployKey{ID: key.ID}, 0)
	key, err = AddDeployKeyForActor(ctx, current, 1, "new-owner-key", governanceTestPublicKey, false)
	require.NoError(t, err)
	require.NoError(t, DeleteDeployKeyForActor(ctx, current, 1, key.ID))
}
