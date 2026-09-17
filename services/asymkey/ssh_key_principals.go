// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package asymkey

import (
	"context"
	"fmt"

	asymkey_model "gitea.dev/models/asymkey"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/perm"
	user_model "gitea.dev/models/user"
)

// AddPrincipalKey adds new principal to database and authorized_principals file.
func AddPrincipalKey(ctx context.Context, ownerID int64, content string, authSourceID int64) (*asymkey_model.PublicKey, error) {
	key := &asymkey_model.PublicKey{
		OwnerID:       ownerID,
		Name:          content,
		Content:       content,
		Mode:          perm.AccessModeWrite,
		Type:          asymkey_model.KeyTypePrincipal,
		LoginSourceID: authSourceID,
	}

	if err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		if _, err := user_model.GetUserByID(ctx, ownerID); err != nil {
			return err
		}
		// Principals cannot be duplicated.
		has, err := db.GetEngine(ctx).
			Where("content = ? AND type = ?", content, asymkey_model.KeyTypePrincipal).
			Get(new(asymkey_model.PublicKey))
		if err != nil {
			return err
		} else if has {
			return asymkey_model.ErrKeyAlreadyExist{
				Content: content,
			}
		}

		if err = db.Insert(ctx, key); err != nil {
			return fmt.Errorf("addKey: %w", err)
		}
		return asymkey_model.AppendPublicKeyAudit(ctx, key, "credential.ssh_key_created")
	}); err != nil {
		return nil, err
	}

	return key, SyncSSHKeyFiles(ctx)
}
