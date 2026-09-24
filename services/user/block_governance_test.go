// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package user

import (
	"fmt"
	"testing"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestNativeBlockRechecksActorAndLifecycle(t *testing.T) {
	t.Run("revoked administrator snapshot", func(t *testing.T) {
		unittest.PrepareTestEnv(t)
		ctx := t.Context()
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		blocker := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 28})
		require.True(t, CanBlockUser(ctx, doer, blocker, blockee))
		_, err := db.GetEngine(ctx).ID(doer.ID).Cols("is_admin").Update(&user_model.User{IsAdmin: false})
		require.NoError(t, err)
		require.ErrorIs(t, BlockUser(ctx, doer, blocker, blockee, ""), user_model.ErrCanNotBlock)
		_, err = user_model.GetBlocking(ctx, blocker.ID, blockee.ID)
		require.Error(t, err)
	})

	t.Run("revoked acting administrator cannot use owner identity", func(t *testing.T) {
		unittest.PrepareTestEnv(t)
		ctx := t.Context()
		admin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		blocker := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})
		blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29})
		ctx = gm.WithAuditActor(ctx, gm.Actor{ID: admin.ID, ActingAsID: doer.ID, Kind: "user", Transport: "api"})
		_, err := db.GetEngine(ctx).ID(admin.ID).Cols("is_admin").Update(&user_model.User{IsAdmin: false})
		require.NoError(t, err)
		require.ErrorIs(t, BlockUser(ctx, doer, blocker, blockee, ""), gm.ErrNotFound)
		_, err = user_model.GetBlocking(ctx, blocker.ID, blockee.ID)
		require.Error(t, err)
	})

	t.Run("revoked owner cannot unblock", func(t *testing.T) {
		unittest.PrepareTestEnv(t)
		ctx := t.Context()
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 15})
		blocker := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 17})
		blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 28})
		require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
		require.True(t, CanUnblockUser(ctx, doer, blocker, blockee))
		_, err := db.GetEngine(ctx).Where("org_id = ? AND team_id = ? AND uid = ?", blocker.ID, 5, doer.ID).Delete(new(organization.TeamUser))
		require.NoError(t, err)
		require.ErrorIs(t, UnblockUser(ctx, doer, blocker, blockee), user_model.ErrCanNotUnblock)
		_, err = user_model.GetBlocking(ctx, blocker.ID, blockee.ID)
		require.NoError(t, err)
	})

	t.Run("archived group cannot unblock", func(t *testing.T) {
		unittest.PrepareTestEnv(t)
		ctx := t.Context()
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		blocker := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 17})
		blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 28})
		require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
		_, err := db.GetEngine(ctx).ID(blocker.ID).Cols("archived").Update(&gm.Namespace{Archived: true})
		require.NoError(t, err)
		require.ErrorIs(t, UnblockUser(ctx, doer, blocker, blockee), gm.ErrConflict)
		_, err = user_model.GetBlocking(ctx, blocker.ID, blockee.ID)
		require.NoError(t, err)
	})

	t.Run("pending deletion cannot unblock", func(t *testing.T) {
		unittest.PrepareTestEnv(t)
		ctx := t.Context()
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 15})
		blocker := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 17})
		blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 28})
		require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
		_, err := db.GetEngine(ctx).ID(blocker.ID).Cols("delete_after").Update(&gm.Namespace{DeleteAfter: 1})
		require.NoError(t, err)
		require.ErrorIs(t, UnblockUser(ctx, doer, blocker, blockee), gm.ErrConflict)
	})

	t.Run("archived group can narrow access and edit note", func(t *testing.T) {
		unittest.PrepareTestEnv(t)
		ctx := t.Context()
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		blocker := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})
		blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29})
		require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
		_, err := db.GetEngine(ctx).ID(blocker.ID).Cols("archived").Update(&gm.Namespace{Archived: true})
		require.NoError(t, err)
		require.NoError(t, BlockUser(ctx, doer, blocker, blockee, "before"))
		require.NoError(t, UpdateBlockingNote(ctx, doer, blocker, blockee, "after"))
		block, err := user_model.GetBlocking(ctx, blocker.ID, blockee.ID)
		require.NoError(t, err)
		require.Equal(t, "after", block.Note)
	})

	t.Run("revoked owner cannot edit note", func(t *testing.T) {
		unittest.PrepareTestEnv(t)
		ctx := t.Context()
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 15})
		blocker := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 17})
		blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 28})
		require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
		block, err := user_model.GetBlocking(ctx, blocker.ID, blockee.ID)
		require.NoError(t, err)
		_, err = db.GetEngine(ctx).Where("org_id = ? AND team_id = ? AND uid = ?", blocker.ID, 5, doer.ID).Delete(new(organization.TeamUser))
		require.NoError(t, err)
		require.ErrorIs(t, UpdateBlockingNote(ctx, doer, blocker, blockee, "denied"), user_model.ErrCanNotBlock)
		current, err := user_model.GetBlocking(ctx, blocker.ID, blockee.ID)
		require.NoError(t, err)
		require.Equal(t, block.Note, current.Note)
	})

	t.Run("parent and child owner boundaries", func(t *testing.T) {
		unittest.PrepareTestEnv(t)
		ctx := t.Context()
		require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
		parentOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		childOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
		parent := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})
		blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29})
		actor := gm.Actor{ID: parentOwner.ID, Name: parentOwner.Name, Kind: "user", Transport: "api"}
		child, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "block-child", ParentID: parent.ID, Visibility: 2})
		require.NoError(t, err)
		require.NoError(t, governance_service.SetGroupMember(ctx, actor, child.ID, governance_service.GroupMemberOption{UserID: childOwner.ID, Role: gm.Owner, Revision: child.Revision}, false))
		childUser, err := user_model.GetUserByID(ctx, child.ID)
		require.NoError(t, err)
		require.True(t, CanBlockUser(ctx, parentOwner, childUser, blockee))
		require.True(t, CanBlockUser(ctx, childOwner, childUser, blockee))
		require.False(t, CanBlockUser(ctx, childOwner, parent, blockee))
		require.NoError(t, BlockUser(ctx, childOwner, childUser, blockee, ""))
		require.NoError(t, UnblockUser(ctx, parentOwner, childUser, blockee))
		require.NoError(t, BlockUser(ctx, parentOwner, childUser, blockee, ""))
		_, err = db.GetEngine(ctx).ID(parent.ID).Cols("archived").Update(&gm.Namespace{Archived: true})
		require.NoError(t, err)
		require.ErrorIs(t, UnblockUser(ctx, childOwner, childUser, blockee), gm.ErrConflict)
	})

	t.Run("personal blocker remains compatible", func(t *testing.T) {
		unittest.PrepareTestEnv(t)
		ctx := t.Context()
		owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 28})
		require.NoError(t, BlockUser(ctx, owner, owner, blockee, "personal"))
		require.NoError(t, UpdateBlockingNote(ctx, owner, owner, blockee, "personal updated"))
		require.NoError(t, UnblockUser(ctx, owner, owner, blockee))
		_, err := user_model.GetBlocking(ctx, owner.ID, blockee.ID)
		require.Error(t, err)
	})

	t.Run("more than one page of collaborations", func(t *testing.T) {
		unittest.PrepareTestEnv(t)
		ctx := t.Context()
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		blocker := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})
		blockee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29})
		for i := range 26 {
			name := fmt.Sprintf("block-page-%02d", i)
			repo := &repo_model.Repository{OwnerID: blocker.ID, OwnerName: blocker.Name, Name: name, LowerName: name, IsPrivate: true}
			require.NoError(t, db.Insert(ctx, repo))
			require.NoError(t, db.Insert(ctx, &repo_model.Collaboration{RepoID: repo.ID, UserID: blockee.ID}))
		}
		before, err := db.GetEngine(ctx).Where("repository.owner_id = ? AND collaboration.user_id = ?", blocker.ID, blockee.ID).
			Join("INNER", "repository", "repository.id = collaboration.repo_id").Count(new(repo_model.Collaboration))
		require.NoError(t, err)
		require.GreaterOrEqual(t, before, int64(26))
		require.NoError(t, BlockUser(ctx, doer, blocker, blockee, ""))
		after, err := db.GetEngine(ctx).Where("repository.owner_id = ? AND collaboration.user_id = ?", blocker.ID, blockee.ID).
			Join("INNER", "repository", "repository.id = collaboration.repo_id").Count(new(repo_model.Collaboration))
		require.NoError(t, err)
		require.Zero(t, after)
	})
}
