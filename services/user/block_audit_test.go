// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package user

import (
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestNativeBlockAuditScopeAndNoNote(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29})
	actor := governance_model.Actor{ID: owner.ID, Name: owner.Name, Kind: "user", Transport: "web"}
	child, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "block-audit-child", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	blocker, err := user_model.GetUserByID(ctx, child.ID)
	require.NoError(t, err)
	ctx = governance_model.WithAuditActor(ctx, actor)

	require.NoError(t, BlockUser(ctx, owner, blocker, blockee, "不可写入审计的备注"))
	created := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "user.blocked", ScopeType: "group", ScopeID: child.ID, ObjectID: blockee.ID})
	unittest.AssertExistsAndLoadBean(t, &governance_model.AuditScope{EventSequence: created.ID, ScopeType: "group", ScopeID: 3})
	require.NoError(t, governance_service.CheckAuditAccess(ctx, owner.ID, "group", 3))
	parentPage, err := governance_model.QueryAudit(ctx, governance_model.AuditFilter{ScopeType: "group", ScopeID: 3, From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Second), EventType: "user.blocked"}, 0, 10)
	require.NoError(t, err)
	require.Len(t, parentPage.Events, 1)
	require.Equal(t, created.ID, parentPage.Events[0].ID)
	require.NotContains(t, string(created.Details), "不可写入审计的备注")
	require.JSONEq(t, `{"blocked_before":false,"blocked_after":true,"note_changed":false}`, string(created.Details))
	require.NoError(t, UpdateBlockingNote(ctx, owner, blocker, blockee, "另一条不可写入的备注"))
	updated := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "user.block_note_updated", ScopeType: "group", ScopeID: child.ID, ObjectID: blockee.ID})
	require.NotContains(t, string(updated.Details), "另一条不可写入的备注")
	require.JSONEq(t, `{"blocked_before":true,"blocked_after":true,"note_changed":true}`, string(updated.Details))
	require.NoError(t, UpdateBlockingNote(ctx, owner, blocker, blockee, "另一条不可写入的备注"))
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "user.block_note_updated", ObjectID: blockee.ID}, 1)
	require.NoError(t, UnblockUser(ctx, owner, blocker, blockee))
	removed := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "user.unblocked", ScopeType: "group", ScopeID: child.ID, ObjectID: blockee.ID})
	unittest.AssertExistsAndLoadBean(t, &governance_model.AuditScope{EventSequence: removed.ID, ScopeType: "group", ScopeID: 3})
}

func TestNativePersonalBlockAuditAndRejectedWrite(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 28})
	ctx = governance_model.WithAuditActor(ctx, governance_model.Actor{ID: owner.ID, Name: owner.Name, Kind: "user", Transport: "web"})
	require.NoError(t, BlockUser(ctx, owner, owner, blockee, "个人秘密备注"))
	event := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "user.blocked", ScopeType: "user", ScopeID: owner.ID, ObjectID: blockee.ID})
	require.NotContains(t, string(event.Details), "个人秘密备注")
	unittest.AssertNotExistsBean(t, &governance_model.AuditScope{EventSequence: event.ID, ScopeType: "group"})
	require.NoError(t, governance_service.CheckAuditAccess(ctx, owner.ID, "user", owner.ID))
	personalPage, err := governance_model.QueryAudit(ctx, governance_model.AuditFilter{ScopeType: "user", ScopeID: owner.ID, From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Second), EventType: "user.blocked"}, 0, 10)
	require.NoError(t, err)
	require.Len(t, personalPage.Events, 1)
	require.Equal(t, event.ID, personalPage.Events[0].ID)
	require.ErrorIs(t, BlockUser(ctx, owner, owner, blockee, "重复"), user_model.ErrCanNotBlock)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "user.blocked", ObjectID: blockee.ID}, 1)
}

func TestNativeBlockAuditFailureRollsBack(t *testing.T) {
	if !setting.Database.Type.IsSQLite3() {
		t.Skip("审计写入故障屏障仅使用本机 SQLite 触发器")
	}
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 28})
	require.NoError(t, user_model.FollowUser(ctx, owner, blockee))
	require.True(t, user_model.IsFollowing(ctx, owner.ID, blockee.ID))
	_, err := db.GetEngine(ctx).Exec("CREATE TRIGGER block_audit_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'user.blocked' BEGIN SELECT RAISE(ABORT, '审计写入失败'); END")
	require.NoError(t, err)
	defer func() { _, _ = db.GetEngine(ctx).Exec("DROP TRIGGER IF EXISTS block_audit_fail") }()
	require.Error(t, BlockUser(ctx, owner, owner, blockee, "应回滚"))
	require.True(t, user_model.IsFollowing(ctx, owner.ID, blockee.ID))
	_, err = user_model.GetBlocking(ctx, owner.ID, blockee.ID)
	require.Error(t, err)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "user.blocked", ObjectID: blockee.ID}, 0)
}

func TestNativeBlockAuditRejectedUnblockAndNoteRollback(t *testing.T) {
	t.Run("archived group cannot create an unblock success event", func(t *testing.T) {
		unittest.PrepareTestEnv(t)
		ctx := t.Context()
		require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
		owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 15})
		blocker := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 17})
		blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 28})
		_, err := db.GetEngine(ctx).ID(blocker.ID).Cols("archived").Update(&governance_model.Namespace{Archived: true})
		require.NoError(t, err)
		require.ErrorIs(t, UnblockUser(ctx, owner, blocker, blockee), governance_model.ErrConflict)
		unittest.AssertCount(t, &governance_model.AuditEvent{Type: "user.unblocked", ScopeID: blocker.ID, ObjectID: blockee.ID}, 0)
	})

	t.Run("failed note audit preserves prior note", func(t *testing.T) {
		if !setting.Database.Type.IsSQLite3() {
			t.Skip("审计写入故障屏障仅使用本机 SQLite 触发器")
		}
		unittest.PrepareTestEnv(t)
		ctx := t.Context()
		owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 28})
		require.NoError(t, BlockUser(ctx, owner, owner, blockee, "原备注"))
		_, err := db.GetEngine(ctx).Exec("CREATE TRIGGER block_note_audit_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'user.block_note_updated' BEGIN SELECT RAISE(ABORT, '审计写入失败'); END")
		require.NoError(t, err)
		defer func() { _, _ = db.GetEngine(ctx).Exec("DROP TRIGGER IF EXISTS block_note_audit_fail") }()
		require.Error(t, UpdateBlockingNote(ctx, owner, owner, blockee, "新备注"))
		stored, err := user_model.GetBlocking(ctx, owner.ID, blockee.ID)
		require.NoError(t, err)
		require.Equal(t, "原备注", stored.Note)
		unittest.AssertCount(t, &governance_model.AuditEvent{Type: "user.block_note_updated", ObjectID: blockee.ID}, 0)
	})
}
