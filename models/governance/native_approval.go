// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"

	"gitea.dev/models/db"
)

// NativeBranchApproval 仅用于兼容原生字段；审批规则表是运行时配置真源。
type NativeBranchApproval struct {
	ID                        int64 `xorm:"pk"`
	RepoID                    int64
	RuleName                  string `xorm:"'branch_name'"`
	RequiredApprovals         int64
	EnableApprovalsWhitelist  bool
	ApprovalsWhitelistUserIDs []int64 `xorm:"JSON TEXT"`
	ApprovalsWhitelistTeamIDs []int64 `xorm:"JSON TEXT"`
	IgnoreStaleApprovals      bool
	DismissStaleApprovals     bool
}

func (*NativeBranchApproval) TableName() string { return "protected_branch" }

// SyncNativeBranchApproval 必须在保护规则保存事务内调用，旧客户端更新也只产生同一条规则。
func SyncNativeBranchApproval(ctx context.Context, branch *NativeBranchApproval) error {
	var previous ApprovalRule
	has, err := db.GetEngine(ctx).Where("native_protection_id = ?", branch.ID).Get(&previous)
	if err != nil {
		return err
	}
	if branch.ID <= 0 || branch.RepoID <= 0 || branch.RequiredApprovals < 0 {
		return ErrInvalid
	}
	next := previous
	if !has {
		next.Name = fmt.Sprintf("总人数要求 · #%d", branch.ID)
		next.Revision = 0
	}
	next.ScopeType, next.ScopeID = "repository", branch.RepoID
	next.Required = int(branch.RequiredApprovals)
	next.NativeProtectionID, next.ProtectionIDs = branch.ID, []int64{branch.ID}
	next.BranchMode, next.Branches = "protection_ids", nil
	next.AllEligible = !branch.EnableApprovalsWhitelist
	next.UserIDs, next.TeamIDs = slices.Clone(branch.ApprovalsWhitelistUserIDs), slices.Clone(branch.ApprovalsWhitelistTeamIDs)
	next.GroupIDs = nil
	next.NativeIgnoreStale, next.NativeDismissStale = branch.IgnoreStaleApprovals, branch.DismissStaleApprovals
	next.RespectNativeApprovalPool, next.Enabled, next.Locked = true, true, true
	if reflect.DeepEqual(previous, next) {
		return nil
	}
	next.Revision++
	if !has {
		return db.Insert(ctx, &next)
	}
	_, err = db.GetEngine(ctx).ID(next.ID).AllCols().Update(&next)
	return err
}

// WriteNativeApprovalProjection 只更新审批兼容字段，审批管理权限不能借此修改推送保护。
func WriteNativeApprovalProjection(ctx context.Context, rule *ApprovalRule) error {
	if rule.NativeProtectionID == 0 {
		return nil
	}
	if rule.ScopeType != "repository" || !rule.Enabled || len(rule.GroupIDs) != 0 {
		return ErrInvalid
	}
	branch := &NativeBranchApproval{RequiredApprovals: int64(rule.Required), EnableApprovalsWhitelist: !rule.AllEligible,
		ApprovalsWhitelistUserIDs: rule.UserIDs, ApprovalsWhitelistTeamIDs: rule.TeamIDs,
		IgnoreStaleApprovals: rule.NativeIgnoreStale, DismissStaleApprovals: rule.NativeDismissStale}
	n, err := db.GetEngine(ctx).Where("id = ? AND repo_id = ?", rule.NativeProtectionID, rule.ScopeID).
		Cols("required_approvals", "enable_approvals_whitelist", "approvals_whitelist_user_i_ds", "approvals_whitelist_team_i_ds", "ignore_stale_approvals", "dismiss_stale_approvals").Update(branch)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func DetachProtectionApprovalRules(ctx context.Context, repoID, protectionID int64) error {
	var rules []ApprovalRule
	if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repoID).Find(&rules); err != nil {
		return err
	}
	for _, rule := range rules {
		if rule.NativeProtectionID == protectionID {
			if _, err := db.GetEngine(ctx).ID(rule.ID).Delete(new(ApprovalRule)); err != nil {
				return err
			}
			continue
		}
		if !slices.Contains(rule.ProtectionIDs, protectionID) {
			continue
		}
		rule.ProtectionIDs = slices.DeleteFunc(rule.ProtectionIDs, func(id int64) bool { return id == protectionID })
		if len(rule.ProtectionIDs) == 0 {
			rule.Enabled = false
		}
		rule.Revision++
		if _, err := db.GetEngine(ctx).ID(rule.ID).Cols("protection_i_ds", "enabled", "revision").Update(&rule); err != nil {
			return err
		}
	}
	return nil
}

