// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"time"

	"gitea.dev/models/db"

	"xorm.io/builder"
)

func activeMemberships(ctx context.Context, scope string, ids []int64, userID, now int64) ([]*Membership, error) {
	rows := make([]*Membership, 0)
	if len(ids) == 0 || userID <= 0 {
		return rows, nil
	}
	err := db.GetEngine(ctx).Where("scope_type = ? AND user_id = ?", scope, userID).
		In("scope_id", ids).And(builder.Or(builder.Eq{"expires_unix": 0}, builder.Gt{"expires_unix": now})).
		Asc("id").Find(&rows)
	return rows, err
}

func activeShares(ctx context.Context, scope string, ids []int64, now int64) ([]*Share, error) {
	rows := make([]*Share, 0)
	if len(ids) == 0 {
		return rows, nil
	}
	err := db.GetEngine(ctx).Where("scope_type = ?", scope).In("scope_id", ids).
		And(builder.Or(builder.Eq{"expires_unix": 0}, builder.Gt{"expires_unix": now})).Asc("id").Find(&rows)
	return rows, err
}

func membershipGrant(ctx context.Context, member *Membership, rootID int64, source string) (Grant, error) {
	var extra []string
	if member.CustomRoleID != 0 {
		role, has, err := db.GetByID[CustomRole](ctx, member.CustomRoleID)
		if err != nil {
			return Grant{}, err
		}
		if !has || role.RootID != rootID || role.BaseRole != member.Role {
			return Grant{}, fmt.Errorf("%w：自定义角色与授权范围不一致", ErrConflict)
		}
		extra = role.Abilities
	}
	abilities, err := AbilitiesFor(member.Role, extra)
	if err != nil {
		return Grant{}, err
	}
	return Grant{
		MembershipID: member.ID, ScopeType: member.ScopeType, ScopeID: member.ScopeID,
		Source: source, Role: member.Role, CustomRoleID: member.CustomRoleID,
		ExpiresUnix: member.ExpiresUnix, Abilities: abilities,
	}, nil
}

func earlierExpiry(a, b int64) int64 {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	return min(a, b)
}

func groupChain(ctx context.Context, id int64) ([]int64, int64, error) {
	chain, err := Ancestors(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]int64, 0, len(chain))
	for _, n := range chain {
		if n.Kind != "group" {
			continue
		}
		ids = append(ids, n.ID)
	}
	return ids, chain[len(chain)-1].ID, nil
}

