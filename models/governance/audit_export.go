// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/json"

	"github.com/google/uuid"
	"xorm.io/builder"
)

// AuditExport 的分段和游标与证据块一起提交，中断恢复不重复生成已完成的块。
type AuditExport struct {
	ID          string      `xorm:"VARCHAR(36) pk" json:"id"`
	UserID      int64       `xorm:"INDEX NOT NULL" json:"user_id"`
	Filter      AuditFilter `xorm:"JSON TEXT" json:"filter"`
	Format      string      `xorm:"VARCHAR(8) NOT NULL" json:"format"`
	State       string      `xorm:"VARCHAR(16) INDEX NOT NULL" json:"state"`
	Actor       Actor       `xorm:"JSON TEXT" json:"-"`
	AncestorIDs []int64     `xorm:"JSON TEXT" json:"-"`
	CreatedAt   time.Time   `xorm:"NOT NULL" json:"created_at"`
	ExpiresAt   time.Time   `xorm:"INDEX NOT NULL" json:"expires_at"`
	MaxID       int64       `json:"max_sequence"`
	SegmentFrom time.Time   `json:"-"`
	CursorID    int64       `json:"-"`
	Chunks      int64       `json:"-"`
	Rows        int64       `json:"rows"`
}

func (*AuditExport) TableName() string { return "governance_audit_export" }

type AuditExportChunk struct {
	ID       int64         `xorm:"pk autoincr"`
	ExportID string        `xorm:"VARCHAR(36) UNIQUE(export_chunk) NOT NULL"`
	Ordinal  int64         `xorm:"UNIQUE(export_chunk) NOT NULL"`
	Events   []*AuditEvent `xorm:"JSON TEXT"`
}

func (*AuditExportChunk) TableName() string { return "governance_audit_export_chunk" }

// CreateAuditExport 的授权检查与截止序号读取共用事务；归档内容只在后台分段生成。
func CreateAuditExport(ctx context.Context, actor Actor, filter AuditFilter, format string, authorize func(context.Context) error) (*AuditExport, error) {
	if err := filter.Validate(true); err != nil {
		return nil, err
	}
	if actor.ID <= 0 || authorize == nil || (format != "csv" && format != "json") {
		return nil, ErrInvalid
	}
	filter.From, filter.To = filter.From.UTC(), filter.To.UTC()
	job := &AuditExport{ID: uuid.NewString(), UserID: actor.EffectiveUserID(), Actor: actor, Filter: filter, Format: format, State: "queued", CreatedAt: time.Now().UTC(), SegmentFrom: filter.From.UTC()}
	job.ExpiresAt = job.CreatedAt.Add(7 * 24 * time.Hour)
	err := WithWrite(ctx, nil, func(ctx context.Context) error {
		if err := authorize(ctx); err != nil {
			return err
		}
		groupID := filter.ScopeID
		if filter.ScopeType == "repository" {
			var repository struct{ OwnerID int64 }
			if _, err := db.GetEngine(ctx).Table("repository").Where("id = ?", filter.ScopeID).Get(&repository); err != nil {
				return err
			}
			groupID = repository.OwnerID
		}
		if (filter.ScopeType == "repository" || filter.ScopeType == "group") && groupID > 0 {
			chain, err := Ancestors(ctx, groupID)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			if errors.Is(err, ErrNotFound) {
				var owner struct{ Type int }
				has, err := db.GetEngine(ctx).Table("user").Where("id = ?", groupID).Get(&owner)
				if err != nil {
					return err
				}
				if has && owner.Type == 1 {
					job.AncestorIDs = append(job.AncestorIDs, groupID)
				}
			}
			for _, namespace := range chain {
				if namespace.Kind == "group" {
					job.AncestorIDs = append(job.AncestorIDs, namespace.ID)
				}
			}
		}
		count, err := db.GetEngine(ctx).Where("user_id = ? AND expires_at > ?", job.UserID, time.Now().UTC()).Count(new(AuditExport))
		if err != nil {
			return err
		}
		if count >= 20 {
			return ErrConflict
		}
		var latest AuditEvent
		if _, err := db.GetEngine(ctx).Desc("id").Get(&latest); err != nil {
			return err
		}
		job.MaxID = latest.ID
		if err := db.Insert(ctx, job); err != nil {
			return err
		}
		return appendExportAudit(ctx, job, "audit.export_requested")
	})
	return job, err
}

