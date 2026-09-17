// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"strings"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
)

type ScopeApprovalSettingsOption struct {
	ApprovalSettingsOption
	Locked bool `json:"locked"`
}

type ScopeApprovalSettings struct {
	Local     *governance_model.ApprovalSettings          `json:"local"`
	Effective *governance_model.EffectiveApprovalSettings `json:"effective"`
}

func ListScopeApprovalSettings(ctx context.Context, actorID int64, scope string, id int64) (*ScopeApprovalSettings, error) {
	if _, err := checkApprovalPolicyAccess(ctx, actorID, scope, id); err != nil {
		return nil, err
	}
	result := &ScopeApprovalSettings{Local: &governance_model.ApprovalSettings{ScopeType: scope, ScopeID: id}}
	if _, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", scope, id).Get(result.Local); err != nil {
		return nil, err
	}
	var err error
	result.Effective, err = governance_model.ResolveScopeApprovalSettings(ctx, scope, id)
	return result, err
}

// SaveScopeApprovalSettings 在改变继承前保留 PR 快照，解除锁定后不复活早先被替换的规则。
func SaveScopeApprovalSettings(ctx context.Context, actor governance_model.Actor, scope string, id int64, option ScopeApprovalSettingsOption, remove bool) (*governance_model.ApprovalSettings, error) {
	var result *governance_model.ApprovalSettings
	err := withActorWrite(ctx, actor, []string{governance_model.Resource(scope, id)}, func(ctx context.Context) error {
		path, err := checkApprovalPolicyAccess(ctx, actor.EffectiveUserID(), scope, id)
		if err != nil {
			return err
		}
		state, err := ListScopeApprovalSettings(ctx, actor.EffectiveUserID(), scope, id)
		if err != nil {
			return err
		}
		previous := state.Local
		if previous.Revision != option.Revision {
			return governance_model.ErrConflict
		}
		if remove && (previous.ID == 0 || previous.Inherit) {
			return governance_model.ErrNotFound
		}
		if !remove {
			values := map[string]bool{"prevent_author": option.PreventAuthor, "prevent_committer": option.PreventCommitter, "prevent_overrides": option.PreventOverrides, "reset_on_change": option.ResetOnChange, "require_reauthentication": option.RequireReauthentication}
			for name, value := range values {
				source := state.Effective.Sources[name]
				if source.Locked && (source.ScopeType != scope || source.ScopeID != id) && source.Value != value {
					return governance_model.ErrForbidden
				}
			}
			if option.RequireReauthentication {
				supported, err := CanReauthenticateApproval(ctx, actor.EffectiveUserID())
				if err != nil {
					return err
				}
				if !supported {
					return fmt.Errorf("%w：当前账号没有可用的再次认证方式，不能启用此设置", governance_model.ErrConflict)
				}
			}
		}
		var repositories []struct {
			ID      int64
			OwnerID int64
		}
		var groupPath string
		if scope == "group" {
			group, err := governance_model.GetNamespace(ctx, id)
			if err != nil {
				return err
			}
			groupPath = group.LowerPath
		}
		// 事务共用会话，先完成命名空间读取，再构造仓库查询。
		query := db.GetEngine(ctx).Table("repository").Select("repository.id, repository.owner_id")
		if scope == "group" {
			prefix := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(groupPath) + "/%"
			query = query.Join("INNER", "governance_namespace", "governance_namespace.id = repository.owner_id").Where("governance_namespace.lower_path = ? OR governance_namespace.lower_path LIKE ? ESCAPE '!'", groupPath, prefix)
		}
		if err := query.Find(&repositories); err != nil {
			return err
		}
		before := make(map[int64]bool, len(repositories))
		for _, repo := range repositories {
			settings, err := governance_model.ResolveApprovalSettings(ctx, repo.ID, repo.OwnerID)
			if err != nil {
				return err
			}
			before[repo.ID] = settings.Settings.PreventOverrides
			if err := snapshotRepositoryPullRules(ctx, repo.ID, false); err != nil {
				return err
			}
		}
		if remove {
			result = &governance_model.ApprovalSettings{ID: previous.ID, ScopeType: scope, ScopeID: id, Inherit: true, Revision: previous.Revision + 1}
			_, err = db.GetEngine(ctx).ID(previous.ID).AllCols().Update(result)
		} else {
			result = &governance_model.ApprovalSettings{ID: previous.ID, ScopeType: scope, ScopeID: id, PreventAuthor: option.PreventAuthor, PreventCommitter: option.PreventCommitter, PreventOverrides: option.PreventOverrides, ResetOnChange: option.ResetOnChange, RequireReauthentication: option.RequireReauthentication, Locked: option.Locked, Revision: option.Revision + 1}
			if previous.ID == 0 {
				err = db.Insert(ctx, result)
			} else {
				_, err = db.GetEngine(ctx).ID(previous.ID).AllCols().Update(result)
			}
		}
		if err != nil {
			return err
		}
		for _, repo := range repositories {
			settings, err := governance_model.ResolveApprovalSettings(ctx, repo.ID, repo.OwnerID)
			if err != nil {
				return err
			}
			if before[repo.ID] || settings.Settings.PreventOverrides {
				if err := snapshotRepositoryPullRules(ctx, repo.ID, true); err != nil {
					return err
				}
			}
		}
		details, err := json.Marshal(map[string]any{"before": previous, "after": result})
		if err != nil {
			return err
		}
		ancestors := []int64{}
		if scope == "group" {
			ancestors = append(ancestors, id)
		}
		objectID := previous.ID
		if result != nil {
			objectID = result.ID
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "approval.settings_changed", Actor: actor, ScopeType: scope, ScopeID: id, AncestorIDs: ancestors, ObjectType: "approval_settings", ObjectID: objectID, ObjectPath: path, Result: "success", Details: details})
	})
	return result, err
}
