// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestLeaveGroupOnlyRemovesOwnDirectMembership(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	owner := gm.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	member := gm.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "web"}
	parent, err := CreateGroup(ctx, owner, GroupOption{Path: "leave-source-parent", Visibility: 2})
	require.NoError(t, err)
	child, err := CreateGroup(ctx, owner, GroupOption{Path: "leave-source-child", ParentID: parent.ID, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, owner, parent.ID, GroupMemberOption{UserID: 4, Role: gm.Reporter, Revision: parent.Revision}, false))
	require.NoError(t, SetGroupMember(ctx, owner, child.ID, GroupMemberOption{UserID: 4, Role: gm.Reporter, Revision: child.Revision}, false))
	_, err = db.GetEngine(ctx).ID(child.ID).Cols("archived").Update(&gm.Namespace{Archived: true})
	require.NoError(t, err)
	before, err := gm.GetNamespace(ctx, child.ID)
	require.NoError(t, err)
	invalid := member
	invalid.Transport = ""
	require.Error(t, LeaveGroup(ctx, invalid, child.ID))
	unittest.AssertCount(t, &gm.Membership{ScopeType: "group", ScopeID: child.ID, UserID: 4}, 1)
	after, err := gm.GetNamespace(ctx, child.ID)
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
	require.NoError(t, LeaveGroup(ctx, member, child.ID))
	unittest.AssertCount(t, &gm.Membership{ScopeType: "group", ScopeID: child.ID, UserID: 4}, 0)
	unittest.AssertCount(t, &gm.Membership{ScopeType: "group", ScopeID: parent.ID, UserID: 4}, 1)
	_, err = CheckGroupAccess(ctx, member.ID, child.ID, gm.ReadGroup)
	require.NoError(t, err, "退出直接授权后仍可使用父组来源")
	require.ErrorIs(t, LeaveGroup(ctx, member, child.ID), gm.ErrNotFound)
	require.ErrorIs(t, LeaveGroup(ctx, gm.Actor{ID: 5, Name: "user5", Kind: "user", Transport: "web"}, parent.ID), gm.ErrNotFound)
	delegated := member
	delegated.ActingAsID = member.ID
	require.ErrorIs(t, LeaveGroup(ctx, delegated, parent.ID), gm.ErrNotFound)
	unittest.AssertCount(t, &gm.AuditEvent{Type: "member.removed", ScopeType: "group", ScopeID: child.ID, ActorID: 4}, 1)
}

func TestLeaveGroupRejectsLastPermanentOwnerAndDisabledActor(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	owner := gm.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	member := gm.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "web"}
	group, err := CreateGroup(ctx, owner, GroupOption{Path: "leave-last-owner", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, owner, group.ID, GroupMemberOption{UserID: 4, Role: gm.Owner, Revision: group.Revision}, false))
	team, err := organization.GetOwnerTeam(ctx, group.ID)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Where("team_id = ? AND uid = ?", team.ID, 2).Delete(new(organization.TeamUser))
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(member.ID).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: true})
	require.NoError(t, err)
	require.ErrorIs(t, LeaveGroup(ctx, member, group.ID), gm.ErrNotFound)
	_, err = db.GetEngine(ctx).ID(member.ID).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: false})
	require.NoError(t, err)
	require.ErrorIs(t, LeaveGroup(ctx, member, group.ID), gm.ErrConflict)
	unittest.AssertCount(t, &gm.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4, Role: gm.Owner}, 1)
	unittest.AssertCount(t, &gm.AuditEvent{Type: "member.removed", ScopeType: "group", ScopeID: group.ID, ActorID: 4}, 0)
}
