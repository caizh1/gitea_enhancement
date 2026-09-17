// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestExpiredGrantsAuditAndRenewal(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	expired := time.Now().Add(-time.Hour).Unix()
	member := &governance_model.Membership{ScopeType: "group", ScopeID: 3, UserID: 4, Role: governance_model.Reporter, ExpiresUnix: expired}
	share := &governance_model.Share{ScopeType: "repository", ScopeID: 1, GroupID: 3, MaxRole: governance_model.Reporter, ExpiresUnix: expired}
	require.NoError(t, db.Insert(ctx, member, share))
	grants, err := governance_model.GroupGrants(ctx, 3, 4, time.Now())
	require.NoError(t, err)
	for _, grant := range grants {
		require.NotEqual(t, member.ID, grant.MembershipID, "后台未运行也不能继续使用到期授权")
	}
	// 外层失败不能留下已删除的授权或到期事件。
	require.ErrorIs(t, db.WithTx(ctx, func(ctx context.Context) error {
		if err := RunExpiredGrants(ctx); err != nil {
			return err
		}
		return governance_model.ErrConflict
	}), governance_model.ErrConflict)
	unittest.AssertExistsAndLoadBean(t, &governance_model.Membership{ID: member.ID})
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "member.expired"}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "share.expired"}, 0)

	group, err := governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	actor := governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "api"}
	require.NoError(t, SetGroupMember(ctx, actor, 3, GroupMemberOption{UserID: 4, Role: governance_model.Reporter, Revision: group.Revision, ExpiresUnix: time.Now().Add(time.Hour).Unix()}, false))
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "member.expired"}, 1)
	require.NoError(t, RunExpiredGrants(ctx))
	require.NoError(t, RunExpiredGrants(ctx))
	unittest.AssertExistsAndLoadBean(t, &governance_model.Membership{ID: member.ID})
	unittest.AssertCount(t, &governance_model.Share{ID: share.ID}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "member.expired"}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "share.expired"}, 1)

	_, err = db.GetEngine(ctx).ID(member.ID).Cols("expires_unix").Update(&governance_model.Membership{ExpiresUnix: expired})
	require.NoError(t, err)
	require.NoError(t, RunExpiredGrants(ctx))
	unittest.AssertCount(t, &governance_model.Membership{ID: member.ID}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "member.expired"}, 2)
	events, err := governance_model.QueryAudit(ctx, governance_model.AuditFilter{ScopeType: "user", ScopeID: 4, EventType: "member.expired", From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Second)}, 0, 100)
	require.NoError(t, err)
	require.Len(t, events.Events, 2, "个人审计入口必须包含本人的授权到期证据")
}