// AddUnifiedBranchApprovals 保留旧评审与快照，只迁移配置和稳定关联；失败时阻止启动。
func AddUnifiedBranchApprovals(engine db.EngineMigration) error {
	if err := engine.Sync(new(ApprovalRule)); err != nil {
		return err
	}
	sess := engine.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}
	var branches []NativeBranchApproval
	if err := sess.Find(&branches); err != nil {
		return err
	}
	byRepo := map[int64]map[string]int64{}
	for _, branch := range branches {
		if byRepo[branch.RepoID] == nil {
			byRepo[branch.RepoID] = map[string]int64{}
		}
		byRepo[branch.RepoID][branch.RuleName] = branch.ID
		var existing ApprovalRule
		has, err := sess.Where("native_protection_id = ?", branch.ID).Get(&existing)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if branch.RequiredApprovals < 0 {
			return fmt.Errorf("保护规则 %d 的审批人数无效", branch.ID)
		}
		rule := ApprovalRule{ScopeType: "repository", ScopeID: branch.RepoID, Name: fmt.Sprintf("总人数要求 · #%d", branch.ID), Required: int(branch.RequiredApprovals),
			NativeProtectionID: branch.ID, ProtectionIDs: []int64{branch.ID}, BranchMode: "protection_ids", AllEligible: !branch.EnableApprovalsWhitelist,
			UserIDs: branch.ApprovalsWhitelistUserIDs, TeamIDs: branch.ApprovalsWhitelistTeamIDs, NativeIgnoreStale: branch.IgnoreStaleApprovals,
			NativeDismissStale: branch.DismissStaleApprovals, RespectNativeApprovalPool: true, Enabled: true, Locked: true, Revision: 1}
		if _, err := sess.Insert(&rule); err != nil {
			return err
		}
	}
	var rules []ApprovalRule
	if err := sess.Find(&rules); err != nil {
		return err
	}
	for _, rule := range rules {
		if err := ValidateApprovalRule(&rule); err != nil {
			return fmt.Errorf("审批规则 %d 无法等价迁移：%w", rule.ID, err)
		}
		for _, subject := range []struct {
			table string
			ids   []int64
		}{{"user", rule.UserIDs}, {"team", rule.TeamIDs}, {"governance_namespace", rule.GroupIDs}} {
			ids := slices.Clone(subject.ids)
			slices.Sort(ids)
			ids = slices.Compact(ids)
			if len(ids) == 0 {
				continue
			}
			count, err := sess.Table(subject.table).In("id", ids).Count()
			if err != nil {
				return err
			}
			if count != int64(len(ids)) {
				return fmt.Errorf("审批规则 %d 存在已删除的 %s 主体，需先核对修复", rule.ID, subject.table)
			}
		}
		if rule.ScopeType != "repository" || rule.BranchMode != "protection_rules" {
			continue
		}
		for _, name := range rule.Branches {
			id := byRepo[rule.ScopeID][name]
			if id == 0 {
				return fmt.Errorf("审批规则 %d 引用了不存在的保护规则 %s，需先修复关联", rule.ID, name)
			}
			rule.ProtectionIDs = append(rule.ProtectionIDs, id)
		}
		rule.BranchMode, rule.Branches = "protection_ids", nil
		if _, err := sess.ID(rule.ID).Cols("branch_mode", "branches", "protection_i_ds").Update(&rule); err != nil {
			return err
		}
	}
	// 追加当前 PR 的稳定关联版本；历史快照内容保持不变。
	var snapshots []PullRuleVersion
	if err := sess.Where("id IN (SELECT MAX(id) FROM governance_pull_rule_version GROUP BY pull_id)").Find(&snapshots); err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		changed := false
		var pull struct{ BaseRepoID int64 }
		has, err := sess.Table("pull_request").Where("id = ?", snapshot.PullID).Get(&pull)
		if err != nil {
			return err
		}
		if !has {
			continue
		}
		for i := range snapshot.Rules {
			rule := &snapshot.Rules[i]
			if rule.BranchMode != "protection_rules" {
				continue
			}
			rule.ProtectionIDs = nil
			for _, name := range rule.Branches {
				id := byRepo[pull.BaseRepoID][name]
				if id == 0 {
					return fmt.Errorf("PR %d 的审批快照引用不存在的保护规则 %s", snapshot.PullID, name)
				}
				rule.ProtectionIDs = append(rule.ProtectionIDs, id)
			}
			rule.BranchMode, rule.Branches = "protection_ids", nil
			changed = true
		}
		if changed {
			snapshot.ID = 0
			snapshot.Revision++
			snapshot.CreatedAt = time.Now().UTC()
			if _, err := sess.Insert(&snapshot); err != nil {
				return err
			}
		}
	}
	return sess.Commit()
}
