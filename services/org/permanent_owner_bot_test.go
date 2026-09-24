// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package org

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestNativeBotCannotReplaceLastHumanOwner(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	human := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	bot := &user_model.User{Name: "owner-bot", LowerName: "owner-bot", Email: "owner-bot@example.invalid", Type: user_model.UserTypeBot, IsActive: true}
	require.NoError(t, db.Insert(ctx, bot))
	require.NoError(t, AddTeamMember(ctx, team, bot))
	require.ErrorIs(t, RemoveTeamMember(ctx, team, human), governance_model.ErrConflict)
	unittest.AssertExistsAndLoadBean(t, &organization.TeamUser{TeamID: team.ID, UID: human.ID})
	backup := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	require.NoError(t, AddTeamMember(ctx, team, backup))
	require.NoError(t, RemoveTeamMember(ctx, team, human))
	require.NoError(t, governance_model.EnsurePermanentGroupOwner(ctx, team.OrgID, 0))
}

func TestDirectBotCannotSatisfyPermanentGroupOwner(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := db.GetEngine(ctx).ID(3).Cols("native_owner_team_id").Update(&governance_model.Namespace{NativeOwnerTeamID: 0})
	require.NoError(t, err)
	bot := &user_model.User{Name: "direct-owner-bot", LowerName: "direct-owner-bot", Email: "direct-owner-bot@example.invalid", Type: user_model.UserTypeBot, IsActive: true}
	require.NoError(t, db.Insert(ctx, bot))
	require.NoError(t, db.Insert(ctx, &governance_model.Membership{ScopeType: "group", ScopeID: 3, UserID: bot.ID, Role: governance_model.Owner}))
	require.ErrorIs(t, governance_model.EnsurePermanentGroupOwner(ctx, 3, 0), governance_model.ErrConflict)
	require.NoError(t, db.Insert(ctx, &governance_model.Membership{ScopeType: "group", ScopeID: 3, UserID: 4, Role: governance_model.Owner}))
	require.NoError(t, governance_model.EnsurePermanentGroupOwner(ctx, 3, 0))
}
