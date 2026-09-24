// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	"net/url"
	"strconv"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	secret_model "gitea.dev/models/secret"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/services/context"
	"gitea.dev/services/contexttest"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestInheritedSecretsHideRestrictedSourceAndCannotDeleteParent(t *testing.T) {
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
	parent, err := secret_model.InsertEncryptedSecret(ctx, 3, 0, "UPSTREAM_SECRET", "synthetic-parent-value", "parent-private-description", true)
	require.NoError(t, err)
	repo, err := repo_model.GetRepositoryByID(ctx, 1)
	require.NoError(t, err)
	webCtx, _ := contexttest.MockContext(t, "/org6/repo1/settings/actions/secrets")
	contexttest.LoadUser(t, webCtx, 5)
	webCtx.Repo = &context.Repository{Repository: repo}
	SetSecretsContext(webCtx, 0, 1)
	require.Contains(t, webCtx.Data, "InheritedSecrets")
	entries := webCtx.Data["InheritedSecrets"].([]inheritedSecret)
	require.Len(t, entries, 1)
	require.Equal(t, "UPSTREAM_SECRET", entries[0].Name)
	require.NotContains(t, entries[0].Source, "org3")
	require.Empty(t, entries[0].Description)
	require.False(t, entries[0].Protected)

	webCtx.Req.Form = url.Values{"id": {strconv.FormatInt(parent.ID, 10)}}
	PerformSecretsDelete(webCtx, 0, 1, "/org6/repo1/settings/actions/secrets")
	found, err := db.GetEngine(ctx).ID(parent.ID).Exist(new(secret_model.Secret))
	require.NoError(t, err)
	require.True(t, found)
}
