// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package context

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	packages_model "gitea.dev/models/packages"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	"gitea.dev/models/user"
	"gitea.dev/modules/structs"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { unittest.MainTest(m) }

func TestDeterminePackageAccessModeForLimitedOwner(t *testing.T) {
	owner := &user.User{ID: 1, Visibility: structs.VisibleTypeLimited}

	accessMode, err := determineAccessMode(&Base{}, owner, &user.User{ID: 2, IsActive: true})
	assert.NoError(t, err)
	assert.Equal(t, perm.AccessModeRead, accessMode)

	accessMode, err = determineAccessMode(&Base{}, owner, &user.User{ID: 3, IsActive: true, IsRestricted: true})
	assert.NoError(t, err)
	assert.Equal(t, perm.AccessModeNone, accessMode)
}

func TestDeterminePackageAccessModeUsesEffectiveGroupAbilities(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	child, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "package-child", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	sibling, err := governance_service.CreateGroup(ctx, actor, governance_service.GroupOption{Path: "package-sibling", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, actor, child.ID, governance_service.GroupMemberOption{UserID: 5, Role: governance_model.Owner, Revision: 1}, false))
	share := &governance_model.Share{ScopeType: "group", ScopeID: child.ID, GroupID: 19, MaxRole: governance_model.Reporter}
	require.NoError(t, db.Insert(ctx, share))

	base := NewBaseContextForTest(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	childOwner, err := user.GetUserByID(ctx, child.ID)
	require.NoError(t, err)
	siblingOwner, err := user.GetUserByID(ctx, sibling.ID)
	require.NoError(t, err)
	rootOwner, err := user.GetUserByID(ctx, 3)
	require.NoError(t, err)
	parentManager, err := user.GetUserByID(ctx, 2)
	require.NoError(t, err)
	childManager, err := user.GetUserByID(ctx, 5)
	require.NoError(t, err)
	sharedReader, err := user.GetUserByID(ctx, 20)
	require.NoError(t, err)
	nativeCodeWriter, err := user.GetUserByID(ctx, 4)
	require.NoError(t, err)

	mode, err := determineAccessMode(base, childOwner, parentManager)
	require.NoError(t, err)
	require.GreaterOrEqual(t, mode, perm.AccessModeWrite, "父组 Owner 可发布子组包")
	mode, err = determineAccessMode(base, childOwner, childManager)
	require.NoError(t, err)
	require.GreaterOrEqual(t, mode, perm.AccessModeWrite, "子组 Owner 可发布本组包")
	mode, err = determineAccessMode(base, rootOwner, childManager)
	require.NoError(t, err)
	require.Less(t, mode, perm.AccessModeWrite, "子组 Owner 不可管理父组包")
	mode, err = determineAccessMode(base, rootOwner, nativeCodeWriter)
	require.NoError(t, err)
	require.Less(t, mode, perm.AccessModeWrite, "原生团队的其他单元写权不授予包发布权")
	mode, err = determineAccessMode(base, siblingOwner, childManager)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeNone, mode, "子组 Owner 不继承兄弟组权限")
	mode, err = determineAccessMode(base, childOwner, sharedReader)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeRead, mode, "Reporter 上限共享只可下载，不可发布或删除")
	pkg, err := packages_model.TryInsertPackage(ctx, &packages_model.Package{OwnerID: child.ID, Type: packages_model.TypeContainer, Name: "shared-blob", LowerName: "shared-blob"})
	require.NoError(t, err)
	version, err := packages_model.GetOrInsertVersion(ctx, &packages_model.PackageVersion{PackageID: pkg.ID, Version: "1", LowerVersion: "1"})
	require.NoError(t, err)
	blob, _, err := packages_model.GetOrInsertBlob(ctx, &packages_model.PackageBlob{HashSHA256: strings.Repeat("c", 64)})
	require.NoError(t, err)
	_, err = packages_model.TryInsertFile(ctx, &packages_model.PackageFile{VersionID: version.ID, BlobID: blob.ID, Name: "blob", LowerName: "blob"})
	require.NoError(t, err)
	accessible, err := packages_model.IsBlobAccessibleForUser(ctx, blob.ID, parentManager)
	require.NoError(t, err)
	require.True(t, accessible, "OCI 跨镜像挂载应识别父组包读取权")
	accessible, err = packages_model.IsBlobAccessibleForUser(ctx, blob.ID, sharedReader)
	require.NoError(t, err)
	require.True(t, accessible, "OCI 跨镜像挂载应识别共享只读包权")
	require.NoError(t, db.DeleteBeans(ctx, share))
	mode, err = determineAccessMode(base, childOwner, sharedReader)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeNone, mode, "共享撤销后立即失去私有包访问")
	accessible, err = packages_model.IsBlobAccessibleForUser(ctx, blob.ID, sharedReader)
	require.NoError(t, err)
	require.False(t, accessible, "共享撤销后不可挂载私有包 blob")
	require.NoError(t, governance_service.SetGroupMember(ctx, actor, child.ID, governance_service.GroupMemberOption{UserID: 5, Revision: 2}, true))
	mode, err = determineAccessMode(base, childOwner, childManager)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeNone, mode, "子组成员撤权后立即失去包访问")
	mode, err = determineAccessMode(base, childOwner, user.NewActionsUserWithTaskID(1))
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeNone, mode, "任务身份不得借群组继承获得包权限")
	mode, err = determineAccessMode(base, childOwner, nil)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeNone, mode, "匿名身份不得读取私有子组包")
}

