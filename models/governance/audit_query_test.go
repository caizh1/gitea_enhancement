// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"testing"
	"time"

	"gitea.dev/models/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditQueryHistoricalScopeAndFilters(t *testing.T) {
	ctx := testDatabase(t)
	db.GetEngine(ctx).Context(ctx).Engine().SetTZDatabase(time.FixedZone("验收时区", 8*3600))
	first := testAudit()
	first.ObjectType, first.ObjectID, first.ObjectPath, first.Actor.RequestID = "repository", 99, "acme/rd/firmware", "请求-一"
	require.NoError(t, AppendAudit(ctx, first))
	second := testAudit()
	second.ScopeID, second.AncestorIDs, second.Actor.ID, second.Actor.RequestID = 99, []int64{7}, 12, "请求-二"
	require.NoError(t, AppendAudit(ctx, second))
	filter := AuditFilter{ScopeType: "instance", From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Second)}
	page, err := QueryAudit(ctx, filter, 0, 1)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	assert.Equal(t, second.EventID, page.Events[0].EventID)
	assert.Equal(t, second.ID, page.NextBefore)
	page, err = QueryAudit(ctx, filter, page.NextBefore, 1)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	assert.Equal(t, first.EventID, page.Events[0].EventID)
	assert.Zero(t, page.NextBefore)
	filter.ScopeType, filter.ScopeID, filter.RequestID, filter.ActorID = "group", 1, "请求-一", first.ActorID
	filter.ObjectType, filter.ObjectID, filter.EventType, filter.Result = first.ObjectType, first.ObjectID, first.Type, "success"
	page, err = QueryAudit(ctx, filter, 0, 50)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	assert.Equal(t, first.ObjectPath, page.Events[0].ObjectPath, "后续对象转移不改变原范围的历史快照")
	filter.ScopeID = 7
	page, err = QueryAudit(ctx, filter, 0, 50)
	require.NoError(t, err)
	assert.Empty(t, page.Events, "其他范围不能从当前归属反推获取旧事件")
	filter = AuditFilter{ScopeType: "user", ScopeID: first.ActorID, From: filter.From, To: filter.To}
	page, err = QueryAudit(ctx, filter, 0, 50)
	require.NoError(t, err)
	require.Len(t, page.Events, 1)
	filter.From = filter.To.Add(-31 * 24 * time.Hour)
	_, err = QueryAudit(ctx, filter, 0, 50)
	assert.ErrorIs(t, err, ErrInvalid)
	filter.From = filter.To
	assert.ErrorIs(t, filter.Validate(true), ErrInvalid)
}

func TestAuditExportResumesAtTimeAndRowBoundaries(t *testing.T) {
	ctx := testDatabase(t)
	start := time.Now().UTC().Add(-61 * 24 * time.Hour).Truncate(time.Second)
	// 固定大样本直接造数；前一条恰好落在时间边界之前，后一条落在下一段的起点。
	for i := range 1002 {
		event := testAudit()
		require.NoError(t, AppendAudit(ctx, event))
		at := start.Add(time.Hour)
		if i == 1001 {
			at = start.Add(AuditQueryWindow)
		}
		_, err := db.GetEngine(ctx).ID(event.ID).Cols("occurred_at").Update(&AuditEvent{OccurredAt: at})
		require.NoError(t, err)
		_, err = db.GetEngine(ctx).Where("event_sequence = ?", event.ID).Cols("occurred_at").Update(&AuditScope{OccurredAt: at})
		require.NoError(t, err)
	}
	filter := AuditFilter{ScopeType: "instance", From: start, To: start.Add(60 * 24 * time.Hour)}
	actor := testAudit().Actor
	job, err := CreateAuditExport(ctx, actor, filter, "json", func(context.Context) error { return nil })
	require.NoError(t, err)
	check := func(context.Context, *AuditExport) error { return nil }
	require.NoError(t, AdvanceAuditExport(ctx, job.ID, check))
	var stored AuditExport
	_, err = db.GetEngine(ctx).ID(job.ID).Get(&stored)
	require.NoError(t, err)
	assert.EqualValues(t, 1000, stored.Rows)
	assert.Equal(t, "running", stored.State)
	assert.ErrorIs(t, AdvanceAuditExport(ctx, job.ID, func(context.Context, *AuditExport) error { return ErrForbidden }), ErrForbidden)
	// 使用持久化游标继续，不依赖前一次调用的内存状态。
	require.NoError(t, AdvanceAuditExport(ctx, job.ID, check))
	require.NoError(t, AdvanceAuditExport(ctx, job.ID, check))
	require.NoError(t, AdvanceAuditExport(ctx, job.ID, check))
	stored = AuditExport{}
	_, err = db.GetEngine(ctx).ID(job.ID).Get(&stored)
	require.NoError(t, err)
	assert.Equal(t, "ready", stored.State)
	assert.EqualValues(t, 1002, stored.Rows)
	var chunks []*AuditExportChunk
	require.NoError(t, db.GetEngine(ctx).Where("export_id = ?", job.ID).Asc("ordinal").Find(&chunks))
	seen := map[string]bool{}
	for _, chunk := range chunks {
		for _, event := range chunk.Events {
			require.False(t, seen[event.EventID])
			seen[event.EventID] = true
		}
	}
	assert.Len(t, seen, 1002, "恢复、千条分页及三十天分段不能重复或遗漏")
	require.NoError(t, ExpireAuditExports(ctx, stored.ExpiresAt.Add(time.Second)))
	count, err := db.GetEngine(ctx).Count(new(AuditExportChunk))
	require.NoError(t, err)
	assert.Zero(t, count)
	count, err = db.GetEngine(ctx).Count(new(AuditEvent))
	require.NoError(t, err)
	assert.EqualValues(t, 1003, count, "清理临时导出不得删除永久事件")
}
