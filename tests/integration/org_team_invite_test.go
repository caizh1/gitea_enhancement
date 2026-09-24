// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	governance_service "gitea.dev/services/governance"
	org_service "gitea.dev/services/org"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrgTeamInviteRevokedInviterCannotGrantOwner(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	ctx := t.Context()
	owners := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	inviter := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	replacement := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	invitee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	invite, err := organization.CreateTeamInvite(ctx, inviter, owners, invitee.Email)
	require.NoError(t, err)
	require.NoError(t, org_service.AddTeamMember(ctx, owners, replacement))
	require.NoError(t, org_service.RemoveTeamMember(ctx, owners, inviter))
	owner, err := organization.IsOrganizationOwner(ctx, owners.OrgID, inviter.ID)
	require.NoError(t, err)
	require.False(t, owner)

	session := loginUser(t, invitee.Name)
	session.MakeRequest(t, NewRequest(t, "POST", "/org/invite/"+invite.Token), http.StatusNotFound)
	member, err := organization.IsTeamMember(ctx, owners.OrgID, owners.ID, invitee.ID)
	require.NoError(t, err)
	require.False(t, member)
}

func TestOrgTeamInviteRevokedGovernanceOwnerCannotGrantNativeOwner(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	require.NoError(t, governance_service.SetGroupMember(ctx, actor, 3, governance_service.GroupMemberOption{UserID: 4, Role: governance_model.Owner, Revision: 1}, false))
	owner, err := organization.IsOrganizationOwner(ctx, 3, 4)
	require.NoError(t, err)
	require.True(t, owner)

	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	invitee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	issuer := loginUser(t, "user4")
	teamURL := "/org/org3/teams/" + team.Name
	issuer.MakeRequest(t, NewRequestWithValues(t, "POST", teamURL+"/action/add", map[string]string{"uname": invitee.Email}), http.StatusSeeOther)
	invites, err := organization.GetInvitesByTeamID(ctx, team.ID)
	require.NoError(t, err)
	require.Len(t, invites, 1)
	require.Equal(t, int64(4), invites[0].InviterID)

	require.NoError(t, governance_service.SetGroupMember(ctx, actor, 3, governance_service.GroupMemberOption{UserID: 4, Revision: 2}, true))
	owner, err = organization.IsOrganizationOwner(ctx, 3, 4)
	require.NoError(t, err)
	require.False(t, owner)

	recipient := loginUser(t, invitee.Name)
	recipient.MakeRequest(t, NewRequest(t, "POST", "/org/invite/"+invites[0].Token), http.StatusNotFound)
	member, err := organization.IsTeamMember(ctx, team.OrgID, team.ID, invitee.ID)
	require.NoError(t, err)
	require.False(t, member)
}

func TestOrgTeamInviteArchivedGroupRejectsNewGrantButAllowsRevocation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 1})
	inviter := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	invitee := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	invite, err := organization.CreateTeamInvite(ctx, inviter, team, invitee.Email)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(team.OrgID).Cols("archived").Update(&governance_model.Namespace{Archived: true})
	require.NoError(t, err)

	recipient := loginUser(t, invitee.Name)
	recipient.MakeRequest(t, NewRequest(t, "POST", "/org/invite/"+invite.Token), http.StatusConflict)
	member, err := organization.IsTeamMember(ctx, team.OrgID, team.ID, invitee.ID)
	require.NoError(t, err)
	require.False(t, member)

	issuer := loginUser(t, inviter.Name)
	teamURL := "/org/org3/teams/" + team.Name
	issuer.MakeRequest(t, NewRequestWithValues(t, "POST", teamURL+"/action/add", map[string]string{"uname": "new@example.invalid"}), http.StatusConflict)
	issuer.MakeRequest(t, NewRequestWithValues(t, "POST", teamURL+"/action/remove_invite", map[string]string{"iid": strconv.FormatInt(invite.ID, 10)}), http.StatusSeeOther)
	_, err = organization.GetInviteByToken(ctx, invite.Token)
	require.True(t, organization.IsErrTeamInviteNotFound(err))
}

