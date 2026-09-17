// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import "context"

type auditActorKey struct{}

// AuditActorContextKey 供原生请求上下文在认证及管理员代办完成后保存真实身份。
var AuditActorContextKey = auditActorKey{}

// WithAuditActor 只接受入口完成认证后构造的身份，不读取请求正文中的操作者字段。
func WithAuditActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, AuditActorContextKey, actor)
}

func AuditActor(ctx context.Context) Actor {
	if actor, ok := ctx.Value(AuditActorContextKey).(Actor); ok {
		return actor
	}
	return Actor{Kind: "system", Name: "Gitea 内部任务", Transport: "internal"}
}
