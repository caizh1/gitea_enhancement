// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	"context"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	secret_model "gitea.dev/models/secret"
)

func CreateOrUpdateSecret(ctx context.Context, ownerID, repoID int64, name, data, description string, protected ...*bool) (*secret_model.Secret, bool, error) {
	if err := ValidateName(name); err != nil {
		return nil, false, err
	}

	s, err := db.Find[secret_model.Secret](ctx, secret_model.FindSecretsOptions{
		OwnerID: ownerID,
		RepoID:  repoID,
		Name:    name,
	})
	if err != nil {
		return nil, false, err
	}

	if len(s) == 0 {
		isProtected := len(protected) > 0 && protected[0] != nil && *protected[0]
		s, err := secret_model.InsertEncryptedSecret(ctx, ownerID, repoID, name, data, description, isProtected)
		if err != nil {
			return nil, false, err
		}
		return s, true, nil
	}

	if err := secret_model.UpdateSecret(ctx, s[0].ID, data, description, protected...); err != nil {
		return nil, false, err
	}
	if len(protected) > 0 && protected[0] != nil {
		s[0].Protected = *protected[0]
	}

	return s[0], false, nil
}

func DeleteSecretByID(ctx context.Context, ownerID, repoID, secretID int64) error {
	s, err := db.Find[secret_model.Secret](ctx, secret_model.FindSecretsOptions{
		OwnerID:  ownerID,
		RepoID:   repoID,
		SecretID: secretID,
	})
	if err != nil {
		return err
	}
	if len(s) != 1 {
		return secret_model.ErrSecretNotFound{}
	}

	return deleteSecret(ctx, s[0])
}

func DeleteSecretByName(ctx context.Context, ownerID, repoID int64, name string) error {
	s, err := db.Find[secret_model.Secret](ctx, secret_model.FindSecretsOptions{
		OwnerID: ownerID,
		RepoID:  repoID,
		Name:    name,
	})
	if err != nil {
		return err
	}
	if len(s) != 1 {
		return secret_model.ErrSecretNotFound{}
	}

	return deleteSecret(ctx, s[0])
}

func deleteSecret(ctx context.Context, s *secret_model.Secret) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		fresh, has, err := db.GetByID[secret_model.Secret](ctx, s.ID)
		if err != nil {
			return err
		}
		if !has {
			return secret_model.ErrSecretNotFound{Name: s.Name}
		}
		if count, err := db.DeleteByID[secret_model.Secret](ctx, fresh.ID); err != nil {
			return err
		} else if count != 1 {
			return secret_model.ErrSecretNotFound{Name: fresh.Name}
		}
		return actions_model.AppendConfigurationAudit(ctx, fresh.OwnerID, fresh.RepoID, "actions.secret_deleted", "actions_secret", fresh.ID, fresh.Name, map[string]any{"name": fresh.Name, "value_configured": fresh.Data != "", "description_configured": fresh.Description != ""})
	})
}
