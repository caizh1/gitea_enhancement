// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package repository

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestTeamRepositoryActorWriteRechecksDelegationAndAdmin(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 5})
	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	_, err := db.GetEngine(ctx).ID(team.OrgID).Cols("repo_admin_change_team_access").Update(&user_model.User{RepoAdminChangeTeamAccess: true})
	require.NoError(t, err)
	require.NoError(t, repo.LoadOwner(ctx))
	require.NoError(t, AddOrUpdateCollaborator(ctx, repo, actor, perm.AccessModeAdmin))
	org, err := organization.GetOrgByID(ctx, team.OrgID)
	require.NoError(t, err)
	allowed, err := org.CanChangeRepoTeamAccess(ctx, actor)
	require.NoError(t, err)
	require.True(t, allowed)
	mode, err := access_model.AccessLevel(ctx, actor, repo)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeAdmin, mode)
	require.NoError(t, ChangeTeamRepositoryAsActor(ctx, actor, team, repo.ID, true))
	unittest.AssertExistsAndLoadBean(t, &organization.TeamRepo{TeamID: team.ID, RepoID: repo.ID})
	require.NoError(t, ChangeTeamRepositoryAsActor(ctx, actor, team, repo.ID, false))
	unittest.AssertNotExistsBean(t, &organization.TeamRepo{TeamID: team.ID, RepoID: repo.ID})

	_, err = db.GetEngine(ctx).ID(team.OrgID).Cols("repo_admin_change_team_access").Update(&user_model.User{RepoAdminChangeTeamAccess: false})
	require.NoError(t, err)
	require.ErrorIs(t, ChangeTeamRepositoryAsActor(ctx, actor, team, repo.ID, true), governance_model.ErrForbidden)
	_, err = db.GetEngine(ctx).ID(team.OrgID).Cols("repo_admin_change_team_access").Update(&user_model.User{RepoAdminChangeTeamAccess: true})
	require.NoError(t, err)
	require.NoError(t, AddOrUpdateCollaborator(ctx, repo, actor, perm.AccessModeWrite))
	require.ErrorIs(t, ChangeTeamRepositoryAsActor(ctx, actor, team, repo.ID, true), governance_model.ErrForbidden)
}

func TestTeamRepositoryActorWriteRejectsRevokedOwnerAndMissingTeam(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	owners := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})
	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	replacement := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	require.NoError(t, db.Insert(ctx, &organization.TeamUser{OrgID: owners.OrgID, TeamID: owners.ID, UID: replacement.ID}))
	_, err := db.GetEngine(ctx).Where("team_id = ? AND uid = ?", owners.ID, actor.ID).Delete(new(organization.TeamUser))
	require.NoError(t, err)
	require.ErrorIs(t, ChangeTeamRepositoryAsOwner(ctx, actor, team, 5, true), governance_model.ErrNotFound)
	require.ErrorIs(t, ChangeAllTeamRepositoriesAsOwner(ctx, actor, team, true), governance_model.ErrNotFound)
	_, err = db.GetEngine(ctx).ID(team.ID).Delete(new(organization.Team))
	require.NoError(t, err)
	require.ErrorIs(t, ChangeTeamRepositoryAsOwner(ctx, replacement, team, 5, true), governance_model.ErrNotFound)
}

func TestTeamRepositoryArchiveBlocksAddsButAllowsRemovals(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})
	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	_, err := db.GetEngine(ctx).ID(team.OrgID).Cols("archived").Update(&governance_model.Namespace{Archived: true})
	require.NoError(t, err)
	require.ErrorIs(t, ChangeTeamRepositoryAsOwner(ctx, actor, team, 5, true), governance_model.ErrConflict)
	require.NoError(t, ChangeTeamRepositoryAsOwner(ctx, actor, team, 3, false))
	unittest.AssertNotExistsBean(t, &organization.TeamRepo{TeamID: team.ID, RepoID: 3})
}
