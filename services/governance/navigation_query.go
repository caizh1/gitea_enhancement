// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
)

type NavigationBreadcrumb struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type GroupNavigation struct {
	NavigationPage
	Group                NavigationNode         `json:"group"`
	Breadcrumbs          []NavigationBreadcrumb `json:"breadcrumbs"`
	RestrictedNavigation bool                   `json:"restricted_navigation"`
	AllowedActions       []string               `json:"allowed_actions"`
}

type navigationGroup struct {
	repositories []NavigationNode
	namespace    *gm.Namespace
	node         NavigationNode
	member       bool
}

func navigationState(archived, deleted bool) string {
	if deleted {
		return "pending_deletion"
	}
	if archived {
		return "archived"
	}
	return "active"
}

func navigationMatches(node NavigationNode, q NavigationQuery) bool {
	if (node.State == "active") != (q.State == "active") {
		return false
	}
	return q.Q == "" || strings.Contains(strings.ToLower(node.Name), q.Q) || strings.Contains(strings.ToLower(node.FullPath), q.Q)
}

// 查询内复用已鉴权结果，不跨请求缓存，撤权后下一次展开重新计算。
func navigationGroups(ctx context.Context, actorID int64) (map[int64]*navigationGroup, error) {
	var actor *user_model.User
	var err error
	if actorID > 0 {
		actor, err = activeActor(ctx, actorID)
		if err != nil {
			return nil, err
		}
	}
	var namespaces []*gm.Namespace
	if err := db.GetEngine(ctx).Where("kind = ?", "group").Find(&namespaces); err != nil {
		return nil, err
	}
	groups := make(map[int64]*navigationGroup)
	for _, n := range namespaces {
		org, err := user_model.GetUserByID(ctx, n.ID)
		if err != nil {
			return nil, err
		}
		abilities := gm.Abilities{}
		member := false
		visible := organization.HasOrgOrUserVisible(ctx, org, actor)
		if actor != nil {
			grants, err := gm.GroupGrants(ctx, n.ID, actorID, time.Now())
			if err != nil {
				return nil, err
			}
			abilities = gm.EffectiveAbilities(grants)
			member = len(grants) > 0
			native, err := organization.IsOrganizationMember(ctx, n.ID, actorID)
			if err != nil {
				return nil, err
			}
			member = member || native
			visible = visible || abilities[gm.ReadGroup]
			if actor.IsAdmin {
				abilities, _ = gm.AbilitiesFor(gm.Owner, nil)
			}
			if actor.IsAuditor {
				gm.IncludeAuditorAbilities(abilities)
				visible = true
			}
		}
		if !visible {
			continue
		}
		name := org.FullName
		if name == "" {
			name = n.Slug
		}
		node := NavigationNode{Key: fmt.Sprintf("group:%d", n.ID), ID: n.ID, Type: "group", Name: name, FullPath: n.FullPath, URL: setting.AppSubURL + "/" + n.FullPath, Visibility: n.Visibility, State: navigationState(n.Archived, n.DeleteAfter != 0), CreatedAt: int64(org.CreatedUnix), UpdatedAt: int64(org.UpdatedUnix), AllowedActions: []string{}}
		for ability, enabled := range abilities {
			if enabled {
				node.AllowedActions = append(node.AllowedActions, ability)
			}
		}
		slices.Sort(node.AllowedActions)
		groups[n.ID] = &navigationGroup{namespace: n, node: node, member: member}
	}
	// ponytail: 当前逐项目核对权限；大规模时可批量缩小候选集，仍保留最终鉴权。
	byID := make(map[int64]*gm.Namespace, len(namespaces))
	for _, n := range namespaces {
		byID[n.ID] = n
	}
	var repositories []*repo_model.Repository
	if err := db.GetEngine(ctx).Find(&repositories); err != nil {
		return nil, err
	}
	for _, repo := range repositories {
		n := byID[repo.OwnerID]
		if n == nil {
			continue
		}
		permission, err := access_model.GetDoerRepoPermission(ctx, repo, actor)
		if err != nil {
			return nil, err
		}
		if !permission.HasAnyUnitAccessOrPublicAccess() {
			continue
		}
		member := false
		if actor != nil {
			collaborator, err := repo_model.IsCollaborator(ctx, repo.ID, actorID)
			if err != nil {
				return nil, err
			}
			teams, err := organization.GetUserRepoTeams(ctx, repo.OwnerID, actorID, repo.ID)
			if err != nil {
				return nil, err
			}
			grants, err := gm.RepositoryGrants(ctx, repo.ID, repo.OwnerID, actorID, time.Now())
			if err != nil && !errors.Is(err, gm.ErrNotFound) {
				return nil, err
			}
			// 公开可读和管理员通行权不构成成员关系。
			member = collaborator || len(teams) > 0 || len(grants) > 0
		}
		group := groups[n.ID]
		if group == nil {
			if !member {
				continue
			}
			group = &navigationGroup{namespace: n, member: true, node: NavigationNode{Key: fmt.Sprintf("group:%d", n.ID), ID: n.ID, Type: "group", Name: n.Slug, FullPath: n.FullPath, URL: setting.AppSubURL + "/" + n.FullPath, Visibility: -1, State: navigationState(n.Archived, n.DeleteAfter != 0), RestrictedNavigation: true, AllowedActions: []string{}}}
			groups[n.ID] = group
		}
		group.member = group.member || member
		if group.node.RestrictedNavigation && !member {
			continue
		}
		var deletion gm.RepositoryDeletion
		pending, err := db.GetEngine(ctx).ID(repo.ID).Get(&deletion)
		if err != nil {
			return nil, err
		}
		path := n.FullPath + "/" + repo.Name
		node := NavigationNode{Key: fmt.Sprintf("repository:%d", repo.ID), ID: repo.ID, Type: "repository", Name: repo.Name, FullPath: path, URL: setting.AppSubURL + "/" + path, State: navigationState(repo.IsArchived, pending), CreatedAt: int64(repo.CreatedUnix), UpdatedAt: int64(repo.UpdatedUnix), AllowedActions: []string{}}
		if repo.IsPrivate {
			node.Visibility = 2
		} else if n.Visibility == 1 {
			node.Visibility = 1
		}
		group.repositories = append(group.repositories, node)
	}

	return groups, nil
}

