// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package org

import (
	"net/http"
	"strconv"
	"testing"

	"gitea.dev/models/auth"
	"gitea.dev/models/organization"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/services/context"
	"gitea.dev/services/contexttest"
	org_service "gitea.dev/services/org"

	"github.com/stretchr/testify/require"
)

func TestOrganizationOAuthApplicationUsesPublicGroupPath(t *testing.T) {
	ctx := &context.Context{
		Doer: &user_model.User{ID: 2},
		Org: &context.Organization{
			Organization: organization.OrgFromUser(&user_model.User{ID: 24, Name: "g_internal_alias"}),
			OrgLink:      "/org/parent/child",
		},
	}
	handlers := newOAuth2CommonHandlers(ctx)
	require.Equal(t, int64(24), handlers.OwnerID)
	require.Equal(t, "/org/parent/child/settings/applications", handlers.BasePathList)
	require.Equal(t, "/org/parent/child/settings/applications/oauth2", handlers.BasePathEditPrefix)
	require.NotNil(t, handlers.AuthorizeWrite)
}

func TestOrganizationOAuthSecretRotationRejectsStaleOwnerContext(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockContext(t, "POST /org/org3/settings/applications/oauth2/1/regenerate_secret")
	contexttest.LoadUser(t, ctx, 2)
	owners := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	orgRecord, err := organization.GetOrgByID(ctx, owners.OrgID)
	require.NoError(t, err)
	ctx.Org = &context.Organization{IsOwner: true, Organization: orgRecord, OrgLink: "/org/org3"}
	app, err := auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{Name: "stale-context", UserID: owners.OrgID})
	require.NoError(t, err)
	_, err = app.GenerateClientSecret(ctx)
	require.NoError(t, err)
	before := app.ClientSecret
	ctx.SetPathParam("id", strconv.FormatInt(app.ID, 10))
	replacement := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	require.NoError(t, org_service.AddTeamMember(ctx, owners, replacement))
	require.NoError(t, org_service.RemoveTeamMember(ctx, owners, ctx.Doer))

	OAuthApplicationsRegenerateSecret(ctx)
	require.Equal(t, http.StatusNotFound, ctx.WrittenStatus())
	stored, err := auth.GetOAuth2ApplicationByID(ctx, app.ID)
	require.NoError(t, err)
	require.Equal(t, before, stored.ClientSecret)
}
