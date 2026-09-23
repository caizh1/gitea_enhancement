// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package user

import (
	"context"
	"testing"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/optional"

	"github.com/stretchr/testify/require"
)

func TestAdminCannotProhibitLastRepositoryOwner(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := context.WithValue(t.Context(), gm.AuditActorContextKey, gm.Actor{ID: 1, Name: "user1", Kind: "user", Transport: "api"})
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	repo := &repo_model.Repository{OwnerID: owner.ID, OwnerName: owner.Name, OwnerNamespace: owner.Name, Name: "admin-owner-guard", LowerName: "admin-owner-guard", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	opts := &UpdateAuthOptions{ProhibitLogin: optional.Some(true)}
	require.ErrorIs(t, UpdateAdminUser(ctx, owner, opts, &UpdateOptions{}, optional.None[string](), false), gm.ErrConflict, "OWN-04：管理员组合入口也必须保护最后一位 Owner")
	fresh, err := user_model.GetUserByID(ctx, owner.ID)
	require.NoError(t, err)
	require.False(t, fresh.ProhibitLogin)
	require.NoError(t, db.Insert(ctx, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 5, Role: gm.Owner}))
	require.NoError(t, UpdateAdminUser(ctx, fresh, opts, &UpdateOptions{}, optional.None[string](), false))
	fresh, err = user_model.GetUserByID(ctx, owner.ID)
	require.NoError(t, err)
	require.True(t, fresh.ProhibitLogin)
	require.NoError(t, gm.EnsurePermanentRepositoryOwner(ctx, repo.ID, 0))
}
