// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
)

var repositoryMemberRoleNames = map[gm.Role]string{gm.MinimalAccess: "Minimal Access", gm.Guest: "Guest · 访客", gm.Planner: "Planner · 计划者", gm.Reporter: "Reporter · 只读成员", gm.Developer: "Developer · 开发者", gm.Maintainer: "Maintainer · 维护者", gm.Owner: "Owner · 所有者"}

type RepositoryMemberQuery struct {
	Q       string  `json:"q"`
	Role    gm.Role `json:"role"`
	Source  string  `json:"source"`
	AfterID int64   `json:"after_id"`
}

type RepositoryMemberSource struct {
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Role        string            `json:"role"`
	Ceiling     string            `json:"ceiling,omitempty"`
	ExpiresUnix int64             `json:"expires_unix"`
	Expired     bool              `json:"expired"`
	ManageURL   string            `json:"manage_url,omitempty"`
	CanEdit     bool              `json:"can_edit"`
	Reason      string            `json:"reason,omitempty"`
	Abilities   gm.Abilities      `json:"abilities,omitempty"`
	Units       map[string]string `json:"units,omitempty"`
}

type RepositoryMemberView struct {
	UserID    int64                    `json:"user_id"`
	Username  string                   `json:"username"`
	Role      gm.Role                  `json:"role"`
	RoleName  string                   `json:"role_name"`
	Active    bool                     `json:"active"`
	SiteAdmin bool                     `json:"site_admin"`
	CanEdit   bool                     `json:"can_edit"`
	Reason    string                   `json:"reason"`
	Direct    *gm.Membership           `json:"direct,omitempty"`
	Sources   []RepositoryMemberSource `json:"sources"`
}

type RepositorySharedGroupView struct {
	Name        string `json:"name"`
	Role        string `json:"role"`
	ExpiresUnix int64  `json:"expires_unix"`
	Expired     bool   `json:"expired"`
}

type RepositoryMembersView struct {
	Shares           []RepositorySharedGroupView `json:"shares"`
	PublicProjection bool                        `json:"public_projection"`

	RepositoryID int64                   `json:"repository_id"`
	FullPath     string                  `json:"full_path"`
	CanManage    bool                    `json:"can_manage"`
	CanOwn       bool                    `json:"can_own"`
	Archived     bool                    `json:"archived"`
	Revision     int64                   `json:"revision"`
	NextID       int64                   `json:"next_id,omitempty"`
	Members      []*RepositoryMemberView `json:"members"`
	Roles        []*gm.CustomRole        `json:"roles,omitempty"`
	AllowedRoles []gm.Role               `json:"allowed_roles"`
	Query        RepositoryMemberQuery   `json:"query"`
}

func repositoryViewAccess(ctx context.Context, viewerID, repoID int64) (*repo_model.Repository, *user_model.User, bool, bool, error) {
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if repo_model.IsErrRepoNotExist(err) {
		return nil, nil, false, false, gm.ErrNotFound
	}
	if err != nil {
		return nil, nil, false, false, err
	}
	var viewer *user_model.User
	if viewerID > 0 {
		viewer, err = activeActor(ctx, viewerID)
		if err != nil {
			return nil, nil, false, false, err
		}
	}
	p, err := access_model.GetIndividualUserRepoPermission(ctx, repo, viewer)
	if err != nil {
		return nil, nil, false, false, err
	}
	canRead := p.HasAnyUnitAccess() || p.AccessMode >= perm.AccessModeRead
	if !canRead && viewer != nil {
		canRead, err = access_model.HasGovernanceAbility(ctx, repo, viewer, gm.ReadGroup)
		if err != nil {
			return nil, nil, false, false, err
		}
	}
	if !canRead {
		return nil, nil, false, false, gm.ErrNotFound
	}
	canManage := false
	if viewer != nil {
		_, _, manageErr := repositoryMemberManager(ctx, viewer.ID, repoID)
		if manageErr != nil && !errors.Is(manageErr, gm.ErrNotFound) {
			return nil, nil, false, false, manageErr
		}
		canManage = manageErr == nil
	}
	p = p.ForMutation()
	return repo, viewer, canManage, viewer != nil && p.IsOwner(), nil
}

