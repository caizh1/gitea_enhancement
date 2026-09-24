// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/setting"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
	"xorm.io/xorm/names"
)

func testDatabase(t *testing.T) context.Context {
	t.Helper()
	driver, connection, err := db.ConnStr(db.ConnOptions{
		Type:       setting.DatabaseTypeSQLite3,
		SQLitePath: filepath.Join(t.TempDir(), "治理-test.db"), SQLiteBusyTimeout: 5000,
	})
	require.NoError(t, err)
	if postgres := os.Getenv("GOVERNANCE_TEST_POSTGRES_DSN"); postgres != "" {
		require.Contains(t, postgres, "dbname=governance_test", "只允许专用验收数据库")
		driver, connection = "postgres", postgres
	}
	engine, err := xorm.NewEngine(driver, connection)
	require.NoError(t, err)
	engine.SetMapper(names.GonicMapper{})
	if driver == "postgres" {
		schema := "governance_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err := engine.Exec("CREATE SCHEMA " + schema)
		require.NoError(t, err)
		engine.SetSchema(schema)
		deferCleanup := func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := engine.Context(cleanupCtx).Exec("DROP SCHEMA " + schema + " CASCADE")
			assert.NoError(t, err)
		}
		// 先登记连接清理，后登记模式清理，测试结束时反向执行。
		t.Cleanup(db.UnsetDefaultEngine)
		t.Cleanup(deferCleanup)
	} else {
		t.Cleanup(db.UnsetDefaultEngine)
	}
	db.SetDefaultEngine(t.Context(), engine)
	require.NoError(t, engine.Sync(Beans()...))
	require.NoError(t, db.Insert(t.Context(), &WriteLock{ID: 1}))
	return t.Context()
}

func testGroup(ctx context.Context, t *testing.T, id, parentID int64, slug string) {
	t.Helper()
	require.NoError(t, InsertNamespace(ctx, &Namespace{ID: id, ParentID: parentID, Slug: slug, Kind: "group", Visibility: 2}))
}

func TestNamespaceDepthAndCollision(t *testing.T) {
	ctx := testDatabase(t)
	for id := int64(1); id <= MaxDepth; id++ {
		testGroup(ctx, t, id, id-1, "division")
	}
	assert.ErrorIs(t, InsertNamespace(ctx, &Namespace{ID: 21, ParentID: 20, Slug: "overflow", Kind: "group", Visibility: 2}), ErrInvalid)
	assert.ErrorIs(t, InsertNamespace(ctx, &Namespace{ID: 22, ParentID: 0, Slug: "DIVISION", Kind: "group", Visibility: 2}), ErrConflict)
	assert.ErrorIs(t, InsertNamespace(ctx, &Namespace{ID: 23, ParentID: 1, Slug: "public", Kind: "group", Visibility: 0}), ErrConflict)
	assert.ErrorIs(t, ValidateSlug("../escape"), ErrInvalid)
	assert.ErrorIs(t, ValidateSlug("encoded%2fpath"), ErrInvalid)
	chain, err := Ancestors(ctx, 20)
	require.NoError(t, err)
	require.Len(t, chain, 20)
	assert.EqualValues(t, 20, chain[0].ID)
	assert.EqualValues(t, 1, chain[19].ID)
}

func TestLongNamespacePathsKeepFullIdentity(t *testing.T) {
	ctx := testDatabase(t)
	parentID := int64(0)
	for id := int64(1); id <= 9; id++ {
		testGroup(ctx, t, id, parentID, strings.Repeat("a", 90))
		parentID = id
	}
	for _, item := range []struct {
		id   int64
		slug string
	}{{10, "lower"}, {11, "LOWER2"}} {
		testGroup(ctx, t, item.id, parentID, item.slug)
	}
	left, err := GetNamespace(ctx, 10)
	require.NoError(t, err)
	right, err := GetNamespace(ctx, 11)
	require.NoError(t, err)
	require.Greater(t, len(left.LowerPath), 768)
	require.Equal(t, left.LowerPath[:768], right.LowerPath[:768])
	require.NotEqual(t, left.LowerPathHash, right.LowerPathHash)
	for _, namespace := range []*Namespace{left, right} {
		path, err := ResolvePath(ctx, namespace.FullPath)
		require.NoError(t, err)
		assert.Equal(t, namespace.ID, path.ResourceID)
	}
	assert.ErrorIs(t, InsertNamespace(ctx, &Namespace{ID: 12, ParentID: parentID, Slug: "LOWER", Kind: "group", Visibility: 2}), ErrConflict)
}

