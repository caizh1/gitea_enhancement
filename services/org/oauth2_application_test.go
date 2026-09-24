// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package org

import (
	"context"
	"testing"
	"time"

	"gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func oauthWrite(actorID, orgID int64, revoke bool) func(context.Context) error {
	return func(ctx context.Context) error {
		return AuthorizeOAuthApplicationWrite(ctx, actorID, orgID, revoke)
	}
}

func TestNativeOAuthRevokedOwnerCannotMutate(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	owners := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	replacement := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	app, err := auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "before-revocation", UserID: owners.OrgID})
	require.NoError(t, err)
	_, err = app.GenerateClientSecret(ctx)
	require.NoError(t, err)
	before := app.ClientSecret
	require.NoError(t, AddTeamMember(ctx, owners, replacement))
	require.NoError(t, RemoveTeamMember(ctx, owners, actor))

	_, err = auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "stale-create", UserID: owners.OrgID, Authorize: oauthWrite(actor.ID, owners.OrgID, false)})
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	_, err = auth.UpdateOAuth2Application(ctx, auth.UpdateOAuth2ApplicationOptions{ID: app.ID, UserID: owners.OrgID, Name: "stale-edit", Authorize: oauthWrite(actor.ID, owners.OrgID, false)})
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	_, err = app.GenerateClientSecret(ctx, oauthWrite(actor.ID, owners.OrgID, false))
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	require.ErrorIs(t, auth.DeleteOAuth2Application(ctx, app.ID, owners.OrgID, oauthWrite(actor.ID, owners.OrgID, true)), governance_model.ErrNotFound)
	stored, err := auth.GetOAuth2ApplicationByID(ctx, app.ID)
	require.NoError(t, err)
	require.Equal(t, "before-revocation", stored.Name)
	require.Equal(t, before, stored.ClientSecret)
	unittest.AssertNotExistsBean(t, &auth.OAuth2Application{Name: "stale-create", UID: owners.OrgID})
}

func TestNativeOAuthGroupOwnerAndLifecycle(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	rootOwner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	rootApp, err := auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "root-owner", UserID: 3, Authorize: oauthWrite(2, 3, false)})
	require.NoError(t, err)
	require.NoError(t, auth.DeleteOAuth2Application(ctx, rootApp.ID, 3, oauthWrite(2, 3, true)))
	child, err := governance_service.CreateGroup(ctx, rootOwner, governance_service.GroupOption{Path: "oauth-child", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, rootOwner, child.ID, governance_service.GroupMemberOption{UserID: 5, Role: governance_model.Owner, Revision: child.Revision}, false))
	for _, ownerID := range []int64{2, 5} {
		secret, hash, err := auth.NewOAuth2ClientSecret()
		require.NoError(t, err)
		app, err := auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "child-owner", UserID: child.ID, ClientSecretHash: hash, Authorize: oauthWrite(ownerID, child.ID, false)})
		require.NoError(t, err)
		require.True(t, app.ValidateClientSecret([]byte(secret)))
		require.NoError(t, auth.DeleteOAuth2Application(ctx, app.ID, child.ID, oauthWrite(ownerID, child.ID, true)))
	}

	for _, state := range []struct {
		name        string
		archived    bool
		deleteAfter int64
	}{
		{name: "archived ancestor", archived: true},
		{name: "pending deletion", deleteAfter: time.Now().Add(time.Hour).Unix()},
	} {
		t.Run(state.name, func(t *testing.T) {
			app, err := auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "existing", UserID: child.ID})
			require.NoError(t, err)
			_, err = db.GetEngine(ctx).ID(3).Cols("archived", "delete_after").Update(&governance_model.Namespace{Archived: state.archived, DeleteAfter: state.deleteAfter})
			require.NoError(t, err)
			_, err = auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "blocked", UserID: child.ID, Authorize: oauthWrite(2, child.ID, false)})
			require.ErrorIs(t, err, governance_model.ErrConflict)
			_, err = auth.UpdateOAuth2Application(ctx, auth.UpdateOAuth2ApplicationOptions{ID: app.ID, UserID: child.ID, Name: "blocked", Authorize: oauthWrite(2, child.ID, false)})
			require.ErrorIs(t, err, governance_model.ErrConflict)
			_, err = app.GenerateClientSecret(ctx, oauthWrite(2, child.ID, false))
			require.ErrorIs(t, err, governance_model.ErrConflict)
			require.NoError(t, auth.DeleteOAuth2Application(ctx, app.ID, child.ID, oauthWrite(2, child.ID, true)))
			_, err = db.GetEngine(ctx).ID(3).Cols("archived", "delete_after").Update(&governance_model.Namespace{})
			require.NoError(t, err)
		})
	}
}

func TestNativeOAuthPersonalAndInstanceCompatibility(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	for _, ownerID := range []int64{0, 2} {
		app, err := auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "non-org", UserID: ownerID})
		require.NoError(t, err)
		_, err = app.GenerateClientSecret(ctx)
		require.NoError(t, err)
		_, err = auth.UpdateOAuth2Application(ctx, auth.UpdateOAuth2ApplicationOptions{ID: app.ID, UserID: ownerID, Name: "updated"})
		require.NoError(t, err)
		require.NoError(t, auth.DeleteOAuth2Application(ctx, app.ID, ownerID))
	}
}