func TestTaskPackageAccessUsesCurrentTaskAndOwnerScope(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 3, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	run := &actions_model.ActionRun{RepoID: 3, OwnerID: 3, TriggerUserID: 2, Status: actions_model.StatusRunning}
	require.NoError(t, db.Insert(ctx, run))
	declared := repo_model.MakeActionsTokenPermissions(perm.AccessModeWrite)
	job := &actions_model.ActionRunJob{RepoID: 3, OwnerID: 3, RunID: run.ID, JobID: "package-publish", Status: actions_model.StatusRunning, TokenPermissions: &declared}
	require.NoError(t, db.Insert(ctx, job))
	runner := &actions_model.ActionRunner{UUID: "package-token-runner", Name: "package-token-runner"}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, runner))
	task := &actions_model.ActionTask{RepoID: 3, OwnerID: 3, JobID: job.ID, RunnerID: runner.ID, Status: actions_model.StatusRunning}
	task.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, task))
	job.TaskID = task.ID
	_, err := db.GetEngine(ctx).ID(job.ID).Cols("task_id").Update(job)
	require.NoError(t, err)

	base := NewBaseContextForTest(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
	owner, err := user.GetUserByID(ctx, 3)
	require.NoError(t, err)
	otherOwner, err := user.GetUserByID(ctx, 2)
	require.NoError(t, err)
	doer := user.NewActionsUserWithTaskID(task.ID)
	mode, err := determineAccessMode(base, owner, doer)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeWrite, mode, "同所有者运行任务的包写权受 YAML 权限控制")
	require.NoError(t, packages_model.WithAuthenticatedOwnerWrite(ctx, owner.ID, doer, func(context.Context) error { return nil }))
	mode, err = determineAccessMode(base, otherOwner, doer)
	require.NoError(t, err)
	require.Less(t, mode, perm.AccessModeWrite, "任务不能写入其他包所有者")
	require.ErrorIs(t, packages_model.WithAuthenticatedOwnerWrite(ctx, otherOwner.ID, doer, func(context.Context) error { return nil }), governance_model.ErrForbidden)

	readOnly := repo_model.MakeActionsTokenPermissions(perm.AccessModeRead)
	job.TokenPermissions = &readOnly
	_, err = db.GetEngine(ctx).ID(job.ID).Cols("token_permissions").Update(job)
	require.NoError(t, err)
	mode, err = determineAccessMode(base, owner, doer)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeRead, mode, "当前 YAML 权限降级立即禁止发布")
	require.ErrorIs(t, packages_model.WithAuthenticatedOwnerWrite(ctx, owner.ID, doer, func(context.Context) error { return nil }), governance_model.ErrForbidden)

	job.TokenPermissions = &declared
	_, err = db.GetEngine(ctx).ID(job.ID).Cols("token_permissions").Update(job)
	require.NoError(t, err)
	task.Status = actions_model.StatusCancelling
	_, err = db.GetEngine(ctx).ID(task.ID).Cols("status").Update(task)
	require.NoError(t, err)
	mode, err = determineAccessMode(base, owner, doer)
	require.NoError(t, err)
	require.Less(t, mode, perm.AccessModeWrite, "取消中的任务不得发布")
	require.ErrorIs(t, packages_model.WithAuthenticatedOwnerWrite(ctx, owner.ID, doer, func(context.Context) error { return nil }), governance_model.ErrForbidden)

	task.Status = actions_model.StatusRunning
	_, err = db.GetEngine(ctx).ID(task.ID).Cols("status").Update(task)
	require.NoError(t, err)
	runner.IsDisabled = true
	_, err = db.GetEngine(ctx).ID(runner.ID).Cols("is_disabled").Update(runner)
	require.NoError(t, err)
	mode, err = determineAccessMode(base, owner, doer)
	require.NoError(t, err)
	require.Less(t, mode, perm.AccessModeWrite, "Runner 停用后任务令牌失效")
	require.ErrorIs(t, packages_model.WithAuthenticatedOwnerWrite(ctx, owner.ID, doer, func(context.Context) error { return nil }), governance_model.ErrForbidden)

	runner.IsDisabled = false
	_, err = db.GetEngine(ctx).ID(runner.ID).Cols("is_disabled").Update(runner)
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &repo_model.Collaboration{RepoID: 3, UserID: 4, Mode: perm.AccessModeWrite}))
	_, err = db.GetEngine(ctx).Exec("UPDATE action_run SET trigger_user_id = ? WHERE id = ?", 4, run.ID)
	require.NoError(t, err)
	currentRun, err := actions_model.GetRunByRepoAndID(ctx, 3, run.ID)
	require.NoError(t, err)
	require.EqualValues(t, 4, currentRun.TriggerUserID)
	abilities, err := organization.GovernanceGroupAbilities(ctx, 3, 4)
	require.NoError(t, err)
	require.False(t, abilities[governance_model.WritePackages])
	currentTask, err := actions_model.GetTaskByID(ctx, task.ID)
	require.NoError(t, err)
	canReadSource, err := access_model.TaskTriggererCanReadSource(ctx, currentTask)
	require.NoError(t, err)
	require.True(t, canReadSource, "仓库协作者仍可访问任务源仓")
	mode, err = determineAccessMode(base, owner, doer)
	require.NoError(t, err)
	require.Less(t, mode, perm.AccessModeWrite, "仅有仓库写权不能发布整个群组的包")
	require.ErrorIs(t, packages_model.WithAuthenticatedOwnerWrite(ctx, owner.ID, doer, func(context.Context) error { return nil }), governance_model.ErrForbidden)
}
