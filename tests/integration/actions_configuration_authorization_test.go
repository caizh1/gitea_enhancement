// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"

	actions_model "gitea.dev/models/actions"
	auth_model "gitea.dev/models/auth"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/util"
	actions_service "gitea.dev/services/actions"
	"gitea.dev/services/contexttest"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm/schemas"
)

func TestActionsConfigurationWriteRejectsRevokedOwnerPostgres(t *testing.T) {
	if os.Getenv("GITEA_TEST_DATABASE") != "pgsql" {
		t.Skip("需要真实 PostgreSQL 集成引擎")
	}
	defer tests.PrepareTestEnv(t)()
	require.True(t, setting.Database.Type.IsPostgreSQL())
	require.Equal(t, schemas.POSTGRES, unittest.GetXORMEngine().Dialect().URI().DBType)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	group, err := governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, owner, 3,
		governance_service.GroupMemberOption{UserID: 4, Role: governance_model.Owner, Revision: group.Revision}, false))
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	actor := governance_service.RequestActor(doer, "", "api")
	request := governance_model.WithAuditActor(ctx, actor)
	_, err = governance_service.CheckGroupAccess(request, doer.ID, 3, governance_model.ManageGroup)
	require.NoError(t, err)
	baseline := actions_model.OwnerActionsConfig{TokenPermissionMode: repo_model.ActionsTokenPermissionModeRestricted}
	require.NoError(t, governance_service.WithConfigurationWrite(request, actor, 3, 0, func(tx context.Context) error {
		return actions_model.SetOwnerActionsConfig(tx, 3, baseline)
	}))
	variable, err := actions_service.CreateVariable(request, 3, 0, "ACCESS_CHECK", "before", "initial")
	require.NoError(t, err)
	group, err = governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, owner, 3,
		governance_service.GroupMemberOption{UserID: 4, Revision: group.Revision}, true))

	maximum := repo_model.MakeActionsTokenPermissions(0)
	changed := actions_model.OwnerActionsConfig{
		TokenPermissionMode: repo_model.ActionsTokenPermissionModePermissive,
		MaxTokenPermissions: &maximum,
		AllowedCrossRepoIDs: []int64{1},
	}
	err = governance_service.WithConfigurationWrite(request, actor, 3, 0, func(tx context.Context) error {
		return actions_model.SetOwnerActionsConfig(tx, 3, changed)
	})
	require.ErrorIs(t, err, util.ErrPermissionDenied)
	_, err = actions_service.CreateVariable(request, 3, 0, "ACCESS_NEW", "after", "revoked")
	require.ErrorIs(t, err, util.ErrPermissionDenied)
	variable.Data = "after"
	_, err = actions_service.UpdateVariableNameData(request, variable)
	require.ErrorIs(t, err, util.ErrPermissionDenied)
	require.ErrorIs(t, actions_service.DeleteVariableByID(request, variable.ID), util.ErrPermissionDenied)

	actual, err := actions_model.GetOwnerActionsConfig(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, baseline.TokenPermissionMode, actual.TokenPermissionMode)
	require.Nil(t, actual.MaxTokenPermissions)
	require.Empty(t, actual.AllowedCrossRepoIDs)
	values, err := actions_model.FindVariables(ctx, actions_model.FindVariablesOpts{OwnerID: 3, Name: "ACCESS_CHECK"})
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.Equal(t, "before", values[0].Data)
	newValues, err := actions_model.FindVariables(ctx, actions_model.FindVariablesOpts{OwnerID: 3, Name: "ACCESS_NEW"})
	require.NoError(t, err)
	require.Empty(t, newValues)
}

