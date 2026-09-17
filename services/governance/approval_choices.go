// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
)

type ApprovalSubject struct {
	ID   int64
	Name string
}

type ApprovalChoices struct {
	Users  []ApprovalSubject
	Groups []ApprovalSubject
	Teams  []ApprovalSubject
}

func canReadApprovalTeam(ctx context.Context, actorID int64, team *organization.Team) (bool, error) {
	actor, err := activeActor(ctx, actorID)
	if err != nil {
		return false, err
	}
	if actor.IsAdmin {
		return true, nil
	}
	owner, err := organization.IsOrganizationOwner(ctx, team.OrgID, actorID)
	if err != nil {
		return false, err
	}
	if owner || team.IsMember(ctx, actorID) {
		return true, nil
	}
	org, err := user_model.GetUserByID(ctx, team.OrgID)
	if err != nil {
		return false, err
	}
	return team.CanNonMemberReadMeta(ctx, org, actor)
}

// ListApprovalChoices 使用原生用户可见性和当前项目资格，界面展示名称、提交稳定 ID。
func ListApprovalChoices(ctx context.Context, actorID, repoID int64) (*ApprovalChoices, error) {
	repo, _, err := checkApprovalRuleAccess(ctx, actorID, repoID, false)
	if err != nil {
		return nil, err
	}
	return listApprovalChoices(ctx, actorID, repo)
}

func ListApprovalPolicyChoices(ctx context.Context, actorID int64, scope string, id int64) (*ApprovalChoices, error) {
	if _, err := checkApprovalPolicyAccess(ctx, actorID, scope, id); err != nil {
		return nil, err
	}
	return listApprovalChoices(ctx, actorID, nil)
}

func listApprovalChoices(ctx context.Context, actorID int64, repo *repo_model.Repository) (*ApprovalChoices, error) {
	actor, err := activeActor(ctx, actorID)
	if err != nil {
		return nil, err
	}
	result := &ApprovalChoices{}
	users, _, err := user_model.SearchUsers(ctx, user_model.SearchUserOptions{ListOptions: db.ListOptionsAll, Actor: actor, Types: []user_model.UserType{user_model.UserTypeIndividual, user_model.UserTypeBot}})
	if err != nil {
		return nil, err
	}
	for _, user := range users {
		if !user.IsActive || user.ProhibitLogin {
			continue
		}
		if repo == nil {
			result.Users = append(result.Users, ApprovalSubject{ID: user.ID, Name: user.Name})
			continue
		}
		permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
		if err != nil {
			return nil, err
		}
		if permission.CanRead(unit.TypePullRequests) {
			result.Users = append(result.Users, ApprovalSubject{ID: user.ID, Name: user.Name})
		}
	}
	var namespaces []*governance_model.Namespace
	if err := db.GetEngine(ctx).Where("kind = ? AND delete_after = ?", "group", 0).Asc("full_path").Find(&namespaces); err != nil {
		return nil, err
	}
	for _, namespace := range namespaces {
		group, err := CheckGroupAccess(ctx, actorID, namespace.ID, governance_model.ReadGroup)
		if errors.Is(err, governance_model.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result.Groups = append(result.Groups, ApprovalSubject{ID: group.ID, Name: group.FullPath})
	}
	var teams []*organization.Team
	query := db.GetEngine(ctx).Asc("name")
	if repo != nil {
		query = query.Where("org_id = ?", repo.OwnerID)
	}
	if err := query.Find(&teams); err != nil {
		return nil, err
	}
	for _, team := range teams {
		visible, err := canReadApprovalTeam(ctx, actorID, team)
		if err != nil {
			return nil, err
		}
		if visible {
			name := team.Name
			if repo == nil {
				group, err := governance_model.GetNamespace(ctx, team.OrgID)
				if err != nil {
					return nil, err
				}
				name = group.FullPath + "/" + name
			}
			result.Teams = append(result.Teams, ApprovalSubject{ID: team.ID, Name: name})
		}
	}
	return result, nil
}
