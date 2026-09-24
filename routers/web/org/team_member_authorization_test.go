// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package org_test

import (
	"net/http"
	"testing"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/routers/web/org"
	"gitea.dev/services/context"
	"gitea.dev/services/contexttest"
	org_service "gitea.dev/services/org"

	"github.com/stretchr/testify/require"
)

func TestTeamActionCannotAddAfterOwnerRevocation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockContext(t, "POST /org/org3/teams/Owners/action/add?uname=user5")
	contexttest.LoadUser(t, ctx, 2)
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	orgUser, err := organization.GetOrgByID(ctx, team.OrgID)
	require.NoError(t, err)
	ctx.Org = &context.Organization{IsOwner: true, Organization: orgUser, Team: team, OrgLink: "/org/org3"}
	ctx.SetPathParam("action", "add")
	inviter := ctx.Doer
	replacement := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	invitee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	require.NoError(t, org_service.AddTeamMember(ctx, team, replacement))
	require.NoError(t, org_service.RemoveTeamMember(ctx, team, inviter))

	org.TeamsAction(ctx)
	require.Equal(t, http.StatusNotFound, ctx.WrittenStatus())
	member, err := organization.IsTeamMember(ctx, team.OrgID, team.ID, invitee.ID)
	require.NoError(t, err)
	require.False(t, member)
}

func TestTeamActionLastOwnerErrorsAreVisible(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		action     string
		governance bool
	}{
		{name: "governed leave", action: "leave", governance: true},
		{name: "governed remove", action: "remove", governance: true},
		{name: "native leave", action: "leave"},
		{name: "native remove", action: "remove"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			unittest.PrepareTestEnv(t)
			ctx, response := contexttest.MockContext(t, "POST /org/org3/teams/Owners/action/"+testCase.action+"?uid=2")
			contexttest.LoadUser(t, ctx, 2)
			team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
			orgRecord, err := organization.GetOrgByID(ctx, team.OrgID)
			require.NoError(t, err)
			ctx.Org = &context.Organization{IsOwner: true, Organization: orgRecord, Team: team, OrgLink: "/org/org3"}
			ctx.SetPathParam("action", testCase.action)
			if testCase.governance {
				require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
			}

			org.TeamsAction(ctx)
			require.Equal(t, http.StatusBadRequest, ctx.WrittenStatus())
			var body struct {
				ErrorMessage string `json:"errorMessage"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.NotEmpty(t, body.ErrorMessage)
			if testCase.governance {
				require.Contains(t, body.ErrorMessage, "Owner")
			} else {
				require.Equal(t, string(ctx.Tr("form.last_org_owner")), body.ErrorMessage)
			}
			member, err := organization.IsTeamMember(ctx, team.OrgID, team.ID, ctx.Doer.ID)
			require.NoError(t, err)
			require.True(t, member)
		})
	}
}

func TestTeamActionMemberChangesStillRedirectOnSuccess(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		action  string
		actorID int64
	}{
		{name: "leave", action: "leave", actorID: 4},
		{name: "remove", action: "remove", actorID: 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			unittest.PrepareTestEnv(t)
			ctx, response := contexttest.MockContext(t, "POST /org/org3/teams/team1/action/"+testCase.action+"?uid=4")
			contexttest.LoadUser(t, ctx, testCase.actorID)
			team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})
			orgRecord, err := organization.GetOrgByID(ctx, team.OrgID)
			require.NoError(t, err)
			ctx.Org = &context.Organization{IsOwner: testCase.action == "remove", Organization: orgRecord, Team: team, OrgLink: "/org/org3"}
			ctx.SetPathParam("action", testCase.action)

			org.TeamsAction(ctx)
			require.Equal(t, http.StatusOK, ctx.WrittenStatus())
			var body struct {
				Redirect string `json:"redirect"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.NotEmpty(t, body.Redirect)
			member, err := organization.IsTeamMember(ctx, team.OrgID, team.ID, 4)
			require.NoError(t, err)
			require.False(t, member)
		})
	}
}
