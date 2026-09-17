// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"slices"

	"gitea.dev/models/db"

	"xorm.io/builder"
)

// ApprovalSettingSource 分别解释每个生效字段，不能用整行来源掩盖混合继承。
type ApprovalSettingSource struct {
	Value     bool   `json:"value"`
	Locked    bool   `json:"locked"`
	ScopeType string `json:"scope_type"`
	ScopeID   int64  `json:"scope_id"`
	Revision  int64  `json:"revision"`
}

type EffectiveApprovalSettings struct {
	Settings ApprovalSettings                 `json:"settings"`
	Sources  map[string]ApprovalSettingSource `json:"sources"`
}

func approvalSettingValues(s ApprovalSettings) map[string]bool {
	return map[string]bool{
		"prevent_author":           s.PreventAuthor,
		"prevent_committer":        s.PreventCommitter,
		"prevent_overrides":        s.PreventOverrides,
		"reset_on_change":          s.ResetOnChange,
		"require_reauthentication": s.RequireReauthentication,
	}
}

// ResolveApprovalSettings 的 ownerID 必须来自已加载的目标仓库，不能使用请求提交的群组 ID。
func ResolveApprovalSettings(ctx context.Context, repoID, ownerID int64) (*EffectiveApprovalSettings, error) {
	if repoID <= 0 || ownerID <= 0 {
		return nil, ErrInvalid
	}
	return resolveApprovalSettings(ctx, repoID, ownerID)
}

func ResolveScopeApprovalSettings(ctx context.Context, scope string, id int64) (*EffectiveApprovalSettings, error) {
	if scope == "instance" && id == 0 {
		return resolveApprovalSettings(ctx, 0, 0)
	}
	if scope == "group" && id > 0 {
		return resolveApprovalSettings(ctx, 0, id)
	}
	return nil, ErrInvalid
}

func resolveApprovalSettings(ctx context.Context, repoID, ownerID int64) (*EffectiveApprovalSettings, error) {
	result := &EffectiveApprovalSettings{Sources: make(map[string]ApprovalSettingSource)}
	defaults := ApprovalSettings{ScopeType: "repository", ScopeID: repoID, PreventAuthor: true, ResetOnChange: true}
	if repoID == 0 {
		defaults.ScopeType, defaults.ScopeID = "group", ownerID
		if ownerID == 0 {
			defaults.ScopeType = "instance"
		}
	}
	result.Settings = defaults
	for name, value := range approvalSettingValues(defaults) {
		result.Sources[name] = ApprovalSettingSource{Value: value, ScopeType: "default"}
	}
	// 没有任何设置时无需层级查询，兼容尚未接入治理的原生评审。
	configured, err := db.GetEngine(ctx).Exist(new(ApprovalSettings))
	if err != nil || !configured {
		return result, err
	}
	var chain []*Namespace
	if ownerID > 0 {
		chain, err = Ancestors(ctx, ownerID)
	}

	if err != nil {
		return nil, err
	}
	slices.Reverse(chain)
	groupIDs := make([]int64, 0, len(chain))
	for _, group := range chain {
		if group.Kind == "group" {
			groupIDs = append(groupIDs, group.ID)
		}
	}
	scopes := builder.Or(builder.Eq{"scope_type": "instance", "scope_id": 0})
	if repoID > 0 {
		scopes = scopes.Or(builder.Eq{"scope_type": "repository", "scope_id": repoID})
	}
	if len(groupIDs) > 0 {
		scopes = scopes.Or(builder.And(builder.Eq{"scope_type": "group"}, builder.In("scope_id", groupIDs)))
	}
	var rows []ApprovalSettings
	if err := db.GetEngine(ctx).Where(scopes).Find(&rows); err != nil {
		return nil, err
	}
	byScope := make(map[string]ApprovalSettings, len(rows))
	for _, row := range rows {
		byScope[Resource(row.ScopeType, row.ScopeID)] = row
	}
	order := []string{Resource("instance", 0)}
	for _, id := range groupIDs {
		order = append(order, Resource("group", id))
	}
	if repoID > 0 {
		order = append(order, Resource("repository", repoID))
	}
	for _, scope := range order {
		row, exists := byScope[scope]
		if !exists || row.Inherit {
			continue
		}
		for name, value := range approvalSettingValues(row) {
			if result.Sources[name].Locked {
				continue
			}
			// 群组开启的限制锁定后代；关闭只提供默认值，项目仍可配置。
			locked := row.Locked || row.ScopeType == "instance" || row.ScopeType == "group" && value && name != "reset_on_change"
			result.Sources[name] = ApprovalSettingSource{Value: value, Locked: locked, ScopeType: row.ScopeType, ScopeID: row.ScopeID, Revision: row.Revision}
		}
	}
	result.Settings = ApprovalSettings{
		ScopeType: "repository", ScopeID: repoID,
		PreventAuthor:           result.Sources["prevent_author"].Value,
		PreventCommitter:        result.Sources["prevent_committer"].Value,
		PreventOverrides:        result.Sources["prevent_overrides"].Value,
		ResetOnChange:           result.Sources["reset_on_change"].Value,
		RequireReauthentication: result.Sources["require_reauthentication"].Value,
	}
	if repoID == 0 {
		result.Settings.ScopeType, result.Settings.ScopeID = "group", ownerID
		if ownerID == 0 {
			result.Settings.ScopeType = "instance"
		}
	}
	return result, nil
}
