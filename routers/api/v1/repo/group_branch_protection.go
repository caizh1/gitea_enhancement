// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"errors"
	"fmt"
	"net/http"

	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

// ListInheritedBranchProtections returns read-only group rules for repository administrators.
func ListInheritedBranchProtections(ctx *context.APIContext) {
	rules, sourceID, err := git_model.GroupProtectedRulesForRepo(ctx, ctx.Repo.Repository)
	if err != nil {
		ctx.APIErrorInternal(err)
		return
	}
	response := map[string]any{"rules": rules, "source_id": sourceID, "editable_here": false}
	if sourceID != 0 {
		response["source"] = fmt.Sprintf("受限群组来源 #%d", sourceID)
		state, err := governance_service.CheckGroupAccess(ctx, ctx.Doer.ID, sourceID, governance_model.ReadGroup)
		if err == nil {
			response["source"] = state.FullPath
			if state.Abilities[governance_model.ManageGroup] {
				response["manage_url"] = fmt.Sprintf("/governance/groups/%d?tab=settings", sourceID)
			}
		} else if !errors.Is(err, governance_model.ErrNotFound) {
			ctx.APIErrorInternal(err)
			return
		}
	}
	ctx.JSON(http.StatusOK, response)
}
