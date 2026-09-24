// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"strconv"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestRequiredScopedRunnerTrustBoundary(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := db.GetEngine(ctx).ID(6).Cols("parent_id", "full_path", "lower_path").Update(&governance_model.Namespace{ParentID: 3, FullPath: "org3/org6", LowerPath: "org3/org6"})
	require.NoError(t, err)
	require.NoError(t, AddScopedWorkflowSource(ctx, 3, 3))
	require.NoError(t, SetScopedWorkflowSourceConfigs(ctx, 3, 3, map[string]*ScopedWorkflowConfig{"ci.yaml": {Required: true, Patterns: []string{"* / build (*)"}}}))
	run := &ActionRun{RepoID: 1, OwnerID: 6, WorkflowRepoID: 3, WorkflowID: "ci.yaml", IsScopedRun: true}
	for _, tc := range []struct {
		name    string
		runner  ActionRunner
		allowed bool
	}{
		{"child group", ActionRunner{OwnerID: 6}, false},
		{"repo", ActionRunner{RepoID: 1}, false},
		{"parent group", ActionRunner{OwnerID: 3}, true},
		{"instance", ActionRunner{}, true},
		{"unrelated group", ActionRunner{OwnerID: 7}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allowed, err := RequiredScopedRunnerAllowed(ctx, run, &tc.runner)
			require.NoError(t, err)
			require.Equal(t, tc.allowed, allowed)
		})
	}
	require.NoError(t, AddScopedWorkflowSource(ctx, 0, 3))
	require.NoError(t, SetScopedWorkflowSourceConfigs(ctx, 0, 3, map[string]*ScopedWorkflowConfig{"ci.yaml": {Required: true, Patterns: []string{"* / build (*)"}}}))
	allowed, err := RequiredScopedRunnerAllowed(ctx, run, &ActionRunner{OwnerID: 3})
	require.NoError(t, err)
	require.False(t, allowed, "instance and group requirements intersect at instance Runner")
	allowed, err = RequiredScopedRunnerAllowed(ctx, run, &ActionRunner{})
	require.NoError(t, err)
	require.True(t, allowed)

	plain := &ActionRun{RepoID: 1, OwnerID: 6, WorkflowRepoID: 3, WorkflowID: "optional.yaml", IsScopedRun: true}
	allowed, err = RequiredScopedRunnerAllowed(ctx, plain, &ActionRunner{RepoID: 1})
	require.NoError(t, err)
	require.True(t, allowed, "ordinary scoped workflows keep repository Runners")
}

func TestRequiredScopedRunnerClaimAndCredential(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := db.GetEngine(ctx).ID(6).Cols("parent_id", "full_path", "lower_path").Update(&governance_model.Namespace{ParentID: 3, FullPath: "org3/org6", LowerPath: "org3/org6"})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(1).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 6})
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 1, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	require.NoError(t, AddScopedWorkflowSource(ctx, 3, 3))
	require.NoError(t, SetScopedWorkflowSourceConfigs(ctx, 3, 3, map[string]*ScopedWorkflowConfig{"ci.yaml": {Required: true, Patterns: []string{"* / build (*)"}}}))
	registration, err := GetScopedWorkflowSource(ctx, 3, 3)
	require.NoError(t, err)
	source := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	run := &ActionRun{RepoID: 1, OwnerID: 6, TriggerUserID: 2, Status: StatusWaiting, WorkflowID: "ci.yaml", CommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0", WorkflowRepoID: 3, WorkflowCommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0", WorkflowSourceScopeRevision: source.ActionsScopeRevision, IsScopedRun: true, ScopedConfigRevisions: map[string]int64{strconv.FormatInt(registration.ID, 10): registration.ConfigRevision}}
	require.NoError(t, db.Insert(ctx, run))
	job := &ActionRunJob{RunID: run.ID, RepoID: 1, OwnerID: 6, CommitSHA: run.CommitSHA, JobID: "build", Attempt: 1, Status: StatusWaiting, RunsOn: []string{"ubuntu-latest"}, WorkflowPayload: []byte("on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n")}
	require.NoError(t, db.Insert(ctx, job))
	child := &ActionRunner{UUID: "required-child-runner", Name: "required-child-runner", OwnerID: 6, AgentLabels: []string{"ubuntu-latest"}}
	child.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, child))
	_, claimed, err := CreateTaskForRunner(ctx, child)
	require.NoError(t, err)
	require.False(t, claimed, "consumer-owned Runner cannot claim ancestor-required workflow")
	parent := &ActionRunner{UUID: "required-parent-runner", Name: "required-parent-runner", OwnerID: 3, AgentLabels: []string{"ubuntu-latest"}}
	parent.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, parent))
	task, claimed, err := CreateTaskForRunner(ctx, parent)
	require.NoError(t, err)
	require.True(t, claimed)
	valid, err := TaskCredentialValid(ctx, task)
	require.NoError(t, err)
	require.True(t, valid)
	_, err = db.GetEngine(ctx).ID(parent.ID).Cols("owner_id").Update(&ActionRunner{OwnerID: 6})
	require.NoError(t, err)
	valid, err = TaskCredentialValid(ctx, task)
	require.NoError(t, err)
	require.False(t, valid, "a lower-scope Runner loses task credential access")
}
