// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package private

import (
	"context"
	"net/http"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	pull_model "gitea.dev/models/pull"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/git"
	"gitea.dev/modules/private"
	repo_module "gitea.dev/modules/repository"
	"gitea.dev/services/contexttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlePullRequestMergingKeepsAuthorizationBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*private.HookOptions, []*repo_module.PushUpdateOptions)
		allow  bool
	}{
		{name: "exact", allow: true},
		{name: "wrong actor", change: func(opts *private.HookOptions, _ []*repo_module.PushUpdateOptions) { opts.UserID = 1 }},
		{name: "wrong new commit", change: func(_ *private.HookOptions, updates []*repo_module.PushUpdateOptions) {
			updates[0].NewCommitID = "other"
		}},
		{name: "wrong old commit", change: func(_ *private.HookOptions, updates []*repo_module.PushUpdateOptions) {
			updates[0].OldCommitID = "other"
		}},
		{name: "wrong ref", change: func(_ *private.HookOptions, updates []*repo_module.PushUpdateOptions) {
			updates[0].RefFullName = git.RefNameFromBranch("other")
		}},
		{name: "wrong pull", change: func(opts *private.HookOptions, _ []*repo_module.PushUpdateOptions) { opts.PullRequestID = 5 }},
		{name: "wrong repo", change: func(opts *private.HookOptions, _ []*repo_module.PushUpdateOptions) { opts.RepositoryID = 10 }},
		{name: "not service merge", change: func(opts *private.HookOptions, _ []*repo_module.PushUpdateOptions) { opts.PushTrigger = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			pr, err := issues_model.GetUnmergedPullRequest(t.Context(), 1, 1, "branch2", "master", issues_model.PullRequestFlowGithub)
			require.NoError(t, err)
			authorization := &governance_model.MergeAuthorization{
				RepoID: pr.BaseRepoID, PullID: pr.ID, Branch: pr.BaseBranch,
				Head: "head", OldTarget: "before", NewTarget: "after", Actor: governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"},
			}
			require.NoError(t, governance_model.AuthorizeMerge(t.Context(), authorization, nil, func(context.Context) error { return nil }))
			opts := &private.HookOptions{RepositoryID: pr.BaseRepoID, PullRequestID: pr.ID, UserID: 2, MergeAuthorizationID: authorization.ID, PushTrigger: repo_module.PushTriggerPRMergeToBase}
			updates := []*repo_module.PushUpdateOptions{{OldCommitID: "before", NewCommitID: "after", RefFullName: git.RefNameFromBranch(pr.BaseBranch)}}
			if tc.change != nil {
				tc.change(opts, updates)
			}
			ctx, response := contexttest.MockPrivateContext(t, "/")
			assert.Equal(t, tc.allow, hookPostReceiveHandlePullRequestMerging(ctx, opts, updates), response.Body.String())
			if tc.allow {
				assert.Empty(t, response.Body.String())
				unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID, HasMerged: true, MergedCommitID: "after"})
			} else {
				assert.Equal(t, http.StatusInternalServerError, response.Code)
				fresh, err := issues_model.GetPullRequestByID(t.Context(), pr.ID)
				require.NoError(t, err)
				assert.False(t, fresh.HasMerged)
				other, err := issues_model.GetPullRequestByID(t.Context(), opts.PullRequestID)
				require.NoError(t, err)
				assert.False(t, other.HasMerged)
			}
			reservations, err := db.GetEngine(t.Context()).Where("authorization_id = ?", authorization.ID).Count(new(governance_model.Reservation))
			require.NoError(t, err)
			assert.Positive(t, reservations, "回调不能提前释放未核对的合并占用")
		})
	}
}

func TestHandlePullRequestMerging(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	pr, err := issues_model.GetUnmergedPullRequest(t.Context(), 1, 1, "branch2", "master", issues_model.PullRequestFlowGithub)
	assert.NoError(t, err)
	assert.NoError(t, pr.LoadBaseRepo(t.Context()))

	user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})

	err = pull_model.ScheduleAutoMerge(t.Context(), user1, pr.ID, repo_model.MergeStyleSquash, "squash merge a pr", false)
	assert.NoError(t, err)

	autoMerge := unittest.AssertExistsAndLoadBean(t, &pull_model.AutoMerge{PullID: pr.ID})

	ctx, resp := contexttest.MockPrivateContext(t, "/")
	hookPostReceiveHandlePullRequestMerging(ctx, &private.HookOptions{
		PullRequestID: pr.ID,
		UserID:        2,
	}, []*repo_module.PushUpdateOptions{
		{NewCommitID: "01234567"},
	})
	assert.Empty(t, resp.Body.String())
	pr, err = issues_model.GetPullRequestByID(t.Context(), pr.ID)
	assert.NoError(t, err)
	assert.True(t, pr.HasMerged)
	assert.Equal(t, "01234567", pr.MergedCommitID)

	unittest.AssertNotExistsBean(t, &pull_model.AutoMerge{ID: autoMerge.ID})
}
