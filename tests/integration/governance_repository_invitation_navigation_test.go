// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"testing"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestRepositoryInvitationLinkFromCollaborators(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))

	const collaborators = "/user2/repo1/collaborators"
	const invitations = "/governance/repositories/1/invitations"
	link := `href="` + invitations + `"`
	owner := loginUser(t, "user2")
	page := owner.MakeRequest(t, NewRequest(t, "GET", collaborators), http.StatusOK)
	require.Contains(t, page.Body.String(), link, "有管理权者应能从协作者页进入邮箱邀请")
	require.Contains(t, page.Body.String(), "通过邮箱邀请成员")
	invitationPage := owner.MakeRequest(t, NewRequest(t, "GET", invitations), http.StatusOK)
	require.Contains(t, invitationPage.Body.String(), "邀请新成员")

	reader := loginUser(t, "user4")
	page = reader.MakeRequest(t, NewRequest(t, "GET", collaborators), http.StatusOK)
	require.NotContains(t, page.Body.String(), link, "无管理权者不能看到邮箱邀请入口")
	reader.MakeRequest(t, NewRequest(t, "GET", invitations), http.StatusNotFound)
}