func ListNavigationGroups(ctx context.Context, actorID int64, query NavigationQuery) (*NavigationPage, error) {
	if actorID <= 0 {
		return nil, gm.ErrNotFound
	}
	q, err := normalizeNavigationQuery(query)
	if err != nil {
		return nil, err
	}
	groups, err := navigationGroups(ctx, actorID)
	if err != nil {
		return nil, err
	}
	items := []NavigationNode{}
	for _, group := range groups {
		if !group.member || !navigationMatches(group.node, q) {
			continue
		}
		if q.Q == "" {
			parent := groups[group.namespace.ParentID]
			if parent != nil && parent.member && !parent.node.RestrictedNavigation && navigationMatches(parent.node, q) {
				continue
			}
		}
		node := group.node
		for _, child := range groups {
			if !node.RestrictedNavigation && child.namespace.ParentID == node.ID && navigationMatches(child.node, NavigationQuery{State: q.State}) {
				node.Expandable = true
				break
			}
		}
		items = append(items, node)
	}
	return paginateNavigation(items, actorID, 0, q)
}

func GetGroupNavigation(ctx context.Context, actorID, groupID int64, query NavigationQuery) (*GroupNavigation, error) {
	q, err := normalizeNavigationQuery(query)
	if err != nil {
		return nil, err
	}
	groups, err := navigationGroups(ctx, actorID)
	if err != nil {
		return nil, err
	}
	group := groups[groupID]
	if group == nil {
		return nil, gm.ErrNotFound
	}
	items := []NavigationNode{}
	for _, child := range groups {
		inside := child.namespace.ParentID == groupID
		if q.Q != "" {
			inside = strings.HasPrefix(child.node.FullPath, group.node.FullPath+"/")
		}
		if group.node.RestrictedNavigation || !inside || !navigationMatches(child.node, q) {
			continue
		}
		node := child.node
		if !q.GroupsOnly {
			for _, repo := range child.repositories {
				if navigationMatches(repo, NavigationQuery{State: q.State}) {
					node.Expandable = true
					break
				}
			}
		}
		for _, descendant := range groups {
			if !node.RestrictedNavigation && descendant.namespace.ParentID == node.ID && navigationMatches(descendant.node, NavigationQuery{State: q.State}) {
				node.Expandable = true
				break
			}
		}
		items = append(items, node)
	}
	if !q.GroupsOnly {
		for id, owner := range groups {
			if id != groupID && (q.Q == "" || group.node.RestrictedNavigation || !strings.HasPrefix(owner.node.FullPath, group.node.FullPath+"/")) {
				continue
			}
			for _, repo := range owner.repositories {
				if navigationMatches(repo, q) {
					items = append(items, repo)
				}
			}
		}
	}

	page, err := paginateNavigation(items, actorID, groupID, q)
	if err != nil {
		return nil, err
	}
	chain, err := gm.Ancestors(ctx, groupID)
	if err != nil && !errors.Is(err, gm.ErrNotFound) {
		return nil, err
	}
	breadcrumbs := []NavigationBreadcrumb{}
	for i := len(chain) - 1; i >= 0; i-- {
		crumb := NavigationBreadcrumb{Name: chain[i].Slug}
		if ancestor := groups[chain[i].ID]; ancestor != nil {
			crumb.Name = ancestor.node.Name
			crumb.URL = ancestor.node.URL
		}
		breadcrumbs = append(breadcrumbs, crumb)
	}
	return &GroupNavigation{NavigationPage: *page, Group: group.node, Breadcrumbs: breadcrumbs, RestrictedNavigation: group.node.RestrictedNavigation, AllowedActions: group.node.AllowedActions}, nil
}

