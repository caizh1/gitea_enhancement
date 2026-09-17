// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestPermanentOwnerRequiresActiveDirectOrInheritedSource(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	root, err := governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(root.ID).Cols("native_owner_team_id").Update(&governance_model.Namespace{NativeOwnerTeamID: 0})
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &governance_model.Membership{ScopeType: "group", ScopeID: root.ID, UserID: 4, Role: governance_model.Owner, ExpiresUnix: 0}))
	require.ErrorIs(t, governance_model.EnsureUserCanLoseOwnerAccess(ctx, 4), governance_model.ErrConflict)
	require.NoError(t, db.Insert(ctx, &governance_model.Membership{ScopeType: "group", ScopeID: root.ID, UserID: 5, Role: governance_model.Owner, ExpiresUnix: 0}))
	require.NoError(t, governance_model.EnsureUserCanLoseOwnerAccess(ctx, 4))
	require.NoError(t, user_model.UpdateUserCols(ctx, &user_model.User{ID: 5, ProhibitLogin: true}, "prohibit_login"))
	require.ErrorIs(t, governance_model.EnsureUserCanLoseOwnerAccess(ctx, 4), governance_model.ErrConflict)
}