func TestPermissionSourcesAndSharing(t *testing.T) {
	ctx := testDatabase(t)
	testGroup(ctx, t, 1, 0, "acme")
	testGroup(ctx, t, 2, 1, "rd")
	testGroup(ctx, t, 3, 1, "sales")
	testGroup(ctx, t, 4, 0, "supplier")
	testGroup(ctx, t, 5, 4, "engineering")
	now := time.Unix(2000000000, 0)
	require.NoError(t, db.Insert(ctx,
		&Membership{ScopeType: "group", ScopeID: 1, UserID: 11, Role: Reporter},
		&Membership{ScopeType: "group", ScopeID: 2, UserID: 11, Role: Developer},
		&Membership{ScopeType: "group", ScopeID: 4, UserID: 12, Role: Developer},
		&Membership{ScopeType: "group", ScopeID: 5, UserID: 13, Role: Developer},
		&Membership{ScopeType: "group", ScopeID: 2, UserID: 14, Role: Owner, ExpiresUnix: now.Unix()},
		&Share{ScopeType: "group", ScopeID: 2, GroupID: 5, MaxRole: Developer},
		&Share{ScopeType: "repository", ScopeID: 99, GroupID: 5, MaxRole: Reporter},
	))
	grants, err := GroupGrants(ctx, 2, 11, now)
	require.NoError(t, err)
	require.Len(t, grants, 2)
	assert.True(t, EffectiveAbilities(grants)[PushCode])
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "group", 2, 11).Delete(new(Membership))
	require.NoError(t, err)
	grants, err = GroupGrants(ctx, 2, 11, now)
	require.NoError(t, err)
	require.Len(t, grants, 1)
	assert.Equal(t, "inherited", grants[0].Source)
	assert.True(t, EffectiveAbilities(grants)[ReadCode])
	assert.False(t, EffectiveAbilities(grants)[PushCode])
	grants, err = GroupGrants(ctx, 2, 12, now)
	require.NoError(t, err)
	assert.Empty(t, grants, "群组共享不能引入受邀群组的继承成员")
	grants, err = RepositoryGrants(ctx, 99, 2, 12, now)
	require.NoError(t, err)
	assert.True(t, EffectiveAbilities(grants)[ReadCode], "项目共享可以引入受邀群组的继承成员")
	assert.False(t, EffectiveAbilities(grants)[PushCode], "共享上限不能被原角色突破")
	grants, err = GroupGrants(ctx, 2, 13, now)
	require.NoError(t, err)
	assert.True(t, EffectiveAbilities(grants)[PushCode])
	grants, err = GroupGrants(ctx, 3, 13, now)
	require.NoError(t, err)
	assert.Empty(t, grants, "子组授权不能扩散到兄弟群组")
	grants, err = GroupGrants(ctx, 2, 14, now)
	require.NoError(t, err)
	assert.Empty(t, grants, "到期秒即失效")
}

