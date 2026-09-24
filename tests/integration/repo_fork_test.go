// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	governance_model "gitea.dev/models/governance"
	org_model "gitea.dev/models/organization"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	repo_module "gitea.dev/modules/repository"
	"gitea.dev/modules/structs"
	"gitea.dev/modules/test"
	governance_service "gitea.dev/services/governance"
	org_service "gitea.dev/services/org"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRepoFork(t *testing.T, session *TestSession, ownerName, repoName, forkOwnerName, forkRepoName, forkBranch string) *httptest.ResponseRecorder {
	forkOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: forkOwnerName})

	// Step0: check the existence of the to-fork repo
	req := NewRequestf(t, "GET", "/%s/%s", forkOwnerName, forkRepoName)
	session.MakeRequest(t, req, http.StatusNotFound)

	// Step1: go to the main page of repo
	req = NewRequestf(t, "GET", "/%s/%s", ownerName, repoName)
	resp := session.MakeRequest(t, req, http.StatusOK)

	// Step2: click the fork button
	htmlDoc := NewHTMLParser(t, resp.Body)
	link, exists := htmlDoc.doc.Find(`a.ui.button[href*="/fork"]`).Attr("href")
	assert.True(t, exists, "The template has changed")
	req = NewRequest(t, "GET", link)
	resp = session.MakeRequest(t, req, http.StatusOK)

	// Step3: fill the form of the forking
	htmlDoc = NewHTMLParser(t, resp.Body)
	link, exists = htmlDoc.doc.Find(`form.ui.form[action*="/fork"]`).Attr("action")
	assert.True(t, exists, "The template has changed")
	_, exists = htmlDoc.doc.Find(fmt.Sprintf(".owner.dropdown .item[data-value=\"%d\"]", forkOwner.ID)).Attr("data-value")
	assert.True(t, exists, "Fork owner '%s' is not present in select box", forkOwnerName)
	req = NewRequestWithValues(t, "POST", link, map[string]string{
		"uid":                strconv.FormatInt(forkOwner.ID, 10),
		"repo_name":          forkRepoName,
		"fork_single_branch": forkBranch,
	})
	resp = session.MakeRequest(t, req, http.StatusOK)
	assert.Equal(t, fmt.Sprintf("/%s/%s", forkOwnerName, forkRepoName), test.RedirectURL(resp))

	// Step4: check the existence of the forked repo
	req = NewRequestf(t, "GET", "/%s/%s", forkOwnerName, forkRepoName)
	resp = session.MakeRequest(t, req, http.StatusOK)

	return resp
}

func TestRepoFork(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	session := loginUser(t, "user1")
	testRepoFork(t, session, "user2", "repo1", "user1", "repo1", "")
}

func TestRepoForkToOrg(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	session := loginUser(t, "user2")
	testRepoFork(t, session, "user2", "repo1", "org3", "repo1", "")

	// Check that no more forking is allowed as user2 owns repository
	//  and org3 organization that owner user2 is also now has forked this repository
	req := NewRequest(t, "GET", "/user2/repo1")
	resp := session.MakeRequest(t, req, http.StatusOK)
	htmlDoc := NewHTMLParser(t, resp.Body)
	_, exists := htmlDoc.doc.Find(`a.ui.button[href*="/fork"]`).Attr("href")
	assert.False(t, exists, "Forking should not be allowed anymore")
}

