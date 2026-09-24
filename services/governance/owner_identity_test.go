// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"
	"time"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestOrganizationOwnerRequiresCompleteGrant(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	source, err := CreateGroup(ctx, owner, GroupOption{Name: "所有者判定来源", Path: "owner-identity-source", Visibility: 2})
	require.NoError(t, err)
	invited, err := CreateGroup(ctx, owner, GroupOption{Name: "所有者判定共享", Path: "owner-identity-invited", Visibility: 2})
	require.NoError(t, err)
	role, err := SaveGroupRole(ctx, owner, source.ID, 0, GroupRoleOption{Name: "仅三项群组管理权", BaseRole: governance_model.Reporter, Abilities: []string{governance_model.ManageGroup, governance_model.ManageGroupMembers, governance_model.ManageAudit}, Revision: source.Revision}, false)
	require.NoError(t, err)
	source, err = CheckGroupAccess(ctx, owner.ID, source.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, owner, source.ID, GroupMemberOption{UserID: 4, Role: governance_model.Reporter, CustomRoleID: role.ID, Revision: source.Revision}, false))
	require.NoError(t, SetGroupMember(ctx, owner, invited.ID, GroupMemberOption{UserID: 4, Role: governance_model.Maintainer, Revision: invited.Revision}, false))
	invited, err = CheckGroupAccess(ctx, owner.ID, invited.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, owner, invited.ID, GroupMemberOption{UserID: 5, Role: governance_model.Owner, Revision: invited.Revision}, false))
	source, err = CheckGroupAccess(ctx, owner.ID, source.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	require.NoError(t, SetGroupShare(ctx, owner, source.ID, GroupShareOption{GroupID: invited.ID, MaxRole: governance_model.Maintainer, Revision: source.Revision}, false))

	grants, err := governance_model.GroupGrants(ctx, source.ID, 4, time.Now())
	require.NoError(t, err)
	require.False(t, governance_model.HasOwnerGrant(grants))
	abilities := governance_model.EffectiveAbilities(grants)
	ownerAbilities, err := governance_model.AbilitiesFor(governance_model.Owner, nil)
	require.NoError(t, err)
	for ability := range ownerAbilities {
		require.True(t, abilities[ability], "两条不完整授权合并后覆盖了 %s", ability)
	}
	actualOwner, err := organization.IsOrganizationOwner(ctx, source.ID, 4)
	require.NoError(t, err)
	require.False(t, actualOwner, "能力并集不能构造原生 Owner 身份")

	actualOwner, err = organization.IsOrganizationOwner(ctx, source.ID, owner.ID)
	require.NoError(t, err)
	require.True(t, actualOwner, "原有真正 Owner 不受影响")
	child, err := CreateGroup(ctx, owner, GroupOption{Name: "继承所有者", Path: "child", ParentID: source.ID, Visibility: 2})
	require.NoError(t, err)
	actualOwner, err = organization.IsOrganizationOwner(ctx, child.ID, owner.ID)
	require.NoError(t, err)
	require.True(t, actualOwner, "祖先完整 Owner 授权继续继承")

	source, err = CheckGroupAccess(ctx, owner.ID, source.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	require.NoError(t, SetGroupShare(ctx, owner, source.ID, GroupShareOption{GroupID: invited.ID, MaxRole: governance_model.Owner, Revision: source.Revision}, false))
	actualOwner, err = organization.IsOrganizationOwner(ctx, source.ID, 5)
	require.NoError(t, err)
	require.True(t, actualOwner, "完整 Owner 上限的群组共享仍有效")
}