func TestPlannerAndCustomRoleCeiling(t *testing.T) {
	planner, err := AbilitiesFor(Planner, []string{ReadAudit})
	require.NoError(t, err)
	reporter, err := AbilitiesFor(Reporter, nil)
	require.NoError(t, err)
	result := planner.Intersect(reporter)
	assert.False(t, result[ReadCode], "共享不能根据角色数字给 Planner 新增代码权限")
	assert.False(t, result[ReadAudit], "自定义能力受共享上限约束")
	_, err = AbilitiesFor(Planner, []string{"未知权限"})
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestNamespaceMoveIsAtomicAndKeepsAliases(t *testing.T) {
	ctx := testDatabase(t)
	testGroup(ctx, t, 1, 0, "acme")
	testGroup(ctx, t, 2, 1, "rd_core")
	testGroup(ctx, t, 3, 2, "storage")
	testGroup(ctx, t, 4, 1, "rdXcore")
	testGroup(ctx, t, 5, 0, "destination")
	require.NoError(t, reservePath(ctx, "acme/rd_core/storage/firmware", "repository", 99))
	authorize := func(context.Context, *Namespace, *Namespace) error { return nil }
	assert.ErrorIs(t, MoveNamespace(ctx, 2, 3, 1, "cycle", testAudit().Actor, authorize), ErrConflict)
	require.NoError(t, MoveNamespace(ctx, 2, 5, 1, "research", testAudit().Actor, authorize))
	namespace, err := GetNamespace(ctx, 3)
	require.NoError(t, err)
	assert.Equal(t, "destination/research/storage", namespace.FullPath)
	assert.EqualValues(t, 2, namespace.Revision)
	unchanged, err := GetNamespace(ctx, 4)
	require.NoError(t, err)
	assert.Equal(t, "acme/rdXcore", unchanged.FullPath, "路径中的下划线不能作为 SQL 通配符")
	old, err := ResolvePath(ctx, "acme/rd_core/storage/firmware")
	require.NoError(t, err)
	assert.True(t, old.Alias)
	assert.EqualValues(t, 99, old.ResourceID)
	current, err := ResolvePath(ctx, "destination/research/storage/firmware")
	require.NoError(t, err)
	assert.False(t, current.Alias)
	assert.Equal(t, old.ResourceID, current.ResourceID)
	assert.ErrorIs(t, InsertNamespace(ctx, &Namespace{ID: 6, ParentID: 1, Kind: "group", Slug: "rd_core", Visibility: 2}), ErrConflict)
	assert.ErrorIs(t, MoveNamespace(ctx, 2, 1, 1, "stale", testAudit().Actor, authorize), ErrConflict)
	assert.Error(t, MoveNamespace(ctx, 2, 1, 2, "rdXcore", testAudit().Actor, authorize))
	namespace, err = GetNamespace(ctx, 3)
	require.NoError(t, err)
	assert.Equal(t, "destination/research/storage", namespace.FullPath, "目标冲突必须回滚整个子树")
	require.NoError(t, MoveNamespace(ctx, 2, 1, 2, "rd_core", testAudit().Actor, authorize))
	current, err = ResolvePath(ctx, "acme/rd_core/storage/firmware")
	require.NoError(t, err)
	assert.False(t, current.Alias, "移回原路径应恢复规范地址")
	old, err = ResolvePath(ctx, "destination/research/storage/firmware")
	require.NoError(t, err)
	assert.True(t, old.Alias, "移动后的旧地址仍受同一资源占用")
}

func testAudit() *AuditEvent {
	return &AuditEvent{
		Type: "member.updated", Actor: Actor{ID: 11, Name: "测试用户", Kind: "user", Transport: "api"},
		ScopeType: "repository", ScopeID: 99, AncestorIDs: []int64{2, 1}, ObjectType: "membership", ObjectID: 1, Result: "success",
	}
}

func TestAuditTransactionRollbackAndDelivery(t *testing.T) {
	ctx := testDatabase(t)
	stream := &AuditStream{ScopeType: "instance", Kind: "http", Endpoint: "https://audit.invalid", Enabled: true, Revision: 1}
	require.NoError(t, db.Insert(ctx, stream))
	failure := errors.New("故障注入：业务事务回滚")
	err := db.WithTx(ctx, func(ctx context.Context) error {
		require.NoError(t, db.Insert(ctx, &Membership{ScopeType: "group", ScopeID: 1, UserID: 11, Role: Developer}))
		require.NoError(t, AppendAudit(ctx, testAudit()))
		return failure
	})
	require.ErrorIs(t, err, failure)
	for _, bean := range []any{new(Membership), new(AuditEvent), new(AuditScope), new(AuditDelivery)} {
		count, err := db.GetEngine(ctx).Count(bean)
		require.NoError(t, err)
		assert.Zero(t, count, "业务、审计正文、范围索引、外送队列必须一起回滚")
	}
	event := testAudit()
	event.Details = []byte(`{"before":{"role":"Reporter"},"after":{"role":"Developer"}}`)
	require.NoError(t, AppendAudit(ctx, event))
	stored, has, err := db.GetByID[AuditEvent](ctx, event.ID)
	require.NoError(t, err)
	require.True(t, has)
	assert.JSONEq(t, string(event.Details), string(stored.Details), "审计前后值必须可完整读取")
	count, err := db.GetEngine(ctx).Count(new(AuditScope))
	require.NoError(t, err)
	assert.EqualValues(t, 5, count, "增加操作者个人范围，实例和群组历史范围仍保留")
	now := time.Now()
	first, err := LeaseDelivery(ctx, stream.ID, now)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, event.EventID, first.EventID)
	second, err := LeaseDelivery(ctx, stream.ID, now)
	require.NoError(t, err)
	assert.Nil(t, second)
	second, err = LeaseDelivery(ctx, stream.ID, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, first.EventID, second.EventID)
	assert.ErrorIs(t, FinishDelivery(ctx, first, true, now), ErrConflict, "迟到确认不能删除已重新领取的事件")
	require.NoError(t, FinishDelivery(ctx, second, true, now))
	count, err = db.GetEngine(ctx).Count(new(AuditEvent))
	require.NoError(t, err)
	assert.EqualValues(t, 1, count, "外送成功不删除治理事件")
}