func TestOrgTeamEmailInvite(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	org := unittest.AssertExistsAndLoadBean(t, &organization.Organization{ID: 3})
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})

	isMember, err := organization.IsTeamMember(t.Context(), team.OrgID, team.ID, user.ID)
	assert.NoError(t, err)
	assert.False(t, isMember)

	session := loginUser(t, "user1")

	teamURL := fmt.Sprintf("/org/%s/teams/%s", org.Name, team.Name)
	req := NewRequestWithValues(t, "POST", teamURL+"/action/add", map[string]string{
		"uid":   "1",
		"uname": user.Email,
	})
	resp := session.MakeRequest(t, req, http.StatusSeeOther)
	req = NewRequest(t, "GET", test.RedirectURL(resp))
	session.MakeRequest(t, req, http.StatusOK)

	// get the invite token
	invites, err := organization.GetInvitesByTeamID(t.Context(), team.ID)
	assert.NoError(t, err)
	assert.Len(t, invites, 1)

	session = loginUser(t, user.Name)

	// join the team
	inviteURL := "/org/invite/" + invites[0].Token
	req = NewRequest(t, "POST", inviteURL)
	resp = session.MakeRequest(t, req, http.StatusSeeOther)
	req = NewRequest(t, "GET", test.RedirectURL(resp))
	session.MakeRequest(t, req, http.StatusOK)

	isMember, err = organization.IsTeamMember(t.Context(), team.OrgID, team.ID, user.ID)
	assert.NoError(t, err)
	assert.True(t, isMember)
}

// Check that users are redirected to accept the invitation correctly after login
func TestOrgTeamEmailInviteRedirectsExistingUser(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	org := unittest.AssertExistsAndLoadBean(t, &organization.Organization{ID: 3})
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})

	isMember, err := organization.IsTeamMember(t.Context(), team.OrgID, team.ID, user.ID)
	assert.NoError(t, err)
	assert.False(t, isMember)

	// create the invite
	session := loginUser(t, "user1")

	teamURL := fmt.Sprintf("/org/%s/teams/%s", org.Name, team.Name)
	req := NewRequestWithValues(t, "POST", teamURL+"/action/add", map[string]string{
		"uid":   "1",
		"uname": user.Email,
	})
	resp := session.MakeRequest(t, req, http.StatusSeeOther)
	req = NewRequest(t, "GET", test.RedirectURL(resp))
	session.MakeRequest(t, req, http.StatusOK)

	// get the invite token
	invites, err := organization.GetInvitesByTeamID(t.Context(), team.ID)
	assert.NoError(t, err)
	assert.Len(t, invites, 1)

	// accept the invite
	inviteURL := "/org/invite/" + invites[0].Token
	req = NewRequest(t, "GET", "/user/login?redirect_to="+url.QueryEscape(inviteURL))
	resp = MakeRequest(t, req, http.StatusOK)

	req = NewRequestWithValues(t, "POST", "/user/login", map[string]string{
		"user_name": "user5",
		"password":  "password",
	})
	for _, c := range resp.Result().Cookies() {
		req.AddCookie(c)
	}

	resp = MakeRequest(t, req, http.StatusSeeOther)
	assert.Equal(t, inviteURL, test.RedirectURL(resp))

	// complete the login process
	ch := http.Header{}
	ch.Add("Cookie", strings.Join(resp.Header()["Set-Cookie"], ";"))
	cr := http.Request{Header: ch}

	session = emptyTestSession(t)
	baseURL, err := url.Parse(setting.AppURL)
	assert.NoError(t, err)
	session.jar.SetCookies(baseURL, cr.Cookies())

	// make the request
	req = NewRequest(t, "POST", test.RedirectURL(resp))
	resp = session.MakeRequest(t, req, http.StatusSeeOther)
	req = NewRequest(t, "GET", test.RedirectURL(resp))
	session.MakeRequest(t, req, http.StatusOK)

	isMember, err = organization.IsTeamMember(t.Context(), team.OrgID, team.ID, user.ID)
	assert.NoError(t, err)
	assert.True(t, isMember)
}

