// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"gitea.dev/models/db"
	"gitea.dev/modules/json"
)

// RuleCandidates 的候选人必须由服务端按当前身份、访问权限和直接成员关系产生。
type RuleCandidates struct {
	Rule    ApprovalRule
	UserIDs []int64
	// NativeApprovedIDs 仅由服务端读取仍有效的原生评审，不能从配置请求提供。
	NativeApprovedIDs []int64
}

type RuleResult struct {
	SubjectsHidden  bool    `json:"subjects_hidden,omitempty"`
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	Required        int     `json:"required"`
	ApprovedUserIDs []int64 `json:"approved_user_ids"`
	EligibleCount   int     `json:"eligible_count"`
	Missing         int     `json:"missing"`
	Satisfied       bool    `json:"satisfied"`
	NeedsAttention  bool    `json:"needs_attention"`
	Optional        bool    `json:"optional"`
	ScopeType       string  `json:"scope_type"`
	ScopeID         int64   `json:"scope_id"`
	Revision        int64   `json:"revision"`
}

type ApprovalState struct {
	Satisfied  bool         `json:"satisfied"`
	Generation int64        `json:"generation"`
	Head       string       `json:"head"`
	Rules      []RuleResult `json:"rules"`
}

func ValidateApprovalRule(rule *ApprovalRule) error {
	if strings.TrimSpace(rule.Name) == "" || len([]rune(rule.Name)) > 100 || rule.Required < 0 || (rule.Required > 100 && rule.NativeProtectionID == 0) ||
		!slices.Contains([]string{"instance", "group", "repository", "pull"}, rule.ScopeType) ||
		!slices.Contains([]string{"all", "protected", "branches", "protection_rules", "protection_ids"}, rule.BranchMode) {
		return ErrInvalid
	}
	if rule.ScopeID < 0 || (rule.ScopeType == "instance") != (rule.ScopeID == 0) {
		return ErrInvalid
	}
	if (rule.BranchMode == "branches" || rule.BranchMode == "protection_rules") && len(rule.Branches) == 0 {
		return ErrInvalid
	}
	if rule.BranchMode == "protection_ids" && rule.Enabled && len(rule.ProtectionIDs) == 0 {
		return ErrInvalid
	}
	for _, ids := range [][]int64{rule.UserIDs, rule.GroupIDs, rule.TeamIDs, rule.ProtectionIDs} {
		for _, id := range ids {
			if id <= 0 {
				return ErrInvalid
			}
		}
	}
	return nil
}

// CountApprovalRules 独立逐条计票。同一个人可满足多条规则，但每条规则只计一票。
// 总人数以单独的 AllEligible 规则表达；不能用所有规则票数相加代替。
func CountApprovalRules(version PullVersion, rules []RuleCandidates, approvals []ApprovalEvidence, requireReauthentication bool) (ApprovalState, error) {
	state := ApprovalState{Satisfied: true, Generation: version.Generation, Head: version.Head, Rules: make([]RuleResult, 0, len(rules))}
	approved := make(map[int64]bool)
	recorded := make(map[int64]bool)
	for _, approval := range approvals {
		if approval.PullID == version.PullID {
			recorded[approval.UserID] = true
		}
		if approval.PullID == version.PullID && approval.UserID > 0 && approval.Generation == version.Generation &&
			(!requireReauthentication || approval.Reauthenticated) {
			approved[approval.UserID] = true
		}
	}
	for _, candidates := range rules {
		rule := candidates.Rule
		if err := ValidateApprovalRule(&rule); err != nil {
			return ApprovalState{}, err
		}
		if !rule.Enabled {
			continue
		}
		result := RuleResult{
			ID: rule.ID, Name: rule.Name, Required: rule.Required, Optional: rule.Required == 0,
			ScopeType: rule.ScopeType, ScopeID: rule.ScopeID, Revision: rule.Revision, ApprovedUserIDs: []int64{},
		}
		ruleApproved := approved
		if rule.NativeProtectionID > 0 {
			ruleApproved = make(map[int64]bool)
			for _, id := range candidates.NativeApprovedIDs {
				// 已有差异证据的评审必须满足当前代次及认证要求；不能退回历史票。
				// 无证据的迁移历史仅在初始差异代次且未要求重新认证时保留。
				ruleApproved[id] = approved[id] || (!recorded[id] && version.Generation == 1 && !requireReauthentication)
			}
		}
		eligible := make(map[int64]bool)
		for _, id := range candidates.UserIDs {
			if id <= 0 || eligible[id] {
				continue
			}
			eligible[id] = true
			if ruleApproved[id] {
				result.ApprovedUserIDs = append(result.ApprovedUserIDs, id)
			}
		}
		slices.Sort(result.ApprovedUserIDs)
		result.EligibleCount = len(eligible)
		result.Missing = max(0, rule.Required-len(result.ApprovedUserIDs))
		result.Satisfied = result.Missing == 0
		result.NeedsAttention = rule.Required > len(eligible)
		state.Satisfied = state.Satisfied && result.Satisfied
		state.Rules = append(state.Rules, result)
	}
	return state, nil
}

