// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package org

import (
	"context"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	user_model "gitea.dev/models/user"
)

// NewTeamAsOwner rechecks the current owner before creating a native team.
func NewTeamAsOwner(ctx context.Context, actor *user_model.User, team *organization.Team) error {
	resources := []string{governance_model.Resource("group", team.OrgID), governance_model.Resource("user", actor.ID)}
	return governance_model.WithWrite(ctx, resources, func(ctx context.Context) error {
		org, err := user_model.GetUserByID(ctx, team.OrgID)
		if user_model.IsErrUserNotExist(err) {
			return governance_model.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !org.IsOrganization() {
			return governance_model.ErrNotFound
		}
		if _, err := inviteOwner(ctx, actor.ID, team.OrgID); err != nil {
			return err
		}
		if err := inviteGroupWritable(ctx, team.OrgID); err != nil {
			return err
		}
		return NewTeam(ctx, team)
	})
}

// UpdateTeamAsOwner rechecks the current team and owner before changing native privileges.
func UpdateTeamAsOwner(ctx context.Context, actor *user_model.User, team *organization.Team, authChanged, includeAllChanged bool) error {
	resources := []string{governance_model.Resource("group", team.OrgID), governance_model.Resource("team", team.ID), governance_model.Resource("user", actor.ID)}
	return governance_model.WithWrite(ctx, resources, func(ctx context.Context) error {
		if _, err := inviteTeam(ctx, team.ID, team.OrgID); err != nil {
			return err
		}
		if _, err := inviteOwner(ctx, actor.ID, team.OrgID); err != nil {
			return err
		}
		if err := inviteGroupWritable(ctx, team.OrgID); err != nil {
			return err
		}
		return UpdateTeam(ctx, team, authChanged, includeAllChanged)
	})
}

// DeleteTeamAsOwner permits current owners to revoke native team access.
func DeleteTeamAsOwner(ctx context.Context, actor *user_model.User, team *organization.Team) error {
	resources := []string{governance_model.Resource("group", team.OrgID), governance_model.Resource("team", team.ID), governance_model.Resource("user", actor.ID)}
	return governance_model.WithWrite(ctx, resources, func(ctx context.Context) error {
		current, err := inviteTeam(ctx, team.ID, team.OrgID)
		if err != nil {
			return err
		}
		if _, err := inviteOwner(ctx, actor.ID, current.OrgID); err != nil {
			return err
		}
		return DeleteTeam(ctx, current)
	})
}

// AddTeamMemberAsOwner rechecks the requesting owner at the membership write.
func AddTeamMemberAsOwner(ctx context.Context, actor *user_model.User, team *organization.Team, target *user_model.User) error {
	resources := []string{governance_model.Resource("group", team.OrgID), governance_model.Resource("team", team.ID), governance_model.Resource("user", actor.ID), governance_model.Resource("user", target.ID)}
	return governance_model.WithWrite(ctx, resources, func(ctx context.Context) error {
		current, err := inviteTeam(ctx, team.ID, team.OrgID)
		if err != nil {
			return err
		}
		if _, err := inviteOwner(ctx, actor.ID, current.OrgID); err != nil {
			return err
		}
		if err := inviteGroupWritable(ctx, current.OrgID); err != nil {
			return err
		}
		user, err := activeInviteUser(ctx, target.ID)
		if err != nil {
			return err
		}
		return addTeamMember(ctx, current, user)
	})
}

// RemoveTeamMemberAsOwner keeps revocation available while the group is archived.
func RemoveTeamMemberAsOwner(ctx context.Context, actor *user_model.User, team *organization.Team, target *user_model.User) error {
	resources := []string{governance_model.Resource("group", team.OrgID), governance_model.Resource("team", team.ID), governance_model.Resource("user", actor.ID), governance_model.Resource("user", target.ID)}
	return governance_model.WithWrite(ctx, resources, func(ctx context.Context) error {
		current, err := inviteTeam(ctx, team.ID, team.OrgID)
		if err != nil {
			return err
		}
		if _, err := inviteOwner(ctx, actor.ID, current.OrgID); err != nil {
			return err
		}
		user, err := user_model.GetUserByID(ctx, target.ID)
		if user_model.IsErrUserNotExist(err) {
			return governance_model.ErrNotFound
		}
		if err != nil {
			return err
		}
		return db.WithTx(ctx, func(ctx context.Context) error {
			return removeTeamMember(ctx, current, user)
		})
	})
}
