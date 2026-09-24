// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"testing"

	activities_model "gitea.dev/models/activities"
	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/organization"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	api "gitea.dev/modules/structs"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPINotificationRevokedUnit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	reader := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 6})
	notification := &activities_model.Notification{UserID: reader.ID, RepoID: repo.ID, IssueID: issue.ID, Source: activities_model.NotificationSourceIssue, Status: activities_model.NotificationStatusUnread}
	require.NoError(t, db.Insert(ctx, notification))
	issue.Title = "撤权后更新的保密标题"
	_, err := db.GetEngine(ctx).ID(issue.ID).Cols("name").Update(issue)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Where("team_id = ? AND type <> ?", 2, unit.TypeCode).Delete(&organization.TeamUnit{})
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx,
		&activities_model.Notification{UserID: reader.ID, RepoID: repo.ID, Source: activities_model.NotificationSourceCommit, CommitID: "abc123", Status: activities_model.NotificationStatusUnread},
		&activities_model.Notification{UserID: reader.ID, RepoID: repo.ID, Source: activities_model.NotificationSourceRepository, Status: activities_model.NotificationStatusUnread},
	))

	session := loginUser(t, reader.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadNotification, auth_model.AccessTokenScopeReadRepository)
	response := MakeRequest(t, NewRequest(t, "GET", "/api/v1/notifications?all=true").AddTokenAuth(token), http.StatusOK)
	threads := DecodeJSON(t, response, []api.NotificationThread{})
	require.Len(t, threads, 2)
	assert.Equal(t, "2", response.Header().Get("X-Total-Count"))
	for _, thread := range threads {
		assert.NotEqual(t, notification.ID, thread.ID)
	}
	MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/api/v1/notifications/threads/%d", notification.ID)).AddTokenAuth(token), http.StatusNotFound)
	response = MakeRequest(t, NewRequest(t, "GET", "/api/v1/notifications/new").AddTokenAuth(token), http.StatusOK)
	count := DecodeJSON(t, response, &api.NotificationCount{})
	assert.EqualValues(t, 2, count.New)
	response = session.MakeRequest(t, NewRequest(t, "GET", "/notifications"), http.StatusOK)
	assert.NotContains(t, response.Body.String(), "撤权后更新的保密标题")
	response = session.MakeRequest(t, NewRequest(t, "GET", "/notifications/new"), http.StatusOK)
	count = DecodeJSON(t, response, &api.NotificationCount{})
	assert.EqualValues(t, 2, count.New)
}

func TestAPINotification(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	thread5 := unittest.AssertExistsAndLoadBean(t, &activities_model.Notification{ID: 5})
	assert.NoError(t, thread5.LoadAttributes(t.Context()))
	session := loginUser(t, user2.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteNotification, auth_model.AccessTokenScopeWriteRepository)

	MakeRequest(t, NewRequest(t, "GET", "/api/v1/notifications"), http.StatusUnauthorized)

	// -- GET /notifications --
	// test filter
	since := "2000-01-01T00%3A50%3A01%2B00%3A00" // 946687801
	req := NewRequest(t, "GET", "/api/v1/notifications?since="+since).
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	apiNL := DecodeJSON(t, resp, []api.NotificationThread{})

	assert.Len(t, apiNL, 1)
	assert.EqualValues(t, 5, apiNL[0].ID)

	// test filter
	before := "2000-01-01T01%3A06%3A59%2B00%3A00" // 946688819

	req = NewRequest(t, "GET", fmt.Sprintf("/api/v1/notifications?all=%s&before=%s", "true", before)).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	apiNL = DecodeJSON(t, resp, []api.NotificationThread{})

	assert.Len(t, apiNL, 3)
	assert.EqualValues(t, 4, apiNL[0].ID)
	assert.True(t, apiNL[0].Unread)
	assert.False(t, apiNL[0].Pinned)
	assert.EqualValues(t, 3, apiNL[1].ID)
	assert.False(t, apiNL[1].Unread)
	assert.True(t, apiNL[1].Pinned)
	assert.EqualValues(t, 2, apiNL[2].ID)
	assert.False(t, apiNL[2].Unread)
	assert.False(t, apiNL[2].Pinned)

	// -- GET /repos/{owner}/{repo}/notifications --
	req = NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/%s/notifications?status-types=unread", user2.Name, repo1.Name)).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	apiNL = DecodeJSON(t, resp, []api.NotificationThread{})

	assert.Len(t, apiNL, 1)
	assert.EqualValues(t, 4, apiNL[0].ID)

	// -- GET /repos/{owner}/{repo}/notifications -- multiple status-types
	req = NewRequest(t, "GET", fmt.Sprintf("/api/v1/repos/%s/%s/notifications?status-types=unread&status-types=pinned", user2.Name, repo1.Name)).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	apiNL = DecodeJSON(t, resp, []api.NotificationThread{})

	assert.Len(t, apiNL, 2)
	assert.EqualValues(t, 4, apiNL[0].ID)
	assert.True(t, apiNL[0].Unread)
	assert.False(t, apiNL[0].Pinned)
	assert.EqualValues(t, 3, apiNL[1].ID)
	assert.False(t, apiNL[1].Unread)
	assert.True(t, apiNL[1].Pinned)

	MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/api/v1/notifications/threads/%d", 1)), http.StatusUnauthorized)

	// -- GET /notifications/threads/{id} --
	// get forbidden
	req = NewRequest(t, "GET", fmt.Sprintf("/api/v1/notifications/threads/%d", 1)).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusForbidden)

	// get own
	req = NewRequest(t, "GET", fmt.Sprintf("/api/v1/notifications/threads/%d", thread5.ID)).
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	apiN := DecodeJSON(t, resp, &api.NotificationThread{})

	assert.EqualValues(t, 5, apiN.ID)
	assert.False(t, apiN.Pinned)
	assert.True(t, apiN.Unread)
	assert.Equal(t, "issue4", apiN.Subject.Title)
	assert.EqualValues(t, "Issue", apiN.Subject.Type)
	assert.Equal(t, thread5.Issue.APIURL(t.Context()), apiN.Subject.URL)
	assert.Equal(t, thread5.Repository.HTMLURL(), apiN.Repository.HTMLURL)

	MakeRequest(t, NewRequest(t, "GET", "/api/v1/notifications/new"), http.StatusUnauthorized)

	// -- check notifications --
	req = NewRequest(t, "GET", "/api/v1/notifications/new").
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	newStruct := DecodeJSON(t, resp, &struct {
		New int64 `json:"new"`
	}{})
	assert.Positive(t, newStruct.New)

	// -- mark notifications as read --
	req = NewRequest(t, "GET", "/api/v1/notifications?status-types=unread").
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	apiNL = DecodeJSON(t, resp, []api.NotificationThread{})
	assert.Len(t, apiNL, 2)

	lastReadAt := "2000-01-01T00%3A50%3A01%2B00%3A00" // 946687801 <- only Notification 4 is in this filter ...
	req = NewRequest(t, "PUT", fmt.Sprintf("/api/v1/repos/%s/%s/notifications?last_read_at=%s", user2.Name, repo1.Name, lastReadAt)).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusResetContent)

	req = NewRequest(t, "GET", "/api/v1/notifications?status-types=unread").
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	apiNL = DecodeJSON(t, resp, []api.NotificationThread{})
	assert.Len(t, apiNL, 1)

	// -- PATCH /notifications/threads/{id} --
	req = NewRequest(t, "PATCH", fmt.Sprintf("/api/v1/notifications/threads/%d", thread5.ID)).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusResetContent)

	assert.Equal(t, activities_model.NotificationStatusUnread, thread5.Status)
	thread5 = unittest.AssertExistsAndLoadBean(t, &activities_model.Notification{ID: 5})
	assert.Equal(t, activities_model.NotificationStatusRead, thread5.Status)

	// -- check notifications --
	req = NewRequest(t, "GET", "/api/v1/notifications/new").
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	newStruct = DecodeJSON(t, resp, &struct {
		New int64 `json:"new"`
	}{})
	assert.Zero(t, newStruct.New)
}