// AdvancePullVersion 每次已确认的代码变化推进版本，防止 A→B→A 恢复旧票。
// 调用者必须从原生 Git 引用变更链逐次传入 expectedHead，不能只轮询最终提交。
func AdvancePullVersion(ctx context.Context, next PullVersion, expectedHead string, resetOnChange bool, auditScope ...*PullVersionAuditScope) (*PullVersion, error) {
	if next.PullID <= 0 || next.Head == "" || next.BaseBranch == "" || next.PatchID == "" {
		return nil, ErrInvalid
	}
	var result *PullVersion
	err := WithWrite(ctx, []string{Resource("pull", next.PullID)}, func(ctx context.Context) error {
		previous, has, err := db.GetByID[PullVersion](ctx, next.PullID)
		if err != nil {
			return err
		}
		if !has {
			if expectedHead != "" {
				return fmt.Errorf("%w：缺少起始差异版本", ErrConflict)
			}
			next.Generation = 1
			result = &next
			return db.Insert(ctx, &next)
		}
		if previous.Head != expectedHead {
			return fmt.Errorf("%w：差异版本已变化", ErrConflict)
		}
		next.Generation = previous.Generation
		if previous.BaseBranch != next.BaseBranch || (resetOnChange && previous.PatchID != next.PatchID) {
			next.Generation++
		}
		_, err = db.GetEngine(ctx).Where("pull_id = ?", next.PullID).AllCols().Update(&next)
		result = &next
		if err != nil {
			return err
		}
		if next.Generation != previous.Generation && len(auditScope) > 0 && auditScope[0] != nil {
			scope := auditScope[0]
			details, err := json.Marshal(map[string]any{"before": previous, "after": next, "reason": "代码差异或目标分支变化"})
			if err != nil {
				return err
			}
			return AppendAudit(ctx, &AuditEvent{Type: "approval.invalidated", Actor: AuditActor(ctx), ScopeType: "repository", ScopeID: scope.RepoID, AncestorIDs: scope.AncestorIDs, ObjectType: "pull", ObjectID: next.PullID, ObjectPath: scope.Path, Result: "success", Details: details})
		}
		return nil
	})
	return result, err
}

// PullVersionAuditScope 随引用事务保存目标项目归属，恢复不能使用 Fork 源项目的审计范围。
type PullVersionAuditScope struct {
	RepoID      int64   `json:"repository_id"`
	Path        string  `json:"path"`
	AncestorIDs []int64 `json:"ancestor_ids"`
}

type reauthenticatedApprovalKey struct{}

// WithReauthenticatedApproval 仅在原生密码认证成功后设置，不能从请求正文复制认证结果。
func WithReauthenticatedApproval(ctx context.Context) context.Context {
	return context.WithValue(ctx, reauthenticatedApprovalKey{}, true)
}

func IsApprovalReauthenticated(ctx context.Context) bool {
	verified, _ := ctx.Value(reauthenticatedApprovalKey{}).(bool)
	return verified
}
