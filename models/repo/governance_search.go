// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package repo

import (
	"context"
	"errors"
	"time"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	"gitea.dev/models/unit"
)

// 在分页之前纳入真实治理授权，不把管理员可见或公开访问当成个人成员关系。
func prepareGovernanceSearch(ctx context.Context, opts *SearchRepoOptions) error {
	actor := opts.Actor
	if actor == nil || actor.ID <= 0 || !actor.IsActive || actor.ProhibitLogin || actor.IsOrganization() || actor.IsGiteaActions() {
		return nil
	}
	var repos []*Repository
	if err := db.GetEngine(ctx).Find(&repos); err != nil {
		return err
	}
	abilitiesByUnit := map[unit.Type]string{unit.TypeCode: gm.ReadCode, unit.TypeIssues: gm.ReadIssues, unit.TypeProjects: gm.ReadIssues, unit.TypePullRequests: gm.ReadPulls, unit.TypeWiki: gm.ReadWiki, unit.TypeActions: gm.ReadActions, unit.TypePackages: gm.ReadPackages, unit.TypeReleases: gm.ReadReleases}
	now := time.Now()
	for _, repo := range repos {
		grants, err := gm.RepositoryGrants(ctx, repo.ID, repo.OwnerID, actor.ID, now)
		if errors.Is(err, gm.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		abilities := gm.EffectiveAbilities(grants)
		if len(abilities) == 0 {
			continue
		}
		if err := repo.LoadUnits(ctx); err != nil {
			return err
		}
		for _, u := range repo.Units {
			if (opts.UnitType == unit.TypeInvalid || opts.UnitType == u.Type) && abilities[abilitiesByUnit[u.Type]] {
				opts.governanceRepoIDs = append(opts.governanceRepoIDs, repo.ID)
				break
			}
		}
	}
	return nil
}
