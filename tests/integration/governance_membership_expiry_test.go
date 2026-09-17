// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceDelegationWriteRechecksRole(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 1, Name: "user1", ActingAsID: 2, ActingAsName: "user2", Kind: "user", Transport: "api"}
	_, err := db.GetEngine(ctx).ID(1).Cols("is_admin").Update(&user_model.User{IsAdmin: false})
	require.NoError(t, err)
	group, err := governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	option := governance_service.GroupMemberOption{UserID: 4, Role: governance_model.Developer, Revision: group.Revision}
	require.ErrorIs(t, governance_service.SetGroupMember(ctx, actor, 3, option, false), governance_model.ErrNotFound)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: 3, UserID: 4}, 0)
	_, err = db.GetEngine(ctx).ID(1).Cols("is_admin").Update(&user_model.User{IsAdmin: true})
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, actor, 3, option, false))
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: 3, UserID: 4}, 1)
}

func TestGovernanceExpiredGrantsAuditFailure(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	member := &governance_model.Membership{ScopeType: "group", ScopeID: 3, UserID: 4, Role: governance_model.Reporter, ExpiresUnix: time.Now().Add(-time.Hour).Unix()}
	share := &governance_model.Share{ScopeType: "repository", ScopeID: 1, GroupID: 3, MaxRole: governance_model.Reporter, ExpiresUnix: member.ExpiresUnix}
	require.NoError(t, db.Insert(ctx, member, share))
	reservation := &governance_model.Reservation{AuthorizationID: "到期授权排序样本", Resource: governance_model.Resource("group", 3)}
	require.NoError(t, db.Insert(ctx, reservation))
	require.Error(t, governance_service.RunExpiredGrants(ctx), "尚未完成的最终授权必须保持先后顺序")
	unittest.AssertExistsAndLoadBean(t, &governance_model.Membership{ID: member.ID})
	unittest.AssertExistsAndLoadBean(t, &governance_model.Share{ID: share.ID})
	_, err := db.GetEngine(ctx).Where("authorization_id = ?", reservation.AuthorizationID).Delete(new(governance_model.Reservation))
	require.NoError(t, err)
	create := "CREATE TRIGGER governance_expiry_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'member.expired' BEGIN SELECT RAISE(ABORT, '授权到期审计故障'); END"
	drop := "DROP TRIGGER IF EXISTS governance_expiry_fail"
	if setting.Database.Type.IsPostgreSQL() {
		_, err := db.GetEngine(ctx).Exec("CREATE FUNCTION governance_expiry_fail_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type = 'member.expired' THEN RAISE EXCEPTION '授权到期审计故障'; END IF; RETURN NEW; END $$")
		require.NoError(t, err)
		defer func() {
			_, err := db.GetEngine(context.WithoutCancel(ctx)).Exec("DROP FUNCTION IF EXISTS governance_expiry_fail_fn() CASCADE")
			require.NoError(t, err)
		}()
		create = "CREATE TRIGGER governance_expiry_fail BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION governance_expiry_fail_fn()"
		drop += " ON governance_audit_event"
	}
	_, err = db.GetEngine(ctx).Exec(create)
	require.NoError(t, err)
	defer func() {
		_, err := db.GetEngine(context.WithoutCancel(ctx)).Exec(drop)
		require.NoError(t, err)
	}()
	require.Error(t, governance_service.RunExpiredGrants(ctx))
	unittest.AssertExistsAndLoadBean(t, &governance_model.Membership{ID: member.ID})
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "member.expired"}, 0)
	unittest.AssertCount(t, &governance_model.Share{ID: share.ID}, 0)
	// 单条审计故障不阻止其他独立到期事务。
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "share.expired"}, 1)
	_, err = governance_service.CheckGroupAccess(ctx, 4, 3, governance_model.ReadCode)
	require.ErrorIs(t, err, governance_model.ErrNotFound, "审计故障不能重新启用已到期权限")
	_, err = db.GetEngine(ctx).Exec(drop)
	require.NoError(t, err)
	require.NoError(t, governance_service.RunExpiredGrants(ctx))
	require.NoError(t, governance_service.RunExpiredGrants(ctx))
	unittest.AssertCount(t, &governance_model.Membership{ID: member.ID}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "member.expired"}, 1)
}
