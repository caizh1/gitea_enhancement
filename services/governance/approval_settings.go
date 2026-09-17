// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
)

type ApprovalSettingsOption struct {
	PreventAuthor           bool  `json:"prevent_author"`
	PreventCommitter        bool  `json:"prevent_committer"`
	PreventOverrides        bool  `json:"prevent_overrides"`
	ResetOnChange           bool  `json:"reset_on_change"`
	RequireReauthentication bool  `json:"require_reauthentication"`
	Revision                int64 `json:"revision"`
}

func SaveRepositoryApprovalSettings(ctx context.Context, actor governance_model.Actor, repoID int64, option ApprovalSettingsOption) (*governance_model.ApprovalSettings, error) {
	var result *governance_model.ApprovalSettings
	err := withActorWrite(ctx, actor, []string{governance_model.Resource("repository", repoID)}, func(ctx context.Context) error {
		repo, _, err := checkApprovalRuleAccess(ctx, actor.EffectiveUserID(), repoID, true)
		if err != nil {
			return err
		}
		chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
		if err != nil {
			return err
		}
		if repo.IsArchived {
			return governance_model.ErrConflict
		}
		var ancestors []int64
		for _, ancestor := range chain {
			if ancestor.Archived || ancestor.DeleteAfter != 0 {
				return governance_model.ErrConflict
			}
			if ancestor.Kind == "group" {
				ancestors = append(ancestors, ancestor.ID)
			}
		}
		var previous governance_model.ApprovalSettings
		has, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repoID).Get(&previous)
		if err != nil {
			return err
		}
		if has && previous.Revision != option.Revision || !has && option.Revision != 0 {
			return governance_model.ErrConflict
		}
		effective, err := governance_model.ResolveApprovalSettings(ctx, repoID, repo.OwnerID)
		if err != nil {
			return err
		}
		values := map[string]bool{"prevent_author": option.PreventAuthor, "prevent_committer": option.PreventCommitter, "prevent_overrides": option.PreventOverrides, "reset_on_change": option.ResetOnChange, "require_reauthentication": option.RequireReauthentication}
		for name, value := range values {
			source := effective.Sources[name]
			if source.Locked && source.Value != value {
				return fmt.Errorf("%w：不能修改继承锁定的审批设置", governance_model.ErrForbidden)
			}
		}
		if option.RequireReauthentication && !effective.Settings.RequireReauthentication {
			supported, err := CanReauthenticateApproval(ctx, actor.EffectiveUserID())
			if err != nil {
				return err
			}
			if !supported {
				return fmt.Errorf("%w：当前账号没有可用的再次认证方式，不能启用此设置", governance_model.ErrConflict)
			}
		}
		if err := snapshotRepositoryPullRules(ctx, repoID, false); err != nil {
			return err
		}
		// 关闭覆盖时同步当前模板；重新允许覆盖前也保留此前实际生效的规则。
		if option.PreventOverrides || effective.Settings.PreventOverrides {
			if err := snapshotRepositoryPullRules(ctx, repoID, true); err != nil {
				return err
			}
		}
		result = &governance_model.ApprovalSettings{ID: previous.ID, ScopeType: "repository", ScopeID: repoID, PreventAuthor: option.PreventAuthor, PreventCommitter: option.PreventCommitter, PreventOverrides: option.PreventOverrides, ResetOnChange: option.ResetOnChange, RequireReauthentication: option.RequireReauthentication, Revision: option.Revision + 1}
		if has {
			_, err = db.GetEngine(ctx).ID(previous.ID).AllCols().Update(result)
		} else {
			err = db.Insert(ctx, result)
		}
		if err != nil {
			return err
		}
		details, err := json.Marshal(map[string]any{"before": previous, "after": result})
		if err != nil {
			return err
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "approval.settings_changed", Actor: actor, ScopeType: "repository", ScopeID: repoID, AncestorIDs: ancestors, ObjectType: "approval_settings", ObjectID: result.ID, ObjectPath: repo.FullPath(), Result: "success", Details: details})
	})
	return result, err
}
