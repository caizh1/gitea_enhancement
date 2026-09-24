// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package organization

import (
	"context"
	"errors"
	"time"

	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
)

func governanceGroupGrants(ctx context.Context, groupID, userID int64) ([]governance_model.Grant, error) {
	if userID <= 0 {
		return nil, nil
	}
	user, err := user_model.GetUserByID(ctx, userID)
	if user_model.IsErrUserNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !user.IsActive || user.ProhibitLogin || user.IsOrganization() || user.IsGiteaActions() {
		return nil, nil
	}
	grants, err := governance_model.GroupGrants(ctx, groupID, userID, time.Now())
	if errors.Is(err, governance_model.ErrNotFound) {
		return nil, nil
	}
	return grants, err
}

func GovernanceGroupAbilities(ctx context.Context, groupID, userID int64) (governance_model.Abilities, error) {
	grants, err := governanceGroupGrants(ctx, groupID, userID)
	if err != nil {
		return nil, err
	}
	return governance_model.EffectiveAbilities(grants), nil
}

func hasGovernanceOwner(ctx context.Context, groupID, userID int64) (bool, error) {
	grants, err := governanceGroupGrants(ctx, groupID, userID)
	return governance_model.HasOwnerGrant(grants), err
}
