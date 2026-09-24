// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"net/http"
	"testing"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	api "gitea.dev/modules/structs"
	"gitea.dev/services/contexttest"

	"github.com/stretchr/testify/require"
)

func TestIssueLabelAPIRejectsForeignID(t *testing.T) {
	unittest.PrepareTestEnv(t)
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	for _, tc := range []struct {
		id     float64
		status int
	}{
		{3, http.StatusOK},       // org 3 label
		{5, http.StatusNotFound}, // another repository label
	} {
		ctx, response := contexttest.MockAPIContext(t, "/api/v1/repos/org3/repo3/issues/1/labels")
		contexttest.LoadUser(t, ctx, 2)
		contexttest.LoadRepo(t, ctx, 3)
		ctx.SetPathParam("index", "1")
		issue, labels, err := prepareForReplaceOrAdd(ctx, api.IssueLabelsOption{Labels: []any{tc.id}})
		if tc.status == http.StatusOK {
			require.NoError(t, err)
			require.NotNil(t, issue)
			require.Len(t, labels, 1)
		} else {
			require.Error(t, err)
			require.Equal(t, tc.status, response.Code)
		}
	}
}
