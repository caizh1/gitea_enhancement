// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/modules/setting"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceOwnerIdentityRealDatabase(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	if os.Getenv("GITEA_TEST_DATABASE") == "pgsql" {
		require.True(t, setting.Database.Type.IsPostgreSQL(), "必须使用真正 PostgreSQL 集成测试引擎")
		rows, err := db.GetEngine(ctx).Query("SELECT current_database() AS database_name")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, os.Getenv("TEST_PGSQL_DBNAME"), string(rows[0]["database_name"]))
		t.Logf("数据库引擎：%s；数据库：%s", setting.Database.Type, string(rows[0]["database_name"]))
	}
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	limited := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}
	source, err := governance_service.CreateGroup(ctx, owner, governance_service.GroupOption{Name: "集成所有者判定", Path: "integration-owner-source", Visibility: 2})
	require.NoError(t, err)
	invited, err := governance_service.CreateGroup(ctx, owner, governance_service.GroupOption{Name: "集成所有者共享", Path: "integration-owner-invited", Visibility: 2})
	require.NoError(t, err)
	role, err := governance_service.SaveGroupRole(ctx, owner, source.ID, 0, governance_service.GroupRoleOption{Name: "不完整所有者能力", BaseRole: governance_model.Reporter, Abilities: []string{governance_model.ManageGroup, governance_model.ManageGroupMembers, governance_model.ManageAudit}, Revision: source.Revision}, false)
	require.NoError(t, err)
	source, err = governance_service.CheckGroupAccess(ctx, owner.ID, source.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, owner, source.ID, governance_service.GroupMemberOption{UserID: limited.ID, Role: governance_model.Reporter, CustomRoleID: role.ID, Revision: source.Revision}, false))
	require.NoError(t, governance_service.SetGroupMember(ctx, owner, invited.ID, governance_service.GroupMemberOption{UserID: limited.ID, Role: governance_model.Maintainer, Revision: invited.Revision}, false))
	invited, err = governance_service.CheckGroupAccess(ctx, owner.ID, invited.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, owner, invited.ID, governance_service.GroupMemberOption{UserID: 5, Role: governance_model.Owner, Revision: invited.Revision}, false))
	source, err = governance_service.CheckGroupAccess(ctx, owner.ID, source.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupShare(ctx, owner, source.ID, governance_service.GroupShareOption{GroupID: invited.ID, MaxRole: governance_model.Maintainer, Revision: source.Revision}, false))

	grants, err := governance_model.GroupGrants(ctx, source.ID, limited.ID, time.Now())
	require.NoError(t, err)
	require.False(t, governance_model.HasOwnerGrant(grants))
	abilities := governance_model.EffectiveAbilities(grants)
	ownerAbilities, err := governance_model.AbilitiesFor(governance_model.Owner, nil)
	require.NoError(t, err)
	for ability := range ownerAbilities {
		require.True(t, abilities[ability], "受支持的两条授权合并覆盖 %s", ability)
	}
	actualOwner, err := organization.IsOrganizationOwner(ctx, source.ID, limited.ID)
	require.NoError(t, err)
	require.False(t, actualOwner)
	require.ErrorIs(t, governance_service.CheckAuditAccess(ctx, limited.ID, "group", source.ID), governance_model.ErrNotFound)
	_, err = governance_service.PreviewGroupArchive(ctx, limited.ID, source.ID)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	_, err = governance_service.SetGroupArchiveState(ctx, limited, source.ID, governance_service.GroupArchiveOption{Archived: true, Revision: source.Revision})
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	_, err = governance_service.ScheduleGroupDeletion(ctx, limited, source.ID, governance_service.GroupDeletionOption{Revision: source.Revision, ConfirmationPath: source.FullPath})
	require.ErrorIs(t, err, governance_model.ErrNotFound)

	actualOwner, err = organization.IsOrganizationOwner(ctx, source.ID, owner.ID)
	require.NoError(t, err)
	require.True(t, actualOwner)
	source, err = governance_service.CheckGroupAccess(ctx, owner.ID, source.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupShare(ctx, owner, source.ID, governance_service.GroupShareOption{GroupID: invited.ID, MaxRole: governance_model.Owner, Revision: source.Revision}, false))
	actualOwner, err = organization.IsOrganizationOwner(ctx, source.ID, 5)
	require.NoError(t, err)
	require.True(t, actualOwner, "单条完整 Owner 共享仍有效")
	source, err = governance_service.CheckGroupAccess(ctx, owner.ID, source.ID, governance_model.ReadGroup)
	require.NoError(t, err)
	source, err = governance_service.ScheduleGroupDeletion(ctx, owner, source.ID, governance_service.GroupDeletionOption{Revision: source.Revision, ConfirmationPath: source.FullPath})
	require.NoError(t, err)
	option := governance_service.GroupDeletionOption{Revision: source.Revision, ConfirmationPath: source.FullPath}
	_, err = governance_service.RestoreGroupDeletion(ctx, limited, source.ID, option)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	removed := false
	err = governance_service.ProcessGroupDeletion(ctx, source.ID, time.Now(), &limited, option, func(context.Context, []*governance_model.Namespace) error { removed = true; return nil })
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	require.False(t, removed)
	_, err = governance_service.RestoreGroupDeletion(ctx, owner, source.ID, option)
	require.NoError(t, err)
}