// ListRepositoryMembersView 只返回可展示字段；原始授权记录仍由管理接口保护。
func ListRepositoryMembersView(ctx context.Context, viewerID, repoID int64, query RepositoryMemberQuery) (*RepositoryMembersView, error) {
	if len(query.Q) > 200 || query.AfterID < 0 || !slices.Contains([]string{"", "direct", "inherited", "shared", "inherited_shared", "native_team", "native_collaborator", "owner"}, query.Source) {
		return nil, gm.ErrInvalid
	}
	if query.Role != 0 {
		if _, err := gm.AbilitiesFor(query.Role, nil); err != nil {
			return nil, err
		}
	}
	repo, viewer, manage, owner, err := repositoryViewAccess(ctx, viewerID, repoID)
	if err != nil {
		return nil, err
	}
	result := &RepositoryMembersView{RepositoryID: repoID, FullPath: repo.FullPath(), CanManage: manage, CanOwn: owner, Members: []*RepositoryMemberView{}, Query: query}
	var shares []*gm.Share
	if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repoID).Asc("id").Find(&shares); err != nil {
		return nil, err
	}
	result.Shares = []RepositorySharedGroupView{}
	for _, share := range shares {
		name, _, err := repositorySourceGroup(ctx, viewer, share.GroupID, owner)
		if err != nil {
			return nil, err
		}
		result.Shares = append(result.Shares, RepositorySharedGroupView{Name: name, Role: repositoryMemberRoleNames[share.MaxRole], ExpiresUnix: share.ExpiresUnix, Expired: share.ExpiresUnix > 0 && share.ExpiresUnix <= time.Now().Unix()})
	}
	seePrivateMembers := manage
	if viewer != nil && !seePrivateMembers {
		grants, err := gm.RepositoryGrants(ctx, repoID, repo.OwnerID, viewer.ID, time.Now())
		if err != nil {
			return nil, err
		}
		native, err := organization.GetUserRepoTeams(ctx, repo.OwnerID, viewer.ID, repoID)
		if err != nil {
			return nil, err
		}
		collaborator, err := repo_model.IsCollaborator(ctx, repoID, viewer.ID)
		if err != nil {
			return nil, err
		}
		seePrivateMembers = viewer.IsAuditor || repo.OwnerID == viewer.ID || len(grants) > 0 || len(native) > 0 || collaborator
	}
	result.PublicProjection = !seePrivateMembers
	cursor := query.AfterID
	for {
		page, err := listRepositoryAllMembers(ctx, repo, manage, cursor)
		if err != nil {
			return nil, err
		}
		result.Revision, result.Archived = page.Revision, page.Archived
		if manage {
			_, ceiling, err := repositoryMemberManager(ctx, viewerID, repoID)
			if err != nil {
				return nil, err
			}
			result.AllowedRoles = nil
			for _, role := range []gm.Role{gm.Guest, gm.Planner, gm.Reporter, gm.Developer, gm.Maintainer, gm.Owner} {
				if checkMemberRole(ctx, "repository", repo.OwnerID, GroupMemberOption{Role: role}, ceiling) == nil {
					result.AllowedRoles = append(result.AllowedRoles, role)
				}
			}
			result.Roles = nil
			for _, role := range page.Roles {
				if checkMemberRole(ctx, "repository", repo.OwnerID, GroupMemberOption{Role: role.BaseRole, CustomRoleID: role.ID}, ceiling) == nil {
					result.Roles = append(result.Roles, role)
				}
			}
		}
		for _, member := range page.Members {
			if !seePrivateMembers {
				visible := *member
				visible.Grants = nil
				for _, grant := range member.Grants {
					visibleGrant := true
					groupID := grant.ScopeID
					if grant.ShareID > 0 {
						share, has, err := db.GetByID[gm.Share](ctx, grant.ShareID)
						if err != nil {
							return nil, err
						}
						if !has {
							continue
						}
						groupID = share.GroupID
					}
					if grant.ScopeType == "group" || grant.ShareID > 0 {
						chain, err := gm.Ancestors(ctx, groupID)
						if err != nil {
							return nil, err
						}
						for _, ancestor := range chain {
							visibleGrant = visibleGrant && ancestor.Visibility == 0
						}
					}
					if visibleGrant {
						visible.Grants = append(visible.Grants, grant)
					}
				}
				member = &visible
				if len(member.Grants) == 0 && len(member.NativeSources) == 0 && member.Direct == nil {
					continue
				}
			}

			if query.Q != "" && !strings.Contains(strings.ToLower(member.Username), strings.ToLower(query.Q)) {
				continue
			}
			item, err := presentRepositoryMember(ctx, repo, viewer, member, manage, owner)
			if err != nil {
				return nil, err
			}
			if query.Role != 0 && item.Role != query.Role {
				continue
			}
			if query.Source != "" && !slices.ContainsFunc(item.Sources, func(s RepositoryMemberSource) bool { return s.Kind == query.Source }) {
				continue
			}
			result.Members = append(result.Members, item)
			if len(result.Members) > 100 {
				result.Members = result.Members[:100]
				result.NextID = result.Members[99].UserID
				return result, nil
			}
		}
		if page.NextID == 0 {
			return result, nil
		}
		cursor = page.NextID
	}
}

