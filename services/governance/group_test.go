// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"net/url"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeGroupInheritanceAndPaths(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	child, err := CreateGroup(ctx, actor, GroupOption{Name: "研发", Path: "rd", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	assert.Equal(t, "org3/rd", child.FullPath)
	assert.NotEqual(t, "rd", child.InternalName)
	require.NoError(t, SetGroupMember(ctx, actor, 3, GroupMemberOption{UserID: 4, Role: governance_model.Reporter, Revision: 1}, false))
	member, err := CheckGroupAccess(ctx, 4, child.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	assert.True(t, member.Abilities[governance_model.ReadCode])
	assert.False(t, member.Abilities[governance_model.PushCode])
	owner, err := user_model.GetUserByID(ctx, child.ID)
	require.NoError(t, err)
	repo := &repo_model.Repository{OwnerID: owner.ID, OwnerName: owner.Name, Name: "firmware", LowerName: "firmware", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	repo.OwnerNamespace, err = governance_model.RegisterNativeRepository(ctx, repo.ID, owner.ID, repo.Name)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(repo.ID).Cols("owner_namespace").Update(repo)
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeIssues}))
	user, err := user_model.GetUserByID(ctx, 4)
	require.NoError(t, err)
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
	require.NoError(t, err)
	assert.True(t, permission.CanRead(unit.TypeCode))
	assert.False(t, permission.CanWrite(unit.TypeCode))
	physical := repo.RepoPath()
	for _, sample := range []struct {
		before, after string
		api           bool
	}{
		{"/org3/rd/firmware", "/" + owner.Name + "/firmware", false},
		{"/org3/rd/firmware.git/info/refs", "/" + owner.Name + "/firmware.git/info/refs", false},
		{"/org3/rd/firmware/src/branch/feature%2Ftest/file%20name", "/" + owner.Name + "/firmware/src/branch/feature%2Ftest/file%20name", false},
		{"/repos/org3/rd/firmware/issues", "/repos/" + owner.Name + "/firmware/issues", true},
		{"/repos/" + url.PathEscape("org3/rd") + "/firmware", "/repos/" + owner.Name + "/firmware", true},
		{"/org/org3/rd/settings", "/org/" + owner.Name + "/settings", false},
		{"/api/packages/org3/rd/firmware", "/api/packages/org3/rd/firmware", false},
	} {
		rewritten, err := rewriteNamespaceRoute(ctx, sample.before, sample.api)
		require.NoError(t, err)
		assert.Equal(t, sample.after, rewritten)
	}
	moved, err := MoveGroup(ctx, actor, child.ID, GroupOption{Path: "research", ParentID: 3, Revision: child.Revision})
	require.NoError(t, err)
	assert.Equal(t, "org3/research", moved.FullPath)
	current, err := repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	assert.Equal(t, "org3/research/firmware", current.FullPath())
	assert.Equal(t, physical, current.RepoPath())
	for _, path := range []string{"org3/rd/firmware", "org3/research/firmware"} {
		resolved, err := ResolveRepositoryIdentity(ctx, path)
		require.NoError(t, err)
		assert.Equal(t, repo.ID, resolved.ID)
	}
	require.NoError(t, SetGroupMember(ctx, actor, 3, GroupMemberOption{UserID: 4, Revision: 2}, true))
	permission, err = access_model.GetIndividualUserRepoPermission(ctx, current, user)
	require.NoError(t, err)
	assert.False(t, permission.CanRead(unit.TypeCode), "父组撤权后下一次原生权限判定必须拒绝")
	_, err = CheckGroupAccess(ctx, 4, child.ID, governance_model.ReadGroup)
	assert.ErrorIs(t, err, governance_model.ErrNotFound)
}

