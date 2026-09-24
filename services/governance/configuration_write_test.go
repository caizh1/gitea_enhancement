// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"testing"

	actions_model "gitea.dev/models/actions"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/util"

	"github.com/stretchr/testify/require"
)

func TestConfigurationWriteChecksRevokedGroupOwner(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	group, err := governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, owner, 3,
		GroupMemberOption{UserID: 4, Role: governance_model.Owner, Revision: group.Revision}, false))
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	request := RequestActor(doer, "", "web")
	initial := actions_model.OwnerActionsConfig{TokenPermissionMode: repo_model.ActionsTokenPermissionModeRestricted}
	require.NoError(t, WithConfigurationWrite(ctx, request, 3, 0, func(tx context.Context) error {
		return actions_model.SetOwnerActionsConfig(tx, 3, initial)
	}))
	group, err = governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, owner, 3,
		GroupMemberOption{UserID: 4, Revision: group.Revision}, true))
	maximum := repo_model.MakeActionsTokenPermissions(0)
	changed := actions_model.OwnerActionsConfig{
		TokenPermissionMode: repo_model.ActionsTokenPermissionModePermissive,
		MaxTokenPermissions: &maximum,
		AllowedCrossRepoIDs: []int64{1},
	}
	err = WithConfigurationWrite(ctx, request, 3, 0, func(tx context.Context) error {
		return actions_model.SetOwnerActionsConfig(tx, 3, changed)
	})
	require.ErrorIs(t, err, util.ErrPermissionDenied)
	actual, err := actions_model.GetOwnerActionsConfig(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, initial.TokenPermissionMode, actual.TokenPermissionMode)
	require.Nil(t, actual.MaxTokenPermissions)
	require.Empty(t, actual.AllowedCrossRepoIDs)
}
