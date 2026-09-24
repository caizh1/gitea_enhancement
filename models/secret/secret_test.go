// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secret

import (
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	actions_module "gitea.dev/modules/actions"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskSecretsPreferNearestNamespaceAndKeepForkBoundary(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := db.GetEngine(ctx).ID(6).Cols("parent_id", "full_path", "lower_path").Update(&governance_model.Namespace{
		ParentID: 3, FullPath: "org3/org6", LowerPath: "org3/org6",
	})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(1).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 6})
	require.NoError(t, err)
	for _, entry := range []struct {
		ownerID, repoID int64
		name, value     string
	}{
		{3, 0, "CI_SECRET", "parent"},
		{6, 0, "CI_SECRET", "child"},
		{0, 1, "CI_SECRET", "repo"},
		{3, 0, "CHILD_SECRET", "parent"},
		{6, 0, "CHILD_SECRET", "child"},
		{3, 0, "PARENT_SECRET", "inherited"},
		{7, 0, "UNRELATED_SECRET", "excluded"},
	} {
		_, err := InsertEncryptedSecret(ctx, entry.ownerID, entry.repoID, entry.name, entry.value, "")
		require.NoError(t, err)
	}
	repo, err := repo_model.GetRepositoryByID(ctx, 1)
	require.NoError(t, err)
	run := &actions_model.ActionRun{RepoID: 1, OwnerID: 6, Repo: repo}
	job := &actions_model.ActionRunJob{RepoID: 1, OwnerID: 6, Run: run}
	task := &actions_model.ActionTask{RepoID: 1, OwnerID: 6, Token: "synthetic-token", Job: job}
	secrets, err := GetSecretsOfTask(ctx, task)
	require.NoError(t, err)
	require.Equal(t, "repo", secrets["CI_SECRET"])
	require.Equal(t, "child", secrets["CHILD_SECRET"])
	require.Equal(t, "inherited", secrets["PARENT_SECRET"])
	require.NotContains(t, secrets, "UNRELATED_SECRET")

	run.IsForkPullRequest = true
	secrets, err = GetSecretsOfTask(ctx, task)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"GITHUB_TOKEN": "synthetic-token", "GITEA_TOKEN": "synthetic-token"}, secrets)

	run.IsForkPullRequest = false
	_, err = db.GetEngine(ctx).Where("owner_id = ? AND name = ?", 6, "CHILD_SECRET").Cols("data").Update(&Secret{Data: "invalid-ciphertext"})
	require.NoError(t, err)
	secrets, err = GetSecretsOfTask(ctx, task)
	require.Error(t, err)
	require.Nil(t, secrets)
}

