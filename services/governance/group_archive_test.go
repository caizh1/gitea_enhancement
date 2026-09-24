// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestGroupArchiveTreeAtomicity(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	root, err := CreateGroup(ctx, actor, GroupOption{Name: "归档测试", Path: "archive-tree", Visibility: 2})
	require.NoError(t, err)
	child, err := CreateGroup(ctx, actor, GroupOption{Name: "子组", Path: "child", ParentID: root.ID, Visibility: 2})
	require.NoError(t, err)
	repository := &repo_model.Repository{OwnerID: child.ID, OwnerName: child.InternalName, Name: "firmware", LowerName: "firmware", OwnerNamespace: child.FullPath, DefaultBranch: "main", IsPrivate: true, IsArchived: true}
	require.NoError(t, db.Insert(ctx, repository))
	_, err = governance_model.RegisterNativeRepository(ctx, repository.ID, child.ID, repository.Name)
	require.NoError(t, err)
	impact, err := PreviewGroupArchive(ctx, actor.ID, root.ID)
	require.NoError(t, err)
	require.Equal(t, 2, impact.Groups)
	require.Equal(t, 1, impact.PreviouslyArchivedRepositories)
	option := GroupArchiveOption{Archived: true, Revision: root.Revision}
	_, err = SetGroupArchiveState(ctx, governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}, root.ID, option)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	invalidActor := actor
	invalidActor.Transport = ""
	_, err = SetGroupArchiveState(ctx, invalidActor, root.ID, option)
	require.ErrorIs(t, err, governance_model.ErrInvalid)
	unchanged, err := governance_model.GetNamespace(ctx, child.ID)
	require.NoError(t, err)
	require.False(t, unchanged.Archived, "审计失败不能留下部分归档")
	operation := &governance_model.MergeAuthorization{PullID: 999, RepoID: repository.ID, Branch: "main", Head: "head", OldTarget: "before", NewTarget: "after", Actor: actor}
	require.NoError(t, governance_model.AuthorizeMerge(ctx, operation, nil, func(context.Context) error { return nil }))
	_, err = SetGroupArchiveState(ctx, actor, root.ID, option)
	require.ErrorIs(t, err, governance_model.ErrConflict, "后代项目正在合并时不能归档父组")
	_, err = governance_model.ReconcileMerge(ctx, operation.ID, "before")
	require.NoError(t, err)
	root, err = SetGroupArchiveState(ctx, actor, root.ID, option)
	require.NoError(t, err)
	require.True(t, root.Archived)
	childState, err := governance_model.GetNamespace(ctx, child.ID)
	require.NoError(t, err)
	require.True(t, childState.Archived)
	_, err = SetGroupArchiveState(ctx, actor, child.ID, GroupArchiveOption{Archived: false, Revision: childState.Revision})
	require.ErrorIs(t, err, governance_model.ErrConflict)
	require.ErrorIs(t, repo_model.SetArchiveRepoState(ctx, repository, false), governance_model.ErrConflict)
	require.ErrorIs(t, governance_model.PrepareNativeRepositoryPath(ctx, repository.ID, 2, repository.Name), governance_model.ErrConflict, "归档群组中的项目不能通过转移脱离只读限制")
	_, err = MoveGroup(ctx, actor, root.ID, GroupOption{Path: "moved", Revision: root.Revision})
	require.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = CreateGroup(ctx, actor, GroupOption{Name: "拒绝创建", Path: "blocked", ParentID: child.ID, Visibility: 2})
	require.ErrorIs(t, err, governance_model.ErrConflict)
	root, err = SetGroupArchiveState(ctx, actor, root.ID, GroupArchiveOption{Archived: false, Revision: root.Revision})
	require.NoError(t, err)
	require.False(t, root.Archived)
	current, err := repo_model.GetRepositoryByID(ctx, repository.ID)
	require.NoError(t, err)
	require.False(t, current.IsArchived, "当前恢复整棵树的语义不保留项目原有独立归档标记；官方版本对齐仍待核实")
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "group.archived", ObjectID: root.ID}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "group.unarchived", ObjectID: root.ID}, 1)
}

func TestArchivedGroupExistingMemberManagement(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	root, err := CreateGroup(ctx, actor, GroupOption{Name: "归档成员", Path: "archive-members", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, root.ID, GroupMemberOption{UserID: 4, Role: governance_model.Developer, Revision: root.Revision}, false))
	root, err = CheckGroupAccess(ctx, actor.ID, root.ID, governance_model.ManageGroup)
	require.NoError(t, err)
	root, err = SetGroupArchiveState(ctx, actor, root.ID, GroupArchiveOption{Archived: true, Revision: root.Revision})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, root.ID, GroupMemberOption{UserID: 4, Role: governance_model.Reporter, Revision: root.Revision}, false), "归档后仍允许调整已有成员角色")
	member, err := CheckGroupAccess(ctx, 4, root.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	require.False(t, member.Abilities[governance_model.PushCode])
	require.NoError(t, SetGroupMember(ctx, actor, root.ID, GroupMemberOption{UserID: 4, Revision: member.Revision}, true), "归档不能阻止撤权")
	_, err = CheckGroupAccess(ctx, 4, root.ID, governance_model.ReadGroup)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	root, err = CheckGroupAccess(ctx, actor.ID, root.ID, governance_model.ManageGroup)
	require.NoError(t, err)
	require.ErrorIs(t, SetGroupMember(ctx, actor, root.ID, GroupMemberOption{UserID: 4, Role: governance_model.Reporter, Revision: root.Revision}, false), governance_model.ErrConflict, "归档后禁止新增邀请成员")
	require.Error(t, SetGroupMember(ctx, actor, root.ID, GroupMemberOption{UserID: actor.ID, Revision: root.Revision}, true), "最后一个有效所有者不能移除")
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "member.added", ObjectID: 4}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "member.updated", ObjectID: 4}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "member.removed", ObjectID: 4}, 1)
}
