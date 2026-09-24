// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package integration

import (
	"os"
	"sync"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	governance_service "gitea.dev/services/governance"
	org_service "gitea.dev/services/org"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestLastPermanentGroupOwnerConcurrentLeave(t *testing.T) {
	if os.Getenv("GITEA_TEST_DATABASE") != "pgsql" {
		t.Skip("requires the real PostgreSQL integration engine")
	}
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.True(t, setting.Database.Type.IsPostgreSQL())
	rows, err := db.GetEngine(ctx).Query("SELECT current_database() AS database_name")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, os.Getenv("TEST_PGSQL_DBNAME"), string(rows[0]["database_name"]))
	t.Logf("真实数据库：%s", string(rows[0]["database_name"]))
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))

	creator := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	owner4 := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}
	owner5 := governance_model.Actor{ID: 5, Name: "user5", Kind: "user", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, creator, governance_service.GroupOption{Path: "m11-concurrent-owners", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, creator, group.ID, governance_service.GroupMemberOption{UserID: owner4.ID, Role: governance_model.Owner, Revision: group.Revision}, false))
	group, err = governance_service.CheckGroupAccess(ctx, creator.ID, group.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, creator, group.ID, governance_service.GroupMemberOption{UserID: owner5.ID, Role: governance_model.Owner, Revision: group.Revision}, false))
	removeCreatorNativeOwner(t, group.ID, owner4.ID, creator.ID)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, Role: governance_model.Owner, ExpiresUnix: 0}, 2)

	type leaveResult struct {
		userID int64
		err    error
	}
	start := make(chan struct{})
	results := make(chan leaveResult, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, actor := range []governance_model.Actor{owner4, owner5} {
		go func() {
			ready.Done()
			<-start
			results <- leaveResult{userID: actor.ID, err: governance_service.LeaveGroup(ctx, actor, group.ID)}
		}()
	}
	ready.Wait()
	close(start)
	first, second := <-results, <-results
	var succeeded, rejected int
	var remaining governance_model.Actor
	for _, result := range []leaveResult{first, second} {
		if result.err == nil {
			succeeded++
			unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: result.userID, Role: governance_model.Owner}, 0)
			continue
		}
		rejected++
		require.ErrorIs(t, result.err, governance_model.ErrConflict, "退出失败必须是最后 Owner 冲突：%v", result.err)
		require.Contains(t, result.err.Error(), "Owner")
		remaining = owner4
		if result.userID == owner5.ID {
			remaining = owner5
		}
		unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: result.userID, Role: governance_model.Owner, ExpiresUnix: 0}, 1)
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, rejected)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, Role: governance_model.Owner, ExpiresUnix: 0}, 1)
	require.NoError(t, governance_model.EnsurePermanentGroupOwner(ctx, group.ID, 0))
	group, err = governance_service.CheckGroupAccess(ctx, remaining.ID, group.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	err = governance_service.SetGroupMember(ctx, remaining, group.ID, governance_service.GroupMemberOption{UserID: remaining.ID, Revision: group.Revision}, true)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	require.Contains(t, err.Error(), "Owner")
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: remaining.ID, Role: governance_model.Owner, ExpiresUnix: 0}, 1)
}

func TestExpiredTemporaryOwnerCannotReplaceLastPermanentOwner(t *testing.T) {
	if os.Getenv("GITEA_TEST_DATABASE") != "pgsql" {
		t.Skip("requires the real PostgreSQL integration engine")
	}
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.True(t, setting.Database.Type.IsPostgreSQL())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	creator := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	permanent := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}
	const temporaryID int64 = 5
	group, err := governance_service.CreateGroup(ctx, creator, governance_service.GroupOption{Path: "m11-expired-owner", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, creator, group.ID, governance_service.GroupMemberOption{UserID: permanent.ID, Role: governance_model.Owner, Revision: group.Revision}, false))
	group, err = governance_service.CheckGroupAccess(ctx, creator.ID, group.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, creator, group.ID, governance_service.GroupMemberOption{UserID: temporaryID, Role: governance_model.Owner, Revision: group.Revision, ExpiresUnix: time.Now().Add(time.Hour).Unix()}, false))
	removeCreatorNativeOwner(t, group.ID, permanent.ID, creator.ID)
	grants, err := governance_model.GroupGrants(ctx, group.ID, temporaryID, time.Now())
	require.NoError(t, err)
	require.True(t, governance_model.HasOwnerGrant(grants))
	err = governance_service.LeaveGroup(ctx, permanent, group.ID)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	require.Contains(t, err.Error(), "Owner")
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: permanent.ID, Role: governance_model.Owner, ExpiresUnix: 0}, 1)
	member := unittest.AssertExistsAndLoadBean(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: temporaryID, Role: governance_model.Owner})
	_, err = db.GetEngine(ctx).ID(member.ID).Cols("expires_unix").Update(&governance_model.Membership{ExpiresUnix: time.Now().Add(-time.Minute).Unix()})
	require.NoError(t, err)
	grants, err = governance_model.GroupGrants(ctx, group.ID, temporaryID, time.Now())
	require.NoError(t, err)
	require.False(t, governance_model.HasOwnerGrant(grants))
	err = governance_service.LeaveGroup(ctx, permanent, group.ID)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	require.Contains(t, err.Error(), "Owner", "失败原因必须指出最后 Owner：%v", err)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: permanent.ID, Role: governance_model.Owner, ExpiresUnix: 0}, 1)
	require.NoError(t, governance_model.EnsurePermanentGroupOwner(ctx, group.ID, 0))
}

func removeCreatorNativeOwner(t *testing.T, groupID, replacementID, creatorID int64) {
	t.Helper()
	team, err := organization.GetOwnerTeam(t.Context(), groupID)
	require.NoError(t, err)
	replacement := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: replacementID})
	creator := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: creatorID})
	require.NoError(t, org_service.RemoveTeamMemberAsOwner(t.Context(), replacement, team, creator))
	unittest.AssertCount(t, &organization.TeamUser{TeamID: team.ID, UID: creatorID}, 0)
}