func TestNativePlannerDoesNotGainCode(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "api"}
	child, err := CreateGroup(ctx, actor, GroupOption{Path: "planning", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, child.ID, GroupMemberOption{UserID: 4, Role: governance_model.Planner, Revision: 1, ExpiresUnix: time.Now().Add(time.Hour).Unix()}, false))
	repo := &repo_model.Repository{OwnerID: child.ID, OwnerName: child.InternalName, Name: "plan", LowerName: "plan", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeIssues}))
	user, err := user_model.GetUserByID(ctx, 4)
	require.NoError(t, err)
	p, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
	require.NoError(t, err)
	assert.True(t, p.CanWrite(unit.TypeIssues))
	assert.False(t, p.CanRead(unit.TypeCode))
	assert.False(t, p.IsAdmin())
	owner, err := organization.IsOrganizationOwner(ctx, child.ID, 4)
	require.NoError(t, err)
	assert.False(t, owner)
}

func TestMoveGroupLosingInheritedAccessStillReportsSuccess(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	group, err := CreateGroup(ctx, governance_model.Actor{ID: 1, Name: "user1", Kind: "user", Transport: "api"}, GroupOption{Path: "detached", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	_, err = CheckGroupAccess(ctx, 2, group.ID, governance_model.ManageGroup)
	require.NoError(t, err)
	moved, err := MoveGroup(ctx, governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}, group.ID, GroupOption{Path: "detached", Revision: group.Revision})
	require.NoError(t, err)
	assert.Equal(t, "detached", moved.FullPath)
	assert.False(t, moved.Abilities[governance_model.ReadGroup])
	_, err = CheckGroupAccess(ctx, 2, group.ID, governance_model.ReadGroup)
	assert.ErrorIs(t, err, governance_model.ErrNotFound)
}

func TestPreviewGroupMoveShowsChangedGovernanceScopes(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	source, err := CreateGroup(ctx, actor, GroupOption{Path: "preview-source", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	target, err := CreateGroup(ctx, actor, GroupOption{Path: "preview-target", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx,
		&governance_model.Membership{ScopeType: "group", ScopeID: 3, UserID: 4, Role: governance_model.Reporter},
		&governance_model.Share{ScopeType: "group", ScopeID: 3, GroupID: target.ID, MaxRole: governance_model.Reporter},
		&governance_model.ApprovalRule{ScopeType: "group", ScopeID: 3, Name: "父级规则", Required: 1, Enabled: true, Revision: 1},
		&governance_model.ApprovalSettings{ScopeType: "group", ScopeID: 3, PreventAuthor: true, Revision: 1},
		&governance_model.AuditStream{ScopeType: "group", ScopeID: 3, Name: "父级外送", Kind: "http", Endpoint: "https://example.invalid", Enabled: true, Revision: 1},
	))
	impact, err := PreviewGroupMove(ctx, actor.ID, source.ID, GroupOption{Path: "moved", ParentID: target.ID, Revision: source.Revision})
	require.NoError(t, err)
	assert.Equal(t, "org3/preview-source", impact.OldPath)
	assert.Equal(t, "preview-target/moved", impact.NewPath)
	assert.Equal(t, []string{"org3"}, impact.RemovedAncestorPaths)
	assert.Equal(t, []string{"preview-target"}, impact.AddedAncestorPaths)
	assert.EqualValues(t, 1, impact.RemovedMembershipSources)
	assert.EqualValues(t, 1, impact.RemovedShareSources)
	assert.EqualValues(t, 1, impact.RemovedApprovalPolicies)
	assert.EqualValues(t, 1, impact.RemovedApprovalSettings)
	assert.EqualValues(t, 1, impact.RemovedAuditStreams)
	assert.True(t, impact.CompatibilityAliasRetained)
}

func TestCrossRootMovePreservesBaseRoleAndRequestPaths(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	source, err := CreateGroup(ctx, actor, GroupOption{Path: "role-source", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	target, err := CreateGroup(ctx, actor, GroupOption{Path: "role-target", Visibility: 2})
	require.NoError(t, err)
	root, err := governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	role, err := SaveGroupRole(ctx, actor, 3, 0, GroupRoleOption{Name: "源根审计员", BaseRole: governance_model.Reporter, Abilities: []string{governance_model.ReadAudit}, Revision: root.Revision}, false)
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, source.ID, GroupMemberOption{UserID: 4, Role: governance_model.Reporter, CustomRoleID: role.ID, Revision: source.Revision}, false))
	require.NoError(t, db.Insert(ctx,
		&governance_model.Invitation{ScopeType: "group", ScopeID: source.ID, Email: "move@example.test", LowerEmail: "move@example.test", ScopePath: source.FullPath, Role: governance_model.Reporter, CustomRoleID: role.ID},
		&governance_model.AccessRequest{ScopeType: "group", ScopeID: source.ID, UserID: 5, Username: "user5", ScopePath: source.FullPath},
	))
	source, err = CheckGroupAccess(ctx, actor.ID, source.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	option := GroupOption{Path: "moved", ParentID: target.ID, Revision: source.Revision}
	impact, err := PreviewGroupMove(ctx, actor.ID, source.ID, option)
	require.NoError(t, err)
	assert.EqualValues(t, 1, impact.CustomRoleMembershipsRemapped)
	assert.EqualValues(t, 1, impact.CustomRoleInvitationsRemapped)
	_, err = MoveGroup(ctx, actor, source.ID, option)
	require.NoError(t, err)
	member := unittest.AssertExistsAndLoadBean(t, &governance_model.Membership{ScopeType: "group", ScopeID: source.ID, UserID: 4})
	assert.NotZero(t, member.CustomRoleID)
	mappedRole := unittest.AssertExistsAndLoadBean(t, &governance_model.CustomRole{ID: member.CustomRoleID})
	assert.Equal(t, target.ID, mappedRole.RootID)
	assert.Equal(t, role.Abilities, mappedRole.Abilities)
	invitation := unittest.AssertExistsAndLoadBean(t, &governance_model.Invitation{ScopeType: "group", ScopeID: source.ID})
	assert.Equal(t, member.CustomRoleID, invitation.CustomRoleID)
	assert.Equal(t, "role-target/moved", invitation.ScopePath)
	request := unittest.AssertExistsAndLoadBean(t, &governance_model.AccessRequest{ScopeType: "group", ScopeID: source.ID})
	assert.Equal(t, "role-target/moved", request.ScopePath)
	memberAccess, err := CheckGroupAccess(ctx, 4, source.ID, governance_model.ReadCode)
	require.NoError(t, err)
	assert.True(t, memberAccess.Abilities[governance_model.ReadAudit])
}

func TestNativeVisibilityCannotBypassHierarchy(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	parent, err := CreateGroup(ctx, actor, GroupOption{Path: "visibility-root", Visibility: 2})
	require.NoError(t, err)
	child, err := CreateGroup(ctx, actor, GroupOption{Path: "private-child", ParentID: parent.ID, Visibility: 2})
	require.NoError(t, err)
	user := &user_model.User{ID: child.ID, Visibility: 0}
	assert.ErrorIs(t, user_model.UpdateUserColsNoAutoTime(ctx, user, "visibility"), governance_model.ErrConflict)
	current, err := user_model.GetUserByID(ctx, child.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, current.Visibility)
	n, err := governance_model.GetNamespace(ctx, child.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, n.Visibility)
	require.NoError(t, user_model.UpdateUserColsNoAutoTime(ctx, &user_model.User{ID: parent.ID, Visibility: 0}, "visibility"))
	require.NoError(t, user_model.UpdateUserColsNoAutoTime(ctx, user, "visibility"))
	assert.ErrorIs(t, user_model.UpdateUserColsNoAutoTime(ctx, &user_model.User{ID: parent.ID, Visibility: 2}, "visibility"), governance_model.ErrConflict)
}

func TestMaintainerCannotManageGroupMembers(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	require.NoError(t, SetGroupMember(ctx, owner, 3, GroupMemberOption{UserID: 4, Role: governance_model.Maintainer, Revision: 1}, false))
	maintainer := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}
	state, err := CheckGroupAccess(ctx, 4, 3, governance_model.CreateGroup)
	require.NoError(t, err)
	assert.True(t, state.Abilities[governance_model.ManageMembers], "项目成员管理能力保持独立")
	assert.False(t, state.Abilities[governance_model.ManageGroupMembers])
	assert.Error(t, SetGroupMember(ctx, maintainer, 3, GroupMemberOption{UserID: 5, Role: governance_model.Reporter, Revision: 2}, false))
	_, err = ListGroupMembers(ctx, 4, 3, 0, 100)
	assert.Error(t, err)
	require.NoError(t, SetGroupMember(ctx, owner, 3, GroupMemberOption{UserID: 5, Role: governance_model.Reporter, Revision: 2}, false))
}

func TestGroupSharingDirectMembersRevocationAndCycles(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	source, err := CreateGroup(ctx, owner, GroupOption{Path: "share-source", Visibility: 2})
	require.NoError(t, err)
	invitedParent, err := CreateGroup(ctx, owner, GroupOption{Path: "share-parent", Visibility: 2})
	require.NoError(t, err)
	invited, err := CreateGroup(ctx, owner, GroupOption{Path: "invited", ParentID: invitedParent.ID, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, owner, invitedParent.ID, GroupMemberOption{UserID: 4, Role: governance_model.Developer, Revision: 1}, false))
	require.NoError(t, SetGroupMember(ctx, owner, invited.ID, GroupMemberOption{UserID: 5, Role: governance_model.Developer, Revision: 1}, false))
	require.NoError(t, SetGroupShare(ctx, owner, source.ID, GroupShareOption{GroupID: invited.ID, MaxRole: governance_model.Reporter, Revision: 1}, false))
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "share.created", ScopeType: "group", ScopeID: source.ID}, 1)
	_, err = CheckGroupAccess(ctx, 4, source.ID, governance_model.ReadGroup)
	assert.ErrorIs(t, err, governance_model.ErrNotFound, "被邀请组的继承成员不能获得共享权限")
	state, err := CheckGroupAccess(ctx, 5, source.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	assert.True(t, state.Abilities[governance_model.ReadCode])
	assert.False(t, state.Abilities[governance_model.PushCode], "共享权限受最高角色限制")
	assert.ErrorIs(t, SetGroupShare(ctx, owner, invited.ID, GroupShareOption{GroupID: source.ID, MaxRole: governance_model.Reporter, Revision: 2}, false), governance_model.ErrConflict)
	assert.ErrorIs(t, SetGroupShare(ctx, owner, invitedParent.ID, GroupShareOption{GroupID: invited.ID, MaxRole: governance_model.Reporter, Revision: 2}, false), governance_model.ErrConflict, "父组共享给子组会与继承形成循环")
	_, err = MoveGroup(ctx, owner, invited.ID, GroupOption{Path: "invited", ParentID: source.ID, Revision: 2})
	assert.ErrorIs(t, err, governance_model.ErrConflict, "移动不能绕过共享循环检查")
	require.NoError(t, SetGroupShare(ctx, owner, source.ID, GroupShareOption{GroupID: invited.ID, Revision: 2}, true))
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "share.removed", ScopeType: "group", ScopeID: source.ID}, 1)
	_, err = CheckGroupAccess(ctx, 5, source.ID, governance_model.ReadGroup)
	assert.ErrorIs(t, err, governance_model.ErrNotFound)
}