func TestActionsWorkflowWriterAPIUsesCurrentConfiguration(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		ctx := t.Context()
		require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
		owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		writer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
		ownerSession := loginUser(t, owner.Name)
		ownerToken := getTokenForLoggedInUser(t, ownerSession, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
		target := createActionsTestRepo(t, ownerToken, "config-workflow-authorization", false)
		require.Positive(t, target.ID)
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: target.ID})
		path := ".gitea/workflows/config-check.yml"
		content := "name: Config Check\non: workflow_dispatch\njobs:\n  check:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo check\n"
		createWorkflowFile(t, ownerToken, owner.Name, repo.Name, path,
			getWorkflowCreateFileOptions(owner, repo.DefaultBranch, "create "+path, content))
		ownerActor := governance_service.RequestActor(owner, "", "api")
		namespace, err := governance_model.GetNamespace(ctx, owner.ID)
		require.NoError(t, err)
		require.NoError(t, governance_service.SetRepositoryMember(ctx, ownerActor, repo.ID,
			governance_service.GroupMemberOption{UserID: writer.ID, Role: governance_model.Developer, Revision: namespace.Revision}, false))
		writerSession := loginUser(t, writer.Name)
		writerToken := getTokenForLoggedInUser(t, writerSession, auth_model.AccessTokenScopeWriteRepository)
		staleRequest, _ := contexttest.MockAPIContext(t, "PUT /api/v1/repos/user2/config-workflow-authorization/actions/workflows/config-check.yml/disable")
		contexttest.LoadUser(t, staleRequest, writer.ID)
		contexttest.LoadRepo(t, staleRequest, repo.ID)
		contexttest.LoadGitRepo(t, staleRequest)
		t.Cleanup(func() { _ = staleRequest.Repo.GitRepo.Close() })
		writerActor := governance_service.RequestActor(writer, "", "api")
		staleRequest.SetContextValue(governance_model.AuditActorContextKey, writerActor)
		stalePermission := staleRequest.Repo.Permission.ForMutation()
		require.True(t, stalePermission.CanWrite(unit.TypeActions))
		require.False(t, stalePermission.IsAdmin())
		staleUnit, err := staleRequest.Repo.Repository.GetUnit(staleRequest, unit.TypeActions)
		require.NoError(t, err)
		require.Nil(t, staleUnit.ActionsConfig().MaxTokenPermissions)
		maximum := repo_model.MakeActionsTokenPermissions(0)
		require.NoError(t, governance_service.WithConfigurationWrite(ctx, ownerActor, 0, repo.ID, func(tx context.Context) error {
			fresh, err := repo_model.GetRepositoryByID(tx, repo.ID)
			if err != nil {
				return err
			}
			actionsUnit, err := fresh.GetUnit(tx, unit.TypeActions)
			if err != nil {
				return err
			}
			cfg := actionsUnit.ActionsConfig()
			cfg.OverrideOwnerConfig = true
			cfg.TokenPermissionMode = repo_model.ActionsTokenPermissionModeRestricted
			cfg.MaxTokenPermissions = &maximum
			return repo_model.UpdateRepoUnitConfig(tx, actionsUnit)
		}))
		require.NoError(t, actions_service.EnableOrDisableWorkflow(staleRequest, "config-check.yml", false))
		endpoint := fmt.Sprintf("/api/v1/repos/%s/%s/actions/workflows/config-check.yml", owner.Name, repo.Name)
		MakeRequest(t, NewRequest(t, "PUT", endpoint+"/enable").AddTokenAuth(writerToken), http.StatusNoContent)
		MakeRequest(t, NewRequest(t, "PUT", endpoint+"/disable").AddTokenAuth(writerToken), http.StatusNoContent)
		fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
		require.NoError(t, err)
		actionsUnit, err := fresh.GetUnit(ctx, unit.TypeActions)
		require.NoError(t, err)
		require.True(t, actionsUnit.ActionsConfig().IsWorkflowDisabled("config-check.yml"))
		require.True(t, actionsUnit.ActionsConfig().OverrideOwnerConfig)
		require.Equal(t, repo_model.ActionsTokenPermissionModeRestricted, actionsUnit.ActionsConfig().TokenPermissionMode)
		require.Equal(t, &maximum, actionsUnit.ActionsConfig().MaxTokenPermissions)
		namespace, err = governance_model.GetNamespace(ctx, owner.ID)
		require.NoError(t, err)
		require.NoError(t, governance_service.SetRepositoryMember(ctx, ownerActor, repo.ID,
			governance_service.GroupMemberOption{UserID: writer.ID, Revision: namespace.Revision}, true))
		MakeRequest(t, NewRequest(t, "PUT", endpoint+"/enable").AddTokenAuth(writerToken), http.StatusForbidden)
		MakeRequest(t, NewRequest(t, "PUT", endpoint+"/enable").AddTokenAuth(ownerToken), http.StatusNoContent)
		fresh, err = repo_model.GetRepositoryByID(ctx, repo.ID)
		require.NoError(t, err)
		actionsUnit, err = fresh.GetUnit(ctx, unit.TypeActions)
		require.NoError(t, err)
		require.False(t, actionsUnit.ActionsConfig().IsWorkflowDisabled("config-check.yml"))
		require.Equal(t, &maximum, actionsUnit.ActionsConfig().MaxTokenPermissions)
	})
}
