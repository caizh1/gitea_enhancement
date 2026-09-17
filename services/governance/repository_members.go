// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
)

func repositoryMemberViewer(ctx context.Context, actorID, repoID int64) (*repo_model.Repository, bool, error) {
	repo, _, err := repositoryMemberManager(ctx, actorID, repoID)
	if err == nil {
		return repo, true, nil
	}
	if !errors.Is(err, governance_model.ErrNotFound) {
		return nil, false, err
	}
	auditor, checkErr := user_model.IsActiveAuditor(ctx, actorID)
	if checkErr != nil {
		return nil, false, checkErr
	}
	if !auditor {
		return nil, false, err
	}
	repo, err = repo_model.GetRepositoryByID(ctx, repoID)
	if repo_model.IsErrRepoNotExist(err) {
		err = governance_model.ErrNotFound
	}
	return repo, false, err
}

func repositoryMemberManager(ctx context.Context, actorID, repoID int64) (*repo_model.Repository, governance_model.Abilities, error) {
	user, err := activeActor(ctx, actorID)
	if err != nil {
		return nil, nil, err
	}
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if repo_model.IsErrRepoNotExist(err) {
		return nil, nil, governance_model.ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	grants, err := governance_model.RepositoryGrants(ctx, repoID, repo.OwnerID, actorID, time.Now())
	if err != nil {
		return nil, nil, err
	}
	abilities := governance_model.EffectiveAbilities(grants)
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
	if err != nil {
		return nil, nil, err
	}
	if permission.IsAdmin() {
		role := governance_model.Maintainer
		if permission.IsOwner() {
			role = governance_model.Owner
		}
		native, _ := governance_model.AbilitiesFor(role, nil)
		abilities.Include(native)
	}
	if !abilities[governance_model.ManageMembers] {
		return nil, nil, governance_model.ErrNotFound
	}
	return repo, abilities, nil
}

// CheckRepositoryMutationAbility 使用撤权即时生效的权限视图核对项目变更能力。
func CheckRepositoryMutationAbility(ctx context.Context, actorID, repoID int64, ability string) (*repo_model.Repository, error) {
	user, err := activeActor(ctx, actorID)
	if err != nil {
		return nil, err
	}
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return nil, err
	}
	grants, err := governance_model.RepositoryGrants(ctx, repo.ID, repo.OwnerID, actorID, time.Now())
	if err != nil {
		return nil, err
	}
	abilities := governance_model.EffectiveAbilities(grants)
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
	if err != nil {
		return nil, err
	}
	permission = permission.ForMutation()
	if permission.IsAdmin() {
		role := governance_model.Maintainer
		if permission.IsOwner() {
			role = governance_model.Owner
		}
		native, _ := governance_model.AbilitiesFor(role, nil)
		abilities.Include(native)
	}
	if !abilities[ability] {
		return nil, governance_model.ErrNotFound
	}
	return repo, nil
}

// CheckRepositoryOwnerMutation 只接受撤权即时生效的原生 Owner 或直接/继承 Owner 来源。
func CheckRepositoryOwnerMutation(ctx context.Context, actorID, repoID int64) (*repo_model.Repository, error) {
	user, err := activeActor(ctx, actorID)
	if err != nil {
		return nil, err
	}
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return nil, err
	}
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
	if err != nil {
		return nil, err
	}
	permission = permission.ForMutation()
	if permission.IsOwner() {
		return repo, nil
	}
	grants, err := governance_model.RepositoryGrants(ctx, repo.ID, repo.OwnerID, actorID, time.Now())
	if err != nil {
		return nil, err
	}
	for _, grant := range grants {
		if grant.Role == governance_model.Owner && (grant.Source == "direct" || grant.Source == "inherited" || grant.Source == "native_owner") {
			return repo, nil
		}
	}
	return nil, governance_model.ErrNotFound
}

// SetRepositoryMember 仅变更治理直接来源；不改写原生协作者、团队或继承授权。
func SetRepositoryMember(ctx context.Context, actor governance_model.Actor, repoID int64, option GroupMemberOption, remove bool) error {
	return setRepositoryMember(ctx, actor, actor, repoID, option, remove)
}

