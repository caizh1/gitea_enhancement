// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	actions_module "gitea.dev/modules/actions"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/util"
	"gitea.dev/services/contexttest"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopedWorkflowSetRequiredInstallsSourceReferenceHook(t *testing.T) {
	unittest.PrepareTestEnv(t)
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	sourceRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.NoError(t, actions_model.AddScopedWorkflowSource(t.Context(), 0, sourceRepo.ID))
	ctx, resp := contexttest.MockContext(t, "/-/admin/actions/scoped-workflows/required")
	contexttest.LoadUser(t, ctx, 1)
	ctx.Data["PageIsAdmin"] = true
	ctx.Req.Form = url.Values{"repo_id": {"1"}, "workflow_ids": {"ci.yaml"}, "required_workflow_ids": {"ci.yaml"}, "required_patterns[ci.yaml]": {"* / build (*)"}}
	ScopedWorkflowSetRequired(ctx)
	require.Equal(t, http.StatusOK, resp.Code)
	source, err := actions_model.GetScopedWorkflowSource(t.Context(), 0, sourceRepo.ID)
	require.NoError(t, err)
	require.True(t, source.IsWorkflowRequired("ci.yaml"))
	installed, err := gitrepo.ReferenceTransactionHookInstalled(sourceRepo)
	require.NoError(t, err)
	require.True(t, installed)
}

func TestScopedWorkflowSettingsHandlersRejectStaleAdmin(t *testing.T) {
	for _, operation := range []string{"add", "required", "remove"} {
		t.Run(operation, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
			if operation != "add" {
				require.NoError(t, actions_model.AddScopedWorkflowSource(t.Context(), 0, 1))
			}
			ctx, resp := contexttest.MockContext(t, "/-/admin/actions/scoped-workflows/"+operation)
			contexttest.LoadUser(t, ctx, 1)
			ctx.Data["PageIsAdmin"] = true
			ctx.Req.Form = url.Values{"repo_id": {"1"}, "repo_name": {"user2/repo1"}}
			_, err := db.GetEngine(t.Context()).ID(1).Cols("is_admin").Update(&user_model.User{IsAdmin: false})
			require.NoError(t, err)
			switch operation {
			case "add":
				ScopedWorkflowAdd(ctx)
			case "required":
				ScopedWorkflowSetRequired(ctx)
			case "remove":
				ScopedWorkflowRemove(ctx)
			}
			require.Equal(t, http.StatusForbidden, resp.Code)
			_, err = actions_model.GetScopedWorkflowSource(t.Context(), 0, 1)
			if operation == "add" {
				require.ErrorIs(t, err, util.ErrNotExist)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestScopedWorkflowSettingsWriteRejectsStalePermission(t *testing.T) {
	for _, operation := range []string{"add", "required", "remove"} {
		t.Run(operation+"_revoked_owner", func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
			doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
			actor := governance_service.RequestActor(doer, "", "web")
			ownerTeam, err := organization.GetOwnerTeam(t.Context(), 3)
			require.NoError(t, err)
			_, err = db.GetEngine(t.Context()).Where("org_id = ? AND team_id = ? AND uid = ?", 3, ownerTeam.ID, doer.ID).Delete(new(organization.TeamUser))
			require.NoError(t, err)
			called := false
			err = withScopedWorkflowSettingsWrite(t.Context(), actor, &scopedWorkflowsCtx{OwnerID: 3, IsOrg: true}, 3, func(_ context.Context) error {
				called = true
				return nil
			})
			require.ErrorIs(t, err, util.ErrPermissionDenied)
			require.False(t, called)
		})
		t.Run(operation+"_current_owner", func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
			doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
			actor := governance_service.RequestActor(doer, "", "web")
			called := false
			err := withScopedWorkflowSettingsWrite(t.Context(), actor, &scopedWorkflowsCtx{OwnerID: 3, IsOrg: true}, 3, func(_ context.Context) error {
				called = true
				return nil
			})
			require.NoError(t, err)
			require.True(t, called)
		})
	}
}

func TestScopedWorkflowSettingsWriteHonorsReservedScope(t *testing.T) {
	for _, test := range []struct {
		name       string
		userID     int64
		scope      scopedWorkflowsCtx
		resource   string
		sourceRepo int64
	}{
		{"instance", 1, scopedWorkflowsCtx{IsGlobal: true}, governance_model.Resource("instance", 0), 1},
		{"organization", 2, scopedWorkflowsCtx{OwnerID: 3, IsOrg: true}, governance_model.Resource("group", 3), 3},
		{"personal", 2, scopedWorkflowsCtx{OwnerID: 2, IsUser: true}, governance_model.Resource("group", 2), 1},
		{"source_repository", 2, scopedWorkflowsCtx{OwnerID: 3, IsOrg: true}, governance_model.Resource("repository", 3), 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
			doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: test.userID})
			actor := governance_service.RequestActor(doer, "", "web")
			require.NoError(t, db.Insert(t.Context(), &governance_model.Reservation{AuthorizationID: "source-settings-test", Resource: test.resource}))
			called := false
			err := withScopedWorkflowSettingsWrite(t.Context(), actor, &test.scope, test.sourceRepo, func(_ context.Context) error {
				called = true
				return nil
			})
			require.ErrorIs(t, err, governance_model.ErrConflict)
			require.False(t, called)
		})
	}
}

func TestDeriveScopedStatusContexts(t *testing.T) {
	t.Run("jobs x events; job name is its name: or its id", func(t *testing.T) {
		content := []byte(`name: CI
on: [push, pull_request]
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: echo
  build:
    name: Build It
    runs-on: ubuntu-latest
    steps:
      - run: echo
`)
		events, err := actions_module.GetEventsFromContent(content)
		require.NoError(t, err)
		got := deriveScopedStatusContexts("org/src", "CI", content, events)
		assert.ElementsMatch(t, []string{
			"org/src: CI / lint (push)",
			"org/src: CI / lint (pull_request)",
			"org/src: CI / Build It (push)",
			"org/src: CI / Build It (pull_request)",
		}, got)
	})

	t.Run("only status-producing events; workflow_dispatch/schedule/workflow_call skipped", func(t *testing.T) {
		content := []byte(`name: CI
on:
  push:
  workflow_dispatch:
  workflow_call:
  schedule:
    - cron: "0 0 * * *"
jobs:
  j:
    runs-on: ubuntu-latest
    steps:
      - run: echo
`)
		events, err := actions_module.GetEventsFromContent(content)
		require.NoError(t, err)
		got := deriveScopedStatusContexts("org/src", "CI", content, events)
		assert.Equal(t, []string{"org/src: CI / j (push)"}, got) // only push posts a commit status
	})

	t.Run("a workflow_dispatch-only workflow has no expected contexts", func(t *testing.T) {
		content := []byte(`name: Deploy
on: workflow_dispatch
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - run: echo
`)
		events, err := actions_module.GetEventsFromContent(content)
		require.NoError(t, err)
		got := deriveScopedStatusContexts("org/src", "Deploy", content, events)
		assert.Empty(t, got) // workflow_dispatch posts no commit status -> nothing to preview (and it cannot be a required check)
	})
}
