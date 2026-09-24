// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	actions_service "gitea.dev/services/actions"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
	"xorm.io/xorm/contexts"
)

type actionsRunInsertHook struct {
	once     sync.Once
	inserted chan struct{}
	release  chan struct{}
}

func (*actionsRunInsertHook) BeforeProcess(c *contexts.ContextHook) (context.Context, error) {
	return c.Ctx, nil
}

func (h *actionsRunInsertHook) AfterProcess(c *contexts.ContextHook) error {
	if strings.HasPrefix(strings.ToUpper(c.SQL), "INSERT") && strings.Contains(c.SQL, "action_run_job") {
		h.once.Do(func() {
			close(h.inserted)
			select {
			case <-h.release:
			case <-c.Ctx.Done():
			}
		})
	}
	return nil
}

func TestGroupMoveInvalidatesActionsTrust(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	creator := governance_model.Actor{ID: 1, Kind: "user", Name: "user1", Transport: "api"}
	formerOwner := governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, creator, governance_service.GroupOption{Path: "pg-actions-move", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	oldToken, err := actions_service.GetRunnerRegistrationToken(ctx, formerOwner, group.ID, 0, false)
	require.NoError(t, err)
	repo := &repo_model.Repository{OwnerID: group.ID, OwnerName: group.InternalName, Name: "workflow", LowerName: "workflow", IsPrivate: true, Status: repo_model.RepositoryReady}
	require.NoError(t, db.Insert(ctx, repo))
	repo.OwnerNamespace, err = governance_model.RegisterNativeRepository(ctx, repo.ID, group.ID, repo.Name)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(repo.ID).Cols("owner_namespace").Update(repo)
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	history := &actions_model.ActionRun{RepoID: repo.ID, OwnerID: group.ID, WorkflowID: "old.yaml", Index: 9930, TriggerUserID: 1, Status: actions_model.StatusSuccess}
	require.NoError(t, db.Insert(ctx, history))
	queued := &actions_model.ActionRun{RepoID: repo.ID, OwnerID: group.ID, WorkflowID: "queued.yaml", Index: 9931, TriggerUserID: 1, Status: actions_model.StatusWaiting}
	require.NoError(t, db.Insert(ctx, queued))
	oldJob := &actions_model.ActionRunJob{RunID: queued.ID, RepoID: repo.ID, OwnerID: group.ID, JobID: "old-job", Status: actions_model.StatusWaiting}
	require.NoError(t, db.Insert(ctx, oldJob))
	_, err = governance_service.MoveGroup(ctx, formerOwner, group.ID, governance_service.GroupOption{Path: "pg-actions-move", Revision: group.Revision})
	require.NoError(t, err)
	require.False(t, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunnerToken{ID: oldToken.ID}).IsActive)
	require.True(t, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: history.ID}).ScopeInvalidated)
	require.Equal(t, actions_model.StatusCancelled, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: oldJob.ID}).Status)
	oldRunner := &actions_model.ActionRunner{Name: "old-root-runner", OwnerID: group.ID, AgentLabels: []string{"ubuntu-latest"}}
	oldRunner.GenerateAndFillToken()
	require.Error(t, actions_model.RegisterRunnerWithToken(ctx, oldRunner, oldToken.ID))
	newToken, err := actions_service.GetRunnerRegistrationToken(ctx, creator, group.ID, 0, false)
	require.NoError(t, err)
	newRunner := &actions_model.ActionRunner{Name: "new-root-runner", OwnerID: group.ID, AgentLabels: []string{"ubuntu-latest"}}
	newRunner.GenerateAndFillToken()
	require.NoError(t, actions_model.RegisterRunnerWithToken(ctx, newRunner, newToken.ID))
	newRun := &actions_model.ActionRun{RepoID: repo.ID, OwnerID: group.ID, WorkflowID: "new.yaml", Index: 9932, TriggerUserID: 1, Status: actions_model.StatusWaiting}
	require.NoError(t, db.Insert(ctx, newRun))
	newJob := &actions_model.ActionRunJob{RunID: newRun.ID, RepoID: repo.ID, OwnerID: group.ID, JobID: "new-job", Attempt: 1, Status: actions_model.StatusWaiting, RunsOn: []string{"ubuntu-latest"}, WorkflowPayload: minimalConcurrentWorkflowPayload("new-job")}
	require.NoError(t, db.Insert(ctx, newJob))
	task, claimed, err := actions_model.CreateTaskForRunner(ctx, newRunner)
	require.NoError(t, err)
	require.True(t, claimed)
	require.Equal(t, newJob.ID, task.JobID)
}

