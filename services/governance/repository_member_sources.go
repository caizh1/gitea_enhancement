// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"slices"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"

	"xorm.io/builder"
)

type NativeMemberSource struct {
	Kind   string            `json:"kind"`
	TeamID int64             `json:"team_id,omitempty"`
	Access string            `json:"access,omitempty"`
	Units  map[string]string `json:"units,omitempty"`
}

// repositoryMemberGroups 与 RepositoryGrants 使用相同的有限展开规则，不递归共享扩权。
func repositoryMemberGroups(ctx context.Context, repoID, ownerID int64, now time.Time) ([]int64, error) {
	roots := []int64{ownerID}
	var projectShares []*governance_model.Share
	active := builder.Or(builder.Eq{"expires_unix": 0}, builder.Gt{"expires_unix": now.Unix()})
	if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repoID).And(active).Find(&projectShares); err != nil {
		return nil, err
	}
	for _, share := range projectShares {
		roots = append(roots, share.GroupID)
	}
	var chainIDs []int64
	for _, root := range roots {
		chain, err := governance_model.Ancestors(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, group := range chain {
			if group.Kind == "group" {
				chainIDs = append(chainIDs, group.ID)
			}
		}
	}
	slices.Sort(chainIDs)
	chainIDs = slices.Compact(chainIDs)
	ids := slices.Clone(chainIDs)
	if len(chainIDs) > 0 {
		var groupShares []*governance_model.Share
		if err := db.GetEngine(ctx).Where("scope_type = ?", "group").In("scope_id", chainIDs).And(active).Find(&groupShares); err != nil {
			return nil, err
		}
		for _, share := range groupShares {
			ids = append(ids, share.GroupID)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

func repositoryMemberCandidates(ctx context.Context, repo *repo_model.Repository, groups, teamIDs []int64, after int64, now time.Time) ([]int64, error) {
	var ids []int64
	condition := builder.Or(builder.And(builder.Eq{"scope_type": "repository", "scope_id": repo.ID}), builder.And(builder.Eq{"scope_type": "group"}, builder.In("scope_id", groups), builder.Or(builder.Eq{"expires_unix": 0}, builder.Gt{"expires_unix": now.Unix()})))
	if err := db.GetEngine(ctx).Table(new(governance_model.Membership)).Distinct("user_id").Where(condition).And("user_id > ?", after).Asc("user_id").Limit(101).Find(&ids); err != nil {
		return nil, err
	}
	var collaborators []int64
	if err := db.GetEngine(ctx).Table(new(repo_model.Collaboration)).Distinct("user_id").Where("repo_id = ? AND user_id > ?", repo.ID, after).Asc("user_id").Limit(101).Find(&collaborators); err != nil {
		return nil, err
	}
	ids = append(ids, collaborators...)
	if len(teamIDs) > 0 {
		var members []int64
		if err := db.GetEngine(ctx).Table("team_user").Distinct("uid").In("team_id", teamIDs).Where("uid > ?", after).Asc("uid").Limit(101).Find(&members); err != nil {
			return nil, err
		}
		ids = append(ids, members...)
	}
	if !repo.Owner.IsOrganization() && repo.OwnerID > after {
		ids = append(ids, repo.OwnerID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) > 101 {
		ids = ids[:101]
	}
	return ids, nil
}

// ListRepositoryAllMembers 不把公开项目的所有可见用户当成成员。
func ListRepositoryAllMembers(ctx context.Context, viewerID, repoID, afterID int64) (*RepositoryMembersState, error) {
	if afterID < 0 {
		return nil, governance_model.ErrInvalid
	}
	repo, canManage, err := repositoryMemberViewer(ctx, viewerID, repoID)
	if err != nil {
		return nil, err
	}
	return listRepositoryAllMembers(ctx, repo, canManage, afterID)
}

func listRepositoryAllMembers(ctx context.Context, repo *repo_model.Repository, canManage bool, afterID int64) (*RepositoryMembersState, error) {
	repoID := repo.ID
	if err := repo.LoadOwner(ctx); err != nil {
		return nil, err
	}
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		return nil, err
	}
	state := &RepositoryMembersState{CanManage: canManage, FullPath: repo.FullPath(), Revision: chain[0].Revision, Archived: repo.IsArchived, AllSources: true, Members: []*GroupMemberState{}}
	for _, group := range chain {
		state.Archived = state.Archived || group.Archived || group.DeleteAfter != 0
	}
	now := time.Now()
	groups, err := repositoryMemberGroups(ctx, repoID, repo.OwnerID, now)
	if err != nil {
		return nil, err
	}
	var teamIDs []int64
	if len(groups) > 0 {
		var namespaces []*governance_model.Namespace
		if err := db.GetEngine(ctx).In("id", groups).Where("native_owner_team_id > 0").Find(&namespaces); err != nil {
			return nil, err
		}
		for _, group := range namespaces {
			teamIDs = append(teamIDs, group.NativeOwnerTeamID)
		}
	}
	teams, err := organization.GetRepoTeams(ctx, repo.OwnerID, repoID)
	if err != nil {
		return nil, err
	}
	for _, team := range teams {
		teamIDs = append(teamIDs, team.ID)
	}
	cursor := afterID
	for len(state.Members) <= 100 {
		ids, err := repositoryMemberCandidates(ctx, repo, groups, teamIDs, cursor, now)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			break
		}
		cursor = ids[len(ids)-1]
		for _, id := range ids {
			user, has, err := db.GetByID[user_model.User](ctx, id)
			if err != nil {
				return nil, err
			}
			if !has {
				continue
			}
			member := &GroupMemberState{UserID: id, Username: user.Name, Active: user.IsActive && !user.ProhibitLogin}
			member.Direct, has, err = db.Get[governance_model.Membership](ctx, builder.Eq{"scope_type": "repository", "scope_id": repoID, "user_id": id})
			if err != nil {
				return nil, err
			}
			if !has {
				member.Direct = nil
			}
			member.Grants, err = governance_model.RepositoryGrants(ctx, repoID, repo.OwnerID, id, now)
			if err != nil {
				return nil, err
			}
			collaboration, has, err := db.Get[repo_model.Collaboration](ctx, builder.Eq{"repo_id": repoID, "user_id": id})
			if err != nil {
				return nil, err
			}
			if has {
				member.NativeSources = append(member.NativeSources, NativeMemberSource{Kind: "collaborator", Access: collaboration.Mode.ToString()})
			}
			if id == repo.OwnerID && !repo.Owner.IsOrganization() {
				member.NativeSources = append(member.NativeSources, NativeMemberSource{Kind: "owner", Access: "owner"})
			}
			native, err := organization.GetUserRepoTeams(ctx, repo.OwnerID, id, repoID)
			if err != nil {
				return nil, err
			}
			if err := native.LoadUnits(ctx); err != nil {
				return nil, err
			}
			for _, team := range native {
				member.NativeSources = append(member.NativeSources, NativeMemberSource{Kind: "team", TeamID: team.ID, Access: team.AccessMode.ToString(), Units: team.GetUnitsMap()})
			}
			if member.Direct == nil && len(member.Grants) == 0 && len(member.NativeSources) == 0 {
				continue
			}
			state.Members = append(state.Members, member)
			if len(state.Members) > 100 {
				break
			}
		}
		if len(ids) < 101 {
			break
		}
	}
	if len(state.Members) > 100 {
		state.Members = state.Members[:100]
		state.NextID = state.Members[99].UserID
	}
	if err := db.GetEngine(ctx).Where("root_id = ?", chain[len(chain)-1].ID).Asc("id").Find(&state.Roles); err != nil {
		return nil, err
	}
	return state, nil
}
