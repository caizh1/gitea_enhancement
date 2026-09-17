// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package auth

import (
	"context"

	governance_model "gitea.dev/models/governance"
)

func appendCredentialAudit(ctx context.Context, eventType, kind, name string, id, userID int64) error {
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{
		Type: eventType, Actor: governance_model.AuditActor(ctx), ScopeType: "user", ScopeID: userID,
		ObjectType: kind, ObjectID: id, ObjectPath: name, Result: "success",
	})
}

// DeleteUserMFA 供管理员重置与账号删除共用，任一审计失败均回滚全部凭据删除。
func DeleteUserMFA(ctx context.Context, userID int64) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("user", userID)}, func(ctx context.Context) error {
		if tf, err := GetTwoFactorByUID(ctx, userID); err != nil && !IsErrTwoFactorNotEnrolled(err) {
			return err
		} else if tf != nil {
			if err := DeleteTwoFactorByID(ctx, tf.ID, userID); err != nil {
				return err
			}
		}
		credentials, err := GetWebAuthnCredentialsByUID(ctx, userID)
		if err != nil {
			return err
		}
		for _, credential := range credentials {
			if _, err := DeleteCredential(ctx, credential.ID, userID); err != nil {
				return err
			}
		}
		return nil
	})
}