func TestRepoForkAndCreateOfferInheritedGroupOwner(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	child, err := governance_service.CreateGroup(ctx, owner, governance_service.GroupOption{Path: "fork-inherited-owner", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	root, err := governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, owner, 3,
		governance_service.GroupMemberOption{UserID: 5, Role: governance_model.Owner, Revision: root.Revision}, false))
	member, err := org_model.IsOrganizationMember(ctx, child.ID, 5)
	require.NoError(t, err)
	require.True(t, member)
	teams, err := org_model.GetUserOrgTeams(ctx, child.ID, 5)
	require.NoError(t, err)
	require.Empty(t, teams)
	personalOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 21})
	personalRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 34})
	allowed, err := repo_module.CanUserForkRepo(ctx, personalOwner, personalRepo)
	require.NoError(t, err)
	require.False(t, allowed)
	root, err = governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(ctx, owner, 3,
		governance_service.GroupMemberOption{UserID: personalOwner.ID, Role: governance_model.Owner, Revision: root.Revision}, false))
	allowed, err = repo_module.CanUserForkRepo(ctx, personalOwner, personalRepo)
	require.NoError(t, err)
	require.True(t, allowed)

	session := loginUser(t, "user5")
	for _, path := range []string{"/repo/create", "/user2/repo1/fork", fmt.Sprintf("/repo/migrate?service_type=%d", structs.PlainGitService)} {
		response := session.MakeRequest(t, NewRequest(t, "GET", path), http.StatusOK)
		document := NewHTMLParser(t, response.Body)
		selector := fmt.Sprintf(".owner.dropdown .item[data-value=\"%d\"]", child.ID)
		if path == "/repo/create" {
			selector = fmt.Sprintf("#repo_owner_dropdown .item[data-value=\"%d\"]", child.ID)
		}
		item := document.doc.Find(selector)
		require.Equal(t, 1, item.Length(), path)
		title, exists := item.Attr("title")
		require.True(t, exists, path)
		require.Equal(t, child.FullPath, title, path)
		require.Contains(t, item.Text(), child.FullPath, path)
		require.Equal(t, 0, document.doc.Find(fmt.Sprintf(".item[data-value=\"%d\"]", 35)).Length(), path)
	}
}

func TestForkListLimitedAndPrivateRepos(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	forkItemSelector := ".fork-list .item"

	user1Sess := loginUser(t, "user1")
	user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user1"})

	// fork to a limited org
	limitedOrg := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 22})
	assert.Equal(t, structs.VisibleTypeLimited, limitedOrg.Visibility)
	ownerTeam1, err := org_model.OrgFromUser(limitedOrg).GetOwnerTeam(t.Context())
	assert.NoError(t, err)
	assert.NoError(t, org_service.AddTeamMember(t.Context(), ownerTeam1, user1))
	testRepoFork(t, user1Sess, "user2", "repo1", limitedOrg.Name, "repo1", "")

	// fork to a private org
	user4Sess := loginUser(t, "user4")
	user4 := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user4"})
	privateOrg := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 23})
	assert.Equal(t, structs.VisibleTypePrivate, privateOrg.Visibility)
	ownerTeam2, err := org_model.OrgFromUser(privateOrg).GetOwnerTeam(t.Context())
	assert.NoError(t, err)
	assert.NoError(t, org_service.AddTeamMember(t.Context(), ownerTeam2, user4))
	testRepoFork(t, user4Sess, "user2", "repo1", privateOrg.Name, "repo1", "")

	t.Run("Anonymous", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()
		req := NewRequest(t, "GET", "/user2/repo1/forks")
		resp := MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		assert.Equal(t, 0, htmlDoc.Find(forkItemSelector).Length())
	})

	t.Run("Logged in", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		req := NewRequest(t, "GET", "/user2/repo1/forks")
		resp := user1Sess.MakeRequest(t, req, http.StatusOK)
		htmlDoc := NewHTMLParser(t, resp.Body)
		// since user1 is an admin, he can get both of the forked repositories
		assert.Equal(t, 2, htmlDoc.Find(forkItemSelector).Length())

		assert.NoError(t, org_service.AddTeamMember(t.Context(), ownerTeam2, user1))
		resp = user1Sess.MakeRequest(t, req, http.StatusOK)
		htmlDoc = NewHTMLParser(t, resp.Body)
		assert.Equal(t, 2, htmlDoc.Find(forkItemSelector).Length())
	})
}
