// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	stdctx "context"
	"errors"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	secret_model "gitea.dev/models/secret"
	"gitea.dev/modules/log"
	"gitea.dev/modules/util"
	"gitea.dev/modules/web"
	"gitea.dev/services/context"
	"gitea.dev/services/forms"
	governance_service "gitea.dev/services/governance"
	secret_service "gitea.dev/services/secrets"
)

type inheritedSecret struct {
	Name        string
	Description string
	Source      string
	Overridden  bool
	Protected   bool
}

func visibleSecretSource(ctx stdctx.Context, viewerID int64, isAdmin bool, namespace *governance_model.Namespace) (string, bool, error) {
	if namespace.Kind == "user" {
		return namespace.FullPath, viewerID == namespace.ID || isAdmin, nil
	}
	state, err := governance_service.CheckGroupAccess(ctx, viewerID, namespace.ID, governance_model.ReadGroup)
	if errors.Is(err, governance_model.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return state.FullPath, true, nil
}

func SetSecretsContext(ctx *context.Context, ownerID, repoID int64) {
	secrets, err := db.Find[secret_model.Secret](ctx, secret_model.FindSecretsOptions{OwnerID: ownerID, RepoID: repoID})
	if err != nil {
		ctx.ServerError("FindSecrets", err)
		return
	}

	ctx.Data["Secrets"] = secrets
	ancestorOwnerID := ownerID
	if repoID != 0 {
		ancestorOwnerID = ctx.Repo.Repository.OwnerID
	}
	if ancestorOwnerID > 0 {
		chain, err := governance_model.Ancestors(ctx, ancestorOwnerID)
		if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
			ctx.ServerError("Ancestors", err)
			return
		}
		seen := make(map[string]bool, len(secrets))
		for _, secret := range secrets {
			seen[secret.Name] = true
		}
		inherited := make([]inheritedSecret, 0)
		start := 0
		if repoID == 0 {
			start = 1
		}
		if start > len(chain) {
			start = len(chain)
		}
		for _, ancestor := range chain[start:] {
			source, sourceVisible, err := visibleSecretSource(ctx, ctx.Doer.ID, ctx.Doer.IsAdmin, ancestor)
			if err != nil {
				ctx.ServerError("CheckGroupAccess", err)
				return
			}
			if !sourceVisible {
				source = string(ctx.Tr("actions.inheritance.restricted"))
			}
			entries, err := db.Find[secret_model.Secret](ctx, secret_model.FindSecretsOptions{OwnerID: ancestor.ID})
			if err != nil {
				ctx.ServerError("FindSecrets", err)
				return
			}
			for _, secret := range entries {
				entry := inheritedSecret{secret.Name, secret.Description, source, seen[secret.Name], secret.Protected && sourceVisible}
				if !sourceVisible {
					entry.Description = ""
				}
				inherited = append(inherited, entry)
				seen[secret.Name] = true
			}
		}
		ctx.Data["InheritedSecrets"] = inherited
	}
	ctx.Data["DataMaxLength"] = secret_model.SecretDataMaxLength
	ctx.Data["DescriptionMaxLength"] = secret_model.SecretDescriptionMaxLength
}

func PerformSecretsPost(ctx *context.Context, ownerID, repoID int64, redirectURL string) {
	form := web.GetForm(ctx).(*forms.AddSecretForm)

	s, _, err := secret_service.CreateOrUpdateSecret(ctx, ownerID, repoID, form.Name, util.NormalizeStringEOL(form.Data), form.Description, &form.Protected)
	if err != nil {
		log.Error("CreateOrUpdateSecret failed: %v", err)
		ctx.JSONError(ctx.Tr("secrets.save_failed"))
		return
	}

	ctx.Flash.Success(ctx.Tr("secrets.save_success", s.Name))
	ctx.JSONRedirect(redirectURL)
}

func PerformSecretsDelete(ctx *context.Context, ownerID, repoID int64, redirectURL string) {
	id := ctx.FormInt64("id")

	err := secret_service.DeleteSecretByID(ctx, ownerID, repoID, id)
	if err != nil {
		log.Error("DeleteSecretByID(%d) failed: %v", id, err)
		ctx.JSONError(ctx.Tr("secrets.deletion.failed"))
		return
	}

	ctx.Flash.Success(ctx.Tr("secrets.deletion.success"))
	ctx.JSONRedirect(redirectURL)
}
