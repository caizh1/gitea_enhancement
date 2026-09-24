// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package org

import (
	"sync"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestTeamInviteRechecksCurrentActorsAndScope(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	owners := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	inviter := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	invitee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})

	t.Run("revoked inviter cannot create", func(t *testing.T) {
		_, err := db.GetEngine(ctx).ID(inviter.ID).Cols("is_active").Update(&user_model.User{IsActive: false})
		require.NoError(t, err)
		require.ErrorIs(t, CreateTeamInvite(ctx, inviter, owners, "revoked@example.invalid"), governance_model.ErrNotFound)
		invites, err := organization.GetInvitesByTeamID(ctx, owners.ID)
		require.NoError(t, err)
		require.Empty(t, invites)
		_, err = db.GetEngine(ctx).ID(inviter.ID).Cols("is_active").Update(&user_model.User{IsActive: true})
		require.NoError(t, err)
	})

	invite, err := organization.CreateTeamInvite(ctx, inviter, owners, "bearer@example.invalid")
	require.NoError(t, err)
	t.Run("inactive invitee cannot redeem", func(t *testing.T) {
		_, err := db.GetEngine(ctx).ID(invitee.ID).Cols("is_active").Update(&user_model.User{IsActive: false})
		require.NoError(t, err)
		_, _, err = AcceptTeamInvite(ctx, invite.Token, invitee)
		require.ErrorIs(t, err, governance_model.ErrNotFound)
		member, err := organization.IsTeamMember(ctx, owners.OrgID, owners.ID, invitee.ID)
		require.NoError(t, err)
		require.False(t, member)
		_, err = db.GetEngine(ctx).ID(invitee.ID).Cols("is_active").Update(&user_model.User{IsActive: true})
		require.NoError(t, err)
	})

	t.Run("moved invite scope cannot redeem", func(t *testing.T) {
		_, err := db.GetEngine(ctx).ID(invite.ID).Cols("org_id").Update(&organization.TeamInvite{OrgID: 6})
		require.NoError(t, err)
		_, _, err = AcceptTeamInvite(ctx, invite.Token, invitee)
		require.ErrorIs(t, err, governance_model.ErrNotFound)
		_, err = db.GetEngine(ctx).ID(invite.ID).Cols("org_id").Update(&organization.TeamInvite{OrgID: owners.OrgID})
		require.NoError(t, err)
	})

	t.Run("bearer accepts once", func(t *testing.T) {
		org, team, err := AcceptTeamInvite(ctx, invite.Token, invitee)
		require.NoError(t, err)
		require.Equal(t, owners.OrgID, org.ID)
		require.Equal(t, owners.ID, team.ID)
		member, err := organization.IsTeamMember(ctx, owners.OrgID, owners.ID, invitee.ID)
		require.NoError(t, err)
		require.True(t, member)
		_, _, err = AcceptTeamInvite(ctx, invite.Token, invitee)
		require.True(t, organization.IsErrTeamInviteNotFound(err))
	})
}

func TestTeamInviteRevocationAndAcceptanceAreSerialized(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	owners := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	inviter := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	invitee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	invite, err := organization.CreateTeamInvite(ctx, inviter, owners, invitee.Email)
	require.NoError(t, err)
	require.NoError(t, RemoveTeamInvite(ctx, inviter, owners, invite.ID))
	_, _, err = AcceptTeamInvite(ctx, invite.Token, invitee)
	require.True(t, organization.IsErrTeamInviteNotFound(err))
	member, err := organization.IsTeamMember(ctx, owners.OrgID, owners.ID, invitee.ID)
	require.NoError(t, err)
	require.False(t, member)
	require.ErrorIs(t, RemoveTeamInvite(ctx, inviter, owners, invite.ID), governance_model.ErrNotFound)
}

