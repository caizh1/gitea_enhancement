// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	"context"
	"errors"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	org_model "gitea.dev/models/organization"
	user_model "gitea.dev/models/user"
	"gitea.dev/services/mailer"
)

// CreateTeamInvite make a persistent invite in db and mail it
func CreateTeamInvite(ctx context.Context, inviter *user_model.User, team *org_model.Team, uname string) error {
	var invite *org_model.TeamInvite
	var currentInviter *user_model.User
	var currentTeam *org_model.Team
	err := governance_model.WithWrite(ctx, []string{governance_model.Resource("group", team.OrgID), governance_model.Resource("team", team.ID), governance_model.Resource("user", inviter.ID)}, func(ctx context.Context) error {
		var err error
		currentTeam, err = inviteTeam(ctx, team.ID, team.OrgID)
		if err != nil {
			return err
		}
		if err := inviteGroupWritable(ctx, currentTeam.OrgID); err != nil {
			return err
		}
		currentInviter, err = inviteOwner(ctx, inviter.ID, currentTeam.OrgID)
		if err != nil {
			return err
		}
		invite, err = org_model.CreateTeamInvite(ctx, currentInviter, currentTeam, uname)
		return err
	})
	if err != nil {
		return err
	}

	return mailer.MailTeamInvite(ctx, currentInviter, currentTeam, invite)
}

// AcceptTeamInvite rechecks the original inviter and consumes the bearer invite with the membership write.
func AcceptTeamInvite(ctx context.Context, token string, invitee *user_model.User) (*org_model.Organization, *org_model.Team, error) {
	invite, err := org_model.GetInviteByToken(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	var org *org_model.Organization
	var team *org_model.Team
	err = governance_model.WithWrite(ctx, []string{governance_model.Resource("group", invite.OrgID), governance_model.Resource("team", invite.TeamID), governance_model.Resource("user", invite.InviterID), governance_model.Resource("user", invitee.ID)}, func(ctx context.Context) error {
		current, err := org_model.GetInviteByToken(ctx, token)
		if err != nil {
			return err
		}
		if current.ID != invite.ID || current.OrgID != invite.OrgID || current.TeamID != invite.TeamID || current.InviterID != invite.InviterID {
			return org_model.ErrTeamInviteNotFound{Token: token}
		}
		team, err = inviteTeam(ctx, current.TeamID, current.OrgID)
		if err != nil {
			return err
		}
		if err := inviteGroupWritable(ctx, current.OrgID); err != nil {
			return err
		}
		if _, err = inviteOwner(ctx, current.InviterID, current.OrgID); err != nil {
			return err
		}
		user, err := activeInviteUser(ctx, invitee.ID)
		if err != nil {
			return err
		}
		org, err = org_model.GetOrgByID(ctx, current.OrgID)
		if err != nil {
			return err
		}
		if err := addTeamMember(ctx, team, user); err != nil {
			return err
		}
		deleted, err := db.GetEngine(ctx).Where("id = ? AND token = ? AND team_id = ? AND org_id = ?", current.ID, token, current.TeamID, current.OrgID).Delete(new(org_model.TeamInvite))
		if err != nil {
			return err
		}
		if deleted != 1 {
			return org_model.ErrTeamInviteNotFound{Token: token}
		}
		return nil
	})
	return org, team, err
}

// RemoveTeamInvite serializes an owner's revocation with acceptance.
func RemoveTeamInvite(ctx context.Context, actor *user_model.User, team *org_model.Team, inviteID int64) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("group", team.OrgID), governance_model.Resource("team", team.ID), governance_model.Resource("user", actor.ID)}, func(ctx context.Context) error {
		currentTeam, err := inviteTeam(ctx, team.ID, team.OrgID)
		if err != nil {
			return err
		}
		if _, err := inviteOwner(ctx, actor.ID, currentTeam.OrgID); err != nil {
			return err
		}
		deleted, err := db.GetEngine(ctx).Where("id = ? AND team_id = ? AND org_id = ?", inviteID, currentTeam.ID, currentTeam.OrgID).Delete(new(org_model.TeamInvite))
		if err != nil {
			return err
		}
		if deleted != 1 {
			return governance_model.ErrNotFound
		}
		return nil
	})
}

func inviteTeam(ctx context.Context, teamID, orgID int64) (*org_model.Team, error) {
	team, err := org_model.GetTeamByID(ctx, teamID)
	if org_model.IsErrTeamNotExist(err) {
		return nil, governance_model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if team.OrgID != orgID {
		return nil, governance_model.ErrNotFound
	}
	org, err := user_model.GetUserByID(ctx, orgID)
	if user_model.IsErrUserNotExist(err) {
		return nil, governance_model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !org.IsOrganization() {
		return nil, governance_model.ErrNotFound
	}
	return team, nil
}

func inviteGroupWritable(ctx context.Context, orgID int64) error {
	chain, err := governance_model.Ancestors(ctx, orgID)
	if errors.Is(err, governance_model.ErrNotFound) {
		return nil // legacy native organizations have no governance lifecycle
	}
	if err != nil {
		return err
	}
	if chain[0].Kind != "group" {
		return governance_model.ErrNotFound
	}
	for _, group := range chain {
		if group.Archived || group.DeleteAfter != 0 {
			return governance_model.ErrConflict
		}
	}
	return nil
}

func activeInviteUser(ctx context.Context, userID int64) (*user_model.User, error) {
	user, err := user_model.GetUserByID(ctx, userID)
	if user_model.IsErrUserNotExist(err) {
		return nil, governance_model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !user.IsActive || user.ProhibitLogin || user.IsOrganization() || user.IsGiteaActions() || user.IsGhost() {
		return nil, governance_model.ErrNotFound
	}
	return user, nil
}

func inviteOwner(ctx context.Context, userID, orgID int64) (*user_model.User, error) {
	user, err := activeInviteUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.IsAdmin {
		return user, nil
	}
	owner, err := org_model.IsOrganizationOwner(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	if !owner {
		return nil, governance_model.ErrNotFound
	}
	return user, nil
}