func TestAccessAuditRequiresDestinationAndRejectsSecrets(t *testing.T) {
	ctx := testDatabase(t)
	event := testAudit()
	event.Type = "access.code"
	assert.ErrorIs(t, AppendAudit(ctx, event), ErrNoAuditDestination)
	event = testAudit()
	event.Details = []byte(`{"before":{"token":"禁止写入"}}`)
	assert.ErrorIs(t, AppendAudit(ctx, event), ErrInvalid)
}

func TestMergeReservationSurvivesUnknownResult(t *testing.T) {
	ctx := testDatabase(t)
	op := &MergeAuthorization{PullID: 7, RepoID: 99, Head: "提交", OldTarget: "旧引用", NewTarget: "新引用", Branch: "main", Actor: testAudit().Actor}
	resource := Resource("group", 2)
	require.NoError(t, AuthorizeMerge(ctx, op, []string{resource}, func(context.Context) error { return nil }))
	assert.ErrorIs(t, WithWrite(ctx, []string{resource}, func(context.Context) error { return nil }), ErrConflict)
	state, err := ReconcileMerge(ctx, op.ID, "意外引用")
	require.NoError(t, err)
	assert.Equal(t, "unknown", state)
	assert.ErrorIs(t, WithWrite(ctx, []string{resource}, func(context.Context) error { return nil }), ErrConflict)
	state, err = ReconcileMerge(ctx, op.ID, "新引用")
	require.NoError(t, err)
	assert.Equal(t, "succeeded", state)
	require.NoError(t, WithWrite(ctx, []string{resource}, func(context.Context) error { return nil }))
	state, err = ReconcileMerge(ctx, op.ID, "后续提交")
	require.NoError(t, err)
	assert.Equal(t, "succeeded", state, "最终状态幂等，不因后续提交被重写")
}

