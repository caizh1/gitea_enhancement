// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"strconv"
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/services/context"
	"gitea.dev/services/contexttest"

	"github.com/stretchr/testify/require"
)

func TestInheritedVariablesHideRestrictedSourceAndCannotEditParent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := db.GetEngine(ctx).ID(6).Cols("parent_id", "full_path", "lower_path").Update(&governance_model.Namespace{
		ParentID: 3, FullPath: "org3/org6", LowerPath: "org3/org6",
	})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(3).Cols("visibility").Update(&governance_model.Namespace{Visibility: 2})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(3).Cols("visibility").Update(&user_model.User{Visibility: 2})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(1).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 6})
	require.NoError(t, err)
	parent, err := actions_model.InsertVariable(ctx, 3, 0, "UPSTREAM", "parent-value", "parent-private-description")
	require.NoError(t, err)
	_, err = actions_model.InsertVariable(ctx, 3, 0, "ACTIVE_PARENT", "active-value", "active-private-description")
	require.NoError(t, err)
	_, err = actions_model.InsertVariable(ctx, 0, 1, "UPSTREAM", "repo-value", "")
	require.NoError(t, err)

	repo, err := repo_model.GetRepositoryByID(ctx, 1)
	require.NoError(t, err)
	webCtx, _ := contexttest.MockContext(t, "/org6/repo1/settings/actions/variables")
	contexttest.LoadUser(t, webCtx, 5)
	webCtx.Repo = &context.Repository{Repository: repo}
	webCtx.Data["PageIsRepoSettings"] = true
	Variables(webCtx)
	require.Contains(t, webCtx.Data, "InheritedVariables")
	entries := webCtx.Data["InheritedVariables"].([]inheritedVariable)
	require.Len(t, entries, 2)
	for _, entry := range entries {
		require.NotContains(t, entry.Source, "org3")
		require.Empty(t, entry.Description)
		if entry.Name == "UPSTREAM" {
			require.True(t, entry.Overridden)
			require.Empty(t, entry.Data)
		} else {
			require.Equal(t, "active-value", entry.Data)
		}
	}

	editCtx, _ := contexttest.MockContext(t, "/org6/repo1/settings/actions/variables/edit")
	contexttest.LoadUser(t, editCtx, 5)
	editCtx.SetPathParam("variable_id", strconv.FormatInt(parent.ID, 10))
	require.Nil(t, findActionsVariable(editCtx, parent.ID, &variablesCtx{RepoID: 1, IsRepo: true}))
}
