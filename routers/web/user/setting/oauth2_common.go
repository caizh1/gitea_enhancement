// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	stdctx "context"
	"errors"
	"fmt"
	"net/http"

	"gitea.dev/models/auth"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/templates"
	"gitea.dev/modules/util"
	"gitea.dev/modules/web"
	shared_user "gitea.dev/routers/web/shared/user"
	"gitea.dev/services/context"
	"gitea.dev/services/forms"
)

type OAuth2CommonHandlers struct {
	OwnerID            int64             // 0 for instance-wide, otherwise OrgID or UserID
	BasePathList       string            // the base URL for the application list page, eg: "/user/setting/applications"
	BasePathEditPrefix string            // the base URL for the application edit page, will be appended with app id, eg: "/user/setting/applications/oauth2"
	TplAppEdit         templates.TplName // the template for the application edit page
	AuthorizeWrite     func(stdctx.Context, bool) error
}

func (oa *OAuth2CommonHandlers) authorizeWrite(revoke bool) func(stdctx.Context) error {
	if oa.AuthorizeWrite == nil {
		return nil
	}
	return func(ctx stdctx.Context) error { return oa.AuthorizeWrite(ctx, revoke) }
}

func (oa *OAuth2CommonHandlers) mutationError(ctx *context.Context, name string, err error, jsonResponse bool) {
	switch {
	case auth.IsErrOAuthApplicationNotFound(err), errors.Is(err, governance_model.ErrNotFound):
		if jsonResponse {
			ctx.JSONErrorNotFound()
		} else {
			ctx.NotFound(nil)
		}
	case errors.Is(err, governance_model.ErrConflict):
		if jsonResponse {
			ctx.JSON(http.StatusConflict, map[string]any{"errorMessage": ctx.Tr("settings.oauth2_application_write_busy"), "renderFormat": "text"})
		} else {
			ctx.Flash.Error(ctx.Tr("settings.oauth2_application_group_unavailable"))
			ctx.Redirect(oa.BasePathList)
		}
	default:
		ctx.ServerError(name, err)
	}
}

func (oa *OAuth2CommonHandlers) renderEditPage(ctx *context.Context) {
	app := ctx.Data["App"].(*auth.OAuth2Application)
	ctx.Data["FormActionPath"] = fmt.Sprintf("%s/%d", oa.BasePathEditPrefix, app.ID)

	if ctx.ContextUser != nil && ctx.ContextUser.IsOrganization() {
		if _, err := shared_user.RenderUserOrgHeader(ctx); err != nil {
			ctx.ServerError("RenderUserOrgHeader", err)
			return
		}
	}

	ctx.HTML(http.StatusOK, oa.TplAppEdit)
}

// AddApp adds an oauth2 application
func (oa *OAuth2CommonHandlers) AddApp(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.EditOAuth2ApplicationForm)
	if ctx.HasError() {
		ctx.Flash.Error(ctx.GetErrMsg())
		// go to the application list page
		ctx.Redirect(oa.BasePathList)
		return
	}

	clientSecret, secretHash, err := auth.NewOAuth2ClientSecret()
	if err != nil {
		ctx.ServerError("NewOAuth2ClientSecret", err)
		return
	}
	app, err := auth.CreateOAuth2Application(ctx, auth.CreateOAuth2ApplicationOptions{
		Name:                       form.Name,
		RedirectURIs:               util.SplitTrimSpace(form.RedirectURIs, "\n"),
		UserID:                     oa.OwnerID,
		ConfidentialClient:         form.ConfidentialClient,
		SkipSecondaryAuthorization: form.SkipSecondaryAuthorization,
		ClientSecretHash:           secretHash,
		Authorize:                  oa.authorizeWrite(false),
	})
	if err != nil {
		oa.mutationError(ctx, "CreateOAuth2Application", err, false)
		return
	}

	// render the edit page with secret
	ctx.Flash.Success(ctx.Tr("settings.create_oauth2_application_success"), true)
	ctx.Data["App"] = app
	ctx.Data["ClientSecret"] = clientSecret

	oa.renderEditPage(ctx)
}

