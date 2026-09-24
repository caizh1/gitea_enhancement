// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
)

// ChangeTeamRepositoryAsOwner is for the organization team page.
func ChangeTeamRepositoryAsOwner(ctx context.Context, actor *user_model.User, team *organization.Team, repoID int64, add bool) error {
	return changeTeamRepositoryAsActor(ctx, actor, team, repoID, add, true, false)
}

// ChangeAllTeamRepositoriesAsOwner is for the organization team page.
func ChangeAllTeamRepositoriesAsOwner(ctx context.Context, actor *user_model.User, team *organization.Team, add bool) error {
	return changeTeamRepositoryAsActor(ctx, actor, team, 0, add, true, true)
}

// ChangeTeamRepositoryAsActor preserves repository-admin delegation with a final authorization check.
func ChangeTeamRepositoryAsActor(ctx context.Context, actor *user_model.User, team *organization.Team, repoID int64, add bool) error {
	return changeTeamRepositoryAsActor(ctx, actor, team, repoID, add, false, false)
}

func changeTeamRepositoryAsActor(ctx context.Context, actor *user_model.User, team *organization.Team, repoID int64, add, ownerOnly, all bool) error {
	resources := []string{governance_model.Resource("group", team.OrgID), governance_model.Resource("team", team.ID), governance_model.Resource("user", actor.ID)}
	if !all {
		resources = append(resources, governance_model.Resource("repository", repoID))
	}
	return governance_model.WithWrite(ctx, resources, func(ctx context.Context) error {
		currentTeam, err := organization.GetTeamByID(ctx, team.ID)
		if organization.IsErrTeamNotExist(err) {
			return governance_model.ErrNotFound
		}
		if err != nil {
			return err
		}
		if currentTeam.OrgID != team.OrgID {
			return governance_model.ErrNotFound
		}
		org, err := organization.GetOrgByID(ctx, currentTeam.OrgID)
		if user_model.IsErrUserNotExist(err) {
			return governance_model.ErrNotFound
		}
		if err != nil {
			return err
		}
		currentActor, err := user_model.GetUserByID(ctx, actor.ID)
		if user_model.IsErrUserNotExist(err) {
			return governance_model.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !currentActor.IsActive || currentActor.ProhibitLogin || currentActor.IsOrganization() || currentActor.IsGiteaActions() || currentActor.IsGhost() {
			return governance_model.ErrNotFound
		}
		if ownerOnly {
			owner, err := organization.IsOrganizationOwner(ctx, org.ID, currentActor.ID)
			if err != nil {
				return err
			}
			if !owner && !currentActor.IsAdmin {
				return governance_model.ErrNotFound
			}
		} else {
			allowed, err := org.CanChangeRepoTeamAccess(ctx, currentActor)
			if err != nil {
				return err
			}
			if !allowed {
				return governance_model.ErrForbidden
			}
		}
		if add {
			chain, err := governance_model.Ancestors(ctx, org.ID)
			if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
				return err
			}
			for _, group := range chain {
				if group.Archived || group.DeleteAfter != 0 {
					return governance_model.ErrConflict
				}
			}
		}
		if all {
			if add {
				return AddAllRepositoriesToTeam(ctx, currentTeam)
			}
			if err := currentTeam.LoadMembers(ctx); err != nil {
				return err
			}
			return RemoveAllRepositoriesFromTeam(ctx, currentTeam)
		}
		repo, err := repo_model.GetRepositoryByID(ctx, repoID)
		if repo_model.IsErrRepoNotExist(err) {
			return governance_model.ErrNotFound
		}
		if err != nil {
			return err
		}
		if repo.OwnerID != currentTeam.OrgID {
			return governance_model.ErrNotFound
		}
		if !ownerOnly {
			admin, err := access_model.IsUserRepoAdmin(ctx, repo, currentActor)
			if err != nil {
				return err
			}
			if !admin {
				return governance_model.ErrForbidden
			}
		}
		if add {
			return TeamAddRepository(ctx, currentTeam, repo)
		}
		return RemoveRepositoryFromTeam(ctx, currentTeam, repo.ID)
	})
}