func TestProtectedSecretsUseSelectedSourceAndCurrentRules(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := InsertEncryptedSecret(ctx, 2, 0, "DEPLOY_KEY", "synthetic-parent", "")
	require.NoError(t, err)
	local, err := InsertEncryptedSecret(ctx, 0, 1, "DEPLOY_KEY", "synthetic-local", "", true)
	require.NoError(t, err)
	_, err = InsertEncryptedSecret(ctx, 0, 1, "ORDINARY_KEY", "synthetic-ordinary", "")
	require.NoError(t, err)
	branchRule := &git_model.ProtectedBranch{RepoID: 1, RuleName: "main"}
	require.NoError(t, db.Insert(ctx, branchRule))
	branch := &git_model.Branch{RepoID: 1, Name: "main", CommitID: "synthetic-commit"}
	require.NoError(t, db.Insert(ctx, branch))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	run := &actions_model.ActionRun{
		RepoID: 1, OwnerID: repo.OwnerID, Repo: repo,
		Ref: "refs/heads/main", CommitSHA: "synthetic-commit", TriggerEvent: actions_module.GithubEventPush,
	}
	job := &actions_model.ActionRunJob{RepoID: 1, OwnerID: repo.OwnerID, Run: run}
	task := &actions_model.ActionTask{RepoID: 1, OwnerID: repo.OwnerID, Token: "synthetic-token", Job: job}
	assertSelected := func(want string) {
		t.Helper()
		secrets, err := GetSecretsOfTask(ctx, task)
		require.NoError(t, err)
		assert.Equal(t, want, secrets["DEPLOY_KEY"])
		if run.IsForkPullRequest && run.TriggerEvent != actions_module.GithubEventPullRequestTarget {
			assert.NotContains(t, secrets, "ORDINARY_KEY")
		} else {
			assert.Equal(t, "synthetic-ordinary", secrets["ORDINARY_KEY"])
		}
	}
	assertSelected("synthetic-local")
	_, err = db.GetEngine(ctx).ID(branch.ID).Cols("commit_id").Update(&git_model.Branch{CommitID: "later-commit"})
	require.NoError(t, err)
	assertSelected("") // An old queued run loses protected credentials after the ref advances.
	_, err = db.GetEngine(ctx).ID(branch.ID).Cols("commit_id").Update(&git_model.Branch{CommitID: "synthetic-commit"})
	require.NoError(t, err)
	run.Ref = "refs/heads/feature"
	assertSelected("") // The protected local override cannot fall back to the owner value.
	run.Ref = "refs/heads/main"
	run.IsForkPullRequest = true
	assertSelected("")
	run.IsForkPullRequest = false
	run.TriggerEvent = actions_module.GithubEventPullRequestTarget
	assertSelected("")
	run.IsForkPullRequest = true
	assertSelected("") // Ordinary pull_request_target secrets retain their existing behavior.
	run.IsForkPullRequest = false
	run.TriggerEvent = actions_module.GithubEventPush
	run.Ref = "refs/tags/v1"
	assertSelected("")
	tagRule := &git_model.ProtectedTag{RepoID: 1, NamePattern: "v*"}
	require.NoError(t, db.Insert(ctx, tagRule))
	assertSelected("") // A current rule alone cannot prove this tag was protected at enqueue time.
	run.Ref = "refs/heads/main"
	run.TriggerEvent = "workflow_dispatch"
	assertSelected("synthetic-local")
	run.TriggerEvent = actions_module.GithubEventSchedule
	assertSelected("synthetic-local")
	run.TriggerEvent = actions_module.GithubEventPush
	run.Ref = "refs/heads/main"
	_, err = db.GetEngine(ctx).ID(branchRule.ID).Delete(new(git_model.ProtectedBranch))
	require.NoError(t, err)
	assertSelected("")
	require.NoError(t, db.Insert(ctx, &git_model.ProtectedBranch{RepoID: 1, RuleName: "main"}))
	_, err = db.GetEngine(ctx).ID(local.ID).Cols("data").Update(&Secret{Data: "invalid-ciphertext"})
	require.NoError(t, err)
	run.Ref = "refs/heads/main"
	secrets, err := GetSecretsOfTask(ctx, task)
	require.Error(t, err) // Corrupted selected ciphertext fails closed instead of exposing the parent value.
	require.Nil(t, secrets)
}

