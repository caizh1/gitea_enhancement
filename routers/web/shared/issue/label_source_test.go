// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"testing"

	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/unittest"
	"gitea.dev/services/contexttest"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestLabelSourcePathRequiresReadableGroup(t *testing.T) {
	unittest.PrepareTestEnv(t)
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	ctx, _ := contexttest.MockContext(t, "/org/org3/settings/labels")
	labels := []*issues_model.Label{{OrgID: 3}}
	require.NoError(t, PopulateLabelSources(ctx, labels))
	assert.Empty(t, labels[0].SourcePath)
	assert.Empty(t, labels[0].SourceLink)
	contexttest.LoadUser(t, ctx, 2)
	require.NoError(t, PopulateLabelSources(ctx, labels))
	assert.Equal(t, "org3", labels[0].SourcePath)
	assert.True(t, labels[0].CanManageSource)
	assert.Contains(t, labels[0].SourceLink, "/org/org3/settings/labels")
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
	child, err := governance_service.CreateGroup(t.Context(), actor, governance_service.GroupOption{ParentID: 3, Path: "label-child", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, governance_service.SetGroupMember(t.Context(), actor, child.ID, governance_service.GroupMemberOption{UserID: 4, Role: governance_model.Owner, Revision: 1}, false))
	contexttest.LoadUser(t, ctx, 4)
	require.NoError(t, PopulateLabelSources(ctx, labels))
	assert.False(t, labels[0].CanManageSource)
	assert.Empty(t, labels[0].SourceLink)
}