// EditShow displays the given application
func (oa *OAuth2CommonHandlers) EditShow(ctx *context.Context) {
	app, err := auth.GetOAuth2ApplicationByID(ctx, ctx.PathParamInt64("id"))
	if err != nil {
		if auth.IsErrOAuthApplicationNotFound(err) {
			ctx.NotFound(err)
			return
		}
		ctx.ServerError("GetOAuth2ApplicationByID", err)
		return
	}
	if app.UID != oa.OwnerID {
		ctx.NotFound(nil)
		return
	}
	ctx.Data["App"] = app
	oa.renderEditPage(ctx)
}

// EditSave saves the oauth2 application
func (oa *OAuth2CommonHandlers) EditSave(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.EditOAuth2ApplicationForm)

	if ctx.HasError() {
		app, err := auth.GetOAuth2ApplicationByID(ctx, ctx.PathParamInt64("id"))
		if err != nil {
			if auth.IsErrOAuthApplicationNotFound(err) {
				ctx.NotFound(err)
				return
			}
			ctx.ServerError("GetOAuth2ApplicationByID", err)
			return
		}
		if app.UID != oa.OwnerID {
			ctx.NotFound(nil)
			return
		}
		ctx.Data["App"] = app

		oa.renderEditPage(ctx)
		return
	}

	var err error
	if ctx.Data["App"], err = auth.UpdateOAuth2Application(ctx, auth.UpdateOAuth2ApplicationOptions{
		ID:                         ctx.PathParamInt64("id"),
		Name:                       form.Name,
		RedirectURIs:               util.SplitTrimSpace(form.RedirectURIs, "\n"),
		UserID:                     oa.OwnerID,
		ConfidentialClient:         form.ConfidentialClient,
		SkipSecondaryAuthorization: form.SkipSecondaryAuthorization,
		Authorize:                  oa.authorizeWrite(false),
	}); err != nil {
		oa.mutationError(ctx, "UpdateOAuth2Application", err, false)
		return
	}
	ctx.Flash.Success(ctx.Tr("settings.update_oauth2_application_success"))
	ctx.Redirect(oa.BasePathList)
}

// RegenerateSecret regenerates the secret
func (oa *OAuth2CommonHandlers) RegenerateSecret(ctx *context.Context) {
	app, err := auth.GetOAuth2ApplicationByID(ctx, ctx.PathParamInt64("id"))
	if err != nil {
		if auth.IsErrOAuthApplicationNotFound(err) {
			ctx.NotFound(err)
			return
		}
		ctx.ServerError("GetOAuth2ApplicationByID", err)
		return
	}
	if app.UID != oa.OwnerID {
		ctx.NotFound(nil)
		return
	}
	ctx.Data["App"] = app
	ctx.Data["ClientSecret"], err = app.GenerateClientSecret(ctx, oa.authorizeWrite(false))
	if err != nil {
		oa.mutationError(ctx, "GenerateClientSecret", err, false)
		return
	}
	ctx.Flash.Success(ctx.Tr("settings.update_oauth2_application_success"), true)
	oa.renderEditPage(ctx)
}

// DeleteApp deletes the given oauth2 application
func (oa *OAuth2CommonHandlers) DeleteApp(ctx *context.Context) {
	if err := auth.DeleteOAuth2Application(ctx, ctx.PathParamInt64("id"), oa.OwnerID, oa.authorizeWrite(true)); err != nil {
		oa.mutationError(ctx, "DeleteOAuth2Application", err, true)
		return
	}

	ctx.Flash.Success(ctx.Tr("settings.remove_oauth2_application_success"))
	ctx.JSONRedirect(oa.BasePathList)
}

// RevokeGrant revokes the grant
func (oa *OAuth2CommonHandlers) RevokeGrant(ctx *context.Context) {
	if err := auth.RevokeOAuth2Grant(ctx, ctx.PathParamInt64("grantId"), oa.OwnerID); err != nil {
		ctx.ServerError("RevokeOAuth2Grant", err)
		return
	}

	ctx.Flash.Success(ctx.Tr("settings.revoke_oauth2_grant_success"))
	ctx.JSONRedirect(oa.BasePathList)
}
