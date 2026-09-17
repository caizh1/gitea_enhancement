// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package pull

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestMergeCodeIsIndependentFromPushCode(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	mergeUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	pushUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	mergeRole := &governance_model.CustomRole{RootID: repo.OwnerID, Name: "仅合并", BaseRole: governance_model.Reporter, Abilities: []string{governance_model.MergeCode}}
	pushRole := &governance_model.CustomRole{RootID: repo.OwnerID, Name: "仅推送", BaseRole: governance_model.Reporter, Abilities: []string{governance_model.PushCode}}
	require.NoError(t, db.Insert(ctx, mergeRole, pushRole))
	require.NoError(t, db.Insert(ctx,
		&governance_model.Membership{ScopeType: "group", ScopeID: repo.OwnerID, UserID: mergeUser.ID, Role: governance_model.Reporter, CustomRoleID: mergeRole.ID},
		&governance_model.Membership{ScopeType: "group", ScopeID: repo.OwnerID, UserID: pushUser.ID, Role: governance_model.Reporter, CustomRoleID: pushRole.ID},
	))
	mergePermission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, mergeUser)
	require.NoError(t, err)
	pushPermission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, pushUser)
	require.NoError(t, err)

	allowed, err := isUserAllowedToMergeInRepoBranch(ctx, repo.ID, "governance-unprotected", mergePermission, mergeUser)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = isUserAllowedToMergeInRepoBranch(ctx, repo.ID, "governance-unprotected", pushPermission, pushUser)
	require.NoError(t, err)
	require.False(t, allowed, "自定义 PushCode 不能隐式获得 MergeCode")
	require.NoError(t, user_model.UpdateUserCols(ctx, &user_model.User{ID: pushUser.ID, IsAuditor: true}, "is_auditor"))
	pushUser = unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: pushUser.ID})
	pushPermission, err = access_model.GetIndividualUserRepoPermission(ctx, repo, pushUser)
	require.NoError(t, err)
	allowed, err = isUserAllowedToMergeInRepoBranch(ctx, repo.ID, "governance-unprotected", pushPermission.ForMutation(), pushUser)
	require.NoError(t, err)
	require.False(t, allowed, "审计员只读快照不能让 PushCode 绕过独立 MergeCode")
}
