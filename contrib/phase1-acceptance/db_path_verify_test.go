// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package acceptance

import (
	"os"
	"strings"
	"testing"

	"gitea.dev/models/db"
	"gitea.dev/models/governance"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
	"xorm.io/xorm/names"
)

// TestInstalledPathHashes 只连接显式指定的隔离验收库，验证迁移后的真实路径读写。
func TestInstalledPathHashes(t *testing.T) {
	if os.Getenv("PHASE1_DB_ALLOW_WRITE") != "isolated-phase1-only" {
		t.Skip("仅在明确指定隔离验收库时运行")
	}
	driver, dsn := os.Getenv("PHASE1_DB_DRIVER"), os.Getenv("PHASE1_DB_DSN")
	mode := os.Getenv("PHASE1_DB_MODE")
	if driver != "postgres" && driver != "mysql" && driver != "sqlite3" {
		t.Fatal("数据库驱动必须为 postgres、mysql 或 sqlite3")
	}
	if dsn == "" || mode != "old" && mode != "new" || !strings.Contains(dsn, "phase1") {
		t.Fatal("必须显式指定包含 phase1 的隔离库连接串，以及 old 或 new 模式")
	}
	engine, err := xorm.NewEngine(driver, dsn)
	require.NoError(t, err)
	engine.SetMapper(names.GonicMapper{})
	require.NoError(t, engine.Ping())
	db.SetDefaultEngine(t.Context(), engine)
	t.Cleanup(db.UnsetDefaultEngine)

	depth := 19
	if mode == "new" {
		depth = 9
	}
	parentID := int64(0)
	for id := int64(1); id <= int64(depth); id++ {
		if mode == "new" {
			insertTestNamespace(t, id, parentID, strings.Repeat("a", 90))
		}
		parentID = id
	}
	base := strings.TrimSuffix(strings.Repeat(strings.Repeat("a", 90)+"/", depth), "/")
	for _, item := range []struct {
		id   int64
		slug string
	}{{int64(depth + 1), "lower"}, {int64(depth + 2), "lower2"}} {
		if mode == "new" {
			insertTestNamespace(t, item.id, parentID, item.slug)
		}
		path, err := governance.ResolvePath(t.Context(), base+"/"+item.slug)
		require.NoError(t, err)
		require.Equal(t, item.id, path.ResourceID)
		require.False(t, path.Alias)
	}
	require.Greater(t, len(base), 768)
	insertTestNamespace(t, int64(depth+3), parentID, "lower3")
	path, err := governance.ResolvePath(t.Context(), base+"/lower3")
	require.NoError(t, err)
	require.EqualValues(t, depth+3, path.ResourceID)
	require.ErrorIs(t, governance.InsertNamespace(t.Context(), &governance.Namespace{
		ID: int64(depth + 4), ParentID: parentID, Slug: "LOWER3", Kind: "group", Visibility: 2,
	}), governance.ErrConflict)
}

func insertTestNamespace(t *testing.T, id, parentID int64, slug string) {
	t.Helper()
	current, err := governance.GetNamespace(t.Context(), id)
	if err == nil {
		require.Equal(t, parentID, current.ParentID)
		require.Equal(t, slug, current.Slug)
		return
	}
	require.ErrorIs(t, err, governance.ErrNotFound)
	require.NoError(t, governance.InsertNamespace(t.Context(), &governance.Namespace{
		ID: id, ParentID: parentID, Slug: slug, Kind: "group", Visibility: 2,
	}))
}
