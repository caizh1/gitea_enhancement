// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"slices"
	"time"

	"gitea.dev/models/db"

	"xorm.io/builder"
)

const AuditQueryWindow = 30 * 24 * time.Hour

// AuditFilter 使用半开 UTC 时间范围，连续分段不会遗漏或重复边界事件。
type AuditFilter struct {
	ScopeType  string    `json:"scope_type"`
	ScopeID    int64     `json:"scope_id"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	ActorID    int64     `json:"actor_id,omitempty"`
	EventType  string    `json:"event_type,omitempty"`
	ObjectType string    `json:"entity_type,omitempty"`
	ObjectID   int64     `json:"entity_id,omitempty"`
	Result     string    `json:"result,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
}

func (f AuditFilter) Validate(export bool) error {
	if !slices.Contains([]string{"instance", "group", "repository", "user"}, f.ScopeType) ||
		(f.ScopeType == "instance" && f.ScopeID != 0) || (f.ScopeType != "instance" && f.ScopeID <= 0) ||
		f.From.IsZero() || f.To.IsZero() || !f.From.Before(f.To) || (!export && f.To.Sub(f.From) > AuditQueryWindow) ||
		f.From.Year() < 1970 || f.To.After(time.Now().Add(time.Minute)) ||
		f.ActorID < 0 || f.ObjectID < 0 || len(f.EventType) > 100 || len(f.ObjectType) > 40 || len(f.RequestID) > 128 ||
		(f.Result != "" && !slices.Contains([]string{"success", "failure", "denied", "pending", "unknown"}, f.Result)) {
		return fmt.Errorf("%w：检查审计范围、时间和筛选条件；单次查询最多三十天", ErrInvalid)
	}
	return nil
}

type AuditPage struct {
	Events     []*AuditEvent `json:"events"`
	NextBefore int64         `json:"next_before,omitempty"`
}

// auditCondition 只使用事件发生时的范围索引，不从对象当前归属反推历史。
func auditCondition(ctx context.Context, f AuditFilter) builder.Cond {
	ids := builder.Select("event_sequence").From(db.GetEngine(ctx).Context(ctx).Engine().TableName(new(AuditScope), true)).Where(builder.Eq{"scope_type": f.ScopeType, "scope_id": f.ScopeID}).
		And(builder.And(builder.Gte{"occurred_at": auditDatabaseTime(ctx, f.From)}, builder.Lt{"occurred_at": auditDatabaseTime(ctx, f.To)}))
	cond := builder.In("id", ids)
	if f.ActorID > 0 {
		cond = cond.And(builder.Eq{"actor_id": f.ActorID})
	}
	if f.EventType != "" {
		cond = cond.And(builder.Eq{"type": f.EventType})
	}
	if f.ObjectType != "" {
		cond = cond.And(builder.Eq{"object_type": f.ObjectType})
	}
	if f.ObjectID > 0 {
		cond = cond.And(builder.Eq{"object_id": f.ObjectID})
	}
	if f.Result != "" {
		cond = cond.And(builder.Eq{"result": f.Result})
	}
	if f.RequestID != "" {
		cond = cond.And(builder.Eq{"request_id": f.RequestID})
	}
	return cond
}

// 子查询绕过 XORM 的时间参数转换，必须按引擎的存储时区绑定无时区时间戳。
func auditDatabaseTime(ctx context.Context, value time.Time) string {
	return value.In(db.GetEngine(ctx).Context(ctx).Engine().GetTZDatabase()).Format("2006-01-02 15:04:05.999999999")
}

// QueryAudit 的调用者必须先验证范围权限；分页不会暴露范围之外的总数。
func QueryAudit(ctx context.Context, filter AuditFilter, before int64, limit int) (*AuditPage, error) {
	if err := filter.Validate(false); err != nil {
		return nil, err
	}
	if before < 0 || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	cond := auditCondition(ctx, filter)
	if before > 0 {
		cond = cond.And(builder.Lt{"id": before})
	}
	page := &AuditPage{Events: make([]*AuditEvent, 0)}
	if err := db.GetEngine(ctx).Where(cond).Desc("id").Limit(limit + 1).Find(&page.Events); err != nil {
		return nil, err
	}
	if len(page.Events) > limit {
		page.Events = page.Events[:limit]
		page.NextBefore = page.Events[limit-1].ID
	}
	for _, event := range page.Events {
		event.OccurredAt = event.OccurredAt.UTC()
	}
	return page, nil
}
