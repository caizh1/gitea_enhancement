// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"slices"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	user_model "gitea.dev/models/user"

	"xorm.io/builder"
)

type GroupMemberQuery struct {
	Q      string
	Source string
	Own    bool
}

type GroupMemberState struct {
	User          *user_model.User             `json:"-"`
	EffectiveRole string                       `json:"effective_role"`
	Sources       []GroupMemberSource          `json:"sources"`
	CanEdit       bool                         `json:"can_edit"`
	NativeSources []NativeMemberSource         `json:"native_sources,omitempty"`
	UserID        int64                        `json:"user_id"`
	Username      string                       `json:"username"`
	Active        bool                         `json:"active"`
	Direct        *governance_model.Membership `json:"direct,omitempty"`
	Grants        []governance_model.Grant     `json:"grants"`
	NativeTeams   []string                     `json:"native_teams"`
}

// ListGroupMembers 来源候选与计权分开，已到期的直接记录仍可供管理员处理。
func ListGroupMembers(ctx context.Context, viewerID, groupID, afterID int64, limit int, options ...GroupMemberQuery) ([]*GroupMemberState, error) {
	query := GroupMemberQuery{}
	if len(options) > 0 {
		query = options[0]
	}
	if len(query.Q) > 200 || (query.Source != "" && !slices.Contains([]string{"direct", "inherited", "shared", "inherited_shared", "native_team"}, query.Source)) {
		return nil, governance_model.ErrInvalid
	}
	auditor, err := user_model.IsActiveAuditor(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	ability := governance_model.ManageGroupMembers
	if auditor || query.Own {
		ability = governance_model.ReadGroup
	}
	if _, err := CheckGroupAccess(ctx, viewerID, groupID, ability); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 || afterID < 0 {
		return nil, governance_model.ErrInvalid
	}
	chain, err := governance_model.Ancestors(ctx, groupID)
	if err != nil {
		return nil, err
	}
	groupIDs := make([]int64, 0, len(chain))
	for _, n := range chain {
		groupIDs = append(groupIDs, n.ID)
	}
	var shares []*governance_model.Share
	if err := db.GetEngine(ctx).Where("scope_type = ?", "group").In("scope_id", groupIDs).Find(&shares); err != nil {
		return nil, err
	}
	for _, share := range shares {
		groupIDs = append(groupIDs, share.GroupID)
	}
	// 共享只扩展被邀请组的直接成员，不递归遍历其祖先和子组。
	var ownerTeams []*governance_model.Namespace
	if err := db.GetEngine(ctx).In("id", groupIDs).Where("native_owner_team_id > ?", 0).Find(&ownerTeams); err != nil {
		return nil, err
	}
	teamIDs := make([]int64, 0, len(ownerTeams))
	for _, n := range ownerTeams {
		teamIDs = append(teamIDs, n.NativeOwnerTeamID)
	}
	var ids []int64
	if err := db.GetEngine(ctx).Table(new(governance_model.Membership)).Cols("user_id").Where("scope_type = ? AND user_id > ?", "group", afterID).In("scope_id", groupIDs).Find(&ids); err != nil {
		return nil, err
	}
	if len(teamIDs) > 0 {
		var owners []int64
		if err := db.GetEngine(ctx).Table("team_user").Cols("uid").Where("uid > ?", afterID).In("team_id", teamIDs).Find(&owners); err != nil {
			return nil, err
		}
		ids = append(ids, owners...)
	}
	var nativeIDs []int64
	if err := db.GetEngine(ctx).Table("org_user").Cols("uid").Where("org_id = ? AND uid > ?", groupID, afterID).Find(&nativeIDs); err != nil {
		return nil, err
	}
	ids = append(ids, nativeIDs...)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	result := make([]*GroupMemberState, 0, limit)
	for _, id := range ids {
		if query.Own && id != viewerID {
			continue
		}
		user, err := user_model.GetUserByID(ctx, id)
		if user_model.IsErrUserNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if query.Q != "" && !strings.Contains(strings.ToLower(user.Name+" "+user.FullName), strings.ToLower(query.Q)) {
			continue
		}
		state := &GroupMemberState{User: user, UserID: id, Username: user.Name, Active: user.IsActive && !user.ProhibitLogin, NativeTeams: []string{}}
		direct, has, err := db.Get[governance_model.Membership](ctx, builder.Eq{"scope_type": "group", "scope_id": groupID, "user_id": id})
		if err != nil {
			return nil, err
		}
		if has {
			state.Direct = direct
		}
		state.Grants, err = governance_model.GroupGrants(ctx, groupID, id, time.Now())
		if err != nil {
			return nil, err
		}
		teams, err := organization.GetUserOrgTeams(ctx, groupID, id)
		if err != nil {
			return nil, err
		}
		for _, team := range teams {
			state.NativeTeams = append(state.NativeTeams, team.Name)
		}
		if len(state.Grants) == 0 && !has && len(teams) == 0 {
			continue
		}
		if err := presentGroupMember(ctx, viewerID, groupID, state, teams); err != nil {
			return nil, err
		}
		if query.Source != "" && !slices.ContainsFunc(state.Sources, func(source GroupMemberSource) bool { return source.Kind == query.Source }) {
			continue
		}
		result = append(result, state)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}
