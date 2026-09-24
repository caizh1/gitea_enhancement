// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"strconv"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/util"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScopedWorkflowSource_IsWorkflowRequired(t *testing.T) {
	src := &ActionScopedWorkflowSource{WorkflowConfigs: map[string]*ScopedWorkflowConfig{
		"a.yml": {Required: true, Patterns: []string{"p"}},
		"b.yml": {Required: true, Patterns: []string{"p"}},
		"c.yml": {Required: false, Patterns: []string{"p"}}, // patterns kept as history, not required
	}}
	assert.True(t, src.IsWorkflowRequired("a.yml"))
	assert.True(t, src.IsWorkflowRequired("b.yml"))
	assert.False(t, src.IsWorkflowRequired("c.yml"), "config kept as history but not required")
	assert.False(t, src.IsWorkflowRequired("d.yml"))

	empty := &ActionScopedWorkflowSource{}
	assert.False(t, empty.IsWorkflowRequired("a.yml"))
}

func TestIsWorkflowRequiredInSources(t *testing.T) {
	// repo 100 registered twice (org optional + instance required).
	sources := []*ActionScopedWorkflowSource{
		{OwnerID: 2, SourceRepoID: 100, WorkflowConfigs: nil},
		{OwnerID: 0, SourceRepoID: 100, WorkflowConfigs: map[string]*ScopedWorkflowConfig{"a.yml": {Required: true, Patterns: []string{"p"}}}},
		{OwnerID: 0, SourceRepoID: 200, WorkflowConfigs: map[string]*ScopedWorkflowConfig{"b.yml": {Required: true, Patterns: []string{"p"}}}},
	}

	assert.True(t, IsWorkflowRequiredInSources(sources, 100, "a.yml"), "required at instance level wins over org optional")
	assert.False(t, IsWorkflowRequiredInSources(sources, 100, "z.yml"))
	assert.False(t, IsWorkflowRequiredInSources(sources, 200, "a.yml"), "a.yml is required for repo 100, not repo 200")
	assert.True(t, IsWorkflowRequiredInSources(sources, 200, "b.yml"))
	assert.False(t, IsWorkflowRequiredInSources(sources, 999, "a.yml"), "unknown source repo")
}

func TestGetEffectiveScopedWorkflowSources(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	rows := []*ActionScopedWorkflowSource{
		{OwnerID: 2, SourceRepoID: 100, WorkflowConfigs: nil}, // org 2 registers repo 100 (optional)
		{OwnerID: 0, SourceRepoID: 100, WorkflowConfigs: map[string]*ScopedWorkflowConfig{"a.yml": {Required: true, Patterns: []string{"p"}}}}, // instance also registers repo 100 (required)
		{OwnerID: 0, SourceRepoID: 200, WorkflowConfigs: map[string]*ScopedWorkflowConfig{"b.yml": {Required: true, Patterns: []string{"p"}}}}, // instance source 200
		{OwnerID: 3, SourceRepoID: 300, WorkflowConfigs: map[string]*ScopedWorkflowConfig{"c.yml": {Required: true, Patterns: []string{"p"}}}}, // a different owner's source
	}
	for _, r := range rows {
		require.NoError(t, db.Insert(ctx, r))
	}

	// owner 2 sees its own sources plus instance-level ones, but not owner 3's.
	owner2, err := GetEffectiveScopedWorkflowSources(ctx, 2)
	require.NoError(t, err)
	assert.Len(t, owner2, 3)

	required, err := IsScopedWorkflowRequired(ctx, 2, 100, "a.yml")
	require.NoError(t, err)
	assert.True(t, required, "instance marks a.yml required → required for owner 2 even though org left it optional")

	required, err = IsScopedWorkflowRequired(ctx, 2, 100, "x.yml")
	require.NoError(t, err)
	assert.False(t, required)

	required, err = IsScopedWorkflowRequired(ctx, 2, 200, "b.yml")
	require.NoError(t, err)
	assert.True(t, required)

	// owner 3's source must not be effective for owner 2.
	required, err = IsScopedWorkflowRequired(ctx, 2, 300, "c.yml")
	require.NoError(t, err)
	assert.False(t, required)

	// IsScopedWorkflowSourceEffective: owner-level and instance-level sources are effective; another owner's is not.
	effective, err := IsScopedWorkflowSourceEffective(ctx, 2, 100)
	require.NoError(t, err)
	assert.True(t, effective, "owner 2's own source")

	effective, err = IsScopedWorkflowSourceEffective(ctx, 2, 200)
	require.NoError(t, err)
	assert.True(t, effective, "instance-level source is effective for any owner")

	effective, err = IsScopedWorkflowSourceEffective(ctx, 2, 300)
	require.NoError(t, err)
	assert.False(t, effective, "owner 3's source is not effective for owner 2")

	effective, err = IsScopedWorkflowSourceEffective(ctx, 2, 999)
	require.NoError(t, err)
	assert.False(t, effective, "unknown source repo")

	effective, err = IsScopedWorkflowSourceEffective(ctx, 3, 300)
	require.NoError(t, err)
	assert.True(t, effective, "owner 3's own source is effective for owner 3")
}

