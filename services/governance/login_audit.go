// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"

	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
)

// RecordLogin 关联登录授权与会话结果；未知中断保留 pending，不推测登录成功。
func RecordLogin(ctx context.Context, actor governance_model.Actor, result, method, claimedLogin string) error {
	return recordLogin(ctx, actor, actor.ID, actor.Name, result, method, claimedLogin)
}

// RecordFailedLogin 区分尚未认证的访问者与被尝试登录的已知账号。
func RecordFailedLogin(ctx context.Context, targetID int64, claimedLogin, remoteAddr, method string) error {
	actor := RequestActor(&user_model.User{Name: "未认证访问者"}, remoteAddr, "web")
	actor.Kind = "anonymous"
	path := "未确认账号"
	if targetID > 0 {
		target, err := user_model.GetUserByID(ctx, targetID)
		if err != nil {
			return err
		}
		path = target.Name
	}
	return recordLogin(ctx, actor, targetID, path, "failure", method, claimedLogin)
}

func recordLogin(ctx context.Context, actor governance_model.Actor, targetID int64, path, result, method, claimedLogin string) error {
	kind := map[string]string{"pending": "authentication.login_authorized", "success": "authentication.login_succeeded", "failure": "authentication.login_failed"}[result]
	if kind == "" {
		return governance_model.ErrInvalid
	}
	scope, scopeID := "user", targetID
	if targetID == 0 {
		scope, scopeID = "instance", 0
	}
	claimed := []rune(claimedLogin)
	if len(claimed) > 100 {
		claimed = claimed[:100]
	}
	details, err := json.Marshal(map[string]any{"method": method, "claimed_login": string(claimed)})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: kind, Actor: actor, ScopeType: scope, ScopeID: scopeID, ObjectType: "user", ObjectID: targetID, ObjectPath: path, Result: result, Details: details})
}