func setRepositoryMember(ctx context.Context, actor, auditActor governance_model.Actor, repoID int64, option GroupMemberOption, remove bool) error {
	return withActorWrite(ctx, actor, []string{governance_model.Resource("repository", repoID), governance_model.Resource("user", option.UserID)}, func(ctx context.Context) error {
		repo, ceiling, err := repositoryMemberManager(ctx, actor.EffectiveUserID(), repoID)
		if err != nil {
			return err
		}
		chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
		if err != nil {
			return err
		}
		if chain[0].Revision != option.Revision {
			return governance_model.ErrConflict
		}
		var previous governance_model.Membership
		exists, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "repository", repoID, option.UserID).Get(&previous)
		if err != nil {
			return err
		}
		if !remove {
			if repo.IsArchived {
				return governance_model.ErrConflict
			}
			for _, n := range chain {
				if n.Archived || n.DeleteAfter != 0 {
					return governance_model.ErrConflict
				}
			}
			if _, err := activeActor(ctx, option.UserID); err != nil {
				return err
			}
			if err := checkMemberRole(ctx, "repository", repo.OwnerID, option, ceiling); err != nil {
				return err
			}
		}
		if exists {
			if err := auditGrantExpiry(ctx, previous.ScopeType, previous.ScopeID, previous.ExpiresUnix, "member.expired", &previous, time.Now()); err != nil {
				return err
			}
		}
		next := &governance_model.Membership{ID: previous.ID, ScopeType: "repository", ScopeID: repoID, UserID: option.UserID, Role: option.Role, CustomRoleID: option.CustomRoleID, ExpiresUnix: option.ExpiresUnix}
		kind := "member.added"
		if remove {
			if !exists {
				return governance_model.ErrNotFound
			}
			if _, err := db.GetEngine(ctx).ID(previous.ID).Delete(new(governance_model.Membership)); err != nil {
				return err
			}
			kind = "member.removed"
		} else if exists {
			if _, err := db.GetEngine(ctx).ID(previous.ID).Cols("role", "custom_role_id", "expires_unix").Update(next); err != nil {
				return err
			}
			kind = "member.updated"
		} else if err := db.Insert(ctx, next); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).ID(repo.OwnerID).Incr("revision").Update(new(governance_model.Namespace)); err != nil {
			return err
		}
		target, err := user_model.GetUserByID(ctx, option.UserID)
		if err != nil && !user_model.IsErrUserNotExist(err) {
			return err
		}
		name := ""
		if target != nil {
			name = target.Name
		}
		ancestors := make([]int64, 0, len(chain))
		for _, n := range chain {
			if n.Kind == "group" {
				ancestors = append(ancestors, n.ID)
			}
		}
		details, err := json.Marshal(map[string]any{"before": previous, "after": next, "removed": remove, "source": "direct", "repository_path": repo.FullPath()})
		if err != nil {
			return err
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: kind, Actor: auditActor, ScopeType: "repository", ScopeID: repoID, AncestorIDs: ancestors, ObjectType: "user", ObjectID: option.UserID, ObjectPath: name, Result: "success", Details: details})
	})
}

// RepositoryMembersState 管理项目直接来源，同时解释这些成员保留的其他授权。
type RepositoryMembersState struct {
	CanManage      bool                           `json:"can_manage"`
	CanManageRoles bool                           `json:"can_manage_roles"`
	AllSources     bool                           `json:"all_sources"`
	FullPath       string                         `json:"full_path"`
	Revision       int64                          `json:"revision"`
	Archived       bool                           `json:"archived"`
	Members        []*GroupMemberState            `json:"members"`
	Roles          []*governance_model.CustomRole `json:"roles"`
	NextID         int64                          `json:"next_id,omitempty"`
	RootID         int64                          `json:"root_id"`
	PersonalRoot   bool                           `json:"personal_root"`
	RepoID         int64                          `json:"repo_id"`
}

func ListRepositoryMembers(ctx context.Context, viewerID, repoID, afterID int64) (*RepositoryMembersState, error) {
	if afterID < 0 {
		return nil, governance_model.ErrInvalid
	}
	repo, canManage, err := repositoryMemberViewer(ctx, viewerID, repoID)
	if err != nil {
		return nil, err
	}
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		return nil, err
	}
	state := &RepositoryMembersState{CanManage: canManage, FullPath: repo.FullPath(), Revision: chain[0].Revision, Archived: repo.IsArchived, Members: []*GroupMemberState{}, RootID: chain[len(chain)-1].ID, PersonalRoot: chain[len(chain)-1].Kind == "user", RepoID: repo.ID}
	viewer, err := activeActor(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	state.CanManageRoles = state.PersonalRoot && (viewer.IsAdmin || viewer.ID == state.RootID)
	for _, n := range chain {
		state.Archived = state.Archived || n.Archived || n.DeleteAfter != 0
	}
	var direct []*governance_model.Membership
	if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id > ?", "repository", repoID, afterID).Asc("user_id").Limit(101).Find(&direct); err != nil {
		return nil, err
	}
	if len(direct) > 100 {
		direct = direct[:100]
		state.NextID = direct[99].UserID
	}
	for _, member := range direct {
		user, has, err := db.GetByID[user_model.User](ctx, member.UserID)
		if err != nil {
			return nil, err
		}
		item := &GroupMemberState{UserID: member.UserID, Direct: member}
		if has {
			item.Username = user.Name
			item.Active = user.IsActive && !user.ProhibitLogin
		}
		item.Grants, err = governance_model.RepositoryGrants(ctx, repoID, repo.OwnerID, member.UserID, time.Now())
		if err != nil {
			return nil, err
		}
		state.Members = append(state.Members, item)
	}
	if err := db.GetEngine(ctx).Where("root_id = ?", chain[len(chain)-1].ID).Asc("id").Find(&state.Roles); err != nil {
		return nil, err
	}
	return state, nil
}