func TestExternalShareRestrictionBlocksOnlyNewCrossRootShares(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	root, err := CreateGroup(ctx, owner, GroupOption{Path: "restricted-root", Visibility: 2})
	require.NoError(t, err)
	child, err := CreateGroup(ctx, owner, GroupOption{Path: "child", ParentID: root.ID, Visibility: 2})
	require.NoError(t, err)
	sameRoot, err := CreateGroup(ctx, owner, GroupOption{Path: "same-root", ParentID: root.ID, Visibility: 2})
	require.NoError(t, err)
	external, err := CreateGroup(ctx, owner, GroupOption{Path: "external-root", Visibility: 2})
	require.NoError(t, err)

	require.NoError(t, SetExternalShareRestriction(ctx, owner, root.ID, root.Revision, true))
	require.NoError(t, SetGroupShare(ctx, owner, child.ID, GroupShareOption{GroupID: sameRoot.ID, MaxRole: governance_model.Reporter, Revision: child.Revision}, false))
	child, err = CheckGroupAccess(ctx, owner.ID, child.ID, governance_model.ManageGroup)
	require.NoError(t, err)
	assert.ErrorIs(t, SetGroupShare(ctx, owner, child.ID, GroupShareOption{GroupID: external.ID, MaxRole: governance_model.Reporter, Revision: child.Revision}, false), governance_model.ErrForbidden)

	// 开关开启前已有的跨层级关系可单独更新，不会被静默删除。
	require.NoError(t, SetExternalShareRestriction(ctx, owner, root.ID, root.Revision+1, false))
	child, err = CheckGroupAccess(ctx, owner.ID, child.ID, governance_model.ManageGroup)
	require.NoError(t, err)
	require.NoError(t, SetGroupShare(ctx, owner, child.ID, GroupShareOption{GroupID: external.ID, MaxRole: governance_model.Reporter, Revision: child.Revision}, false))
	require.NoError(t, SetExternalShareRestriction(ctx, owner, root.ID, root.Revision+2, true))
	child, err = CheckGroupAccess(ctx, owner.ID, child.ID, governance_model.ManageGroup)
	require.NoError(t, err)
	require.NoError(t, SetGroupShare(ctx, owner, child.ID, GroupShareOption{GroupID: external.ID, MaxRole: governance_model.Developer, Revision: child.Revision}, false))
}

