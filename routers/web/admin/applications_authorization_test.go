// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"gitea.dev/models/auth"
	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/services/contexttest"

	"github.com/stretchr/testify/require"
)

func TestInstanceOAuthSecretRotationRejectsStaleAdminContext(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockContext(t, "POST /-/admin/applications/oauth2/1/regenerate_secret")
	contexttest.LoadUser(t, ctx, 1)
	require.True(t, ctx.Doer.IsAdmin)
	app, err := auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "instance-stale-admin", UserID: 0})
	require.NoError(t, err)
	_, err = app.GenerateClientSecret(ctx)
	require.NoError(t, err)
	before := app.ClientSecret
	ctx.SetPathParam("id", strconv.FormatInt(app.ID, 10))
	_, err = db.GetEngine(ctx).ID(2).Cols("is_admin").Update(&user_model.User{IsAdmin: true})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(ctx.Doer.ID).Cols("is_admin").Update(&user_model.User{IsAdmin: false})
	require.NoError(t, err)

	ApplicationsRegenerateSecret(ctx)
	require.Equal(t, http.StatusNotFound, ctx.WrittenStatus())
	stored, err := auth.GetOAuth2ApplicationByID(ctx, app.ID)
	require.NoError(t, err)
	require.Equal(t, before, stored.ClientSecret)
}

func TestInstanceOAuthCurrentAdminCanMutate(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockContext(t, "POST /-/admin/applications/oauth2")
	contexttest.LoadUser(t, ctx, 1)
	handlers := newOAuth2CommonHandlers(ctx)
	authorize := func(writeCtx context.Context) error { return handlers.AuthorizeWrite(writeCtx, false) }
	revoke := func(writeCtx context.Context) error { return handlers.AuthorizeWrite(writeCtx, true) }
	app, err := auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "instance-current-admin", UserID: 0, Authorize: authorize})
	require.NoError(t, err)
	_, err = auth.UpdateOAuth2Application(ctx, auth.UpdateOAuth2ApplicationOptions{ID: app.ID, UserID: 0, Name: "instance-updated", Authorize: authorize})
	require.NoError(t, err)
	_, err = app.GenerateClientSecret(ctx, authorize)
	require.NoError(t, err)
	require.NoError(t, auth.DeleteOAuth2Application(ctx, app.ID, 0, revoke))
}
