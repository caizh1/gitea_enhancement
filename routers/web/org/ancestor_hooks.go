// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	"context"
	"errors"
	"fmt"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/webhook"
	"gitea.dev/modules/setting"
	governance_service "gitea.dev/services/governance"
)

type ancestorHookSource struct {
	ID        int64
	Path      string
	Count     int64
	URL       string
	CanRead   bool
	CanManage bool
}

func ancestorHookSources(ctx context.Context, groupID, actorID int64) ([]ancestorHookSource, error) {
	chain, err := governance_model.Ancestors(ctx, groupID)
	if errors.Is(err, governance_model.ErrNotFound) { // Native groups without a namespace have no inherited source.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]ancestorHookSource, 0, len(chain)-1)
	for _, ancestor := range chain[1:] {
		if ancestor.Kind != "group" {
			continue
		}
		count, err := db.GetEngine(ctx).Where("owner_id = ? AND repo_id = ? AND is_active = ?", ancestor.ID, 0, true).Count(new(webhook.Webhook))
		if err != nil {
			return nil, err
		}
		if count == 0 {
			continue
		}
		source := ancestorHookSource{ID: ancestor.ID}
		visible, err := governance_service.CheckGroupAccess(ctx, actorID, ancestor.ID, governance_model.ReadGroup)
		if errors.Is(err, governance_model.ErrNotFound) {
			result = append(result, source)
			continue
		}
		if err != nil {
			return nil, err
		}
		source.CanRead, source.Path, source.Count = true, visible.FullPath, count
		if visible.Abilities[governance_model.ManageGroup] {
			owner, err := organization.IsOrganizationOwner(ctx, ancestor.ID, actorID)
			if err != nil {
				return nil, err
			}
			if owner {
				source.CanManage = true
				source.URL = fmt.Sprintf("%s/org/%s/settings/hooks", setting.AppSubURL, visible.FullPath)
			}
		}
		result = append(result, source)
	}
	return result, nil
}