func TestMergeAndRevocationOrdering(t *testing.T) {
	ctx := testDatabase(t)
	resource := Resource("group", 2)
	entered, finish := make(chan struct{}), make(chan struct{})
	mergeResult, revokeResult := make(chan error, 1), make(chan error, 1)
	op := &MergeAuthorization{PullID: 7, RepoID: 99, Head: "提交", OldTarget: "旧引用", NewTarget: "新引用", Branch: "main", Actor: testAudit().Actor}
	go func() {
		mergeResult <- AuthorizeMerge(ctx, op, []string{resource}, func(context.Context) error {
			close(entered)
			<-finish
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-mergeResult:
		t.Fatalf("取得事务锁失败：%v", err)
	}
	go func() {
		revokeResult <- WithWrite(ctx, []string{resource}, func(context.Context) error {
			return errors.New("不应越过尚未完成的合并授权")
		})
	}()
	close(finish)
	require.NoError(t, <-mergeResult)
	assert.ErrorIs(t, <-revokeResult, ErrConflict)
	_, err := ReconcileMerge(ctx, op.ID, op.NewTarget)
	require.NoError(t, err)
	op2 := &MergeAuthorization{PullID: 8, RepoID: 100, Head: "提交", OldTarget: "旧引用", NewTarget: "新引用", Branch: "main", Actor: testAudit().Actor}
	require.NoError(t, WithWrite(ctx, []string{resource}, func(ctx context.Context) error {
		return db.Insert(ctx, &Membership{ScopeType: "group", ScopeID: 2, UserID: 11, Role: Guest})
	}))
	assert.ErrorIs(t, AuthorizeMerge(ctx, op2, []string{resource}, func(ctx context.Context) error {
		var member Membership
		_, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "group", 2, 11).Get(&member)
		if err != nil {
			return err
		}
		if member.Role != Developer {
			return ErrForbidden
		}
		return nil
	}), ErrForbidden, "先完成撤权后，最终授权必须读取新权限并拒绝")
}

func TestConfiguredAccessAuditPausedAndFiltered(t *testing.T) {
	ctx := testDatabase(t)
	event := testAudit()
	event.Type = "access.code"
	recorded, err := AppendConfiguredAccessAudit(ctx, event)
	require.NoError(t, err)
	require.False(t, recorded, "未配置目标时明确返回未启用")
	stream := &AuditStream{ScopeType: "group", ScopeID: 2, Kind: "http", Endpoint: "https://audit.invalid", Enabled: false, EventTypes: []string{"access.code"}, Revision: 1}
	require.NoError(t, db.Insert(ctx, stream))
	_, err = db.GetEngine(ctx).ID(stream.ID).Cols("enabled").Update(&AuditStream{Enabled: false})
	require.NoError(t, err)
	recorded, err = AppendConfiguredAccessAudit(ctx, event)
	require.NoError(t, err)
	require.True(t, recorded, "顶级组目标暂停时仍接收后代访问事件")
	count, err := db.GetEngine(ctx).Where("stream_id = ?", stream.ID).Count(new(AuditDelivery))
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	event.AncestorIDs = []int64{3}
	recorded, err = AppendConfiguredAccessAudit(ctx, event)
	require.NoError(t, err)
	require.False(t, recorded, "不能外送不属于该层级的事件")
	event.AncestorIDs, event.Type = []int64{2}, "access.archive"
	recorded, err = AppendConfiguredAccessAudit(ctx, event)
	require.NoError(t, err)
	require.False(t, recorded, "事件过滤仍生效")
	count, err = db.GetEngine(ctx).Count(new(AuditEvent))
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestLFSContentLockLivesUntilOuterCommit(t *testing.T) {
	testContentLockLivesUntilOuterCommit(t, WithLFSContentLocks)
}

func TestPackageContentLockLivesUntilOuterCommit(t *testing.T) {
	testContentLockLivesUntilOuterCommit(t, WithPackageContentLocks)
}

func testContentLockLivesUntilOuterCommit(t *testing.T, lock func(context.Context, []string, func(context.Context) error) error) {
	t.Helper()
	ctx := testDatabase(t)
	oid := strings.Repeat("a", 64)
	held, release, started := make(chan struct{}), make(chan struct{}), make(chan struct{})
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		first <- db.WithTx(ctx, func(ctx context.Context) error {
			if err := lock(ctx, []string{oid}, func(context.Context) error { return nil }); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-first:
		t.Fatalf("取得内容锁失败：%v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("取得内容锁超时")
	}
	go func() {
		close(started)
		second <- lock(ctx, []string{oid}, func(context.Context) error { return nil })
	}()
	<-started
	select {
	case err := <-second:
		close(release)
		require.NoError(t, <-first)
		t.Fatalf("最外层提交前第二个内容操作不应完成：%v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-first)
	require.NoError(t, <-second)
}
