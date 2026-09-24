// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package org

import (
	"context"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeMembershipAndAccountChangesRespectMergeReservation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	org := unittest.AssertExistsAndLoadBean(t, &organization.Organization{ID: 3})
	operation := &governance_model.MergeAuthorization{PullID: 999, RepoID: 3, Branch: "main", Head: "head", OldTarget: "before", NewTarget: "after", Actor: governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}}
	dependencies := []string{governance_model.Resource("group", 3), governance_model.Resource("team", 2), governance_model.Resource("user", 4)}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, dependencies, func(context.Context) error { return nil }))
	assert.ErrorIs(t, RemoveTeamMember(ctx, team, user), governance_model.ErrConflict)
	assert.ErrorIs(t, RemoveOrgUser(ctx, org, user), governance_model.ErrConflict)
	assert.ErrorIs(t, UpdateTeam(ctx, team, true, false), governance_model.ErrConflict)
	assert.ErrorIs(t, DeleteTeam(ctx, team), governance_model.ErrConflict)
	assert.ErrorIs(t, user_model.UpdateUserCols(ctx, &user_model.User{ID: 4, ProhibitLogin: true}, "prohibit_login"), governance_model.ErrConflict)
	current := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	assert.False(t, current.ProhibitLogin)
	unittest.AssertExistsAndLoadBean(t, &organization.TeamUser{TeamID: 2, UID: 4})
	_, err := governance_model.ReconcileMerge(ctx, operation.ID, "before")
	require.NoError(t, err)
	require.NoError(t, RemoveTeamMember(ctx, team, user))
	unittest.AssertNotExistsBean(t, &organization.TeamUser{TeamID: 2, UID: 4})
	require.NoError(t, user_model.UpdateUserCols(ctx, &user_model.User{ID: 4, ProhibitLogin: true}, "prohibit_login"))
}

func TestLastNativeOwnerUsesCurrentMembershipInsteadOfStaleTeam(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	user4 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	require.NoError(t, AddTeamMember(ctx, team, user4))
	first, second := *team, *team
	require.NoError(t, RemoveTeamMember(ctx, &first, user4))
	err := RemoveTeamMember(ctx, &second, user2)
	assert.True(t, organization.IsErrLastOrgOwner(err))
	unittest.AssertExistsAndLoadBean(t, &organization.TeamUser{TeamID: 1, UID: 2})
	current := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	assert.Equal(t, 1, current.NumMembers)
}

func TestInheritedPermanentOwnerAllowsRemovingLastNativeOwner(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	require.NoError(t, db.Insert(ctx, &governance_model.Membership{ScopeType: "group", ScopeID: team.OrgID, UserID: 4, Role: governance_model.Owner}))

	require.NoError(t, RemoveTeamMember(ctx, team, user2))
	unittest.AssertNotExistsBean(t, &organization.TeamUser{TeamID: team.ID, UID: user2.ID})
}

func TestOwnerTeamRenameCannotRemoveLastOwner(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	team.Name = "renamed-owners"
	err := UpdateTeam(ctx, team, false, false)
	if err == nil {
		renamed := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: team.ID})
		removeErr := RemoveTeamMember(ctx, renamed, user)
		require.Error(t, removeErr, "改名不得绕过最后 Owner 保护")
	}
	require.ErrorIs(t, err, governance_model.ErrConflict)
	require.ErrorIs(t, err, ErrRenameOwnerTeam)
	unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: team.ID, Name: organization.OwnerTeamName})
	unittest.AssertExistsAndLoadBean(t, &organization.TeamUser{TeamID: team.ID, UID: user.ID})
	require.NoError(t, governance_model.EnsurePermanentGroupOwner(ctx, team.OrgID, 0))
}
