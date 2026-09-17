// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package asymkey

import (
	"context"

	asymkey_model "gitea.dev/models/asymkey"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
)

// DeletePublicKey deletes SSH key information both in database and authorized_keys file.
func DeletePublicKey(ctx context.Context, doer *user_model.User, id int64) (err error) {
	if err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		key, err := asymkey_model.GetPublicKeyByID(ctx, id)
		if err != nil {
			return err
		}

		// Check if user has access to delete this key.
		if !doer.IsAdmin && doer.ID != key.OwnerID {
			return asymkey_model.ErrKeyAccessDenied{
				UserID: doer.ID,
				KeyID:  key.ID,
				Note:   "public",
			}
		}

		removed, err := db.DeleteByID[asymkey_model.PublicKey](ctx, id)
		if err != nil {
			return err
		}
		if removed == 0 {
			return asymkey_model.ErrKeyNotExist{ID: id}
		}
		return asymkey_model.AppendPublicKeyAudit(ctx, key, "credential.ssh_key_revoked")
	}); err != nil {
		return err
	}

	return SyncSSHKeyFiles(ctx)
}
