// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"net/http"
	"testing"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/unittest"
	actions_service "gitea.dev/services/actions"
	"gitea.dev/services/context"
	"gitea.dev/services/contexttest"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestRunnerRegistrationTokenResetRejectsOwnerRevokedAfterRequestContext(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	manager := governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "web"}
	group, err := governance_service.CreateGroup(ctx, manager, governance_service.GroupOption{Path: "registration-reset-revoked", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, manager, group.ID, governance_service.GroupMemberOption{
		UserID: 4, Role: governance_model.Owner, ExpiresUnix: time.Now().Add(time.Hour).Unix(), Revision: group.Revision,
	}, false))
	member := governance_model.Actor{ID: 4, Kind: "user", Name: "user4", Transport: "web"}
	initial, err := actions_service.GetRunnerRegistrationToken(ctx, member, group.ID, 0, false)
	require.NoError(t, err)

	request, response := contexttest.MockContext(t, "POST /org/registration-reset-revoked/settings/actions/runners/reset_registration_token")
	contexttest.LoadUser(t, request, member.ID)
	org, err := organization.GetOrgByID(ctx, group.ID)
	require.NoError(t, err)
	request.ContextUser = org.AsUser()
	request.Org = &context.Organization{IsOwner: true, Organization: org, OrgLink: "/org/registration-reset-revoked"}
	request.Data["PageIsOrgSettings"] = true
	updated, err := governance_model.GetNamespace(ctx, group.ID)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, manager, group.ID, governance_service.GroupMemberOption{
		UserID: member.ID, Revision: updated.Revision,
	}, true))

	ResetRunnerRegistrationToken(request)
	require.Equal(t, http.StatusForbidden, response.Code)
	stored := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunnerToken{ID: initial.ID})
	require.True(t, stored.IsActive)
	require.Equal(t, initial.Token, stored.Token)
	count, err := db.GetEngine(ctx).Where("owner_id = ? AND repo_id = ?", group.ID, 0).Count(new(actions_model.ActionRunnerToken))
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}
