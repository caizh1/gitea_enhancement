// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package setting

import (
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

func TestPersonalOAuthSecretRotationRejectsInactiveOwnerSnapshot(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockContext(t, "POST /user/settings/applications/oauth2/1/regenerate_secret")
	contexttest.LoadUser(t, ctx, 2)
	app, err := auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "stale-personal-owner", UserID: ctx.Doer.ID})
	require.NoError(t, err)
	_, err = app.GenerateClientSecret(ctx)
	require.NoError(t, err)
	before := app.ClientSecret
	ctx.SetPathParam("id", strconv.FormatInt(app.ID, 10))
	_, err = db.GetEngine(ctx).ID(ctx.Doer.ID).Cols("is_active").Update(&user_model.User{IsActive: false})
	require.NoError(t, err)

	OAuthApplicationsRegenerateSecret(ctx)
	require.Equal(t, http.StatusNotFound, ctx.WrittenStatus())
	stored, err := auth.GetOAuth2ApplicationByID(ctx, app.ID)
	require.NoError(t, err)
	require.Equal(t, before, stored.ClientSecret)
}
