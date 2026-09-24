// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package access

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/perm"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/container"

	"xorm.io/builder"
)

// repoGovernanceCandidateIDs finds possible grants; the final permission check below remains authoritative.
func repoGovernanceCandidateIDs(ctx context.Context, repo *repo_model.Repository) (container.Set[int64], error) {
	ids := make(container.Set[int64])
	now := time.Now().Unix()
	active := builder.Or(builder.Eq{"expires_unix": 0}, builder.Gt{"expires_unix": now})
	var direct []int64
	if err := db.GetEngine(ctx).Table(new(governance_model.Membership)).Distinct("user_id").
		Where("scope_type = ? AND scope_id = ?", "repository", repo.ID).And(active).Find(&direct); err != nil {
		return nil, err
	}
	ids.AddMultiple(direct...)

	roots := []int64{repo.OwnerID}
	var projectShares []*governance_model.Share
	if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repo.ID).And(active).Find(&projectShares); err != nil {
		return nil, err
	}
	for _, share := range projectShares {
		roots = append(roots, share.GroupID)
	}
	var groupIDs []int64
	for _, root := range roots {
		chain, err := governance_model.Ancestors(ctx, root)
		if errors.Is(err, governance_model.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, group := range chain {
			if group.Kind == "group" {
				groupIDs = append(groupIDs, group.ID)
			}
		}
	}
	slices.Sort(groupIDs)
	groupIDs = slices.Compact(groupIDs)
	if len(groupIDs) == 0 {
		return ids, nil
	}
	var groupShares []*governance_model.Share
	if err := db.GetEngine(ctx).Where("scope_type = ?", "group").In("scope_id", groupIDs).And(active).Find(&groupShares); err != nil {
		return nil, err
	}
	for _, share := range groupShares {
		groupIDs = append(groupIDs, share.GroupID)
	}
	slices.Sort(groupIDs)
	groupIDs = slices.Compact(groupIDs)
	var members []int64
	if err := db.GetEngine(ctx).Table(new(governance_model.Membership)).Distinct("user_id").
		Where("scope_type = ?", "group").In("scope_id", groupIDs).And(active).Find(&members); err != nil {
		return nil, err
	}
	ids.AddMultiple(members...)
	var owners []*governance_model.Namespace
	if err := db.GetEngine(ctx).In("id", groupIDs).Where("native_owner_team_id > 0").Find(&owners); err != nil {
		return nil, err
	}
	if len(owners) > 0 {
		teamIDs := make([]int64, 0, len(owners))
		for _, owner := range owners {
			teamIDs = append(teamIDs, owner.NativeOwnerTeamID)
		}
		var native []int64
		if err := db.GetEngine(ctx).Table("team_user").Distinct("uid").In("team_id", teamIDs).Find(&native); err != nil {
			return nil, err
		}
		ids.AddMultiple(native...)
	}
	return ids, nil
}

func repoCandidateUsers(ctx context.Context, repo *repo_model.Repository, ids container.Set[int64], eligible func(Permission) bool) ([]*user_model.User, error) {
	governanceIDs, err := repoGovernanceCandidateIDs(ctx, repo)
	if err != nil {
		return nil, err
	}
	ids.AddMultiple(governanceIDs.Values()...)
	if len(ids) == 0 {
		return nil, nil
	}
	users := make([]*user_model.User, 0, len(ids))
	// Query in bounded batches; candidate sources are scoped to this repository and its finite grant chain.
	allIDs := ids.Values()
	for len(allIDs) > 0 {
		batch := min(len(allIDs), db.DefaultMaxInSize)
		var fetched []*user_model.User
		if err := db.GetEngine(ctx).In("id", allIDs[:batch]).Where("is_active = ? AND prohibit_login = ?", true, false).Find(&fetched); err != nil {
			return nil, err
		}
		users = append(users, fetched...)
		allIDs = allIDs[batch:]
	}
	result := users[:0]
	for _, user := range users {
		if user.IsOrganization() {
			continue
		}
		permission, err := GetIndividualUserRepoPermission(ctx, repo, user)
		if err != nil {
			return nil, err
		}
		if eligible(permission) {
			result = append(result, user)
		}
	}
	slices.SortFunc(result, func(a, b *user_model.User) int { return strings.Compare(a.LowerName, b.LowerName) })
	return result, nil
}

// GetRepoAssignees returns current users eligible for issue or pull-request assignment.
func GetRepoAssignees(ctx context.Context, repo *repo_model.Repository) ([]*user_model.User, error) {
	native, err := repo_model.GetRepoAssignees(ctx, repo)
	if err != nil {
		return nil, err
	}
	ids := make(container.Set[int64], len(native))
	for _, user := range native {
		ids.Add(user.ID)
	}
	return repoCandidateUsers(ctx, repo, ids, func(p Permission) bool {
		return p.CanAccessAny(perm.AccessModeWrite, unit.AllRepoUnitTypes...) || p.CanRead(unit.TypePullRequests)
	})
}

// GetRepoReviewers returns current users with pull-request read access.
func GetRepoReviewers(ctx context.Context, repo *repo_model.Repository) ([]*user_model.User, error) {
	if err := repo.LoadOwner(ctx); err != nil {
		return nil, err
	}
	ids := make(container.Set[int64])
	var collaborators []int64
	if err := db.GetEngine(ctx).Table("collaboration").Distinct("user_id").
		Where("repo_id = ? AND mode >= ?", repo.ID, perm.AccessModeRead).Find(&collaborators); err != nil {
		return nil, err
	}
	ids.AddMultiple(collaborators...)
	if repo.Owner.IsOrganization() {
		teamUsers, err := organization.GetTeamUserIDsWithAccessToAnyRepoUnit(ctx, repo.OwnerID, repo.ID, perm.AccessModeRead, unit.TypePullRequests)
		if err != nil {
			return nil, err
		}
		ids.AddMultiple(teamUsers...)
	} else {
		ids.Add(repo.OwnerID)
	}
	return repoCandidateUsers(ctx, repo, ids, func(p Permission) bool { return p.CanRead(unit.TypePullRequests) })
}