// Check that newly signed up users are redirected to accept the invitation correctly
func TestOrgTeamEmailInviteRedirectsNewUser(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	org := unittest.AssertExistsAndLoadBean(t, &organization.Organization{ID: 3})
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})

	// create the invite
	session := loginUser(t, "user1")

	teamURL := fmt.Sprintf("/org/%s/teams/%s", org.Name, team.Name)
	req := NewRequestWithValues(t, "POST", teamURL+"/action/add", map[string]string{
		"uid":   "1",
		"uname": "doesnotexist@example.com",
	})
	resp := session.MakeRequest(t, req, http.StatusSeeOther)
	req = NewRequest(t, "GET", test.RedirectURL(resp))
	session.MakeRequest(t, req, http.StatusOK)

	// get the invite token
	invites, err := organization.GetInvitesByTeamID(t.Context(), team.ID)
	assert.NoError(t, err)
	assert.Len(t, invites, 1)

	// accept the invite
	inviteURL := "/org/invite/" + invites[0].Token
	req = NewRequest(t, "GET", "/user/sign_up?redirect_to="+url.QueryEscape(inviteURL))
	resp = MakeRequest(t, req, http.StatusOK)

	req = NewRequestWithValues(t, "POST", "/user/sign_up", map[string]string{
		"user_name": "doesnotexist",
		"email":     "doesnotexist@example.com",
		"password":  "examplePassword!1",
		"retype":    "examplePassword!1",
	})
	for _, c := range resp.Result().Cookies() {
		req.AddCookie(c)
	}

	resp = MakeRequest(t, req, http.StatusSeeOther)
	assert.Equal(t, inviteURL, test.RedirectURL(resp))

	// complete the signup process
	ch := http.Header{}
	ch.Add("Cookie", strings.Join(resp.Header()["Set-Cookie"], ";"))
	cr := http.Request{Header: ch}

	session = emptyTestSession(t)
	baseURL, err := url.Parse(setting.AppURL)
	assert.NoError(t, err)
	session.jar.SetCookies(baseURL, cr.Cookies())

	// make the redirected request
	req = NewRequest(t, "POST", test.RedirectURL(resp))
	resp = session.MakeRequest(t, req, http.StatusSeeOther)
	req = NewRequest(t, "GET", test.RedirectURL(resp))
	session.MakeRequest(t, req, http.StatusOK)

	// get the new user
	newUser, err := user_model.GetUserByName(t.Context(), "doesnotexist")
	assert.NoError(t, err)

	isMember, err := organization.IsTeamMember(t.Context(), team.OrgID, team.ID, newUser.ID)
	assert.NoError(t, err)
	assert.True(t, isMember)
}

