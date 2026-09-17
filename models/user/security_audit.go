// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package user

import (
	"context"
	"net/mail"
	"slices"
	"strings"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
)

func securityAuditValues(u *User) map[string]any {
	// 明确白名单；不能序列化整个账号，避免密码摘要、盐和恢复凭据进入审计。
	return map[string]any{
		"is_active": u.IsActive, "prohibit_login": u.ProhibitLogin, "is_admin": u.IsAdmin,
		"is_restricted": u.IsRestricted, "allow_git_hook": u.AllowGitHook,
		"allow_import_local": u.AllowImportLocal, "allow_create_organization": u.AllowCreateOrganization,
		"repo_admin_change_team_access": u.RepoAdminChangeTeamAccess, "max_repo_creation": u.MaxRepoCreation,
		"login_type": u.LoginType, "login_source": u.LoginSource, "login_name": u.LoginName,
		"must_change_password": u.MustChangePassword,
	}
}

// AppendLifecycleAudit 记录账号生命周期，不包含邮箱、密码摘要、盐或个人资料正文。
func AppendLifecycleAudit(ctx context.Context, user *User, eventType string) error {
	details, err := json.Marshal(map[string]any{"name": user.Name, "login_type": user.LoginType, "login_source": user.LoginSource, "active": user.IsActive, "restricted": user.IsRestricted, "admin": user.IsAdmin, "auditor": user.IsAuditor})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: eventType, Actor: governance_model.AuditActor(ctx), ScopeType: "user", ScopeID: user.ID, ObjectType: "user", ObjectID: user.ID, ObjectPath: user.Name, Result: "success", Details: details})
}

// AppendEmailAudit 记录规范化邮箱和状态；不记录验证码或重置凭据。
func AppendEmailAudit(ctx context.Context, user *User, email *EmailAddress, eventType string) error {
	normalized := strings.ToLower(strings.TrimSpace(email.Email))
	details, err := json.Marshal(map[string]any{"email": normalized, "email_domain": emailAuditDomain(normalized), "primary": email.IsPrimary, "activated": email.IsActivated})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: eventType, Actor: governance_model.AuditActor(ctx), ScopeType: "user", ScopeID: user.ID, ObjectType: "email_address", ObjectID: email.ID, ObjectPath: user.Name + "/email", Result: "success", Details: details})
}

func AppendPrimaryEmailChangedAudit(ctx context.Context, user *User, before, after *EmailAddress) error {
	details, err := json.Marshal(map[string]any{"before_email": strings.ToLower(strings.TrimSpace(before.Email)), "after_email": strings.ToLower(strings.TrimSpace(after.Email)), "before_id": before.ID, "after_id": after.ID, "changed_fields": []string{"primary_email"}})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "credential.primary_email_changed", Actor: governance_model.AuditActor(ctx), ScopeType: "user", ScopeID: user.ID, ObjectType: "email_address", ObjectID: after.ID, ObjectPath: user.Name + "/primary-email", Result: "success", Details: details})
}

func emailAuditDomain(raw string) string {
	parsed, err := mail.ParseAddress(raw)
	if err != nil {
		return ""
	}
	if at := strings.LastIndexByte(parsed.Address, '@'); at >= 0 {
		return strings.ToLower(parsed.Address[at+1:])
	}
	return ""
}

func needsSecurityAudit(cols []string) bool {
	if len(cols) == 0 {
		return true
	}
	return slices.ContainsFunc(cols, func(col string) bool {
		switch col {
		case "is_active", "prohibit_login", "is_admin", "is_restricted", "allow_git_hook", "allow_import_local", "allow_create_organization", "repo_admin_change_team_access", "max_repo_creation", "login_type", "login_source", "login_name", "must_change_password":
			return true
		default:
			return false
		}
	})
}

func appendSecurityAudit(ctx context.Context, before, after *User, cols []string) error {
	oldValues, newValues := securityAuditValues(before), securityAuditValues(after)
	previous, current := map[string]any{}, map[string]any{}
	for field, value := range newValues {
		if (len(cols) == 0 || slices.Contains(cols, field)) && value != oldValues[field] {
			previous[field], current[field] = oldValues[field], value
		}
	}
	if len(current) == 0 {
		return nil
	}
	details, err := json.Marshal(map[string]any{"before": previous, "after": current})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{
		Type: "user.security_changed", Actor: governance_model.AuditActor(ctx),
		ScopeType: "user", ScopeID: before.ID, ObjectType: "user", ObjectID: before.ID,
		ObjectPath: before.Name, Result: "success", Details: details,
	})
}