func appendExportAudit(ctx context.Context, job *AuditExport, eventType string) error {
	details, err := json.Marshal(map[string]any{"export_id": job.ID, "format": job.Format, "from": job.Filter.From.UTC(), "to": job.Filter.To.UTC(), "rows": job.Rows})
	if err != nil {
		return err
	}
	return AppendAudit(ctx, &AuditEvent{Type: eventType, Actor: job.Actor, ScopeType: job.Filter.ScopeType, ScopeID: job.Filter.ScopeID, AncestorIDs: job.AncestorIDs, ObjectType: "audit_export", Result: "success", Details: details})
}

// AdvanceAuditExport 每次至多读取一千条、三十天；不在事务中写文件或发送网络请求。
func AdvanceAuditExport(ctx context.Context, id string, authorize func(context.Context, *AuditExport) error) error {
	if authorize == nil {
		return ErrInvalid
	}
	return WithWrite(ctx, nil, func(ctx context.Context) error {
		job := new(AuditExport)
		has, err := db.GetEngine(ctx).ID(id).Get(job)
		if err != nil {
			return err
		}
		if !has {
			return ErrNotFound
		}
		if job.State != "queued" && job.State != "running" {
			return nil
		}
		if err := authorize(ctx, job); err != nil {
			return err
		}
		if !job.ExpiresAt.After(time.Now()) {
			return ErrConflict
		}
		filter := job.Filter
		filter.From, filter.To = job.SegmentFrom, minTime(job.SegmentFrom.Add(AuditQueryWindow), job.Filter.To)
		if err := filter.Validate(false); err != nil {
			return err
		}
		events := make([]*AuditEvent, 0)
		if job.MaxID > 0 {
			if err := db.GetEngine(ctx).Where(auditCondition(ctx, filter)).And(builder.Gt{"id": job.CursorID}, builder.Lte{"id": job.MaxID}).Asc("id").Limit(1000).Find(&events); err != nil {
				return err
			}
		}
		if len(events) > 0 {
			for _, event := range events {
				event.OccurredAt = event.OccurredAt.UTC()
			}
			job.Chunks++
			if err := db.Insert(ctx, &AuditExportChunk{ExportID: job.ID, Ordinal: job.Chunks, Events: events}); err != nil {
				return err
			}
			job.CursorID = events[len(events)-1].ID
			job.Rows += int64(len(events))
		}
		job.State = "running"
		if len(events) < 1000 {
			job.SegmentFrom, job.CursorID = filter.To, 0
			if !filter.To.Before(job.Filter.To) {
				job.State = "ready"
			}
		}
		_, err = db.GetEngine(ctx).ID(job.ID).Cols("segment_from", "cursor_id", "chunks", "rows", "state").Update(job)
		return err
	})
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// ExpireAuditExports 仅清理临时导出副本，永久治理事件不受影响。
func ExpireAuditExports(ctx context.Context, now time.Time) error {
	return WithWrite(ctx, nil, func(ctx context.Context) error {
		ids := builder.Select("id").From(db.GetEngine(ctx).Context(ctx).Engine().TableName(new(AuditExport), true)).Where(builder.Lte{"expires_at": auditDatabaseTime(ctx, now)})
		if _, err := db.GetEngine(ctx).Where(builder.In("export_id", ids)).Delete(new(AuditExportChunk)); err != nil {
			return err
		}
		_, err := db.GetEngine(ctx).Where("expires_at <= ?", now.UTC()).Delete(new(AuditExport))
		return err
	})
}