// Check that users are redirected correctly after confirming their email
func TestOrgTeamEmailInviteRedirectsNewUserWithActivation(t *testing.T) {
	// enable email confirmation temporarily
	defer test.MockVariableValue(&setting.Service.RegisterEmailConfirm, true)()
	defer tests.PrepareTestEnv(t)()

	org := unittest.AssertExistsAndLoadBean(t, &organization.Organization{ID: 3})
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})

	// user1: create the invite
	session := loginUser(t, "user1")

	teamURL := fmt.Sprintf("/org/%s/teams/%s", org.Name, team.Name)
	req := NewRequestWithValues(t, "POST", teamURL+"/action/add", map[string]string{
		"uid":   "1",
		"uname": "doesnotexist@example.com",
	})
	resp := session.MakeRequest(t, req, http.StatusSeeOther)
	req = NewRequest(t, "GET", test.RedirectURL(resp))
	session.MakeRequest(t, req, http.StatusOK)

	// get the invite token
	invites, err := organization.GetInvitesByTeamID(t.Context(), team.ID)
	assert.NoError(t, err)
	assert.Len(t, invites, 1)

	// new user: accept the invite
	session = emptyTestSession(t)

	inviteURL := "/org/invite/" + invites[0].Token
	req = NewRequest(t, "GET", "/user/sign_up?redirect_to="+url.QueryEscape(inviteURL))
	session.MakeRequest(t, req, http.StatusOK)
	req = NewRequestWithValues(t, "POST", "/user/sign_up", map[string]string{
		"user_name": "doesnotexist",
		"email":     "doesnotexist@example.com",
		"password":  "examplePassword!1",
		"retype":    "examplePassword!1",
	})
	session.MakeRequest(t, req, http.StatusOK)

	user, err := user_model.GetUserByName(t.Context(), "doesnotexist")
	assert.NoError(t, err)

	activationCode := user_model.GenerateUserTimeLimitCode(&user_model.TimeLimitCodeOptions{Purpose: user_model.TimeLimitCodeActivateAccount}, user)
	activateURL := "/user/activate?code=" + activationCode
	req = NewRequestWithValues(t, "POST", activateURL, map[string]string{
		"password": "examplePassword!1",
	})

	resp = session.MakeRequest(t, req, http.StatusSeeOther)
	// should be redirected to accept the invite
	assert.Equal(t, inviteURL, test.RedirectURL(resp))

	req = NewRequest(t, "POST", test.RedirectURL(resp))
	resp = session.MakeRequest(t, req, http.StatusSeeOther)
	req = NewRequest(t, "GET", test.RedirectURL(resp))
	session.MakeRequest(t, req, http.StatusOK)

	isMember, err := organization.IsTeamMember(t.Context(), team.OrgID, team.ID, user.ID)
	assert.NoError(t, err)
	assert.True(t, isMember)
}

// Test that a logged-in user who navigates to the sign-up link is then redirected using redirect_to
// For example: an invite may have been created before the user account was created, but they may be
// accepting the invite after having created an account separately
func TestOrgTeamEmailInviteRedirectsExistingUserWithLogin(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	org := unittest.AssertExistsAndLoadBean(t, &organization.Organization{ID: 3})
	team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 2})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})

	isMember, err := organization.IsTeamMember(t.Context(), team.OrgID, team.ID, user.ID)
	assert.NoError(t, err)
	assert.False(t, isMember)

	// create the invite
	session := loginUser(t, "user1")

	teamURL := fmt.Sprintf("/org/%s/teams/%s", org.Name, team.Name)
	req := NewRequestWithValues(t, "POST", teamURL+"/action/add", map[string]string{
		"uid":   "1",
		"uname": user.Email,
	})
	resp := session.MakeRequest(t, req, http.StatusSeeOther)
	req = NewRequest(t, "GET", test.RedirectURL(resp))
	session.MakeRequest(t, req, http.StatusOK)

	// get the invite token
	invites, err := organization.GetInvitesByTeamID(t.Context(), team.ID)
	assert.NoError(t, err)
	assert.Len(t, invites, 1)

	// note: the invited user has logged in
	session = loginUser(t, "user5")

	// accept the invite (note: this uses the sign_up url)
	inviteURL := "/org/invite/" + invites[0].Token
	req = NewRequest(t, "GET", "/user/sign_up?redirect_to="+url.QueryEscape(inviteURL))
	resp = session.MakeRequest(t, req, http.StatusSeeOther)
	assert.Equal(t, inviteURL, test.RedirectURL(resp))

	// make the request
	req = NewRequest(t, "POST", test.RedirectURL(resp))
	resp = session.MakeRequest(t, req, http.StatusSeeOther)
	req = NewRequest(t, "GET", test.RedirectURL(resp))
	session.MakeRequest(t, req, http.StatusOK)

	isMember, err = organization.IsTeamMember(t.Context(), team.OrgID, team.ID, user.ID)
	assert.NoError(t, err)
	assert.True(t, isMember)
}
