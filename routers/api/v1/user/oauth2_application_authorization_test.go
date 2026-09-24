// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package user

import (
	"net/http"
	"strconv"
	"testing"

	"gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/web"
	"gitea.dev/services/contexttest"

	"github.com/stretchr/testify/require"
)

func TestAPIOAuthApplicationRejectsOrganizationDoer(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockAPIContext(t, "POST /api/v1/user/applications/oauth2")
	contexttest.LoadUser(t, ctx, 3)
	ctx.AuthenticatedUser = unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	web.SetForm(ctx, &api.CreateOAuth2ApplicationOptions{Name: "sudo-organization-application", RedirectURIs: []string{"https://example.invalid/callback"}})

	CreateOauth2Application(ctx)
	require.Equal(t, http.StatusNotFound, ctx.WrittenStatus())
	unittest.AssertNotExistsBean(t, &auth.OAuth2Application{UID: 3, Name: "sudo-organization-application"})
}

func TestAPIOAuthApplicationRejectsInactiveOwnerSnapshot(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockAPIContext(t, "POST /api/v1/user/applications/oauth2")
	contexttest.LoadUser(t, ctx, 2)
	_, err := db.GetEngine(ctx).ID(ctx.Doer.ID).Cols("is_active").Update(&user_model.User{IsActive: false})
	require.NoError(t, err)
	web.SetForm(ctx, &api.CreateOAuth2ApplicationOptions{Name: "inactive-owner-application", RedirectURIs: []string{"https://example.invalid/callback"}})

	CreateOauth2Application(ctx)
	require.Equal(t, http.StatusNotFound, ctx.WrittenStatus())
	unittest.AssertNotExistsBean(t, &auth.OAuth2Application{UID: 2, Name: "inactive-owner-application"})
}

func TestAPIOAuthApplicationRejectsRevokedSudoAdmin(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockAPIContext(t, "POST /api/v1/user/applications/oauth2")
	contexttest.LoadUser(t, ctx, 2)
	ctx.AuthenticatedUser = unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	_, err := db.GetEngine(ctx).ID(5).Cols("is_admin").Update(&user_model.User{IsAdmin: true})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(ctx.AuthenticatedUser.ID).Cols("is_admin").Update(&user_model.User{IsAdmin: false})
	require.NoError(t, err)
	web.SetForm(ctx, &api.CreateOAuth2ApplicationOptions{Name: "stale-sudo-application", RedirectURIs: []string{"https://example.invalid/callback"}})

	CreateOauth2Application(ctx)
	require.Equal(t, http.StatusNotFound, ctx.WrittenStatus())
	unittest.AssertNotExistsBean(t, &auth.OAuth2Application{UID: 2, Name: "stale-sudo-application"})
}

func TestAPIOAuthApplicationRejectsInactiveOwnerMutations(t *testing.T) {
	for _, mutation := range []string{"update", "delete"} {
		t.Run(mutation, func(t *testing.T) {
			unittest.PrepareTestEnv(t)
			ctx, _ := contexttest.MockAPIContext(t, "PATCH /api/v1/user/applications/oauth2/1")
			contexttest.LoadUser(t, ctx, 2)
			app, err := auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "before-inactivation", UserID: ctx.Doer.ID})
			require.NoError(t, err)
			ctx.SetPathParam("id", strconv.FormatInt(app.ID, 10))
			_, err = db.GetEngine(ctx).ID(ctx.Doer.ID).Cols("is_active").Update(&user_model.User{IsActive: false})
			require.NoError(t, err)
			if mutation == "update" {
				web.SetForm(ctx, &api.CreateOAuth2ApplicationOptions{Name: "after-inactivation", RedirectURIs: []string{"https://example.invalid/callback"}})
				UpdateOauth2Application(ctx)
			} else {
				DeleteOauth2Application(ctx)
			}
			require.Equal(t, http.StatusNotFound, ctx.WrittenStatus())
			stored, err := auth.GetOAuth2ApplicationByID(ctx, app.ID)
			require.NoError(t, err)
			require.Equal(t, "before-inactivation", stored.Name)
		})
	}
}

func TestAPIOAuthApplicationCurrentPersonalAndSudoAdminCanCreate(t *testing.T) {
	for _, sudo := range []bool{false, true} {
		t.Run(strconv.FormatBool(sudo), func(t *testing.T) {
			unittest.PrepareTestEnv(t)
			ctx, _ := contexttest.MockAPIContext(t, "POST /api/v1/user/applications/oauth2")
			contexttest.LoadUser(t, ctx, 2)
			if sudo {
				ctx.AuthenticatedUser = unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
			}
			web.SetForm(ctx, &api.CreateOAuth2ApplicationOptions{Name: "current-personal-application", RedirectURIs: []string{"https://example.invalid/callback"}})
			CreateOauth2Application(ctx)
			require.Equal(t, http.StatusCreated, ctx.WrittenStatus())
			unittest.AssertExistsAndLoadBean(t, &auth.OAuth2Application{UID: 2, Name: "current-personal-application"})
		})
	}
}

func TestAPIOAuthApplicationCurrentOwnerCanUpdateAndDelete(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockAPIContext(t, "PATCH /api/v1/user/applications/oauth2/1")
	contexttest.LoadUser(t, ctx, 2)
	app, err := auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "before-update", UserID: ctx.Doer.ID})
	require.NoError(t, err)
	_, err = app.GenerateClientSecret(ctx)
	require.NoError(t, err)
	beforeSecretHash := app.ClientSecret
	ctx.SetPathParam("id", strconv.FormatInt(app.ID, 10))
	web.SetForm(ctx, &api.CreateOAuth2ApplicationOptions{Name: "after-update", RedirectURIs: []string{"https://example.invalid/callback"}})

	UpdateOauth2Application(ctx)
	require.Equal(t, http.StatusOK, ctx.WrittenStatus())
	stored, err := auth.GetOAuth2ApplicationByID(ctx, app.ID)
	require.NoError(t, err)
	require.Equal(t, "after-update", stored.Name)
	require.NotEqual(t, beforeSecretHash, stored.ClientSecret)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "oauth.application_updated", ObjectID: app.ID}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "oauth.application_secret_rotated", ObjectID: app.ID}, 2)
	rotated := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "oauth.application_secret_rotated", ObjectID: app.ID})
	require.NotContains(t, string(rotated.Details), stored.ClientSecret)

	deleteCtx, _ := contexttest.MockAPIContext(t, "DELETE /api/v1/user/applications/oauth2/1")
	contexttest.LoadUser(t, deleteCtx, 2)
	deleteCtx.SetPathParam("id", strconv.FormatInt(app.ID, 10))
	DeleteOauth2Application(deleteCtx)
	require.Equal(t, http.StatusNoContent, deleteCtx.WrittenStatus())
	unittest.AssertNotExistsBean(t, &auth.OAuth2Application{ID: app.ID})
}
