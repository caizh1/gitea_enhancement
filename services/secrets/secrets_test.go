// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secrets

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	secret_model "gitea.dev/models/secret"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestSecretProtectionOmittedUpdatePreservesPolicy(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	protected := true
	secret, created, err := CreateOrUpdateSecret(ctx, 2, 0, "SYNTHETIC_KEY", "synthetic-value-1", "", &protected)
	require.NoError(t, err)
	require.True(t, created)
	require.True(t, secret.Protected)
	_, created, err = CreateOrUpdateSecret(ctx, 2, 0, "SYNTHETIC_KEY", "synthetic-value-2", "")
	require.NoError(t, err)
	require.False(t, created)
	stored, has, err := db.GetByID[secret_model.Secret](ctx, secret.ID)
	require.NoError(t, err)
	require.True(t, has)
	require.True(t, stored.Protected)
	protected = false
	_, _, err = CreateOrUpdateSecret(ctx, 2, 0, "SYNTHETIC_KEY", "synthetic-value-3", "", &protected)
	require.NoError(t, err)
	stored, has, err = db.GetByID[secret_model.Secret](ctx, secret.ID)
	require.NoError(t, err)
	require.True(t, has)
	require.False(t, stored.Protected)
	var events []*governance_model.AuditEvent
	require.NoError(t, db.GetEngine(ctx).Where("object_type = ? AND object_id = ?", "actions_secret", secret.ID).Find(&events))
	require.NotEmpty(t, events)
	for _, event := range events {
		require.NotContains(t, string(event.Details), "synthetic-value")
	}
}