func TestScopedWorkflowSourceValidLifecycle(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	ctx := t.Context()
	require.NoError(t, AddScopedWorkflowSource(ctx, 2, 1))
	require.NoError(t, SetScopedWorkflowSourceConfigs(ctx, 2, 1, map[string]*ScopedWorkflowConfig{"required.yaml": {Required: true, Patterns: []string{"source: required"}}}))
	valid, err := ScopedWorkflowSourceValid(ctx, 2, 1)
	require.NoError(t, err)
	require.True(t, valid)
	_, err = db.GetEngine(ctx).ID(1).Cols("is_archived").Update(&repo_model.Repository{IsArchived: true})
	require.NoError(t, err)
	valid, err = ScopedWorkflowSourceValid(ctx, 2, 1)
	require.NoError(t, err)
	require.False(t, valid)
	required, err := IsScopedWorkflowRequired(ctx, 2, 1, "required.yaml")
	require.NoError(t, err)
	require.True(t, required)
	_, err = db.GetEngine(ctx).ID(1).Cols("is_archived").Update(&repo_model.Repository{IsArchived: false})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(2).Cols("delete_after").Update(&governance_model.Namespace{DeleteAfter: 1})
	require.NoError(t, err)
	valid, err = ScopedWorkflowSourceValid(ctx, 2, 1)
	require.NoError(t, err)
	require.False(t, valid)
	_, err = db.GetEngine(ctx).ID(2).Cols("delete_after").Update(&governance_model.Namespace{DeleteAfter: 0})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(1).Cols("is_archived", "owner_id").Update(&repo_model.Repository{OwnerID: 3})
	require.NoError(t, err)
	valid, err = ScopedWorkflowSourceValid(ctx, 2, 1)
	require.NoError(t, err)
	require.False(t, valid)
	_, err = db.GetEngine(ctx).ID(1).Cols("is_archived").Update(&repo_model.Repository{IsArchived: false})
	require.NoError(t, err)
	valid, err = ScopedWorkflowSourceValid(ctx, 2, 1)
	require.NoError(t, err)
	require.False(t, valid)
	require.NoError(t, AddScopedWorkflowSource(ctx, 0, 1))
	valid, err = ScopedWorkflowSourceValid(ctx, 2, 1)
	require.NoError(t, err)
	require.True(t, valid)
	_, err = db.GetEngine(ctx).ID(1).Cols("owner_id").Incr("actions_scope_revision").Update(&repo_model.Repository{OwnerID: 2})
	require.NoError(t, err)
	valid, err = ScopedWorkflowSourceValid(ctx, 2, 1)
	require.NoError(t, err)
	require.False(t, valid, "旧注册不得在来源移回原 owner 后重新启用")
	require.NoError(t, RemoveScopedWorkflowSource(ctx, 2, 1))
	require.NoError(t, AddScopedWorkflowSource(ctx, 2, 1))
	valid, err = ScopedWorkflowSourceValid(ctx, 2, 1)
	require.NoError(t, err)
	require.True(t, valid)
}

