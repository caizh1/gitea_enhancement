// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/queue"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/require"
)

func TestRequiredWorkflowDefaultBranchChangesRejected(t *testing.T) {
	q, err := queue.NewWorkerPoolQueueWithContext(t.Context(), "default-branch-test", setting.QueueSettings{Type: "dummy"}, func(...*LicenseUpdaterOptions) []*LicenseUpdaterOptions { return nil }, false)
	require.NoError(t, err)
	defer test.MockVariableValue(&licenseUpdaterQueue, q)()
	for _, operation := range []string{"switch", "rename", "stale settings"} {
		t.Run(operation, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
			repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
			t.Cleanup(func() {
				require.NoError(t, gitrepo.SetDefaultBranch(context.WithoutCancel(t.Context()), repo, "master"))
			})
			require.NoError(t, db.Insert(t.Context(), &actions_model.ActionScopedWorkflowSource{
				OwnerID: repo.OwnerID, SourceRepoID: repo.ID,
				WorkflowConfigs: map[string]*actions_model.ScopedWorkflowConfig{"required.yaml": {Required: true}},
			}))
			var err error
			switch operation {
			case "switch":
				err = SetRepoDefaultBranch(t.Context(), repo, "branch2")
			case "rename":
				doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
				_, err = RenameBranch(t.Context(), repo, doer, repo.DefaultBranch, "renamed-required-source")
			case "stale settings":
				repo.DefaultBranch = "branch2"
				err = UpdateRepository(t.Context(), repo, false)
			}
			require.ErrorIs(t, err, ErrRequiredWorkflowDefaultBranch)
			fresh := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
			require.Equal(t, "master", fresh.DefaultBranch)
		})
	}
}

func TestDefaultBranchWaitsForRequiredSourceConfiguration(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	release, err := LockRepositoryWorking(t.Context(), repo.ID)
	require.NoError(t, err)
	defer release()
	started, result := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		result <- SetRepoDefaultBranch(t.Context(), repo, "branch2")
	}()
	<-started
	require.NoError(t, db.Insert(t.Context(), &actions_model.ActionScopedWorkflowSource{
		OwnerID: 0, SourceRepoID: repo.ID,
		WorkflowConfigs: map[string]*actions_model.ScopedWorkflowConfig{"instance.yaml": {Required: true}},
	}))
	release()
	require.ErrorIs(t, <-result, ErrRequiredWorkflowDefaultBranch)
	unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID, DefaultBranch: "master"})
}

func TestDefaultBranchAllowsOptionalSource(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	q, err := queue.NewWorkerPoolQueueWithContext(t.Context(), "optional-default-branch-test", setting.QueueSettings{Type: "dummy"}, func(...*LicenseUpdaterOptions) []*LicenseUpdaterOptions { return nil }, false)
	require.NoError(t, err)
	defer test.MockVariableValue(&licenseUpdaterQueue, q)()
	t.Cleanup(func() {
		require.NoError(t, gitrepo.SetDefaultBranch(context.WithoutCancel(t.Context()), repo, "master"))
	})
	require.NoError(t, db.Insert(t.Context(), &actions_model.ActionScopedWorkflowSource{
		OwnerID: repo.OwnerID, SourceRepoID: repo.ID,
		WorkflowConfigs: map[string]*actions_model.ScopedWorkflowConfig{"optional.yaml": {Required: false}},
	}))
	require.NoError(t, SetRepoDefaultBranch(t.Context(), repo, "branch2"))
	fresh := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	require.Equal(t, "branch2", fresh.DefaultBranch)
	head, err := gitrepo.GetDefaultBranch(t.Context(), fresh)
	require.NoError(t, err)
	require.Equal(t, fresh.DefaultBranch, head)
}
