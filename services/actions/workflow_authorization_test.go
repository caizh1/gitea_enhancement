// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"testing"

	governance_model "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/util"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestWorkflowWriterPreservesFreshTokenLimitAndRejectsRevocation(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
	writer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	ownerActor := governance_service.RequestActor(owner, "", "api")
	writerActor := governance_service.RequestActor(writer, "", "api")
	namespace, err := governance_model.GetNamespace(ctx, repo.OwnerID)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetRepositoryMember(ctx, ownerActor, repo.ID,
		governance_service.GroupMemberOption{UserID: writer.ID, Role: governance_model.Developer, Revision: namespace.Revision}, false))
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, writer)
	require.NoError(t, err)
	permission = permission.ForMutation()
	require.True(t, permission.CanWrite(unit.TypeActions))
	require.False(t, permission.IsAdmin())
	require.NoError(t, governance_service.WithConfigurationWrite(ctx, ownerActor, 0, repo.ID, func(tx context.Context) error {
		freshRepo, err := repo_model.GetRepositoryByID(tx, repo.ID)
		if err != nil {
			return err
		}
		freshUnit, err := freshRepo.GetUnit(tx, unit.TypeActions)
		if err != nil {
			return err
		}
		freshUnit.ActionsConfig().TokenPermissionMode = repo_model.ActionsTokenPermissionModePermissive
		return repo_model.UpdateRepoUnitConfig(tx, freshUnit)
	}))
	repo, err = repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	staleUnit, err := repo.GetUnit(ctx, unit.TypeActions)
	require.NoError(t, err)
	require.Equal(t, repo_model.ActionsTokenPermissionModePermissive, staleUnit.ActionsConfig().TokenPermissionMode)

	maximum := repo_model.MakeActionsTokenPermissions(0)
	require.NoError(t, governance_service.WithConfigurationWrite(ctx, ownerActor, 0, repo.ID, func(tx context.Context) error {
		freshRepo, err := repo_model.GetRepositoryByID(tx, repo.ID)
		if err != nil {
			return err
		}
		freshUnit, err := freshRepo.GetUnit(tx, unit.TypeActions)
		if err != nil {
			return err
		}
		cfg := freshUnit.ActionsConfig()
		cfg.OverrideOwnerConfig = true
		cfg.TokenPermissionMode = repo_model.ActionsTokenPermissionModeRestricted
		cfg.MaxTokenPermissions = &maximum
		return repo_model.UpdateRepoUnitConfig(tx, freshUnit)
	}))
	require.NoError(t, SetWorkflowEnabled(ctx, writerActor, repo.ID, "workflow.yml", 0, false, false))
	freshRepo, err := repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	freshUnit, err := freshRepo.GetUnit(ctx, unit.TypeActions)
	require.NoError(t, err)
	require.Equal(t, repo_model.ActionsTokenPermissionModeRestricted, freshUnit.ActionsConfig().TokenPermissionMode)
	require.True(t, freshUnit.ActionsConfig().OverrideOwnerConfig)
	require.Equal(t, &maximum, freshUnit.ActionsConfig().MaxTokenPermissions)
	require.True(t, freshUnit.ActionsConfig().IsWorkflowDisabled("workflow.yml"))

	namespace, err = governance_model.GetNamespace(ctx, repo.OwnerID)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetRepositoryMember(ctx, ownerActor, repo.ID,
		governance_service.GroupMemberOption{UserID: writer.ID, Revision: namespace.Revision}, true))
	require.ErrorIs(t, SetWorkflowEnabled(ctx, writerActor, repo.ID, "workflow.yml", 0, true, false), util.ErrPermissionDenied)
	require.NoError(t, SetWorkflowEnabled(ctx, ownerActor, repo.ID, "workflow.yml", 0, true, false))
	freshRepo, err = repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	freshUnit, err = freshRepo.GetUnit(ctx, unit.TypeActions)
	require.NoError(t, err)
	require.False(t, freshUnit.ActionsConfig().IsWorkflowDisabled("workflow.yml"))
	require.Equal(t, &maximum, freshUnit.ActionsConfig().MaxTokenPermissions)
}