// 选择器保留原生团队的创建能力，同时纳入治理继承来源。
func NavigationOrganizations(ctx context.Context, actorID int64, forCreation bool) ([]*organization.Organization, error) {
	groups, err := navigationGroups(ctx, actorID)
	if err != nil {
		return nil, err
	}
	result := []*organization.Organization{}
	for _, group := range groups {
		if !forCreation && (!group.member || group.node.RestrictedNavigation) {
			continue
		}
		user, err := user_model.GetUserByID(ctx, group.node.ID)
		if err != nil {
			return nil, err
		}
		org := organization.OrgFromUser(user)
		if forCreation {
			canCreate, err := org.CanCreateOrgRepo(ctx, actorID)
			if err != nil {
				return nil, err
			}
			if !canCreate || group.node.State != "active" {
				continue
			}
		}
		result = append(result, org)
	}
	slices.SortFunc(result, func(a, b *organization.Organization) int { return strings.Compare(a.FullPath(), b.FullPath()) })
	return result, nil
}

// 只在仓库已鉴权后调用，父级名称不能越过可见性边界。
func RepositoryNavigationBreadcrumbs(ctx context.Context, actorID, ownerID int64) ([]NavigationBreadcrumb, error) {
	chain, err := gm.Ancestors(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	var actor *user_model.User
	if actorID > 0 {
		actor, err = activeActor(ctx, actorID)
		if err != nil {
			return nil, err
		}
	}
	result := []NavigationBreadcrumb{}
	for i := len(chain) - 1; i >= 0; i-- {
		n := chain[i]
		crumb := NavigationBreadcrumb{Name: n.Slug}
		owner, err := user_model.GetUserByID(ctx, n.ID)
		if err != nil {
			return nil, err
		}
		visible := organization.HasOrgOrUserVisible(ctx, owner, actor)
		if actorID > 0 && n.Kind == "group" {
			_, accessErr := CheckGroupAccess(ctx, actorID, n.ID, gm.ReadGroup)
			if accessErr != nil && !errors.Is(accessErr, gm.ErrNotFound) {
				return nil, accessErr
			}
			visible = visible || accessErr == nil
		}
		if visible || n.ID == ownerID {
			crumb.URL = setting.AppSubURL + "/" + n.FullPath
		}
		result = append(result, crumb)
	}
	return result, nil
}
