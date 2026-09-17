// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"path/filepath"
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
	"xorm.io/xorm/names"
)

type legacyTestUser struct {
	ID            int64 `xorm:"pk"`
	Name          string
	Type          int
	Visibility    int
	NamespacePath string
}

func (*legacyTestUser) TableName() string { return "user" }

type legacyTestRepo struct {
	ID             int64 `xorm:"pk"`
	OwnerID        int64
	Name           string
	OwnerNamespace string
}

func (*legacyTestRepo) TableName() string { return "repository" }

type legacyTestTeam struct {
	ID        int64 `xorm:"pk"`
	OrgID     int64
	LowerName string
}

func (*legacyTestTeam) TableName() string { return "team" }

func TestLegacyNamespaceImportKeepsIDsAndPermissions(t *testing.T) {
	ctx := testDatabase(t)
	require.NoError(t, db.GetEngine(ctx).Sync(new(legacyTestUser), new(legacyTestRepo), new(legacyTestTeam)))
	require.NoError(t, db.Insert(ctx,
		&legacyTestUser{ID: 2, Name: "team.git", Type: 1, Visibility: 2},
		&legacyTestUser{ID: 5, Name: "automation", Type: 4, Visibility: 2},
		&legacyTestRepo{ID: 9, OwnerID: 2, Name: "firmware"},
		&legacyTestRepo{ID: 10, OwnerID: 5, Name: "automation"},
		&legacyTestTeam{ID: 20, OrgID: 2, LowerName: "owners"},
	))
	require.NoError(t, InitializeLegacyNamespaces(ctx))
	require.NoError(t, InitializeLegacyNamespaces(ctx))
	namespace, err := GetNamespace(ctx, 2)
	require.NoError(t, err)
	assert.Equal(t, "team.git", namespace.FullPath)
	assert.EqualValues(t, 20, namespace.NativeOwnerTeamID, "原生 Owner 团队保留动态授权来源，不能复制成员")
	path, err := ResolvePath(ctx, "team.git/firmware")
	require.NoError(t, err)
	assert.EqualValues(t, 9, path.ResourceID)
	count, err := db.GetEngine(ctx).Count(new(Namespace))
	require.NoError(t, err)
	assert.EqualValues(t, 2, count, "重复初始化不应重复创建命名空间")
	count, err = db.GetEngine(ctx).Count(new(Membership))
	require.NoError(t, err)
	assert.Zero(t, count, "迁移不能把原生分单元团队隐式转为通用角色")
}

func TestSchemaUsesProvidedEngine(t *testing.T) {
	_ = testDatabase(t)
	driver, connection, err := db.ConnStr(db.ConnOptions{
		Type:       setting.DatabaseTypeSQLite3,
		SQLitePath: filepath.Join(t.TempDir(), "独立迁移-test.db"), SQLiteBusyTimeout: 5000,
	})
	require.NoError(t, err)
	engine, err := xorm.NewEngine(driver, connection)
	require.NoError(t, err)
	defer engine.Close()
	engine.SetMapper(names.GonicMapper{})
	require.NoError(t, CreateSchema(engine))
	has, err := engine.ID(1).Exist(new(WriteLock))
	require.NoError(t, err)
	assert.True(t, has, "迁移必须写入传入引擎，不能误用全局数据库")
}

type legacyTestTeamUser struct {
	ID     int64 `xorm:"pk"`
	OrgID  int64
	TeamID int64
	UID    int64
}

func (*legacyTestTeamUser) TableName() string { return "team_user" }

func TestNativeNamespacePathsPreserveInternalNames(t *testing.T) {
	ctx := testDatabase(t)
	engine := db.GetEngine(ctx).Context(ctx).Engine()
	require.NoError(t, engine.Sync(new(legacyTestUser), new(legacyTestRepo), new(legacyTestTeam)))
	require.NoError(t, db.Insert(ctx, &legacyTestUser{ID: 1, Name: "internal_group", Type: 1, Visibility: 2}, &legacyTestRepo{ID: 2, OwnerID: 1, Name: "firmware"}, &legacyTestTeam{ID: 5, OrgID: 1, LowerName: "owners"}))
	testGroup(ctx, t, 1, 0, "acme")
	require.NoError(t, AddNativeNamespacePaths(engine))
	require.NoError(t, AddNativeNamespacePaths(engine))
	var user legacyTestUser
	var repo legacyTestRepo
	_, err := engine.ID(1).Get(&user)
	require.NoError(t, err)
	_, err = engine.ID(2).Get(&repo)
	require.NoError(t, err)
	assert.Equal(t, "internal_group", user.Name)
	assert.Equal(t, "acme", user.NamespacePath)
	assert.Equal(t, "acme", repo.OwnerNamespace)
	assert.Equal(t, "firmware", repo.Name)
	namespace, err := GetNamespace(ctx, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 5, namespace.NativeOwnerTeamID)
}

func TestNativeOwnerInheritanceUsesCurrentTeamMembership(t *testing.T) {
	ctx := testDatabase(t)
	require.NoError(t, db.GetEngine(ctx).Sync(new(legacyTestTeamUser)))
	testGroup(ctx, t, 1, 0, "acme")
	testGroup(ctx, t, 2, 1, "rd")
	_, err := db.GetEngine(ctx).ID(1).Cols("native_owner_team_id").Update(&Namespace{NativeOwnerTeamID: 5})
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &legacyTestTeamUser{ID: 7, OrgID: 1, TeamID: 5, UID: 11}, &Membership{ScopeType: "group", ScopeID: 2, UserID: 11, Role: Reporter}))
	grants, err := GroupGrants(ctx, 2, 11, time.Now())
	require.NoError(t, err)
	require.Len(t, grants, 2)
	assert.True(t, EffectiveAbilities(grants)[ManageGroup])
	assert.EqualValues(t, 5, grants[1].NativeTeamID)
	assert.Equal(t, "inherited", grants[1].Source)
	_, err = db.GetEngine(ctx).ID(7).Delete(new(legacyTestTeamUser))
	require.NoError(t, err)
	grants, err = GroupGrants(ctx, 2, 11, time.Now())
	require.NoError(t, err)
	require.Len(t, grants, 1)
	assert.True(t, EffectiveAbilities(grants)[ReadCode])
	assert.False(t, EffectiveAbilities(grants)[ManageGroup], "移出原生 Owner 团队后，继承权限立即消失，但保留子组直接授权")
}
