// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"slices"
	"strconv"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/optional"

	"xorm.io/builder"
)

type GroupWorkQuery struct {
	Scope    string
	RepoID   int64
	Filter   string
	State    string
	Page     int
	PageSize int
}

type GroupIssuesState struct {
	Query        GroupWorkQuery
	Repositories []*repo_model.Repository
	Issues       issues_model.IssueList
	Count        int64
}

type GroupSharedRepositoriesState struct {
	Repositories []*repo_model.Repository
	Count        int64
	Page         int
	PageSize     int
}

// 群组只约束候选范围；原生分单元权限决定列表、筛选器和计数可包含哪些仓库。
func groupWorkRepositories(ctx context.Context, actorID, groupID int64, scope string, unitType unit.Type, includeArchived bool) ([]*repo_model.Repository, error) {
	if !slices.Contains([]string{"subtree", "direct", "shared"}, scope) || unitType.UnitGlobalDisabled() {
		return nil, governance_model.ErrInvalid
	}
	group, err := governance_model.GetNamespace(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if group.Kind != "group" {
		return nil, governance_model.ErrNotFound
	}
	var actor *user_model.User
	if actorID > 0 {
		actor, err = activeActor(ctx, actorID)
		if err != nil {
			return nil, err
		}
	}
	owner, err := user_model.GetUserByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if !organization.HasOrgOrUserVisible(ctx, owner, actor) {
		return nil, governance_model.ErrNotFound
	}
	var candidates []*repo_model.Repository
	switch scope {
	case "subtree":
		_, candidates, err = groupResourceTree(ctx, group)
	case "direct":
		err = db.GetEngine(ctx).Where("owner_id = ?", groupID).Asc("id").Find(&candidates)
	case "shared":
		shared := builder.Select("scope_id").From(new(governance_model.Share).TableName()).Where(
			builder.Eq{"scope_type": "repository", "group_id": groupID}.And(
				builder.Or(builder.Eq{"expires_unix": 0}, builder.Gt{"expires_unix": time.Now().Unix()})))
		err = db.GetEngine(ctx).In("id", shared).Asc("id").Find(&candidates)
	}
	if err != nil {
		return nil, err
	}
	visible := make([]*repo_model.Repository, 0, len(candidates))
	for _, repo := range candidates {
		if repo.IsArchived && !includeArchived {
			continue
		}
		permission, err := access_model.GetDoerRepoPermission(ctx, repo, actor)
		if err != nil {
			return nil, err
		}
		if permission.CanRead(unitType) {
			visible = append(visible, repo)
		}
	}
	return visible, nil
}

// ListGroupSharedRepositories shows direct shares as a separate list, never as descendants of the target group.
func ListGroupSharedRepositories(ctx context.Context, actorID, groupID int64, page, pageSize int) (*GroupSharedRepositoriesState, error) {
	if page < 1 || pageSize < 1 || pageSize > 100 {
		return nil, governance_model.ErrInvalid
	}
	repos, err := groupWorkRepositories(ctx, actorID, groupID, "shared", unit.TypeCode, true)
	if err != nil {
		return nil, err
	}
	visible := make([]*repo_model.Repository, 0, len(repos))
	for _, repo := range repos {
		ancestors, err := governance_model.Ancestors(ctx, repo.OwnerID)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(ancestors, func(source *governance_model.Namespace) bool { return source.ID == groupID }) {
			visible = append(visible, repo)
		}
	}
	state := &GroupSharedRepositoriesState{Count: int64(len(visible)), Page: page, PageSize: pageSize, Repositories: []*repo_model.Repository{}}
	if page > (len(visible)+pageSize-1)/pageSize {
		return state, nil
	}
	start := (page - 1) * pageSize
	state.Repositories = visible[start:min(start+pageSize, len(visible))]
	return state, nil
}

func ListGroupIssues(ctx context.Context, actorID, groupID int64, isPull bool, query GroupWorkQuery) (*GroupIssuesState, error) {
	if query.Scope == "" {
		query.Scope = "subtree"
	}
	if query.State == "" {
		query.State = "open"
	}
	if query.Page == 0 {
		query.Page = 1
	}
	if query.PageSize == 0 {
		query.PageSize = 20
	}
	if query.RepoID < 0 || query.Page < 1 || query.PageSize < 1 || query.PageSize > 100 ||
		!slices.Contains([]string{"open", "closed", "all"}, query.State) ||
		!slices.Contains([]string{"", "assigned", "created", "review_requested"}, query.Filter) ||
		query.Filter == "review_requested" && !isPull || query.Filter != "" && actorID <= 0 {
		return nil, governance_model.ErrInvalid
	}
	unitType := unit.TypeIssues
	if isPull {
		unitType = unit.TypePullRequests
	}
	repos, err := groupWorkRepositories(ctx, actorID, groupID, query.Scope, unitType, false)
	if err != nil {
		return nil, err
	}
	state := &GroupIssuesState{Query: query, Repositories: repos, Issues: issues_model.IssueList{}}
	ids := []int64{0} // 空授权集合不得退化为全库查询。
	for _, repo := range repos {
		if query.RepoID == 0 || query.RepoID == repo.ID {
			ids = append(ids, repo.ID)
		}
	}
	options := &issues_model.IssuesOptions{RepoIDs: ids, IsPull: optional.Some(isPull), SortType: "recentupdate", Paginator: &db.ListOptions{Page: query.Page, PageSize: query.PageSize}}
	// 上面已经逐仓校验有效权限，不叠加只认识原生组织的 Owner 限制。
	if query.State != "all" {
		options.IsClosed = optional.Some(query.State == "closed")
	}
	switch query.Filter {
	case "assigned":
		options.AssigneeID = strconv.FormatInt(actorID, 10)
	case "created":
		options.PosterID = strconv.FormatInt(actorID, 10)
	case "review_requested":
		options.ReviewRequestedID = actorID
	}
	state.Count, err = issues_model.CountIssues(ctx, options)
	if err != nil {
		return nil, err
	}
	state.Issues, err = issues_model.Issues(ctx, options)
	return state, err
}