func TestCustomGroupRoleInheritanceAndImmediateRevocation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	child, err := CreateGroup(ctx, owner, GroupOption{Path: "custom-role-child", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	role, err := SaveGroupRole(ctx, owner, 3, 0, GroupRoleOption{Name: "局部审计员", BaseRole: governance_model.Reporter, Abilities: []string{governance_model.ReadAudit}, Revision: 1}, false)
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, owner, child.ID, GroupMemberOption{UserID: 4, Role: governance_model.Reporter, CustomRoleID: role.ID, Revision: 1}, false))
	state, err := CheckGroupAccess(ctx, 4, child.ID, governance_model.ReadAudit)
	require.NoError(t, err)
	assert.False(t, state.Abilities[governance_model.PushCode])
	_, err = SaveGroupRole(ctx, owner, 3, role.ID, GroupRoleOption{Revision: 2}, true)
	assert.ErrorIs(t, err, governance_model.ErrConflict, "正在使用的角色不能删除")
	_, err = SaveGroupRole(ctx, owner, 3, role.ID, GroupRoleOption{Name: "只读成员", BaseRole: governance_model.Reporter, Revision: 2}, false)
	require.NoError(t, err)
	_, err = CheckGroupAccess(ctx, 4, child.ID, governance_model.ReadAudit)
	assert.ErrorIs(t, err, governance_model.ErrNotFound)
	_, err = CheckGroupAccess(ctx, 4, child.ID, governance_model.ReadCode)
	require.NoError(t, err)
	_, err = SaveGroupRole(ctx, owner, 3, role.ID, GroupRoleOption{Name: "错误角色", BaseRole: governance_model.Developer, Revision: 3}, false)
	assert.ErrorIs(t, err, governance_model.ErrConflict)
}

