// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/util"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestRunnerRegistrationTokenManagementScope(t *testing.T) {
	for _, test := range []struct {
		name                    string
		userID, ownerID, repoID int64
		allowed                 bool
	}{
		{"instance_admin", 1, 0, 0, true},
		{"instance_member_denied", 2, 0, 0, false},
		{"personal_owner", 2, 2, 0, true},
		{"personal_other_denied", 4, 2, 0, false},
		{"organization_owner", 2, 3, 0, true},
		{"organization_developer_denied", 4, 3, 0, false},
		{"repository_owner", 2, 0, 1, true},
		{"repository_reader_denied", 4, 0, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
			doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: test.userID})
			actor := governance_service.RequestActor(doer, "", "web")
			token, err := GetRunnerRegistrationToken(t.Context(), actor, test.ownerID, test.repoID, false)
			if !test.allowed {
				require.ErrorIs(t, err, util.ErrPermissionDenied)
				require.Nil(t, token)
				return
			}
			require.NoError(t, err)
			require.True(t, token.IsActive)
			reused, err := GetRunnerRegistrationToken(t.Context(), actor, test.ownerID, test.repoID, false)
			require.NoError(t, err)
			require.Equal(t, token.ID, reused.ID)
			rotated, err := GetRunnerRegistrationToken(t.Context(), actor, test.ownerID, test.repoID, true)
			require.NoError(t, err)
			require.NotEqual(t, token.ID, rotated.ID)
			require.False(t, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunnerToken{ID: token.ID}).IsActive)
		})
	}
}

func TestRunnerRegistrationTokenRejectsRevokedActor(t *testing.T) {
	for _, test := range []struct {
		name       string
		delegation bool
	}{
		{"admin_demoted", false},
		{"delegating_admin_demoted", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
			actor := governance_model.Actor{ID: 1, Kind: "user", Name: "user1", Transport: "api"}
			repoID := int64(0)
			if test.delegation {
				actor.ActingAsID, repoID = 2, 1
			}
			_, err := db.GetEngine(t.Context()).ID(1).Cols("is_admin").Update(&user_model.User{IsAdmin: false})
			require.NoError(t, err)
			token, err := GetRunnerRegistrationToken(t.Context(), actor, 0, repoID, true)
			require.ErrorIs(t, err, util.ErrPermissionDenied)
			require.Nil(t, token)
		})
	}
}

func TestRunnerRegistrationTokenInheritedOwner(t *testing.T) {
	unittest.PrepareTestEnv(t)
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	creator := governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "web"}
	parent, err := governance_service.CreateGroup(t.Context(), creator, governance_service.GroupOption{Name: "令牌父组", Path: "registration-parent"})
	require.NoError(t, err)
	child, err := governance_service.CreateGroup(t.Context(), creator, governance_service.GroupOption{Name: "令牌子组", Path: "registration-child", ParentID: parent.ID})
	require.NoError(t, err)
	require.NoError(t, db.Insert(t.Context(), &governance_model.Membership{ScopeType: "group", ScopeID: parent.ID, UserID: 4, Role: governance_model.Owner}))
	actor := governance_model.Actor{ID: 4, Kind: "user", Name: "user4", Transport: "web"}
	token, err := GetRunnerRegistrationToken(t.Context(), actor, child.ID, 0, false)
	require.NoError(t, err)
	require.True(t, token.IsActive)
	_, err = db.GetEngine(t.Context()).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "group", parent.ID, 4).Delete(new(governance_model.Membership))
	require.NoError(t, err)
	token, err = GetRunnerRegistrationToken(t.Context(), actor, child.ID, 0, true)
	require.ErrorIs(t, err, util.ErrPermissionDenied)
	require.Nil(t, token)
}

func TestRunnerRegistrationTokenAfterGroupMove(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	creator := governance_model.Actor{ID: 1, Kind: "user", Name: "user1", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, creator, governance_service.GroupOption{Path: "registration-moved", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	formerOwner := governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "api"}
	oldToken, err := GetRunnerRegistrationToken(ctx, formerOwner, group.ID, 0, false)
	require.NoError(t, err)
	_, err = governance_service.MoveGroup(ctx, formerOwner, group.ID, governance_service.GroupOption{Path: "registration-moved", Revision: group.Revision})
	require.NoError(t, err)
	require.False(t, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunnerToken{ID: oldToken.ID}).IsActive)
	runner := &actions_model.ActionRunner{Name: "former-root-runner", OwnerID: group.ID}
	runner.GenerateAndFillToken()
	require.Error(t, actions_model.RegisterRunnerWithToken(ctx, runner, oldToken.ID))
	_, err = GetRunnerRegistrationToken(ctx, formerOwner, group.ID, 0, false)
	require.ErrorIs(t, err, util.ErrPermissionDenied)
	newToken, err := GetRunnerRegistrationToken(ctx, creator, group.ID, 0, false)
	require.NoError(t, err)
	require.NotEqual(t, oldToken.ID, newToken.ID)
	require.NoError(t, actions_model.RegisterRunnerWithToken(ctx, runner, newToken.ID))
}
