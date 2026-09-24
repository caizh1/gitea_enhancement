// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package admin

import (
	stdctx "context"
	"net/http"

	"gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/templates"
	user_setting "gitea.dev/routers/web/user/setting"
	"gitea.dev/services/context"
)

var (
	tplSettingsApplications          templates.TplName = "admin/applications/list"
	tplSettingsOauth2ApplicationEdit templates.TplName = "admin/applications/oauth2_edit"
)

func newOAuth2CommonHandlers(ctx *context.Context) *user_setting.OAuth2CommonHandlers {
	actorID := ctx.Doer.ID
	return &user_setting.OAuth2CommonHandlers{
		OwnerID:            0,
		BasePathList:       setting.AppSubURL + "/-/admin/applications",
		BasePathEditPrefix: setting.AppSubURL + "/-/admin/applications/oauth2",
		TplAppEdit:         tplSettingsOauth2ApplicationEdit,
		AuthorizeWrite: func(writeCtx stdctx.Context, _ bool) error {
			actor, err := user_model.GetUserByID(writeCtx, actorID)
			if user_model.IsErrUserNotExist(err) {
				return governance_model.ErrNotFound
			}
			if err != nil {
				return err
			}
			if !actor.IsActive || actor.ProhibitLogin || !actor.IsAdmin || actor.IsOrganization() || actor.IsGhost() || actor.IsGiteaActions() {
				return governance_model.ErrNotFound
			}
			return nil
		},
	}
}

// Applications render org applications page (for org, at the moment, there are only OAuth2 applications)
func Applications(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings.applications")
	ctx.Data["PageIsAdminApplications"] = true

	apps, err := db.Find[auth.OAuth2Application](ctx, auth.FindOAuth2ApplicationsOptions{
		IsGlobal: true,
	})
	if err != nil {
		ctx.ServerError("GetOAuth2ApplicationsByUserID", err)
		return
	}
	ctx.Data["Applications"] = apps
	ctx.Data["BuiltinApplications"] = auth.BuiltinApplications()
	ctx.HTML(http.StatusOK, tplSettingsApplications)
}

// ApplicationsPost response for adding an oauth2 application
func ApplicationsPost(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings.applications")
	ctx.Data["PageIsAdminApplications"] = true

	oa := newOAuth2CommonHandlers(ctx)
	oa.AddApp(ctx)
}

// EditApplication displays the given application
func EditApplication(ctx *context.Context) {
	ctx.Data["PageIsAdminApplications"] = true

	oa := newOAuth2CommonHandlers(ctx)
	oa.EditShow(ctx)
}

// EditApplicationPost response for editing oauth2 application
func EditApplicationPost(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings.applications")
	ctx.Data["PageIsAdminApplications"] = true

	oa := newOAuth2CommonHandlers(ctx)
	oa.EditSave(ctx)
}

// ApplicationsRegenerateSecret handles the post request for regenerating the secret
func ApplicationsRegenerateSecret(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("settings_title")
	ctx.Data["PageIsAdminApplications"] = true

	oa := newOAuth2CommonHandlers(ctx)
	oa.RegenerateSecret(ctx)
}

// DeleteApplication deletes the given oauth2 application
func DeleteApplication(ctx *context.Context) {
	oa := newOAuth2CommonHandlers(ctx)
	oa.DeleteApp(ctx)
}

// TODO: revokes the grant with the given id
