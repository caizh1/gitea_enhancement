// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues_test

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewIssueLabelsScope(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 18})
	label1 := unittest.AssertExistsAndLoadBean(t, &issues_model.Label{ID: 7})
	label2 := unittest.AssertExistsAndLoadBean(t, &issues_model.Label{ID: 8})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	assert.NoError(t, issues_model.NewIssueLabels(t.Context(), issue, []*issues_model.Label{label1, label2}, doer))

	assert.Len(t, issue.Labels, 1)
	assert.Equal(t, label2.ID, issue.Labels[0].ID)
}

func TestIssueLabelRejectsForgedSource(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 6}) // org 3 repository
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	valid := unittest.AssertExistsAndLoadBean(t, &issues_model.Label{ID: 3})
	foreign := unittest.AssertExistsAndLoadBean(t, &issues_model.Label{ID: 5})
	foreign.OrgID, foreign.RepoID = 3, 0 // forged in-memory ownership must not authorize ID 5
	require.Error(t, issues_model.NewIssueLabels(ctx, issue, []*issues_model.Label{valid, foreign}, doer))
	assert.False(t, issues_model.HasIssueLabel(ctx, issue.ID, valid.ID))
	issue = unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 6})
	require.NoError(t, issues_model.NewIssueLabels(ctx, issue, []*issues_model.Label{valid}, doer))
	assert.True(t, issues_model.HasIssueLabel(ctx, issue.ID, valid.ID))
	assert.False(t, issues_model.HasIssueLabel(ctx, issue.ID, foreign.ID))
	require.Error(t, issues_model.NewIssueLabel(ctx, issue, foreign, doer))
	assert.False(t, issues_model.HasIssueLabel(ctx, issue.ID, foreign.ID))
}

func TestIssueCanAttachAncestorLabel(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := &user_model.User{ID: 20001, Name: "label-child", LowerName: "label-child", Type: user_model.UserTypeOrganization}
	require.NoError(t, db.Insert(ctx, owner))
	require.NoError(t, governance_model.InsertNamespace(ctx, &governance_model.Namespace{ID: owner.ID, ParentID: 3, Slug: "label-child", Kind: "group", Visibility: 2}))
	repo := &repo_model.Repository{ID: 30001, OwnerID: owner.ID, OwnerName: owner.Name, Name: "labels", LowerName: "labels"}
	require.NoError(t, db.Insert(ctx, repo))
	issue := &issues_model.Issue{ID: 40001, RepoID: repo.ID, Index: 1, PosterID: 2, Title: "ancestor label"}
	require.NoError(t, db.Insert(ctx, issue))
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	ancestor := &issues_model.Label{OrgID: 3, Name: "inherited", Color: "#123456"}
	require.NoError(t, db.Insert(ctx, ancestor))
	require.NoError(t, issues_model.NewIssueLabels(ctx, issue, []*issues_model.Label{ancestor}, doer))
	assert.True(t, issues_model.HasIssueLabel(ctx, issue.ID, ancestor.ID))
}
