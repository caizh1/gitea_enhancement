// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"

	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
)

// AuthorizePersonalOAuthApplicationWrite checks the current identities in the final write transaction.
func AuthorizePersonalOAuthApplicationWrite(ctx context.Context, ownerID, sudoAdminID int64) error {
	owner, err := user_model.GetUserByID(ctx, ownerID)
	if err != nil {
		if user_model.IsErrUserNotExist(err) {
			return governance_model.ErrNotFound
		}
		return err
	}
	if !owner.IsActive || owner.ProhibitLogin || owner.IsOrganization() || owner.IsGhost() || owner.IsGiteaActions() {
		return governance_model.ErrNotFound
	}
	if sudoAdminID == 0 {
		return nil
	}
	admin, err := user_model.GetUserByID(ctx, sudoAdminID)
	if err != nil {
		if user_model.IsErrUserNotExist(err) {
			return governance_model.ErrNotFound
		}
		return err
	}
	if !admin.IsActive || admin.ProhibitLogin || !admin.IsAdmin || admin.IsOrganization() || admin.IsGhost() || admin.IsGiteaActions() {
		return governance_model.ErrNotFound
	}
	return nil
}
