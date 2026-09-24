// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package governance

import (
	"strconv"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

func SaveGroupBranchProtection(ctx *context.Context) {
	groupID := ctx.PathParamInt64("id")
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	_, err := governance_service.SaveGroupBranchProtection(ctx, actor, governance_service.GroupBranchProtectionInput{
		ID: ctx.FormInt64("rule_id"), GroupID: groupID, Revision: ctx.FormInt64("revision"),
		RuleName: ctx.FormString("rule_name"), PushRole: governance_model.Role(ctx.FormInt("push_role")),
		MergeRole: governance_model.Role(ctx.FormInt("merge_role")), AllowForcePush: ctx.FormBool("allow_force_push"),
		Delete: ctx.FormString("action") == "delete",
	})
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Redirect(setting.AppSubURL + "/governance/groups/" + strconv.FormatInt(groupID, 10) + "?tab=settings")
}
