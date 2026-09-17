// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/services/auth/password"

	_ "gitea.dev/services/auth/source/db" // 注册原生本地密码认证，独立治理服务测试同样需要此来源。
)

// ReviewAuthentication 仅在本次请求中传递；不得写入日志、事件或持久化表。
type ReviewAuthentication struct {
	Password string
	Actor    governance_model.Actor
}

func CanReauthenticateApproval(ctx context.Context, actorID int64) (bool, error) {
	if !setting.Service.EnablePasswordSignInForm && !setting.Service.EnableBasicAuth {
		return false, nil
	}
	user, err := activeActor(ctx, actorID)
	if err != nil {
		return false, err
	}
	source, err := auth_model.GetSourceByID(ctx, user.LoginSource)
	if err != nil {
		return false, err
	}
	_, supported := source.Cfg.(password.Authenticator)
	return source.IsActive && supported && (user.LoginSource != 0 || user.IsPasswordSet()), nil
}

// ReauthenticateApproval 复用已存在账号的密码认证，不进行账号注册或切换身份。
func ReauthenticateApproval(ctx context.Context, actorID int64, authentication ReviewAuthentication) error {
	if authentication.Actor.EffectiveUserID() != actorID || authentication.Password == "" || len(authentication.Password) > 4096 {
		return fmt.Errorf("%w：批准前需要再次验证当前账号", governance_model.ErrForbidden)
	}
	supported, err := CanReauthenticateApproval(ctx, actorID)
	if err != nil {
		return err
	}
	if !supported {
		return fmt.Errorf("%w：当前账号没有可用的密码再次认证方式", governance_model.ErrForbidden)
	}
	// ponytail: 单节点治理事务同时记录尝试与限速；容量扩展时再拆分认证计数表。
	err = governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		attempts, err := db.GetEngine(ctx).Where("actor_id = ? AND type = ? AND occurred_at >= ?", actorID, "approval.reauthentication_attempt", time.Now().UTC().Add(-time.Minute)).Count(new(governance_model.AuditEvent))
		if err != nil {
			return err
		}
		if attempts >= 5 {
			return fmt.Errorf("%w：再次认证请求过于频繁，请稍后再试", governance_model.ErrConflict)
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "approval.reauthentication_attempt", Actor: authentication.Actor, ScopeType: "user", ScopeID: actorID, ObjectType: "user", ObjectID: actorID, ObjectPath: authentication.Actor.Name, Result: "unknown"})
	})
	if err != nil {
		return err
	}
	user, err := user_model.GetUserByID(ctx, actorID)
	if err != nil {
		return err
	}
	source, err := auth_model.GetSourceByID(ctx, user.LoginSource)
	if err != nil {
		return err
	}
	authenticator, supported := source.Cfg.(password.Authenticator)
	if !source.IsActive || !supported {
		return governance_model.ErrForbidden
	}
	authenticated, authErr := authenticator.Authenticate(ctx, user, user.LoginName, authentication.Password)
	result := "failure"
	if authErr == nil && authenticated != nil && authenticated.ID == actorID && authenticated.IsActive && !authenticated.ProhibitLogin {
		result = "success"
	}
	if err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "approval.reauthenticated", Actor: authentication.Actor, ScopeType: "user", ScopeID: actorID, ObjectType: "user", ObjectID: actorID, ObjectPath: user.Name, Result: result})
	}); err != nil {
		return err
	}
	if result != "success" {
		return fmt.Errorf("%w：再次认证失败", governance_model.ErrForbidden)
	}
	return nil
}
