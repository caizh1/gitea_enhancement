// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/web"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

type GroupBranchProtectionOption struct {
	Revision       int64                 `json:"revision"`
	RuleName       string                `json:"rule_name"`
	PushRole       governance_model.Role `json:"push_role"`
	MergeRole      governance_model.Role `json:"merge_role"`
	AllowForcePush bool                  `json:"allow_force_push"`
}

func GroupBranchProtections(ctx *context.APIContext) {
	rules, group, err := governance_service.ListGroupBranchProtections(ctx, ctx.Doer.ID, ctx.PathParamInt64("id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, map[string]any{"revision": group.Revision, "rules": rules})
}

func CreateGroupBranchProtection(ctx *context.APIContext) {
	option := web.GetForm(ctx).(*GroupBranchProtectionOption)
	rule, err := governance_service.SaveGroupBranchProtection(ctx, requestActor(ctx), governance_service.GroupBranchProtectionInput{
		GroupID: ctx.PathParamInt64("id"), Revision: option.Revision, RuleName: option.RuleName,
		PushRole: option.PushRole, MergeRole: option.MergeRole, AllowForcePush: option.AllowForcePush,
	})
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, rule)
}

func UpdateGroupBranchProtection(ctx *context.APIContext) {
	if ctx.PathParamInt64("rule_id") <= 0 {
		ctx.APIError(http.StatusBadRequest, "invalid rule ID")
		return
	}
	option := web.GetForm(ctx).(*GroupBranchProtectionOption)
	rule, err := governance_service.SaveGroupBranchProtection(ctx, requestActor(ctx), governance_service.GroupBranchProtectionInput{
		ID: ctx.PathParamInt64("rule_id"), GroupID: ctx.PathParamInt64("id"), Revision: option.Revision,
		RuleName: option.RuleName, PushRole: option.PushRole, MergeRole: option.MergeRole, AllowForcePush: option.AllowForcePush,
	})
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, rule)
}

func DeleteGroupBranchProtection(ctx *context.APIContext) {
	if ctx.PathParamInt64("rule_id") <= 0 {
		ctx.APIError(http.StatusBadRequest, "invalid rule ID")
		return
	}
	_, err := governance_service.SaveGroupBranchProtection(ctx, requestActor(ctx), governance_service.GroupBranchProtectionInput{
		ID: ctx.PathParamInt64("rule_id"), GroupID: ctx.PathParamInt64("id"), Revision: ctx.FormInt64("revision"), Delete: true,
	})
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
