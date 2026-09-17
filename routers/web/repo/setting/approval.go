// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package setting

import (
	"errors"
	"net/http"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

func prepareBranchApprovalEditor(ctx *context.Context, protectionID int64) error {
	state, err := governance_service.ReadBranchApprovals(ctx, ctx.Doer.ID, ctx.Repo.Repository.ID, protectionID)
	if err != nil {
		return err
	}
	choices, err := governance_service.ListApprovalChoices(ctx, ctx.Doer.ID, ctx.Repo.Repository.ID)
	if err != nil {
		return err
	}
	ctx.Data["BranchApprovals"], ctx.Data["ApprovalChoices"] = state, choices
	ctx.Data["ApprovalInitialUpdate"] = governance_service.BranchApprovalUpdate{Version: state.Version, ProtectionID: protectionID, Rules: []governance_service.ApprovalRuleChange{}}
	ctx.Data["ApprovalProjectLink"] = ctx.Repo.RepoLink + "/settings/pulls#pull-request-approvals"
	return nil
}

// RepositoryApprovalSettings 是仓库设置内的拉取请求区域，不依赖独立治理页面。
func RepositoryApprovalSettings(ctx *context.Context) {
	if err := prepareBranchApprovalEditor(ctx, 0); err != nil {
		ctx.NotFound(err)
		return
	}
	ctx.Data["PageIsSettingsPulls"], ctx.Data["ApprovalRepositoryMode"] = true, true
	ctx.HTML(http.StatusOK, "repo/settings/approvals")
}

func SaveRepositoryApprovalConfiguration(ctx *context.Context) {
	if ctx.FormString("action") == "install_reference_hook" {
		err := governance_service.InstallRepositoryReferenceHook(ctx, governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web"), ctx.Repo.Repository.ID)
		if err != nil {
			ctx.JSON(http.StatusConflict, map[string]string{"message": err.Error()})
			return
		}
		ctx.Redirect(ctx.Repo.RepoLink + "/settings/pulls")
		return
	}
	var option governance_service.BranchApprovalUpdate
	if err := json.Unmarshal([]byte(ctx.FormString("approval_configuration")), &option); err != nil {
		ctx.JSON(http.StatusUnprocessableEntity, map[string]string{"message": "审批草稿格式无效"})
		return
	}
	err := governance_service.SaveBranchApprovals(ctx, governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web"), ctx.Repo.Repository.ID, option, nil)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, governance_model.ErrConflict) {
			status = http.StatusConflict
		}
		if errors.Is(err, governance_model.ErrForbidden) {
			status = http.StatusForbidden
		}
		message := map[string]any{"message": err.Error()}
		var ruleError *governance_service.ApprovalRuleSaveError
		if errors.As(err, &ruleError) {
			message["rule_index"] = ruleError.Index
		}
		ctx.JSON(status, message)
		return
	}
	ctx.JSON(http.StatusOK, map[string]string{"redirect": ctx.Repo.RepoLink + "/settings/pulls"})
}