func TestPendingInvitationKeepsCustomRoleValid(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	root, err := governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	role, err := SaveGroupRole(ctx, actor, 3, 0, GroupRoleOption{Name: "待邀请角色", BaseRole: governance_model.Reporter, Revision: root.Revision}, false)
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &governance_model.Invitation{ScopeType: "group", ScopeID: 3, Email: "pending-role@example.test", LowerEmail: "pending-role@example.test", ScopePath: "org3", Role: governance_model.Reporter, CustomRoleID: role.ID, ExpiresUnix: time.Now().Add(time.Hour).Unix()}))
	root, err = governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	_, err = SaveGroupRole(ctx, actor, 3, role.ID, GroupRoleOption{Name: role.Name, BaseRole: governance_model.Developer, Revision: root.Revision}, false)
	assert.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = SaveGroupRole(ctx, actor, 3, role.ID, GroupRoleOption{Revision: root.Revision}, true)
	assert.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = db.GetEngine(ctx).Where("custom_role_id = ?", role.ID).Cols("expires_unix").Update(&governance_model.Invitation{ExpiresUnix: time.Now().Add(-time.Hour).Unix()})
	require.NoError(t, err)
	_, err = SaveGroupRole(ctx, actor, 3, role.ID, GroupRoleOption{Revision: root.Revision}, true)
	require.NoError(t, err)
}