func TestGroupMoveOrdersConcurrentRunCreation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	creator := governance_model.Actor{ID: 1, Kind: "user", Name: "user1", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, creator, governance_service.GroupOption{Path: "pg-concurrent-move", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	repo := &repo_model.Repository{OwnerID: group.ID, OwnerName: group.InternalName, Name: "workflow", LowerName: "workflow", IsPrivate: true, Status: repo_model.RepositoryReady}
	require.NoError(t, db.Insert(ctx, repo))
	repo.OwnerNamespace, err = governance_model.RegisterNativeRepository(ctx, repo.ID, group.ID, repo.Name)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(repo.ID).Cols("owner_namespace").Update(repo)
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	preparedRepo, err := repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	run := &actions_model.ActionRun{RepoID: repo.ID, Repo: preparedRepo, OwnerID: group.ID, WorkflowID: "before-move.yaml", TriggerUserID: 1, Ref: "refs/heads/main", CommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0", Event: "push", TriggerEvent: "push", WorkflowRepoID: repo.ID, WorkflowCommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0"}
	hook := &actionsRunInsertHook{inserted: make(chan struct{}), release: make(chan struct{})}
	unittest.GetXORMEngine().AddHook(hook)
	insertDone := make(chan error, 1)
	go func() {
		insertDone <- actions_service.PrepareRunAndInsert(ctx, []byte("on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"), run, nil)
	}()
	select {
	case <-hook.inserted:
	case <-ctx.Done():
		close(hook.release)
		t.Fatal(ctx.Err())
	}
	formerOwner := governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "api"}
	_, moveErr := governance_service.MoveGroup(ctx, formerOwner, group.ID, governance_service.GroupOption{Path: "pg-concurrent-move", Revision: group.Revision})
	close(hook.release)
	require.NoError(t, moveErr)
	select {
	case insertErr := <-insertDone:
		require.ErrorIs(t, insertErr, governance_model.ErrConflict)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	has, err := db.GetEngine(ctx).ID(run.ID).Exist(new(actions_model.ActionRun))
	require.NoError(t, err)
	require.False(t, has)
}

func TestScopedSourceArchiveOrdersConcurrentRunCreation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	source, err := repo_model.GetRepositoryByID(ctx, 1)
	require.NoError(t, err)
	consumer, err := repo_model.GetRepositoryByID(ctx, 2)
	require.NoError(t, err)
	require.NoError(t, actions_model.AddScopedWorkflowSource(ctx, consumer.OwnerID, source.ID))
	run := &actions_model.ActionRun{
		RepoID: consumer.ID, Repo: consumer, OwnerID: consumer.OwnerID,
		WorkflowRepoID: source.ID, WorkflowSourceScopeRevision: source.ActionsScopeRevision, IsScopedRun: true,
		WorkflowID: "source.yaml", TriggerUserID: 2, Ref: "refs/heads/master", Event: "push", TriggerEvent: "push",
		CommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0", EventPayload: "{}",
	}
	hook := &actionsRunInsertHook{inserted: make(chan struct{}), release: make(chan struct{})}
	unittest.GetXORMEngine().AddHook(hook)
	insertDone := make(chan error, 1)
	go func() {
		insertDone <- actions_service.PrepareRunAndInsert(ctx, []byte("on: push\njobs:\n  check:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo ok\n"), run, nil)
	}()
	select {
	case <-hook.inserted:
	case <-ctx.Done():
		close(hook.release)
		t.Fatal(ctx.Err())
	}
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	archiveErr := repo_model.SetArchiveRepoState(governance_model.WithAuditActor(ctx, actor), source, true)
	close(hook.release)
	require.NoError(t, archiveErr)
	select {
	case insertErr := <-insertDone:
		require.ErrorIs(t, insertErr, governance_model.ErrConflict)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	has, err := db.GetEngine(ctx).ID(run.ID).Exist(new(actions_model.ActionRun))
	require.NoError(t, err)
	require.False(t, has)
	unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScopedWorkflowSource{OwnerID: consumer.OwnerID, SourceRepoID: source.ID})
}

func TestGroupMoveOrdersConcurrentScheduleRunCreation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	creator := governance_model.Actor{ID: 1, Kind: "user", Name: "user1", Transport: "api"}
	group, err := governance_service.CreateGroup(ctx, creator, governance_service.GroupOption{Path: "pg-concurrent-schedule", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	repo := &repo_model.Repository{OwnerID: group.ID, OwnerName: group.InternalName, Name: "workflow", LowerName: "workflow", IsPrivate: true, Status: repo_model.RepositoryReady}
	require.NoError(t, db.Insert(ctx, repo))
	repo.OwnerNamespace, err = governance_model.RegisterNativeRepository(ctx, repo.ID, group.ID, repo.Name)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(repo.ID).Cols("owner_namespace").Update(repo)
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	preparedRepo, err := repo_model.GetRepositoryByID(ctx, repo.ID)
	require.NoError(t, err)
	schedule := &actions_model.ActionSchedule{RepoID: repo.ID, OwnerID: group.ID, ScopeRevision: preparedRepo.ActionsScopeRevision, TriggerUserID: user_model.ActionsUserID, WorkflowID: "scheduled.yaml"}
	require.NoError(t, db.Insert(ctx, schedule))
	run := &actions_model.ActionRun{RepoID: repo.ID, Repo: preparedRepo, OwnerID: group.ID, WorkflowID: "scheduled.yaml", TriggerUserID: user_model.ActionsUserID, ScheduleID: schedule.ID, Ref: "refs/heads/main", CommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0", Event: "schedule", TriggerEvent: "schedule", WorkflowRepoID: repo.ID, WorkflowCommitSHA: "c2d72f548424103f01ee1dc02889c1e2bff816b0"}
	hook := &actionsRunInsertHook{inserted: make(chan struct{}), release: make(chan struct{})}
	unittest.GetXORMEngine().AddHook(hook)
	insertDone := make(chan error, 1)
	go func() {
		insertDone <- actions_service.PrepareRunAndInsert(ctx, []byte("on: schedule\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"), run, nil)
	}()
	select {
	case <-hook.inserted:
	case <-ctx.Done():
		close(hook.release)
		t.Fatal(ctx.Err())
	}
	_, moveErr := governance_service.MoveGroup(ctx, governance_model.Actor{ID: 2, Kind: "user", Name: "user2", Transport: "api"}, group.ID, governance_service.GroupOption{Path: "pg-concurrent-schedule", Revision: group.Revision})
	close(hook.release)
	require.NoError(t, moveErr)
	select {
	case insertErr := <-insertDone:
		require.ErrorIs(t, insertErr, governance_model.ErrConflict)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	has, err := db.GetEngine(ctx).ID(run.ID).Exist(new(actions_model.ActionRun))
	require.NoError(t, err)
	require.False(t, has)
	unittest.AssertNotExistsBean(t, &actions_model.ActionSchedule{ID: schedule.ID})
}
