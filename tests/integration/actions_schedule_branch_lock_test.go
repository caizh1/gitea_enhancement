// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestScheduleBranchAuthorizationLock(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo, err := repo_model.GetRepositoryByID(ctx, 1)
	require.NoError(t, err)
	branch, err := git_model.GetBranch(ctx, repo.ID, repo.DefaultBranch)
	require.NoError(t, err)
	schedule := &actions_model.ActionSchedule{
		RepoID: repo.ID, OwnerID: repo.OwnerID, ScopeRevision: repo.ActionsScopeRevision,
		TriggerUserID: user_model.ActionsUserID, WorkflowID: "lock.yaml",
		Ref: "refs/heads/" + repo.DefaultBranch, CommitSHA: branch.CommitID,
	}
	require.NoError(t, db.Insert(ctx, schedule))
	run := &actions_model.ActionRun{
		RepoID: repo.ID, OwnerID: repo.OwnerID, ScheduleID: schedule.ID,
		WorkflowID: schedule.WorkflowID, Ref: schedule.Ref,
		CommitSHA: schedule.CommitSHA, WorkflowCommitSHA: schedule.CommitSHA,
	}
	check := func() bool {
		t.Helper()
		var valid bool
		require.NoError(t, governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
			var err error
			valid, err = actions_model.ScheduledRunValidForWrite(ctx, run, repo)
			return err
		}))
		return valid
	}
	require.True(t, check())
	if os.Getenv("GITEA_TEST_DATABASE") == "pgsql" {
		require.True(t, setting.Database.Type.IsPostgreSQL())
		waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		locked := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		releaseLock := func() { releaseOnce.Do(func() { close(release) }) }
		defer releaseLock()
		done := make(chan error, 1)
		go func() {
			done <- governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
				valid, err := actions_model.ScheduledRunValidForWrite(ctx, run, repo)
				if err != nil || !valid {
					return fmt.Errorf("claim-time schedule validation failed: %v", err)
				}
				close(locked)
				<-release
				return nil
			})
		}()
		select {
		case <-locked:
		case err := <-done:
			require.NoError(t, err)
			require.FailNow(t, "branch row lock was not acquired")
		case <-waitCtx.Done():
			require.FailNow(t, "timed out waiting for branch row lock")
		}
		err = db.WithTx(ctx, func(ctx context.Context) error {
			if _, err := db.GetEngine(ctx).Exec("SET LOCAL lock_timeout = '200ms'"); err != nil {
				return err
			}
			_, err := db.GetEngine(ctx).ID(branch.ID).Cols("commit_id").Update(&git_model.Branch{CommitID: strings.Repeat("a", 40)})
			return err
		})
		releaseLock()
		select {
		case heldErr := <-done:
			require.NoError(t, heldErr)
		case <-waitCtx.Done():
			require.FailNow(t, "timed out releasing branch row lock")
		}
		require.ErrorContains(t, err, "lock timeout")
	}
	_, err = db.GetEngine(ctx).ID(branch.ID).Cols("commit_id").Update(&git_model.Branch{CommitID: strings.Repeat("b", 40)})
	require.NoError(t, err)
	require.False(t, check(), "a new default-branch commit invalidates the old schedule")
	_, err = db.GetEngine(ctx).ID(branch.ID).Cols("commit_id", "is_deleted").Update(&git_model.Branch{CommitID: schedule.CommitSHA, IsDeleted: true})
	require.NoError(t, err)
	require.False(t, check(), "deleting the default branch invalidates the old schedule")
	_, err = db.GetEngine(ctx).ID(branch.ID).Delete(new(git_model.Branch))
	require.NoError(t, err)
	require.False(t, check(), "a missing branch cannot authorize a schedule")
}
