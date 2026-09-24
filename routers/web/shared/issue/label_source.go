// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"errors"

	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/organization"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

// PopulateLabelSources reveals a source path only to viewers who can read that group.
func PopulateLabelSources(ctx *context.Context, labels []*issues_model.Label) error {
	for _, label := range labels {
		label.SourcePath, label.SourceLink, label.CanManageSource = "", "", false
	}
	if ctx.Doer == nil {
		return nil
	}
	type source struct {
		path   string
		manage bool
	}
	sources := make(map[int64]source)
	for _, label := range labels {
		if label.OrgID == 0 {
			continue
		}
		data, checked := sources[label.OrgID]
		if !checked {
			state, err := governance_service.CheckGroupAccess(ctx, ctx.Doer.ID, label.OrgID, governance_model.ReadGroup)
			if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
				return err
			}
			if err == nil {
				data.path = state.FullPath
				data.manage = state.Abilities[governance_model.ManageGroup]
				if data.manage && !ctx.Doer.IsAdmin {
					data.manage, err = organization.IsOrganizationOwner(ctx, label.OrgID, ctx.Doer.ID)
					if err != nil {
						return err
					}
				}
			}
			sources[label.OrgID] = data
		}
		label.SourcePath = data.path
		label.CanManageSource = data.manage
		if data.manage {
			label.SourceLink = setting.AppSubURL + "/org/" + data.path + "/settings/labels"
		}
	}
	return nil
}
