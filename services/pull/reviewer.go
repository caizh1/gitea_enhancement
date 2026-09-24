// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pull

import (
	"context"

	"gitea.dev/models/organization"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
)

// GetReviewers get all users can be requested to review:
// - Poster should not be listed
// - For collaborator, all users that have read access or higher to the repository.
// - For repository under organization, users under the teams which have read permission or higher of pull request unit
// - Owner will be listed if it's not an organization, not the poster and not in the list of reviewers
func GetReviewers(ctx context.Context, repo *repo_model.Repository, doerID, posterID int64) ([]*user_model.User, error) {
	users, err := access_model.GetRepoReviewers(ctx, repo)
	if err != nil {
		return nil, err
	}
	filtered := users[:0]
	for _, user := range users {
		if user.ID != posterID {
			filtered = append(filtered, user)
		}
	}
	return filtered, nil
}

// GetReviewerTeams get all teams can be requested to review
func GetReviewerTeams(ctx context.Context, repo *repo_model.Repository) ([]*organization.Team, error) {
	if err := repo.LoadOwner(ctx); err != nil {
		return nil, err
	}
	if !repo.Owner.IsOrganization() {
		return nil, nil
	}

	return organization.GetTeamsWithAccessToAnyRepoUnit(ctx, repo.OwnerID, repo.ID, perm.AccessModeRead, unit.TypePullRequests)
}
