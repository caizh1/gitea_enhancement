// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	"context"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	secret_model "gitea.dev/models/secret"
	governance_service "gitea.dev/services/governance"
)

func CreateOrUpdateSecret(ctx context.Context, ownerID, repoID int64, name, data, description string, protected ...*bool) (*secret_model.Secret, bool, error) {
	if err := ValidateName(name); err != nil {
		return nil, false, err
	}

	var result *secret_model.Secret
	created := false
	err := governance_service.WithConfigurationWrite(ctx, governance_model.AuditActor(ctx), ownerID, repoID, func(ctx context.Context) error {
		s, err := db.Find[secret_model.Secret](ctx, secret_model.FindSecretsOptions{OwnerID: ownerID, RepoID: repoID, Name: name})
		if err != nil {
			return err
		}
		if len(s) == 0 {
			isProtected := len(protected) > 0 && protected[0] != nil && *protected[0]
			result, err = secret_model.InsertEncryptedSecret(ctx, ownerID, repoID, name, data, description, isProtected)
			created = err == nil
			return err
		}
		if err := secret_model.UpdateSecret(ctx, s[0].ID, data, description, protected...); err != nil {
			return err
		}
		if len(protected) > 0 && protected[0] != nil {
			s[0].Protected = *protected[0]
		}
		result = s[0]
		return nil
	})

	return result, created, err
}

func DeleteSecretByID(ctx context.Context, ownerID, repoID, secretID int64) error {
	return deleteSecret(ctx, ownerID, repoID, secret_model.FindSecretsOptions{OwnerID: ownerID, RepoID: repoID, SecretID: secretID})
}

func DeleteSecretByName(ctx context.Context, ownerID, repoID int64, name string) error {
	return deleteSecret(ctx, ownerID, repoID, secret_model.FindSecretsOptions{OwnerID: ownerID, RepoID: repoID, Name: name})
}

func deleteSecret(ctx context.Context, ownerID, repoID int64, options secret_model.FindSecretsOptions) error {
	return governance_service.WithConfigurationWrite(ctx, governance_model.AuditActor(ctx), ownerID, repoID, func(ctx context.Context) error {
		s, err := db.Find[secret_model.Secret](ctx, options)
		if err != nil {
			return err
		}
		if len(s) != 1 {
			return secret_model.ErrSecretNotFound{}
		}
		fresh, has, err := db.GetByID[secret_model.Secret](ctx, s[0].ID)
		if err != nil {
			return err
		}
		if !has {
			return secret_model.ErrSecretNotFound{Name: s[0].Name}
		}
		if count, err := db.DeleteByID[secret_model.Secret](ctx, fresh.ID); err != nil {
			return err
		} else if count != 1 {
			return secret_model.ErrSecretNotFound{Name: fresh.Name}
		}
		return actions_model.AppendConfigurationAudit(ctx, fresh.OwnerID, fresh.RepoID, "actions.secret_deleted", "actions_secret", fresh.ID, fresh.Name, map[string]any{"name": fresh.Name, "value_configured": fresh.Data != "", "description_configured": fresh.Description != ""})
	})
}
