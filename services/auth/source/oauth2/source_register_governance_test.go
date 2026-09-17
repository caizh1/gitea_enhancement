// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package oauth2

import (
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"

	"github.com/markbates/goth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparedProviderIsNotPublishedWhenAuditTransactionFails(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	const name = "审计失败不得发布"
	gothRWMutex.Lock()
	delete(goth.GetProviders(), name)
	gothRWMutex.Unlock()
	_, err := db.GetEngine(t.Context()).ID(1).Delete(new(governance_model.WriteLock))
	require.NoError(t, err)

	source := &auth_model.Source{Type: auth_model.OAuth2, Name: name, IsActive: true}
	source.Cfg = &Source{ConfigBase: auth_model.ConfigBase{AuthSource: source}, Provider: "fake", ClientID: "public-id", ClientSecret: "secret"}
	require.Error(t, auth_model.CreateSource(t.Context(), source))
	assert.Zero(t, source.ID)
	unittest.AssertNotExistsBean(t, &auth_model.Source{Name: name})
	gothRWMutex.RLock()
	_, published := goth.GetProviders()[name]
	gothRWMutex.RUnlock()
	assert.False(t, published)
}
