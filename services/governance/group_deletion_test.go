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
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestGroupDeletionRestoresOriginalState(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	root, err := CreateGroup(ctx, actor, GroupOption{Name: "删除恢复", Path: "deletion-root", Visibility: 2})
	require.NoError(t, err)
	child, err := CreateGroup(ctx, actor, GroupOption{Name: "归档子组", Path: "child", ParentID: root.ID, Visibility: 2})
	require.NoError(t, err)
	repo := &repo_model.Repository{OwnerID: child.ID, OwnerName: child.InternalName, Name: "sample", LowerName: "sample", OwnerNamespace: child.FullPath, IsPrivate: true, IsArchived: true}
	require.NoError(t, db.Insert(ctx, repo))
	_, err = governance_model.RegisterNativeRepository(ctx, repo.ID, child.ID, repo.Name)
	require.NoError(t, err)
	child, err = SetGroupArchiveState(ctx, actor, child.ID, GroupArchiveOption{Archived: true, Revision: child.Revision})
	require.NoError(t, err)
	root, err = CheckGroupAccess(ctx, actor.ID, root.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	option := GroupDeletionOption{Revision: root.Revision, ConfirmationPath: root.FullPath}
	badActor := actor
	badActor.Transport = ""
	_, err = ScheduleGroupDeletion(ctx, badActor, root.ID, option)
	require.Error(t, err, "审计失败必须回滚待删除与路径变化")
	unchanged, err := governance_model.GetNamespace(ctx, root.ID)
	require.NoError(t, err)
	require.Zero(t, unchanged.DeleteAfter)
	require.Equal(t, "deletion-root", unchanged.FullPath)
	root, err = ScheduleGroupDeletion(ctx, actor, root.ID, option)
	require.NoError(t, err)
	require.InDelta(t, time.Now().Add(30*24*time.Hour).Unix(), root.DeleteAfter, 5)
	require.Contains(t, root.Slug, "-deletion-")
	require.True(t, root.Archived)
	_, err = CreateGroup(ctx, actor, GroupOption{Name: "拒绝写入", Path: "blocked", ParentID: child.ID, Visibility: 2})
	require.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = RestoreGroupDeletion(ctx, actor, root.ID, option)
	require.ErrorIs(t, err, governance_model.ErrConflict, "旧页面的恢复请求不能覆盖新状态")
	root, err = RestoreGroupDeletion(ctx, actor, root.ID, GroupDeletionOption{Revision: root.Revision, ConfirmationPath: root.FullPath})
	require.NoError(t, err)
	require.Equal(t, "deletion-root", root.FullPath)
	require.Zero(t, root.DeleteAfter)
	require.False(t, root.Archived)
	currentChild, err := governance_model.GetNamespace(ctx, child.ID)
	require.NoError(t, err)
	require.True(t, currentChild.Archived, "恢复必须保留子组原有归档")
	currentRepo, err := repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	require.True(t, currentRepo.IsArchived)
	unittest.AssertCount(t, &governance_model.GroupDeletion{GroupID: root.ID}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "group.deletion_scheduled", ObjectID: root.ID}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "group.restored", ObjectID: root.ID}, 1)
}

func TestGroupDeletionRevokedInitiatorRestores(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	group, err := CreateGroup(ctx, actor, GroupOption{Name: "到期撤权", Path: "deletion-revoked", Visibility: 2})
	require.NoError(t, err)
	group, err = ScheduleGroupDeletion(ctx, actor, group.ID, GroupDeletionOption{Revision: group.Revision, ConfirmationPath: group.FullPath})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(actor.ID).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: true})
	require.NoError(t, err)
	called := false
	require.NoError(t, ProcessGroupDeletion(ctx, group.ID, time.Now().Add(31*24*time.Hour), nil, GroupDeletionOption{}, func(context.Context, []*governance_model.Namespace) error { called = true; return nil }))
	require.False(t, called, "删除发起人已经停用，不能进入物理删除")
	current, err := governance_model.GetNamespace(ctx, group.ID)
	require.NoError(t, err)
	require.Zero(t, current.DeleteAfter)
	require.False(t, current.Archived)
	require.Equal(t, "deletion-revoked", current.FullPath)
	unittest.AssertCount(t, &governance_model.GroupDeletion{GroupID: group.ID}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "group.restored", ObjectID: group.ID}, 1)
}
