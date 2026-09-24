// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	secret_model "gitea.dev/models/secret"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	repo_service "gitea.dev/services/repository"
	secret_service "gitea.dev/services/secrets"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm/contexts"
	"xorm.io/xorm/schemas"
)

type claimScanContextKey struct{}

type markedClaimScanHook struct {
	once          sync.Once
	scanned       chan struct{}
	continueClaim chan struct{}
}

func (*markedClaimScanHook) BeforeProcess(c *contexts.ContextHook) (context.Context, error) {
	return c.Ctx, nil
}

func (h *markedClaimScanHook) AfterProcess(c *contexts.ContextHook) error {
	if c.Ctx.Value(claimScanContextKey{}) == true && strings.HasPrefix(strings.ToUpper(c.SQL), "SELECT") && strings.Contains(c.SQL, "action_run_job") {
		h.once.Do(func() {
			close(h.scanned)
			<-h.continueClaim
		})
	}
	return nil
}

// Fixed barrier: the claim sees an old candidate while a formal lifecycle write
// is uncommitted, then must recheck current authorization before disclosure.
func TestActionsClaimAfterFormalLifecycleMutation(t *testing.T) {
	if os.Getenv("GITEA_TEST_DATABASE") != "pgsql" {
		t.Skip("requires the real PostgreSQL integration engine")
	}
	for _, mutation := range []string{"transfer", "runner-disable", "actions-disable", "archive"} {
		t.Run(mutation, func(t *testing.T) {
			defer tests.PrepareTestEnv(t)()
			require.True(t, setting.Database.Type.IsPostgreSQL())
			require.Equal(t, schemas.POSTGRES, unittest.GetXORMEngine().Dialect().URI().DBType)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
			repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
			require.NoError(t, repo.LoadOwner(ctx))
			require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
			source := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
			target := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
			if mutation == "transfer" {
				require.NoError(t, repo_service.StartRepositoryTransfer(ctx, source, target, repo, nil))
			}
			_, created, err := secret_service.CreateOrUpdateSecret(governance_model.WithAuditActor(ctx, governance_model.Actor{ID: source.ID, Name: source.Name, Kind: "user", Transport: "api"}), source.ID, 0, "SOURCE_SCOPE", "old-only", "")
			require.NoError(t, err)
			require.True(t, created)
			_, created, err = secret_service.CreateOrUpdateSecret(governance_model.WithAuditActor(ctx, governance_model.Actor{ID: target.ID, Name: target.Name, Kind: "user", Transport: "api"}), target.ID, 0, "TARGET_SCOPE", "new-only", "")
			require.NoError(t, err)
			require.True(t, created)

			run := &actions_model.ActionRun{
				RepoID: repo.ID, OwnerID: source.ID, WorkflowID: "claim.yaml", Index: 9915,
				TriggerUserID: source.ID, Ref: "refs/heads/master", CommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0",
				Event: "push", TriggerEvent: "push", Status: actions_model.StatusWaiting,
			}
			require.NoError(t, db.Insert(ctx, run))
			job := &actions_model.ActionRunJob{
				RunID: run.ID, RepoID: repo.ID, OwnerID: source.ID,
				JobID: "old-scope", Attempt: 1, Status: actions_model.StatusWaiting,
				RunsOn: []string{"ubuntu-latest"}, WorkflowPayload: minimalConcurrentWorkflowPayload("old-scope"),
			}
			require.NoError(t, db.Insert(ctx, job))
			runner := &actions_model.ActionRunner{
				UUID: "phase1-claim-" + mutation, Name: "claim-" + mutation,
				OwnerID: source.ID, AgentLabels: []string{"ubuntu-latest"},
			}
			runner.GenerateAndFillToken()
			require.NoError(t, db.Insert(ctx, runner))

			hook := &markedClaimScanHook{scanned: make(chan struct{}), continueClaim: make(chan struct{})}
			unittest.GetXORMEngine().AddHook(hook)
			writerReady := make(chan struct{})
			releaseWriter := make(chan struct{})
			defer func() {
				select {
				case <-releaseWriter:
				default:
					close(releaseWriter)
				}
			}()
			defer func() {
				select {
				case <-hook.continueClaim:
				default:
					close(hook.continueClaim)
				}
			}()
			writerDone := make(chan error, 1)
			go func() {
				writerDone <- db.WithTx(ctx, func(ctx context.Context) error {
					var err error
					switch mutation {
					case "transfer":
						err = repo_service.AcceptTransferOwnership(ctx, repo, target)
					case "runner-disable":
						freshRunner := *runner
						err = actions_model.SetRunnerDisabled(ctx, &freshRunner, true)
					case "actions-disable":
						err = repo_service.UpdateRepositoryUnits(ctx, repo, nil, []unit.Type{unit.TypeActions})
					case "archive":
						err = repo_model.SetArchiveRepoState(ctx, repo, true)
					}
					if err != nil {
						return err
					}
					close(writerReady)
					select {
					case <-releaseWriter:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}()
			select {
			case <-writerReady:
			case err := <-writerDone:
				require.NoError(t, err)
				t.Fatal("lifecycle write did not reach the barrier")
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			visible := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID})
			require.Equal(t, actions_model.StatusWaiting, visible.Status, "候选读取时旧作业仍可见")
			type claimResult struct {
				task      *actions_model.ActionTask
				ok        bool
				secrets   map[string]string
				buildSeen bool
				err       error
			}
			claimed := make(chan claimResult, 1)
			claimCtx := context.WithValue(ctx, claimScanContextKey{}, true)
			go func() {
				result := claimResult{}
				result.task, result.ok, result.err = actions_model.CreateTaskForRunnerWithPayload(claimCtx, runner, func(ctx context.Context, task *actions_model.ActionTask) error {
					result.buildSeen = true
					result.secrets, result.err = secret_model.GetSecretsOfTask(ctx, task)
					return result.err
				})
				claimed <- result
			}()
			select {
			case <-hook.scanned:
			case result := <-claimed:
				require.NoError(t, result.err)
				t.Fatal("claim returned before scanning a candidate")
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			close(releaseWriter)
			require.NoError(t, <-writerDone)
			close(hook.continueClaim)
			result := <-claimed
			require.NoError(t, result.err)
			require.False(t, result.ok)
			require.Nil(t, result.task)
			require.False(t, result.buildSeen, "old run must not reach secret disclosure")
			require.NotContains(t, result.secrets, "TARGET_SCOPE")
			require.NotContains(t, result.secrets, "SOURCE_SCOPE")
			require.Zero(t, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID}).TaskID)
			if mutation == "transfer" {
				current := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
				require.Equal(t, target.ID, current.OwnerID)
				require.True(t, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: run.ID}).ScopeInvalidated)
			}

			current := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
			switch mutation {
			case "actions-disable":
				require.NoError(t, repo_service.UpdateRepositoryUnits(ctx, current, []repo_model.RepoUnit{{RepoID: repo.ID, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}}, nil))
			case "archive":
				require.NoError(t, repo_model.SetArchiveRepoState(ctx, current, false))
			}
			current = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
			newRun := &actions_model.ActionRun{
				RepoID: repo.ID, OwnerID: current.OwnerID, WorkflowID: "current.yaml", Index: 9916,
				TriggerUserID: current.OwnerID, Ref: "refs/heads/master", CommitSHA: run.CommitSHA,
				Event: "push", TriggerEvent: "push", Status: actions_model.StatusWaiting,
			}
			require.NoError(t, db.Insert(ctx, newRun))
			newJob := &actions_model.ActionRunJob{
				RunID: newRun.ID, RepoID: repo.ID, OwnerID: current.OwnerID,
				JobID: "current-scope", Attempt: 1, Status: actions_model.StatusWaiting,
				RunsOn: []string{"current-scope"}, WorkflowPayload: []byte("on: push\njobs:\n  current-scope:\n    runs-on: current-scope\n"),
			}
			require.NoError(t, db.Insert(ctx, newJob))
			newRunner := &actions_model.ActionRunner{
				UUID: "phase1-current-" + mutation, Name: "current-" + mutation,
				OwnerID: current.OwnerID, AgentLabels: []string{"current-scope"},
			}
			newRunner.GenerateAndFillToken()
			require.NoError(t, db.Insert(ctx, newRunner))
			var currentSecrets map[string]string
			newTask, ok, err := actions_model.CreateTaskForRunnerWithPayload(ctx, newRunner, func(ctx context.Context, task *actions_model.ActionTask) error {
				var err error
				currentSecrets, err = secret_model.GetSecretsOfTask(ctx, task)
				return err
			})
			require.NoError(t, err)
			require.True(t, ok, "current scope must remain runnable")
			require.Equal(t, newJob.ID, newTask.JobID)
			if mutation == "transfer" {
				require.Equal(t, "new-only", currentSecrets["TARGET_SCOPE"])
				require.NotContains(t, currentSecrets, "SOURCE_SCOPE")
			} else {
				require.Equal(t, "old-only", currentSecrets["SOURCE_SCOPE"])
				require.NotContains(t, currentSecrets, "TARGET_SCOPE")
			}
		})
	}
}
