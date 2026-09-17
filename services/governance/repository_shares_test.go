// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositorySharingIncludesInheritedMembersAndRevokesOnlyItsSource(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	target, err := CreateGroup(ctx, actor, GroupOption{Path: "sharing-target", Visibility: 2})
	require.NoError(t, err)
	child, err := CreateGroup(ctx, actor, GroupOption{Path: "child", ParentID: target.ID, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, target.ID, GroupMemberOption{UserID: 5, Role: governance_model.Developer, Revision: target.Revision}, false))
	repo := &repo_model.Repository{OwnerID: 3, OwnerName: "org3", Name: "shared-only", LowerName: "shared-only", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypePullRequests}))
	reader := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, reader)
	require.NoError(t, err)
	assert.False(t, permission.CanRead(unit.TypeCode))
	state, err := ListRepositoryShares(ctx, 2, repo.ID, 0)
	require.NoError(t, err)
	option := GroupShareOption{GroupID: child.ID, MaxRole: governance_model.Reporter, Revision: state.Revision, ExpiresUnix: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, SetRepositoryShare(ctx, actor, repo.ID, option, false))
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "share.created", ScopeType: "repository", ScopeID: repo.ID}, 1)
	permission, err = access_model.GetIndividualUserRepoPermission(ctx, repo, reader)
	require.NoError(t, err)
	assert.True(t, permission.CanRead(unit.TypeCode), "项目共享包含被邀请组的继承成员")
	assert.False(t, permission.CanWrite(unit.TypeCode), "共享角色上限限制原有 Developer")
	assert.ErrorIs(t, SetRepositoryShare(ctx, actor, repo.ID, option, true), governance_model.ErrConflict, "旧预览不能撤销新状态")
	state, err = ListRepositoryShares(ctx, 2, repo.ID, 0)
	require.NoError(t, err)
	require.Len(t, state.Shares, 1)
	option.Revision = state.Revision
	require.NoError(t, SetRepositoryShare(ctx, actor, repo.ID, option, true))
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "share.removed", ScopeType: "repository", ScopeID: repo.ID}, 1)
	permission, err = access_model.GetIndividualUserRepoPermission(ctx, repo, reader)
	require.NoError(t, err)
	assert.False(t, permission.CanRead(unit.TypeCode))
	grants, err := governance_model.GroupGrants(ctx, child.ID, reader.ID, time.Now())
	require.NoError(t, err)
	assert.True(t, governance_model.EffectiveAbilities(grants)[governance_model.PushCode], "撤销项目共享保留原群组授权")
	admin := governance_model.Actor{ID: 1, Name: "user1", Kind: "user", Transport: "api"}
	state, err = ListRepositoryShares(ctx, 1, repo.ID, 0)
	require.NoError(t, err)
	option.Revision = state.Revision
	assert.ErrorIs(t, SetRepositoryShare(ctx, admin, repo.ID, option, false), governance_model.ErrNotFound, "管理员可见被邀请组并不等于具有其成员资格")
}
