// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/perm"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestTaskTokenAncestorDefaultAndAbsoluteMaximum(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := db.GetEngine(ctx).ID(6).Cols("parent_id", "full_path", "lower_path").Update(&governance_model.Namespace{
		ParentID: 3, FullPath: "org3/org6", LowerPath: "org3/org6",
	})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(1).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 6})
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 1, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	job := &ActionRunJob{RepoID: repo.ID, Repo: repo}
	task := &ActionTask{Job: job}

	rootMax := repo_model.MakeActionsTokenPermissions(perm.AccessModeWrite)
	rootMax.UnitAccessModes[unit.TypeCode] = perm.AccessModeRead
	require.NoError(t, SetOwnerActionsConfig(ctx, 3, OwnerActionsConfig{
		TokenPermissionMode: repo_model.ActionsTokenPermissionModeRestricted,
		MaxTokenPermissions: &rootMax,
	}))

	// An unset child inherits the nearest configured default.
	effective, err := ComputeTaskTokenPermissions(ctx, task, repo)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeRead, effective.UnitAccessModes[unit.TypeCode])
	require.Equal(t, perm.AccessModeNone, effective.UnitAccessModes[unit.TypeIssues])

	// The repository may override the default, but not the ancestor's ceiling.
	actionsUnit := repo.MustGetUnit(ctx, unit.TypeActions)
	actionsUnit.ActionsConfig().OverrideOwnerConfig = true
	actionsUnit.ActionsConfig().TokenPermissionMode = repo_model.ActionsTokenPermissionModePermissive
	require.NoError(t, repo_model.UpdateRepoUnitConfig(ctx, actionsUnit))
	effective, err = ComputeTaskTokenPermissions(ctx, task, repo)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeRead, effective.UnitAccessModes[unit.TypeCode])
	require.Equal(t, perm.AccessModeWrite, effective.UnitAccessModes[unit.TypeIssues])

	// An explicit child default wins over the ancestor default, while both ceilings remain.
	require.NoError(t, SetOwnerActionsConfig(ctx, 6, OwnerActionsConfig{
		TokenPermissionMode: repo_model.ActionsTokenPermissionModePermissive,
	}))
	actionsUnit.ActionsConfig().OverrideOwnerConfig = false
	require.NoError(t, repo_model.UpdateRepoUnitConfig(ctx, actionsUnit))
	effective, err = ComputeTaskTokenPermissions(ctx, task, repo)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeWrite, effective.UnitAccessModes[unit.TypeIssues])
	require.Equal(t, perm.AccessModeRead, effective.UnitAccessModes[unit.TypeCode])

	// YAML permissions cannot raise an absolute ceiling.
	declared := repo_model.MakeActionsTokenPermissions(perm.AccessModeWrite)
	job.TokenPermissions = &declared
	effective, err = ComputeTaskTokenPermissions(ctx, task, repo)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeRead, effective.UnitAccessModes[unit.TypeCode])
	emptyDeclared := repo_model.ActionsTokenPermissions{}
	job.TokenPermissions = &emptyDeclared
	effective, err = ComputeTaskTokenPermissions(ctx, task, repo)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeNone, effective.UnitAccessModes[unit.TypeCode])
	require.Equal(t, perm.AccessModeNone, effective.UnitAccessModes[unit.TypeIssues])
	job.TokenPermissions = &declared
	task.IsForkPullRequest = true
	effective, err = ComputeTaskTokenPermissions(ctx, task, repo)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeNone, effective.UnitAccessModes[unit.TypeIssues])
	task.IsForkPullRequest = false
	effective, err = ComputeTaskTokenPermissions(ctx, task, &repo_model.Repository{ID: 2})
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeNone, effective.UnitAccessModes[unit.TypeIssues])

	// An explicitly empty ceiling means deny all; nil means no added ceiling.
	empty := repo_model.ActionsTokenPermissions{}
	require.NoError(t, SetOwnerActionsConfig(ctx, 6, OwnerActionsConfig{MaxTokenPermissions: &empty}))
	effective, err = ComputeTaskTokenPermissions(ctx, task, repo)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeNone, effective.UnitAccessModes[unit.TypeCode])
	require.Equal(t, perm.AccessModeNone, effective.UnitAccessModes[unit.TypeIssues])

	// Target repository permissions must apply the same ancestor ceiling.
	require.NoError(t, SetOwnerActionsConfig(ctx, 6, OwnerActionsConfig{}))
	effective, err = ClampTaskTokenPermissionsForRepo(ctx, declared, repo)
	require.NoError(t, err)
	require.Equal(t, perm.AccessModeRead, effective.UnitAccessModes[unit.TypeCode])
}
