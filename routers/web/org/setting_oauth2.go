// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	stdctx "context"
	"net/http"

	"gitea.dev/models/auth"
	"gitea.dev/models/db"
	"gitea.dev/modules/templates"
	shared_user "gitea.dev/routers/web/shared/user"
	user_setting "gitea.dev/routers/web/user/setting"
	"gitea.dev/services/context"
	org_service "gitea.dev/services/org"
)

const (
	tplSettingsApplications         templates.TplName = "org/settings/applications"
	tplSettingsOAuthApplicationEdit templates.TplName = "org/settings/applications_oauth2_edit"
)

func newOAuth2CommonHandlers(ctx *context.Context) *user_setting.OAuth2CommonHandlers {
	actorID, orgID := ctx.Doer.ID, ctx.Org.Organization.ID
	return &user_setting.OAuth2CommonHandlers{
		OwnerID:            orgID,
		BasePathList:       ctx.Org.OrgLink + "/settings/applications",
		BasePathEditPrefix: ctx.Org.OrgLink + "/settings/applications/oauth2",
		TplAppEdit:         tplSettingsOAuthApplicationEdit,
		AuthorizeWrite: func(writeCtx stdctx.Context, revoke bool) error {
			return org_service.AuthorizeOAuthApplicationWrite(writeCtx, actorID, orgID, revoke)
		},
	}
}

// Applications render org applications page (for org, at the moment, there are only OAuth2 applications)
func Applications(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings.applications")
	ctx.Data["PageIsOrgSettings"] = true
	ctx.Data["PageIsSettingsApplications"] = true

	apps, err := db.Find[auth.OAuth2Application](ctx, auth.FindOAuth2ApplicationsOptions{
		OwnerID: ctx.Org.Organization.ID,
	})
	if err != nil {
		ctx.ServerError("GetOAuth2ApplicationsByUserID", err)
		return
	}
	ctx.Data["Applications"] = apps

	if _, err := shared_user.RenderUserOrgHeader(ctx); err != nil {
		ctx.ServerError("RenderUserOrgHeader", err)
		return
	}

	ctx.HTML(http.StatusOK, tplSettingsApplications)
}

// OAuthApplicationsPost response for adding an oauth2 application
func OAuthApplicationsPost(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings.applications")
	ctx.Data["PageIsOrgSettings"] = true
	ctx.Data["PageIsSettingsApplications"] = true

	oa := newOAuth2CommonHandlers(ctx)
	oa.AddApp(ctx)
}

// OAuth2ApplicationShow displays the given application
func OAuth2ApplicationShow(ctx *context.Context) {
	ctx.Data["PageIsOrgSettings"] = true
	ctx.Data["PageIsSettingsApplications"] = true

	oa := newOAuth2CommonHandlers(ctx)
	oa.EditShow(ctx)
}

// OAuth2ApplicationEdit response for editing oauth2 application
func OAuth2ApplicationEdit(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings.applications")
	ctx.Data["PageIsOrgSettings"] = true
	ctx.Data["PageIsSettingsApplications"] = true

	oa := newOAuth2CommonHandlers(ctx)
	oa.EditSave(ctx)
}

// OAuthApplicationsRegenerateSecret handles the post request for regenerating the secret
func OAuthApplicationsRegenerateSecret(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings_title")
	ctx.Data["PageIsOrgSettings"] = true
	ctx.Data["PageIsSettingsApplications"] = true

	oa := newOAuth2CommonHandlers(ctx)
	oa.RegenerateSecret(ctx)
}

// DeleteOAuth2Application deletes the given oauth2 application
func DeleteOAuth2Application(ctx *context.Context) {
	oa := newOAuth2CommonHandlers(ctx)
	oa.DeleteApp(ctx)
}

// TODO: revokes the grant with the given id
