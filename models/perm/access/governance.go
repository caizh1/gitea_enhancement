// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package access

import (
	"context"
	"errors"
	"time"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/perm"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
)

func repositoryGovernanceAbilities(ctx context.Context, repo *repo_model.Repository, user *user_model.User) (governance_model.Abilities, error) {
	if user == nil || user.ID <= 0 || !user.IsActive || user.ProhibitLogin || user.IsOrganization() || user.IsGiteaActions() {
		return governance_model.Abilities{}, nil
	}
	grants, err := governance_model.RepositoryGrants(ctx, repo.ID, repo.OwnerID, user.ID, time.Now())
	if errors.Is(err, governance_model.ErrNotFound) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	return governance_model.EffectiveAbilities(grants), nil
}

// HasGovernanceAbility 检查独立治理能力，不把该能力折算成原生仓库管理员或代码写权限。
func HasGovernanceAbility(ctx context.Context, repo *repo_model.Repository, user *user_model.User, ability string) (bool, error) {
	abilities, err := repositoryGovernanceAbilities(ctx, repo, user)
	if err != nil {
		return false, err
	}
	return abilities[ability], nil
}

// 每个单元单独叠加，禁止用 Planner 或自定义角色的数字提升其他单元。
func (p *Permission) includeGovernance(abilities governance_model.Abilities) {
	if len(abilities) == 0 {
		return
	}
	if p.unitsMode == nil {
		p.unitsMode = make(map[unit.Type]perm.AccessMode)
	}
	for _, u := range p.units {
		mode := p.UnitAccessMode(u.Type)
		read, write := "", ""
		switch u.Type {
		case unit.TypeCode:
			read, write = governance_model.ReadCode, governance_model.PushCode
		case unit.TypeIssues, unit.TypeProjects:
			read, write = governance_model.ReadIssues, governance_model.WriteIssues
		case unit.TypePullRequests:
			read, write = governance_model.ReadPulls, governance_model.WritePulls
		case unit.TypeWiki:
			read, write = governance_model.ReadWiki, governance_model.WriteWiki
		case unit.TypeActions:
			read, write = governance_model.ReadActions, governance_model.WriteActions
		case unit.TypeReleases:
			read, write = governance_model.ReadReleases, governance_model.WriteReleases
		case unit.TypePackages:
			read, write = governance_model.ReadPackages, governance_model.WritePackages
		}
		if abilities[read] {
			mode = max(mode, perm.AccessModeRead)
		}
		if abilities[write] {
			mode = max(mode, perm.AccessModeWrite)
		}
		p.unitsMode[u.Type] = mode
	}
	// 原生管理员可以管理全部设置；只有已拥有完整 Maintainer 能力集合才能映射。
	maintainer, _ := governance_model.AbilitiesFor(governance_model.Maintainer, nil)
	fullAdmin := true
	for ability := range maintainer {
		fullAdmin = fullAdmin && abilities[ability]
	}
	if fullAdmin {
		p.AccessMode = max(p.AccessMode, perm.AccessModeAdmin)
	}
	owner, _ := governance_model.AbilitiesFor(governance_model.Owner, nil)
	fullOwner := true
	for ability := range owner {
		fullOwner = fullOwner && abilities[ability]
	}
	if fullOwner {
		p.AccessMode = max(p.AccessMode, perm.AccessModeOwner)
	}
}