func TestGetScopedSecretsForJob(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()

	base := map[string]string{
		"GITHUB_TOKEN": "tok",
		"GITEA_TOKEN":  "tok",
		"PROD_API_KEY": "prod-secret",
		"DEV_API_KEY":  "dev-secret",
	}

	// insertCaller create an ActionRunJob caller row with the given CallSecrets policy
	insertCaller := func(t *testing.T, runID, parentJobID int64, callSecrets string) *actions_model.ActionRunJob {
		t.Helper()
		job := &actions_model.ActionRunJob{
			RunID:            runID,
			RepoID:           1,
			IsReusableCaller: true,
			ParentJobID:      parentJobID,
			CallSecrets:      callSecrets,
			Status:           actions_model.StatusBlocked,
		}
		require.NoError(t, db.Insert(t.Context(), job))
		return job
	}

	t.Run("TopLevelJob_ReturnsBaseUnchanged", func(t *testing.T) {
		const runID = 9001
		leaf := &actions_model.ActionRunJob{RunID: runID, ParentJobID: 0}

		got, err := getScopedSecretsForJob(ctx, leaf, base)
		require.NoError(t, err)
		assert.Equal(t, base, got, "top-level jobs should see the full base scope")
	})

	t.Run("CallerInherit_PassesParentScopeThrough", func(t *testing.T) {
		const runID = 9002
		caller := insertCaller(t, runID, 0, "inherit")
		leaf := &actions_model.ActionRunJob{RunID: runID, ParentJobID: caller.ID}

		got, err := getScopedSecretsForJob(ctx, leaf, base)
		require.NoError(t, err)
		assert.Equal(t, base, got, "secrets: inherit forwards everything from parent scope")
	})

	t.Run("CallerEmptySecrets_ExposesOnlyAutoTokens", func(t *testing.T) {
		const runID = 9003
		caller := insertCaller(t, runID, 0, "")
		leaf := &actions_model.ActionRunJob{RunID: runID, ParentJobID: caller.ID}

		got, err := getScopedSecretsForJob(ctx, leaf, base)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			"GITHUB_TOKEN": "tok",
			"GITEA_TOKEN":  "tok",
		}, got)
	})

	t.Run("CallerMapping_OnlyMappedAliasesPlusTokens", func(t *testing.T) {
		const runID = 9004
		// {alias: source} - the called workflow sees `secrets.MY_KEY` resolved to PROD_API_KEY's value.
		caller := insertCaller(t, runID, 0, `{"MY_KEY":"PROD_API_KEY"}`)
		leaf := &actions_model.ActionRunJob{RunID: runID, ParentJobID: caller.ID}

		got, err := getScopedSecretsForJob(ctx, leaf, base)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			"GITHUB_TOKEN": "tok",
			"GITEA_TOKEN":  "tok",
			"MY_KEY":       "prod-secret",
			// no "dev-secret"
		}, got)
	})

	t.Run("CallerMapping_CaseInsensitiveSource", func(t *testing.T) {
		const runID = 9005
		caller := insertCaller(t, runID, 0, `{"alias":"prod_api_key"}`)
		leaf := &actions_model.ActionRunJob{RunID: runID, ParentJobID: caller.ID}

		got, err := getScopedSecretsForJob(ctx, leaf, base)
		require.NoError(t, err)
		assert.Equal(t, "prod-secret", got["alias"])
	})

	t.Run("CallerMapping_UnknownSourceDropsAlias", func(t *testing.T) {
		const runID = 9006
		// alias points at a non-existent secret name, so it must be dropped.
		caller := insertCaller(t, runID, 0, `{"MAPPED_ALIAS":"DOES_NOT_EXIST"}`)
		leaf := &actions_model.ActionRunJob{RunID: runID, ParentJobID: caller.ID}

		got, err := getScopedSecretsForJob(ctx, leaf, base)
		require.NoError(t, err)
		_, present := got["MAPPED_ALIAS"]
		assert.False(t, present)
	})

	t.Run("Nested_InheritThenInherit_FullScope", func(t *testing.T) {
		const runID = 9007
		outer := insertCaller(t, runID, 0, "inherit")
		inner := insertCaller(t, runID, outer.ID, "inherit")
		leaf := &actions_model.ActionRunJob{RunID: runID, ParentJobID: inner.ID}

		got, err := getScopedSecretsForJob(ctx, leaf, base)
		require.NoError(t, err)
		assert.Equal(t, base, got, "inherit-then-inherit should pass the full base scope through")
	})

	t.Run("Nested_InheritThenMapping_InnerNarrows", func(t *testing.T) {
		const runID = 9008
		// inner mapping narrows the full scope it inherited from outer.
		outer := insertCaller(t, runID, 0, "inherit")
		inner := insertCaller(t, runID, outer.ID, `{"ALIAS_OUT":"PROD_API_KEY"}`)
		leaf := &actions_model.ActionRunJob{RunID: runID, ParentJobID: inner.ID}

		got, err := getScopedSecretsForJob(ctx, leaf, base)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			"GITHUB_TOKEN": "tok",
			"GITEA_TOKEN":  "tok",
			"ALIAS_OUT":    "prod-secret",
			// no "dev-secret"
		}, got)
	})

	t.Run("Nested_MappingThenInherit_OuterNarrows", func(t *testing.T) {
		const runID = 9009
		// inner inherits outer's already-narrowed scope, so leaf sees only auto-tokens + OUTER_ALIAS.
		outer := insertCaller(t, runID, 0, `{"OUTER_ALIAS":"PROD_API_KEY"}`)
		inner := insertCaller(t, runID, outer.ID, "inherit")
		leaf := &actions_model.ActionRunJob{RunID: runID, ParentJobID: inner.ID}

		got, err := getScopedSecretsForJob(ctx, leaf, base)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			"GITHUB_TOKEN": "tok",
			"GITEA_TOKEN":  "tok",
			"OUTER_ALIAS":  "prod-secret",
			// no "dev-secret"
		}, got)
	})

	t.Run("Nested_MappingThenMapping_InnerSourceMustExistInOuterScope", func(t *testing.T) {
		const runID = 9010
		// inner can rename ALIAS_A (in outer's scope) to ALIAS_C, but cannot forward DEV_API_KEY, which outer dropped.
		outer := insertCaller(t, runID, 0, `{"ALIAS_A":"PROD_API_KEY"}`)
		inner := insertCaller(t, runID, outer.ID, `{"ALIAS_B":"DEV_API_KEY","ALIAS_C":"ALIAS_A"}`)
		leaf := &actions_model.ActionRunJob{RunID: runID, ParentJobID: inner.ID}

		got, err := getScopedSecretsForJob(ctx, leaf, base)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			"GITHUB_TOKEN": "tok",
			"GITEA_TOKEN":  "tok",
			"ALIAS_C":      "prod-secret",
			// no "dev-secret"
		}, got)
	})
}
