// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"fmt"
	"slices"
)

type Role int

const (
	MinimalAccess Role = 5
	Guest         Role = 10
	Planner       Role = 15
	Reporter      Role = 20
	Developer     Role = 30
	Maintainer    Role = 40
	Owner         Role = 50
)

const (
	CreateGroup        = "create_group"
	CreateProject      = "create_project"
	ReadGroup          = "read_group"
	ReadIssues         = "read_issues"
	WriteIssues        = "write_issues"
	ReadCode           = "read_code"
	ReadPulls          = "read_pulls"
	WritePulls         = "write_pulls"
	PushCode           = "push_code"
	MergeCode          = "merge_code"
	ApproveCode        = "approve_code"
	ReadWiki           = "read_wiki"
	WriteWiki          = "write_wiki"
	ReadActions        = "read_actions"
	WriteActions       = "write_actions"
	ReadPackages       = "read_packages"
	WritePackages      = "write_packages"
	ReadReleases       = "read_releases"
	WriteReleases      = "write_releases"
	ManageProject      = "manage_project"
	ManageGroup        = "manage_group"
	ManageMembers      = "manage_members"
	ManageGroupMembers = "manage_group_members"
	ManageApprovals    = "manage_approvals"
	ReadAudit          = "read_audit"
	ManageAudit        = "manage_audit"
)

type Abilities map[string]bool

var roleAbilities = map[Role][]string{
	MinimalAccess: {ReadGroup},
	Guest:         {ReadGroup, ReadIssues},
	Planner:       {ReadGroup, ReadIssues, WriteIssues, ReadPulls},
	Reporter:      {ReadGroup, ReadIssues, ReadCode, ReadPulls, ReadWiki, ReadActions, ReadPackages, ReadReleases},
	Developer: {
		ReadGroup, ReadIssues, WriteIssues, ReadCode, ReadPulls, WritePulls, PushCode, MergeCode,
		ApproveCode, ReadWiki, WriteWiki, ReadActions, WriteActions, ReadPackages, WritePackages, ReadReleases, WriteReleases,
	},
	Maintainer: {
		ReadGroup, ReadIssues, WriteIssues, ReadCode, ReadPulls, WritePulls, PushCode, MergeCode,
		ApproveCode, ReadWiki, WriteWiki, ReadActions, WriteActions, ReadPackages, WritePackages, ReadReleases, WriteReleases,
		CreateGroup, CreateProject, ManageProject, ManageMembers, ManageApprovals, ReadAudit,
	},
	Owner: {
		ReadGroup, ReadIssues, WriteIssues, ReadCode, ReadPulls, WritePulls, PushCode, MergeCode,
		ApproveCode, ReadWiki, WriteWiki, ReadActions, WriteActions, ReadPackages, WritePackages, ReadReleases, WriteReleases,
		CreateGroup, CreateProject, ManageProject, ManageGroup, ManageGroupMembers, ManageMembers, ManageApprovals, ReadAudit, ManageAudit,
	},
}

func AbilitiesFor(role Role, extra []string) (Abilities, error) {
	base, ok := roleAbilities[role]
	if !ok {
		return nil, fmt.Errorf("%w：未知角色", ErrInvalid)
	}
	result := make(Abilities, len(base)+len(extra))
	for _, ability := range append(slices.Clone(base), extra...) {
		if !slices.Contains(roleAbilities[Owner], ability) {
			return nil, fmt.Errorf("%w：未知权限 %q", ErrInvalid, ability)
		}
		result[ability] = true
	}
	return result, nil
}

// Intersect 以具体能力限制共享权限，Planner 不能按数字比较后扩权。
func (a Abilities) Intersect(ceiling Abilities) Abilities {
	result := make(Abilities)
	for ability, enabled := range a {
		if enabled && ceiling[ability] {
			result[ability] = true
		}
	}
	return result
}

func (a Abilities) Include(other Abilities) {
	for ability, enabled := range other {
		if enabled {
			a[ability] = true
		}
	}
}

type Grant struct {
	MembershipID int64     `json:"membership_id"`
	NativeTeamID int64     `json:"native_team_id,omitempty"`
	ScopeType    string    `json:"scope_type"`
	ScopeID      int64     `json:"scope_id"`
	Source       string    `json:"source"`
	Role         Role      `json:"role"`
	CustomRoleID int64     `json:"custom_role_id"`
	ShareID      int64     `json:"share_id,omitempty"`
	CeilingRole  Role      `json:"ceiling_role,omitempty"`
	ViaShareIDs  []int64   `json:"via_share_ids,omitempty"`
	ExpiresUnix  int64     `json:"expires_unix"`
	Abilities    Abilities `json:"abilities"`
}

func EffectiveAbilities(grants []Grant) Abilities {
	result := make(Abilities)
	for _, grant := range grants {
		result.Include(grant.Abilities)
	}
	return result
}
