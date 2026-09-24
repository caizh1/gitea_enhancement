// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	governance_api "gitea.dev/routers/api/v1/governance"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceAuditExportScopedSequence(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	visible := &governance_model.AuditEvent{Type: "member.updated", Actor: actor, ScopeType: "group", ScopeID: 3, ObjectType: "user", ObjectID: 4, ObjectPath: "可见审计快照", Result: "success"}
	unrelated := &governance_model.AuditEvent{Type: "member.updated", Actor: actor, ScopeType: "group", ScopeID: 6, ObjectType: "user", ObjectID: 4, ObjectPath: "其他群组活动标记", Result: "success"}
	require.NoError(t, governance_model.AppendAudit(ctx, visible))
	require.NoError(t, governance_model.AppendAudit(ctx, unrelated))
	token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteGovernance)
	option := governance_api.AuditExportOption{Filter: governance_model.AuditFilter{ScopeType: "group", ScopeID: 3, From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Second)}, Format: "json"}
	created := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/audit-exports", option).AddTokenAuth(token), http.StatusAccepted)
	require.Contains(t, created.Body.String(), `"max_sequence"`)
	var job struct {
		ID          string `json:"id"`
		MaxSequence int64  `json:"max_sequence"`
	}
	DecodeJSON(t, created, &job)
	require.NotEmpty(t, job.ID)
	require.Equal(t, visible.ID, job.MaxSequence, "POST 仅返回筛选范围内的快照末序号")
	var persisted governance_model.AuditExport
	has, err := db.GetEngine(ctx).ID(job.ID).Get(&persisted)
	require.NoError(t, err)
	require.True(t, has)
	require.GreaterOrEqual(t, persisted.MaxID, unrelated.ID, "内部快照上界仍须持久化")
	require.Greater(t, persisted.MaxID, job.MaxSequence, "不得外显内部全局上界")

	newer := &governance_model.AuditEvent{Type: "member.updated", Actor: actor, ScopeType: "group", ScopeID: 3, ObjectType: "user", ObjectID: 4, ObjectPath: "快照之后新增", Result: "success"}
	require.NoError(t, governance_model.AppendAudit(ctx, newer))
	otherNewer := &governance_model.AuditEvent{Type: "member.updated", Actor: actor, ScopeType: "group", ScopeID: 6, ObjectType: "user", ObjectID: 4, ObjectPath: "其他群组后续活动", Result: "success"}
	require.NoError(t, governance_model.AppendAudit(ctx, otherNewer))
	require.Greater(t, newer.ID, persisted.MaxID)
	progress := MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/audit-exports/"+job.ID).AddTokenAuth(token), http.StatusOK)
	var progressed struct {
		MaxSequence int64 `json:"max_sequence"`
	}
	DecodeJSON(t, progress, &progressed)
	require.Equal(t, job.MaxSequence, progressed.MaxSequence, "GET 不因其他范围或快照后的事件变化")
	require.NoError(t, governance_service.RunAuditExports(ctx))
	finished := MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/audit-exports/"+job.ID).AddTokenAuth(token), http.StatusOK)
	DecodeJSON(t, finished, &progressed)
	require.Equal(t, job.MaxSequence, progressed.MaxSequence, "导出完成后序号保持快照值")
	content := MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/audit-exports/"+job.ID+"/download").AddTokenAuth(token), http.StatusOK).Body.String()
	require.Contains(t, content, visible.ObjectPath)
	require.NotContains(t, content, unrelated.ObjectPath)
	require.NotContains(t, content, newer.ObjectPath)
	require.NotContains(t, content, otherNewer.ObjectPath)

	option.Filter.EventType = "repository.transferred"
	empty := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/audit-exports", option).AddTokenAuth(token), http.StatusAccepted)
	require.Contains(t, empty.Body.String(), `"max_sequence":0`, "空筛选范围保留字段并返回零")
}