func repositorySourceGroup(ctx context.Context, viewer *user_model.User, groupID int64, identify bool) (string, string, error) {
	group, err := gm.GetNamespace(ctx, groupID)
	if err != nil {
		return "", "", err
	}

	name, link := "受限群组", ""
	viewerID := int64(0)
	if viewer != nil {
		viewerID = viewer.ID
	}
	if viewer == nil {
		chain, err := gm.Ancestors(ctx, groupID)
		if err != nil {
			return "", "", err
		}
		public := true
		for _, ancestor := range chain {
			public = public && ancestor.Visibility == 0
		}
		if public {
			name = group.FullPath
		}
	}
	if identify {
		name = group.FullPath
	} else if visible, err := CheckGroupAccess(ctx, viewerID, groupID, gm.ReadGroup); err == nil {
		name = visible.FullPath
	} else if !errors.Is(err, gm.ErrNotFound) {
		return "", "", err
	}
	if viewer != nil {
		state, err := CheckGroupAccess(ctx, viewer.ID, groupID, gm.ManageGroupMembers)
		if err == nil {
			name, link = state.FullPath, fmt.Sprintf("%s/governance/groups/%d?tab=members", setting.AppSubURL, groupID)
		} else if !errors.Is(err, gm.ErrNotFound) {
			return "", "", err
		}
	}
	return name, link, nil
}

