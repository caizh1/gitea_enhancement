// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package org

import (
	"context"

	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
)

// AuthorizeOAuthApplicationWrite checks the current owner and group state at the write.
func AuthorizeOAuthApplicationWrite(ctx context.Context, actorID, orgID int64, revoke bool) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("group", orgID), governance_model.Resource("user", actorID)}, func(ctx context.Context) error {
		org, err := user_model.GetUserByID(ctx, orgID)
		if user_model.IsErrUserNotExist(err) {
			return governance_model.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !org.IsOrganization() {
			return governance_model.ErrNotFound
		}
		if _, err := inviteOwner(ctx, actorID, orgID); err != nil {
			return err
		}
		if !revoke {
			return inviteGroupWritable(ctx, orgID)
		}
		return nil
	})
}