func TestAPINotificationPUT(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	thread5 := unittest.AssertExistsAndLoadBean(t, &activities_model.Notification{ID: 5})
	assert.NoError(t, thread5.LoadAttributes(t.Context()))
	session := loginUser(t, user2.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteNotification)

	// Check notifications are as expected
	req := NewRequest(t, "GET", "/api/v1/notifications?all=true").
		AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	apiNL := DecodeJSON(t, resp, []api.NotificationThread{})

	assert.Len(t, apiNL, 4)
	assert.EqualValues(t, 5, apiNL[0].ID)
	assert.True(t, apiNL[0].Unread)
	assert.False(t, apiNL[0].Pinned)
	assert.EqualValues(t, 4, apiNL[1].ID)
	assert.True(t, apiNL[1].Unread)
	assert.False(t, apiNL[1].Pinned)
	assert.EqualValues(t, 3, apiNL[2].ID)
	assert.False(t, apiNL[2].Unread)
	assert.True(t, apiNL[2].Pinned)
	assert.EqualValues(t, 2, apiNL[3].ID)
	assert.False(t, apiNL[3].Unread)
	assert.False(t, apiNL[3].Pinned)

	//
	// Notification ID 2 is the only one with status-type read & pinned
	// change it to unread.
	//
	req = NewRequest(t, "PUT", "/api/v1/notifications?status-types=read&status-type=pinned&to-status=unread").
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusResetContent)
	apiNL = DecodeJSON(t, resp, []api.NotificationThread{})
	assert.Len(t, apiNL, 1)
	assert.EqualValues(t, 2, apiNL[0].ID)
	assert.True(t, apiNL[0].Unread)
	assert.False(t, apiNL[0].Pinned)

	//
	// Now notification ID 2 is the first in the list and is unread.
	//
	req = NewRequest(t, "GET", "/api/v1/notifications?all=true").
		AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	apiNL = DecodeJSON(t, resp, []api.NotificationThread{})

	assert.Len(t, apiNL, 4)
	assert.EqualValues(t, 2, apiNL[0].ID)
	assert.True(t, apiNL[0].Unread)
	assert.False(t, apiNL[0].Pinned)
}

func TestAPINotificationPublicOnly(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	thread5 := unittest.AssertExistsAndLoadBean(t, &activities_model.Notification{ID: 5})

	token := getUserToken(t, user2.Name, auth_model.AccessTokenScopeReadNotification, auth_model.AccessTokenScopePublicOnly)
	req := NewRequest(t, "GET", "/api/v1/notifications").
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusForbidden)

	req = NewRequest(t, "GET", "/api/v1/notifications/new").
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusForbidden)

	req = NewRequest(t, "GET", fmt.Sprintf("/api/v1/notifications/threads/%d", thread5.ID)).
		AddTokenAuth(token)
	MakeRequest(t, req, http.StatusForbidden)
}