func presentRepositoryMember(ctx context.Context, repo *repo_model.Repository, viewer *user_model.User, member *GroupMemberState, manage, owner bool) (*RepositoryMemberView, error) {
	u, err := user_model.GetUserByID(ctx, member.UserID)
	if err != nil {
		return nil, err
	}
	item := &RepositoryMemberView{UserID: member.UserID, Username: member.Username, Active: member.Active, SiteAdmin: u.IsAdmin, Sources: []RepositoryMemberSource{}, Reason: "仅可在授权来源修改"}
	abilities := gm.EffectiveAbilities(member.Grants)
	isOwner := gm.HasOwnerGrant(member.Grants)
	partialNative := false
	seenTeams := map[int64]bool{}
	for _, grant := range member.Grants {
		source := RepositoryMemberSource{Kind: grant.Source, Name: "本项目直接授权", Role: repositoryMemberRoleNames[grant.Role], ExpiresUnix: grant.ExpiresUnix, Abilities: grant.Abilities, Reason: "继承或共享授权需要到来源修改"}
		if grant.ScopeType == "group" {
			source.Name, source.ManageURL, err = repositorySourceGroup(ctx, viewer, grant.ScopeID, false)
			if err != nil {
				return nil, err
			}
		}
		if grant.NativeTeamID != 0 && grant.ShareID == 0 {
			seenTeams[grant.NativeTeamID] = true
			source.Kind = "native_team"
			source.Name += " · Owners"
			if source.ManageURL != "" {
				n, err := gm.GetNamespace(ctx, grant.ScopeID)
				if err != nil {
					return nil, err
				}
				source.ManageURL = setting.AppSubURL + "/org/" + n.FullPath + "/teams/owners"
			}
		}
		if grant.ShareID != 0 {
			share, has, err := db.GetByID[gm.Share](ctx, grant.ShareID)
			if err != nil {
				return nil, err
			}
			if has {
				name, _, err := repositorySourceGroup(ctx, viewer, share.GroupID, owner && share.ScopeType == "repository" && share.ScopeID == repo.ID)
				if err != nil {
					return nil, err
				}
				source.Name = "经邀请群组：" + name
				source.ManageURL = ""
				if owner && share.ScopeType == "repository" && share.ScopeID == repo.ID {
					source.ManageURL = repo.Link() + "/collaborators#repo-group-shares-title"
				}
			}
			source.Ceiling = repositoryMemberRoleNames[grant.CeilingRole]
			if len(grant.ViaShareIDs) > 1 {
				ceilings := []string{}
				for _, id := range grant.ViaShareIDs {
					shareCap, found, err := db.GetByID[gm.Share](ctx, id)
					if err != nil {
						return nil, err
					}
					if found {
						ceilings = append(ceilings, repositoryMemberRoleNames[shareCap.MaxRole])
					}
				}
				source.Ceiling = strings.Join(ceilings, " → ")
			}
			if source.ManageURL == "" && share != nil && share.ScopeType == "group" && viewer != nil {
				if _, err := CheckGroupAccess(ctx, viewer.ID, share.ScopeID, gm.ManageGroup); err == nil {
					source.ManageURL = fmt.Sprintf("%s/governance/groups/%d?tab=shares", setting.AppSubURL, share.ScopeID)
				} else if !errors.Is(err, gm.ErrNotFound) {
					return nil, err
				}
			}
		}
		if strings.Contains(source.Name, "受限群组") {
			source.Role = "受限来源（有效能力见明细）"
		}
		item.Sources = append(item.Sources, source)
	}
	for _, native := range member.NativeSources {
		if seenTeams[native.TeamID] && native.Kind == "team" {
			continue
		}
		source := RepositoryMemberSource{Name: "原生协作者", Kind: "native_collaborator", Role: native.Access, Units: native.Units, Reason: "原生授权独立生效"}
		role := map[string]gm.Role{"read": gm.Reporter, "write": gm.Developer, "admin": gm.Maintainer}[native.Access]
		if native.Kind == "owner" {
			isOwner, role = true, gm.Owner
			source.Kind, source.Name, source.Reason = "owner", "个人命名空间所有者", "项目归属只能通过转移项目修改"
		}
		if native.Kind == "team" {
			team, err := organization.GetTeamByID(ctx, native.TeamID)
			if err != nil {
				return nil, err
			}
			name, link, err := repositorySourceGroup(ctx, viewer, team.OrgID, false)
			if err != nil {
				return nil, err
			}
			source.Kind, source.Name = "native_team", name+" · 原生团队"
			if link != "" {
				source.ManageURL = setting.AppSubURL + "/org/" + name + "/teams/" + team.LowerName
				source.Name = name + " · " + team.Name
			}
			if team.IsOwnerTeam() {
				isOwner, role = true, gm.Owner
			} else if !team.HasAdminAccess() {
				role = 0
				partialNative = true
			}
		}
		if role != 0 {
			extra, _ := gm.AbilitiesFor(role, nil)
			abilities.Include(extra)
			source.Role = repositoryMemberRoleNames[role]
		}
		item.Sources = append(item.Sources, source)
	}
	item.RoleName = "组合权限"
	for _, role := range []gm.Role{gm.Guest, gm.Planner, gm.Reporter, gm.Developer, gm.Maintainer} {
		base, _ := gm.AbilitiesFor(role, nil)
		if maps.Equal(abilities, base) {
			item.Role, item.RoleName = role, repositoryMemberRoleNames[role]
		}
	}
	if partialNative {
		item.Role, item.RoleName = 0, "组合权限"
	}
	if isOwner {
		item.Role, item.RoleName = gm.Owner, "Owner · 所有者"
	}
	if len(abilities) == 0 && len(member.NativeSources) == 0 {
		item.Role, item.RoleName = 0, "无有效授权"
	}
	if !item.Active {
		item.Role, item.RoleName = 0, "账号已停用，授权当前无效"
	}
	if member.Direct != nil && !slices.ContainsFunc(item.Sources, func(s RepositoryMemberSource) bool { return s.Kind == "direct" }) {
		item.Sources = append(item.Sources, RepositoryMemberSource{Kind: "direct", Name: "本项目直接授权", Role: repositoryMemberRoleNames[member.Direct.Role], ExpiresUnix: member.Direct.ExpiresUnix, Expired: member.Direct.ExpiresUnix > 0 && member.Direct.ExpiresUnix <= time.Now().Unix()})
	}
	if manage {
		item.Direct = member.Direct
		item.CanEdit = owner || (!isOwner && (member.Direct == nil || member.Direct.Role != gm.Owner))
		if item.CanEdit {
			item.Reason = "只修改本项目直接授权，其他来源继续生效"
		} else {
			item.Reason = "只有项目 Owner 可以修改 Owner 的授权"
		}
	}
	for i := range item.Sources {
		if item.Sources[i].Kind == "native_collaborator" {
			item.Sources[i].CanEdit = item.CanEdit
		}
		if item.Sources[i].Kind == "direct" {
			item.Sources[i].CanEdit, item.Sources[i].Reason = item.CanEdit, item.Reason
		}
	}
	return item, nil
}
