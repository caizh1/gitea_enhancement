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

func GovernanceGroupAbilities(ctx context.Context, groupID, userID int64) (governance_model.Abilities, error) {
	if userID <= 0 {
		return governance_model.Abilities{}, nil
	}
	user, err := user_model.GetUserByID(ctx, userID)
	if user_model.IsErrUserNotExist(err) {
		return governance_model.Abilities{}, nil
	}
	if err != nil {
		return nil, err
	}
	if !user.IsActive || user.ProhibitLogin || user.IsOrganization() || user.IsGiteaActions() {
		return governance_model.Abilities{}, nil
	}
	grants, err := governance_model.GroupGrants(ctx, groupID, userID, time.Now())
	if errors.Is(err, governance_model.ErrNotFound) {
		return governance_model.Abilities{}, nil
	}
	return governance_model.EffectiveAbilities(grants), err
}

func hasGovernanceOwner(ctx context.Context, groupID, userID int64) (bool, error) {
	abilities, err := GovernanceGroupAbilities(ctx, groupID, userID)
	if err != nil {
		return false, err
	}
	owner, _ := governance_model.AbilitiesFor(governance_model.Owner, nil)
	for ability := range owner {
		if !abilities[ability] {
			return false, nil
		}
	}
	return true, nil
}
