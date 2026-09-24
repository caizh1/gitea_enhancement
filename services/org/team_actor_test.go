// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package org

import (
	"testing"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestNativeTeamActorWriteRechecksRevokedOwner(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	owners := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})
	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	replacement := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	target := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	member := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	require.NoError(t, AddTeamMember(ctx, owners, replacement))
	require.NoError(t, RemoveTeamMember(ctx, owners, actor))

	require.ErrorIs(t, NewTeamAsOwner(ctx, actor, &organization.Team{OrgID: team.OrgID, Name: "stale-owner"}), governance_model.ErrNotFound)
	changed := *team
	changed.Name = "stale-owner-update"
	require.ErrorIs(t, UpdateTeamAsOwner(ctx, actor, &changed, false, false), governance_model.ErrNotFound)
	require.ErrorIs(t, DeleteTeamAsOwner(ctx, actor, team), governance_model.ErrNotFound)
	require.ErrorIs(t, AddTeamMemberAsOwner(ctx, actor, team, target), governance_model.ErrNotFound)
	require.ErrorIs(t, RemoveTeamMemberAsOwner(ctx, actor, team, member), governance_model.ErrNotFound)
	unittest.AssertNotExistsBean(t, &organization.Team{OrgID: team.OrgID, LowerName: "stale-owner"})
	unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: team.ID, Name: team.Name})
	unittest.AssertExistsAndLoadBean(t, &organization.TeamUser{TeamID: team.ID, UID: member.ID})
	unittest.AssertNotExistsBean(t, &organization.TeamUser{TeamID: team.ID, UID: target.ID})
}

func TestNativeTeamActorWriteRejectsDeletedTeam(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})
	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	target := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	require.NoError(t, DeleteTeam(ctx, team))
	require.ErrorIs(t, AddTeamMemberAsOwner(ctx, actor, team, target), governance_model.ErrNotFound)
	unittest.AssertNotExistsBean(t, &organization.OrgUser{OrgID: team.OrgID, UID: target.ID})
}

func TestNativeTeamActorWriteAllowsCurrentOwner(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	target := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	team := &organization.Team{OrgID: 3, Name: "current-owner-team"}
	require.NoError(t, NewTeamAsOwner(ctx, actor, team))
	require.NoError(t, AddTeamMemberAsOwner(ctx, actor, team, target))
	unittest.AssertExistsAndLoadBean(t, &organization.TeamUser{TeamID: team.ID, UID: target.ID})
	team.Name = "current-owner-renamed"
	require.NoError(t, UpdateTeamAsOwner(ctx, actor, team, false, false))
	unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: team.ID, LowerName: "current-owner-renamed"})
	require.NoError(t, RemoveTeamMemberAsOwner(ctx, actor, team, target))
	require.NoError(t, DeleteTeamAsOwner(ctx, actor, team))
	unittest.AssertNotExistsBean(t, &organization.Team{ID: team.ID})
}
