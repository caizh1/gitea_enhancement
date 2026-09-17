// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package user

import (
	"context"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"

	"xorm.io/builder"
)

// AuditorCondition 直接读取账号状态，撤销后不依赖调用方持有的用户快照。
func AuditorCondition(id int64) builder.Cond {
	return builder.Eq{"id": id, "is_auditor": true, "is_active": true, "prohibit_login": false, "type": UserTypeIndividual}
}

func IsActiveAuditor(ctx context.Context, id int64) (bool, error) {
	if id <= 0 {
		return false, nil
	}
	return db.GetEngine(ctx).Where(AuditorCondition(id)).Exist(new(User))
}

func appendAuditorAudit(ctx context.Context, u *User, before bool) error {
	actor := governance_model.AuditActor(ctx)
	if actor.Kind != "system" {
		for _, id := range []int64{actor.ID, actor.EffectiveUserID()} {
			admin, err := GetUserByID(ctx, id)
			if err != nil {
				return err
			}
			if !admin.IsAdmin || !admin.IsActive || admin.ProhibitLogin || admin.Type != UserTypeIndividual {
				return governance_model.ErrForbidden
			}
		}
	}
	if u.IsAuditor && u.Type != UserTypeIndividual {
		return governance_model.ErrInvalid
	}
	details, err := json.Marshal(map[string]any{"before": before, "after": u.IsAuditor})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "user.auditor_changed", Actor: governance_model.AuditActor(ctx), ScopeType: "user", ScopeID: u.ID, ObjectType: "user", ObjectID: u.ID, ObjectPath: u.Name, Result: "success", Details: details})
}
