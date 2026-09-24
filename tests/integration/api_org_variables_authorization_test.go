// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"testing"

	auth_model "gitea.dev/models/auth"
	governance_model "gitea.dev/models/governance"
	api "gitea.dev/modules/structs"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestAPIOrgVariablesWithCurrentOwner(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	session := loginUser(t, "user1")
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteOrganization)
	url := "/api/v1/orgs/org3/actions/variables/CONFIG_WRITE_CHECK"

	MakeRequest(t, NewRequestWithJSON(t, "POST", url, api.CreateVariableOption{Value: "before"}).AddTokenAuth(token), http.StatusCreated)
	MakeRequest(t, NewRequestWithJSON(t, "PUT", url, api.UpdateVariableOption{Value: "after"}).AddTokenAuth(token), http.StatusNoContent)
	MakeRequest(t, NewRequest(t, "DELETE", url).AddTokenAuth(token), http.StatusNoContent)
}