// GroupGrants 对群组共享只读取被邀请群组的直接成员，禁止递归共享扩权。
func GroupGrants(ctx context.Context, groupID, userID int64, now time.Time) ([]Grant, error) {
	ids, rootID, err := groupChain(ctx, groupID)
	if err != nil {
		return nil, err
	}
	grants := make([]Grant, 0)
	members, err := activeMemberships(ctx, "group", ids, userID, now.Unix())
	if err != nil {
		return nil, err
	}
	for _, member := range members {
		source := "inherited"
		if member.ScopeID == groupID {
			source = "direct"
		}
		grant, err := membershipGrant(ctx, member, rootID, source)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	legacy, err := nativeOwnerGrants(ctx, ids, groupID, userID)
	if err != nil {
		return nil, err
	}
	grants = append(grants, legacy...)
	shares, err := activeShares(ctx, "group", ids, now.Unix())
	if err != nil {
		return nil, err
	}
	for _, share := range shares {
		_, invitedRoot, err := groupChain(ctx, share.GroupID)
		if err != nil {
			return nil, err
		}
		members, err := activeMemberships(ctx, "group", []int64{share.GroupID}, userID, now.Unix())
		if err != nil {
			return nil, err
		}
		ceiling, err := AbilitiesFor(share.MaxRole, nil)
		if err != nil {
			return nil, err
		}
		for _, member := range members {
			source := "inherited_shared"
			if share.ScopeID == groupID {
				source = "shared"
			}
			grant, err := membershipGrant(ctx, member, invitedRoot, source)
			if err != nil {
				return nil, err
			}
			grant.ShareID = share.ID
			grant.CeilingRole = share.MaxRole
			grant.Abilities = grant.Abilities.Intersect(ceiling)
			grant.ExpiresUnix = earlierExpiry(grant.ExpiresUnix, share.ExpiresUnix)
			grants = append(grants, grant)
		}
		legacy, err := nativeOwnerGrants(ctx, []int64{share.GroupID}, share.GroupID, userID)
		if err != nil {
			return nil, err
		}
		for _, grant := range legacy {
			grant.Source = "inherited_shared"
			if share.ScopeID == groupID {
				grant.Source = "shared"
			}
			grant.ShareID = share.ID
			grant.CeilingRole = share.MaxRole
			grant.Abilities = grant.Abilities.Intersect(ceiling)
			grant.ExpiresUnix = share.ExpiresUnix
			grants = append(grants, grant)
		}
	}
	return grants, nil
}

// 只接入原生 Owners 团队的明确全组授权；其他分单元团队仍由原生仓库权限计算。
func nativeOwnerGrants(ctx context.Context, ids []int64, targetID, userID int64) ([]Grant, error) {
	grants := []Grant{}
	if userID <= 0 || len(ids) == 0 {
		return grants, nil
	}
	var namespaces []*Namespace
	if err := db.GetEngine(ctx).In("id", ids).Where("native_owner_team_id > ?", 0).Find(&namespaces); err != nil {
		return nil, err
	}
	if len(namespaces) == 0 {
		return grants, nil
	}
	teamIDs := make([]int64, 0, len(namespaces))
	byGroup := make(map[int64]int64, len(namespaces))
	for _, namespace := range namespaces {
		teamIDs = append(teamIDs, namespace.NativeOwnerTeamID)
		byGroup[namespace.ID] = namespace.NativeOwnerTeamID
	}
	var members []struct {
		OrgID  int64
		TeamID int64
	}
	if err := db.GetEngine(ctx).Table("team_user").Select("org_id, team_id").Where("uid = ?", userID).In("team_id", teamIDs).Find(&members); err != nil {
		return nil, err
	}
	for _, member := range members {
		if byGroup[member.OrgID] != member.TeamID {
			return nil, ErrConflict
		}
		abilities, err := AbilitiesFor(Owner, nil)
		if err != nil {
			return nil, err
		}
		source := "inherited"
		if member.OrgID == targetID {
			source = "direct"
		}
		grants = append(grants, Grant{ScopeType: "group", ScopeID: member.OrgID, NativeTeamID: member.TeamID, Source: source, Role: Owner, Abilities: abilities})
	}
	return grants, nil
}

// RepositoryGrants 保留项目直接、父级继承和项目共享的独立来源。
// 被共享到项目的群组允许直接、继承、共享成员；这与群组共享不同。
func RepositoryGrants(ctx context.Context, repoID, ownerID, userID int64, now time.Time) ([]Grant, error) {
	grants, err := GroupGrants(ctx, ownerID, userID, now)
	if err != nil {
		return nil, err
	}
	for i := range grants {
		switch grants[i].Source {
		case "direct":
			grants[i].Source = "inherited"
		case "shared":
			grants[i].Source = "inherited_shared"
		}
	}
	_, rootID, err := groupChain(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	members, err := activeMemberships(ctx, "repository", []int64{repoID}, userID, now.Unix())
	if err != nil {
		return nil, err
	}
	for _, member := range members {
		grant, err := membershipGrant(ctx, member, rootID, "direct")
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	shares, err := activeShares(ctx, "repository", []int64{repoID}, now.Unix())
	if err != nil {
		return nil, err
	}
	for _, share := range shares {
		invited, err := GroupGrants(ctx, share.GroupID, userID, now)
		if err != nil {
			return nil, err
		}
		ceiling, err := AbilitiesFor(share.MaxRole, nil)
		if err != nil {
			return nil, err
		}
		for _, grant := range invited {
			grant.ViaShareIDs = append(grant.ViaShareIDs, grant.ShareID)
			grant.Source, grant.ShareID = "shared", share.ID
			grant.CeilingRole = share.MaxRole
			grant.Abilities = grant.Abilities.Intersect(ceiling)
			grant.ExpiresUnix = earlierExpiry(grant.ExpiresUnix, share.ExpiresUnix)
			grants = append(grants, grant)
		}
	}
	return grants, nil
}
