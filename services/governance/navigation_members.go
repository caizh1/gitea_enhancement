// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"time"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/modules/setting"
)

type GroupMemberSource struct {
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Role        string            `json:"role"`
	URL         string            `json:"url"`
	CanManage   bool              `json:"can_manage"`
	ExpiresUnix int64             `json:"expires_unix"`
	Abilities   gm.Abilities      `json:"abilities,omitempty"`
	Units       map[string]string `json:"units,omitempty"`
}

var navigationRoleNames = map[gm.Role]string{gm.MinimalAccess: "Minimal Access", gm.Guest: "Guest", gm.Planner: "Planner", gm.Reporter: "Reporter", gm.Developer: "Developer", gm.Maintainer: "Maintainer", gm.Owner: "Owner"}

func presentGroupMember(ctx context.Context, viewerID, groupID int64, member *GroupMemberState, teams []*organization.Team) error {
	member.Sources = []GroupMemberSource{}
	effective := gm.EffectiveAbilities(member.Grants)
	member.EffectiveRole = "组合授权"
	for role, name := range navigationRoleNames {
		abilities, _ := gm.AbilitiesFor(role, nil)
		if maps.Equal(abilities, effective) {
			member.EffectiveRole = name
			break
		}
	}
	if len(effective) == 0 {
		member.EffectiveRole = "无有效授权"
	}
	for _, grant := range member.Grants {
		source := GroupMemberSource{Kind: grant.Source, Name: "受限来源", Role: navigationRoleNames[grant.Role], ExpiresUnix: grant.ExpiresUnix, Abilities: grant.Abilities}
		visible, err := CheckGroupAccess(ctx, viewerID, grant.ScopeID, gm.ReadGroup)
		if err != nil && !errors.Is(err, gm.ErrNotFound) {
			return err
		}
		if visible != nil {
			source.Name = visible.FullPath
			source.URL = fmt.Sprintf("%s/governance/groups/%d?tab=members", setting.AppSubURL, grant.ScopeID)
			source.CanManage = visible.Abilities[gm.ManageGroupMembers]
		}
		if grant.NativeTeamID != 0 && grant.Source != "shared" && grant.Source != "inherited_shared" {
			source.Kind = "native_team"
			if visible != nil {
				source.URL = setting.AppSubURL + "/org/" + visible.FullPath + "/teams/owners"
			}
		}
		if grant.Source == "shared" || grant.Source == "inherited_shared" {
			// 共享的修改权限属于共享所在范围，不能用被邀请组的成员权限替代。
			source.CanManage = false
			source.URL = ""
			share, found, err := db.GetByID[gm.Share](ctx, grant.ShareID)
			if err != nil {
				return err
			}
			if found && share.ScopeType == "group" {
				origin, err := CheckGroupAccess(ctx, viewerID, share.ScopeID, gm.ReadGroup)
				if err != nil && !errors.Is(err, gm.ErrNotFound) {
					return err
				}
				if origin != nil {
					source.URL = fmt.Sprintf("%s/governance/groups/%d?tab=shares", setting.AppSubURL, share.ScopeID)
					source.CanManage = origin.Abilities[gm.ManageGroup]
				}
			}
		}
		member.Sources = append(member.Sources, source)
	}
	group, err := CheckGroupAccess(ctx, viewerID, groupID, gm.ReadGroup)
	if err != nil {
		return err
	}
	if member.Direct != nil && member.Direct.ExpiresUnix > 0 && member.Direct.ExpiresUnix <= time.Now().Unix() {
		member.Sources = append(member.Sources, GroupMemberSource{Kind: "direct", Name: group.FullPath, Role: navigationRoleNames[member.Direct.Role] + "（已到期）", ExpiresUnix: member.Direct.ExpiresUnix, URL: fmt.Sprintf("%s/governance/groups/%d?tab=members", setting.AppSubURL, groupID), CanManage: group.Abilities[gm.ManageGroupMembers]})
	}
	member.CanEdit = group.Abilities[gm.ManageGroupMembers] && member.Direct != nil
	for _, team := range teams {
		if team.IsOwnerTeam() {
			continue
		}
		if err := team.LoadUnits(ctx); err != nil {
			return err
		}
		member.EffectiveRole = "组合授权"
		member.Sources = append(member.Sources, GroupMemberSource{Kind: "native_team", Name: team.Name, Role: "分单元授权", URL: setting.AppSubURL + "/org/" + group.FullPath + "/teams/" + url.PathEscape(team.LowerName), CanManage: group.Abilities[gm.ManageGroupMembers], Units: team.GetUnitsMap()})
	}
	return nil
}
