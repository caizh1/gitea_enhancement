// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDashboardOrganizationFullPath(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// 测试样本在迁移之后载入，补齐生产迁移已建立的命名空间。
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	_, err := db.GetEngine(t.Context()).Table("user").Where("id = ?", 3).Cols("namespace_path").Update(map[string]any{"namespace_path": "parent/org3"})
	assert.NoError(t, err)

	resp := loginUser(t, "user4").MakeRequest(t, NewRequest(t, "GET", "/"), http.StatusOK)
	assert.Contains(t, resp.Body.String(), `'full_path': "parent/org3"`)
}
