// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"bytes"
	"context"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { unittest.MainTest(m) }

func TestAuditAccessRevocationAndCustomRole(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	require.NoError(t, CheckAuditAccess(ctx, 1, "instance", 0))
	require.NoError(t, CheckAuditAccess(ctx, 2, "group", 3))
	require.NoError(t, CheckAuditAccess(ctx, 2, "repository", 2))
	assert.ErrorIs(t, CheckAuditAccess(ctx, 4, "instance", 0), governance_model.ErrNotFound)
	assert.ErrorIs(t, CheckAuditAccess(ctx, 4, "group", 3), governance_model.ErrNotFound)
	assert.ErrorIs(t, CheckAuditAccess(ctx, 4, "user", 2), governance_model.ErrNotFound)
	require.NoError(t, CheckAuditAccess(ctx, 4, "user", 4))
	role := &governance_model.CustomRole{RootID: 3, Name: "局部审计", BaseRole: governance_model.Guest, Abilities: []string{governance_model.ReadAudit}}
	require.NoError(t, db.Insert(ctx, role))
	member := &governance_model.Membership{ScopeType: "group", ScopeID: 3, UserID: 4, Role: governance_model.Guest, CustomRoleID: role.ID, ExpiresUnix: time.Now().Add(time.Minute).Unix()}
	require.NoError(t, db.Insert(ctx, member))
	require.NoError(t, CheckAuditAccess(ctx, 4, "group", 3))
	_, err := db.GetEngine(ctx).ID(member.ID).Cols("expires_unix").Update(&governance_model.Membership{ExpiresUnix: time.Now().Unix()})
	require.NoError(t, err)
	assert.ErrorIs(t, CheckAuditAccess(ctx, 4, "group", 3), governance_model.ErrNotFound)
	_, err = db.GetEngine(ctx).ID(role.ID).Cols("base_role", "abilities").Update(&governance_model.CustomRole{BaseRole: governance_model.Maintainer, Abilities: []string{governance_model.ReadCode}})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(member.ID).Cols("role", "expires_unix").Update(&governance_model.Membership{Role: governance_model.Maintainer})
	require.NoError(t, err)
	assert.ErrorIs(t, CheckAuditAccess(ctx, 4, "group", 3), governance_model.ErrNotFound, "不相关的自定义角色不能附带群组审计能力")
	_, err = db.GetEngine(ctx).ID(1).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: true})
	require.NoError(t, err)
	assert.ErrorIs(t, CheckAuditAccess(ctx, 1, "instance", 0), governance_model.ErrNotFound, "管理员停用后也不得读取历史导出")
}

func TestAuditDownloadChecksIdentityAndEscapesCSV(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	actor := RequestActor(user, "127.0.0.1:4321", "api")
	actor.Name = "  =HYPERLINK(\"https://example.invalid\")"
	event := &governance_model.AuditEvent{Type: "member.added", Actor: actor, ScopeType: "group", ScopeID: 3, ObjectType: "user", ObjectID: 4, Result: "success", ObjectPath: "=测试公式", Details: []byte(`{"after":{"role":"Reporter"}}`)}
	require.NoError(t, governance_model.AppendAudit(ctx, event))
	filter := governance_model.AuditFilter{ScopeType: "instance", From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Second)}
	for _, format := range []string{"csv", "json"} {
		job, err := governance_model.CreateAuditExport(ctx, actor, filter, format, func(context.Context) error { return nil })
		require.NoError(t, err)
		require.NoError(t, RunAuditExports(ctx))
		job, err = PrepareAuditDownload(ctx, actor, job.ID)
		require.NoError(t, err)
		var output bytes.Buffer
		require.NoError(t, WriteAuditExport(ctx, actor, job, &output))
		if format == "csv" {
			rows, err := csv.NewReader(strings.NewReader(output.String())).ReadAll()
			require.NoError(t, err)
			assert.Equal(t, "'"+actor.Name, rows[1][4])
			assert.Equal(t, "'=测试公式", rows[1][11])
		} else {
			var events []*governance_model.AuditEvent
			require.NoError(t, json.Unmarshal(output.Bytes(), &events))
			require.NotEmpty(t, events)
			assert.Equal(t, event.EventID, events[0].EventID)
			assert.Equal(t, time.UTC, events[0].OccurredAt.Location(), "JSON 导出时间必须是 UTC")
		}
		_, err = GetAuditExport(ctx, 2, job.ID)
		assert.ErrorIs(t, err, governance_model.ErrNotFound)
		_, err = db.GetEngine(ctx).ID(1).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: true})
		require.NoError(t, err)
		_, err = PrepareAuditDownload(ctx, actor, job.ID)
		assert.ErrorIs(t, err, governance_model.ErrNotFound)
		_, err = db.GetEngine(ctx).ID(1).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: false})
		require.NoError(t, err)
	}
}