func TestScopedWorkflowAncestorSources(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	require.NoError(t, governance_model.InsertNamespace(ctx, &governance_model.Namespace{ID: 9001, ParentID: 3, Slug: "child", Kind: "group", Visibility: 2}))
	require.NoError(t, governance_model.InsertNamespace(ctx, &governance_model.Namespace{ID: 9002, ParentID: 0, Slug: "shared", Kind: "group", Visibility: 2}))
	require.NoError(t, AddScopedWorkflowSource(ctx, 3, 3))
	require.NoError(t, AddScopedWorkflowSource(ctx, 0, 1))
	require.NoError(t, db.Insert(ctx, &ActionScopedWorkflowSource{OwnerID: 9002, SourceRepoID: 5, ConfigRevision: 1}))
	sources, err := GetEffectiveScopedWorkflowSources(ctx, 9001)
	require.NoError(t, err)
	require.Len(t, sources, 2, "ancestor and instance sources apply; shared group does not")
	effective, err := IsScopedWorkflowSourceEffective(ctx, 9001, 3)
	require.NoError(t, err)
	require.True(t, effective)
	valid, err := ScopedWorkflowSourceValid(ctx, 9001, 3)
	require.NoError(t, err)
	require.True(t, valid)
	effective, err = IsScopedWorkflowSourceEffective(ctx, 9001, 5)
	require.NoError(t, err)
	require.False(t, effective)
}

func TestScopedWorkflowSourceCRUD(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()

	// add is idempotent
	require.NoError(t, AddScopedWorkflowSource(ctx, 5, 1))
	require.NoError(t, AddScopedWorkflowSource(ctx, 5, 1))
	sources, err := GetScopedWorkflowSourcesByOwner(ctx, 5)
	require.NoError(t, err)
	assert.Len(t, sources, 1)

	// set the per-workflow configs (entry name -> {required, patterns}); a.yml required, b.yml kept as history (not required)
	configs := map[string]*ScopedWorkflowConfig{
		"a.yml": {Required: true, Patterns: []string{"src: a.yml / *"}},
		"b.yml": {Required: false, Patterns: []string{"src: b.yml / build (push)"}},
	}
	require.NoError(t, SetScopedWorkflowSourceConfigs(ctx, 5, 1, configs))
	src, err := GetScopedWorkflowSource(ctx, 5, 1)
	require.NoError(t, err)
	assert.Equal(t, configs, src.WorkflowConfigs)

	// clearing the configs works
	require.NoError(t, SetScopedWorkflowSourceConfigs(ctx, 5, 1, nil))
	src, err = GetScopedWorkflowSource(ctx, 5, 1)
	require.NoError(t, err)
	assert.Empty(t, src.WorkflowConfigs)

	// remove
	require.NoError(t, RemoveScopedWorkflowSource(ctx, 5, 1))
	_, err = GetScopedWorkflowSource(ctx, 5, 1)
	assert.ErrorIs(t, err, util.ErrNotExist)
	sources, err = GetScopedWorkflowSourcesByOwner(ctx, 5)
	require.NoError(t, err)
	assert.Empty(t, sources)
}

func TestScopedWorkflowRunDoesNotReviveAfterSourceReRegistration(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	ctx := t.Context()
	require.NoError(t, AddScopedWorkflowSource(ctx, 2, 1))
	first, err := GetScopedWorkflowSource(ctx, 2, 1)
	require.NoError(t, err)
	run := &ActionRun{
		OwnerID:                     2,
		WorkflowRepoID:              1,
		WorkflowSourceScopeRevision: first.SourceScopeRevision,
		IsScopedRun:                 true,
		ScopedConfigRevisions:       map[string]int64{strconv.FormatInt(first.ID, 10): first.ConfigRevision},
	}
	valid, err := ScopedWorkflowRunValid(ctx, run)
	require.NoError(t, err)
	require.True(t, valid)

	require.NoError(t, SetScopedWorkflowSourceConfigs(ctx, 2, 1, map[string]*ScopedWorkflowConfig{"required.yml": {Required: true}}))
	valid, err = ScopedWorkflowRunValid(ctx, run)
	require.NoError(t, err)
	require.True(t, valid, "配置编辑不终止仍由原注册授权的普通运行")

	require.NoError(t, RemoveScopedWorkflowSource(ctx, 2, 1))
	valid, err = ScopedWorkflowRunValid(ctx, run)
	require.NoError(t, err)
	require.False(t, valid)
	require.NoError(t, AddScopedWorkflowSource(ctx, 2, 1))
	second, err := GetScopedWorkflowSource(ctx, 2, 1)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	valid, err = ScopedWorkflowRunValid(ctx, run)
	require.NoError(t, err)
	require.False(t, valid, "新注册不能复活旧任务凭据")

	run.ScopedConfigRevisions = map[string]int64{strconv.FormatInt(second.ID, 10): second.ConfigRevision}
	valid, err = ScopedWorkflowRunValid(ctx, run)
	require.NoError(t, err)
	require.True(t, valid, "当前注册签发的运行仍可执行")
}
