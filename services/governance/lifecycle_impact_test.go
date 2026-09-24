// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/actions"
	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	"gitea.dev/models/secret"
	"gitea.dev/models/unittest"
	"gitea.dev/models/webhook"
	"gitea.dev/modules/json"

	"github.com/stretchr/testify/require"
)

func TestLifecycleSourceImpactDoesNotExposeCredentialValues(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	require.NoError(t, db.Insert(ctx,
		&actions.ActionVariable{OwnerID: 3, Name: "SYNTHETIC_VARIABLE", Data: "synthetic-variable-value"},
		&actions.ActionVariable{OwnerID: 5, Name: "SYNTHETIC_RESTRICTED_VARIABLE", Data: "synthetic-restricted-value"},
		&secret.Secret{OwnerID: 3, Name: "SYNTHETIC_SECRET", Data: "synthetic-encrypted-value"},
		&webhook.Webhook{OwnerID: 3, RepoID: 0, Name: "synthetic", URL: "https://synthetic-endpoint.invalid"},
	))
	group, err := gm.GetNamespace(ctx, 3)
	require.NoError(t, err)
	items, fingerprint, err := LifecycleSourceImpacts(ctx, 2, []*gm.Namespace{group})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotEmpty(t, fingerprint)
	require.Equal(t, 1, items[0].Variables)
	require.Equal(t, 1, items[0].Secrets)
	require.Equal(t, 1, items[0].Hooks)
	encoded, err := json.Marshal(items)
	require.NoError(t, err)
	for _, private := range []string{"synthetic-variable-value", "synthetic-encrypted-value", "https://synthetic-endpoint.invalid", "SYNTHETIC_SECRET"} {
		require.NotContains(t, string(encoded), private)
	}
	restricted, err := gm.GetNamespace(ctx, 5)
	require.NoError(t, err)
	items, restrictedFingerprint, err := LifecycleSourceImpacts(ctx, 2, []*gm.Namespace{restricted})
	require.NoError(t, err)
	require.True(t, items[0].Restricted)
	require.Empty(t, items[0].SourcePath)
	require.Zero(t, items[0].Variables)
	encoded, err = json.Marshal(items)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "synthetic-restricted-value")
	require.NotContains(t, string(encoded), "user5")
	_, err = db.GetEngine(ctx).Where("owner_id = ? AND name = ?", 5, "SYNTHETIC_RESTRICTED_VARIABLE").Cols("data").Update(&actions.ActionVariable{Data: "changed-restricted-value"})
	require.NoError(t, err)
	_, afterFingerprint, err := LifecycleSourceImpacts(ctx, 2, []*gm.Namespace{restricted})
	require.NoError(t, err)
	require.Equal(t, restrictedFingerprint, afterFingerprint)
}