func TestTeamInviteRevokedOwnerCannotCreateOrRemove(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	owners := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	inviter := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	replacement := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	invite, err := organization.CreateTeamInvite(ctx, inviter, owners, "other@example.invalid")
	require.NoError(t, err)
	require.NoError(t, AddTeamMember(ctx, owners, replacement))
	require.NoError(t, RemoveTeamMember(ctx, owners, inviter))
	require.ErrorIs(t, CreateTeamInvite(ctx, inviter, owners, "new@example.invalid"), governance_model.ErrNotFound)
	require.ErrorIs(t, RemoveTeamInvite(ctx, inviter, owners, invite.ID), governance_model.ErrNotFound)
	_, err = organization.GetInviteByToken(ctx, invite.Token)
	require.NoError(t, err)
}

func TestTeamInviteConcurrentRedeemAndRevoke(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	owners := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	inviter := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	invitee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	invite, err := organization.CreateTeamInvite(ctx, inviter, owners, "other@example.invalid")
	require.NoError(t, err)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var acceptErr, revokeErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, _, acceptErr = AcceptTeamInvite(ctx, invite.Token, invitee)
	}()
	go func() {
		defer wg.Done()
		<-start
		revokeErr = RemoveTeamInvite(ctx, inviter, owners, invite.ID)
	}()
	close(start)
	wg.Wait()
	require.NotEqual(t, acceptErr == nil, revokeErr == nil)
	if acceptErr != nil {
		require.True(t, organization.IsErrTeamInviteNotFound(acceptErr))
	} else {
		require.ErrorIs(t, revokeErr, governance_model.ErrNotFound)
	}
	member, err := organization.IsTeamMember(ctx, owners.OrgID, owners.ID, invitee.ID)
	require.NoError(t, err)
	require.Equal(t, acceptErr == nil, member)
	_, err = organization.GetInviteByToken(ctx, invite.Token)
	require.True(t, organization.IsErrTeamInviteNotFound(err))
}

func TestTeamInviteLifecycleBlocksNewGrantsButAllowsRevocation(t *testing.T) {
	for _, state := range []struct {
		name        string
		archived    bool
		deleteAfter int64
	}{
		{name: "archived", archived: true},
		{name: "pending deletion", deleteAfter: time.Now().Add(time.Hour).Unix()},
	} {
		t.Run(state.name, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			ctx := t.Context()
			require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
			owners := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
			inviter := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
			invitee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
			invite, err := organization.CreateTeamInvite(ctx, inviter, owners, invitee.Email)
			require.NoError(t, err)
			_, err = db.GetEngine(ctx).ID(owners.OrgID).Cols("archived", "delete_after").Update(&governance_model.Namespace{Archived: state.archived, DeleteAfter: state.deleteAfter})
			require.NoError(t, err)

			require.ErrorIs(t, CreateTeamInvite(ctx, inviter, owners, "new@example.invalid"), governance_model.ErrConflict)
			_, _, err = AcceptTeamInvite(ctx, invite.Token, invitee)
			require.ErrorIs(t, err, governance_model.ErrConflict)
			member, err := organization.IsTeamMember(ctx, owners.OrgID, owners.ID, invitee.ID)
			require.NoError(t, err)
			require.False(t, member)
			_, err = organization.GetInviteByToken(ctx, invite.Token)
			require.NoError(t, err)
			require.NoError(t, RemoveTeamInvite(ctx, inviter, owners, invite.ID))
		})
	}
}

func TestTeamInviteAncestorArchiveBlocksChildGrants(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	child, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "invite-child", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	owners, err := organization.GetOwnerTeam(ctx, child.ID)
	require.NoError(t, err)
	inviter := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	invitee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	invite, err := organization.CreateTeamInvite(ctx, inviter, owners, invitee.Email)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(3).Cols("archived").Update(&governance_model.Namespace{Archived: true})
	require.NoError(t, err)
	childState, err := governance_model.GetNamespace(ctx, child.ID)
	require.NoError(t, err)
	require.False(t, childState.Archived)
	require.ErrorIs(t, CreateTeamInvite(ctx, inviter, owners, "new@example.invalid"), governance_model.ErrConflict)
	_, _, err = AcceptTeamInvite(ctx, invite.Token, invitee)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	require.NoError(t, RemoveTeamInvite(ctx, inviter, owners, invite.ID))
}
