// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	actions_model "gitea.dev/models/actions"
	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/timeutil"
	actions_service "gitea.dev/services/actions"

	"github.com/stretchr/testify/require"
)

func TestScopedScheduleWithoutConsumerWorkflow(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
		sourceAPI := createActionsTestRepo(t, token, "scoped-cron-source", false)
		source := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: sourceAPI.ID})
		createRepoWorkflowFile(t, user, token, source, ".gitea/scoped_workflows/ancestor.yaml", `name: Scoped Cron
on:
  schedule:
    - cron: '* * * * *'
jobs:
  job:
    runs-on: ubuntu-latest
    steps:
      - run: echo scoped
`)
		require.NoError(t, actions_model.AddScopedWorkflowSource(t.Context(), user.ID, source.ID))
		consumerAPI := createActionsTestRepo(t, token, "scoped-cron-consumer", false)
		consumer := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: consumerAPI.ID})
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		plan := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{RepoID: consumer.ID, WorkflowID: "ancestor.yaml", IsScopedRun: true})
		require.Equal(t, source.ID, plan.WorkflowRepoID)
		require.NotEmpty(t, plan.WorkflowCommitSHA)
		spec := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{RepoID: consumer.ID, ScheduleID: plan.ID})
		spec.Next = timeutil.TimeStampNow()
		require.NoError(t, actions_model.UpdateScheduleSpec(t.Context(), spec, "next"))
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: consumer.ID, ScheduleID: plan.ID, IsScopedRun: true})
		require.Equal(t, source.ID, run.WorkflowRepoID)
		require.Equal(t, consumer.ID, run.RepoID)
		require.Equal(t, "schedule", run.TriggerEvent)
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		require.Equal(t, 1, unittest.GetCount(t, &actions_model.ActionRun{RepoID: consumer.ID}))
		if setting.Database.Type.IsPostgreSQL() {
			spec = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{ID: spec.ID})
			spec.Next = timeutil.TimeStampNow()
			require.NoError(t, actions_model.UpdateScheduleSpec(t.Context(), spec, "next"))
			spec.Prev, spec.Next = spec.Next, spec.Next+60
			spec.Repo, spec.Schedule = consumer, plan
			start := make(chan struct{})
			results := make(chan error, 2)
			var ready sync.WaitGroup
			ready.Add(2)
			for range 2 {
				specCopy := *spec
				go func() {
					ready.Done()
					<-start
					results <- actions_service.CreateScheduleTask(t.Context(), &specCopy)
				}()
			}
			ready.Wait()
			close(start)
			first, second := <-results, <-results
			if first == nil {
				require.ErrorIs(t, second, governance_model.ErrConflict, "one claim must win and one must fail: %v, %v", first, second)
			} else {
				require.NoError(t, second, "one claim must win and one must fail: %v, %v", first, second)
				require.ErrorIs(t, first, governance_model.ErrConflict, "one claim must win and one must fail: %v, %v", first, second)
			}
			require.Equal(t, 2, unittest.GetCount(t, &actions_model.ActionRun{RepoID: consumer.ID}))
		}
		beforeRevocation := unittest.GetCount(t, &actions_model.ActionRun{RepoID: consumer.ID})
		valid, err := actions_model.ScheduledRunValid(t.Context(), run, consumer)
		require.NoError(t, err)
		require.True(t, valid)
		require.NoError(t, actions_model.RemoveScopedWorkflowSource(t.Context(), user.ID, source.ID))
		valid, err = actions_model.ScheduledRunValid(t.Context(), run, consumer)
		require.NoError(t, err)
		require.False(t, valid)
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		unittest.AssertNotExistsBean(t, &actions_model.ActionSchedule{ID: plan.ID})
		require.Equal(t, beforeRevocation, unittest.GetCount(t, &actions_model.ActionRun{RepoID: consumer.ID}))

		require.NoError(t, actions_model.AddScopedWorkflowSource(t.Context(), user.ID, source.ID))
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		newPlan := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{RepoID: consumer.ID, WorkflowID: "ancestor.yaml", IsScopedRun: true})
		require.NotEqual(t, plan.ID, newPlan.ID)
		valid, err = actions_model.ScheduledRunValid(t.Context(), run, consumer)
		require.NoError(t, err)
		require.False(t, valid)
		newSpec := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{RepoID: consumer.ID, ScheduleID: newPlan.ID})
		newSpec.Next = timeutil.TimeStampNow()
		require.NoError(t, actions_model.UpdateScheduleSpec(t.Context(), newSpec, "next"))
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: consumer.ID, ScheduleID: newPlan.ID, IsScopedRun: true})
		require.Equal(t, beforeRevocation+1, unittest.GetCount(t, &actions_model.ActionRun{RepoID: consumer.ID}))
	})
}

func TestScopedScheduleSourceVersionCancelsOldRun(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
		sourceAPI := createActionsTestRepo(t, token, "scoped-cron-version-source", false)
		source := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: sourceAPI.ID})
		createRepoWorkflowFile(t, user, token, source, ".gitea/scoped_workflows/ancestor.yaml", `on:
  schedule:
    - cron: '* * * * *'
jobs:
  job:
    runs-on: ubuntu-latest
    steps:
      - run: echo old
`)
		require.NoError(t, actions_model.AddScopedWorkflowSource(t.Context(), user.ID, source.ID))
		consumerAPI := createActionsTestRepo(t, token, "scoped-cron-version-consumer", false)
		consumer := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: consumerAPI.ID})
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		oldPlan := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{RepoID: consumer.ID, WorkflowID: "ancestor.yaml", IsScopedRun: true})
		oldSpec := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{RepoID: consumer.ID, ScheduleID: oldPlan.ID})
		oldSpec.Next = timeutil.TimeStampNow()
		require.NoError(t, actions_model.UpdateScheduleSpec(t.Context(), oldSpec, "next"))
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		oldRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: consumer.ID, ScheduleID: oldPlan.ID, IsScopedRun: true})
		oldJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: oldRun.ID})
		require.Equal(t, actions_model.StatusWaiting, oldRun.Status)
		require.Equal(t, actions_model.StatusWaiting, oldJob.Status)
		// A previous plan was deleted before this scan, leaving its scheduled run queued.
		const deletedPlanID = 99000
		unittest.AssertNotExistsBean(t, &actions_model.ActionSchedule{ID: deletedPlanID})
		oldBranchRun := &actions_model.ActionRun{RepoID: consumer.ID, OwnerID: consumer.OwnerID, WorkflowID: "old-default.yaml", Index: 99001, ScheduleID: deletedPlanID, Ref: "refs/heads/former-default", TriggerEvent: "schedule", Status: actions_model.StatusWaiting}
		require.NoError(t, db.Insert(t.Context(), oldBranchRun))
		oldBranchJob := &actions_model.ActionRunJob{RunID: oldBranchRun.ID, RepoID: consumer.ID, OwnerID: consumer.OwnerID, JobID: "old-default", Status: actions_model.StatusWaiting}
		require.NoError(t, db.Insert(t.Context(), oldBranchJob))
		pushRun := &actions_model.ActionRun{RepoID: consumer.ID, OwnerID: consumer.OwnerID, WorkflowID: "push.yaml", Index: 99002, Ref: "refs/heads/main", TriggerEvent: "push", Status: actions_model.StatusWaiting}
		require.NoError(t, db.Insert(t.Context(), pushRun))
		pushJob := &actions_model.ActionRunJob{RunID: pushRun.ID, RepoID: consumer.ID, OwnerID: consumer.OwnerID, JobID: "push", Status: actions_model.StatusWaiting}
		require.NoError(t, db.Insert(t.Context(), pushJob))

		createRepoWorkflowFile(t, user, token, source, "version.txt", "source version changed")
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		unittest.AssertNotExistsBean(t, &actions_model.ActionSchedule{ID: oldPlan.ID})
		require.Equal(t, actions_model.StatusCancelled, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: oldRun.ID}).Status)
		require.Equal(t, actions_model.StatusCancelled, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: oldJob.ID}).Status)
		require.Equal(t, actions_model.StatusCancelled, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: oldBranchRun.ID}).Status)
		require.Equal(t, actions_model.StatusCancelled, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: oldBranchJob.ID}).Status)
		require.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: pushRun.ID}).Status)
		require.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: pushJob.ID}).Status)
		newPlan := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{RepoID: consumer.ID, WorkflowID: "ancestor.yaml", IsScopedRun: true})
		require.NotEqual(t, oldPlan.ID, newPlan.ID)
		require.NotEqual(t, oldPlan.WorkflowCommitSHA, newPlan.WorkflowCommitSHA)
		newSpec := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{RepoID: consumer.ID, ScheduleID: newPlan.ID})
		newSpec.Next = timeutil.TimeStampNow()
		require.NoError(t, actions_model.UpdateScheduleSpec(t.Context(), newSpec, "next"))
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		newRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: consumer.ID, ScheduleID: newPlan.ID, IsScopedRun: true})
		require.Equal(t, actions_model.StatusWaiting, newRun.Status)
	})
}

func TestScopedScheduleReplacementPreservesOtherPlans(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
		sourceAAPI := createActionsTestRepo(t, token, "scoped-cron-partial-a", false)
		sourceA := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: sourceAAPI.ID})
		createRepoWorkflowFile(t, user, token, sourceA, ".gitea/scoped_workflows/a.yaml", `on:
  schedule:
    - cron: '* * * * *'
jobs:
  job:
    runs-on: ubuntu-latest
    steps:
      - run: echo a
`)
		require.NoError(t, actions_model.AddScopedWorkflowSource(t.Context(), user.ID, sourceA.ID))
		sourceBAPI := createActionsTestRepo(t, token, "scoped-cron-partial-b", false)
		sourceB := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: sourceBAPI.ID})
		createRepoWorkflowFile(t, user, token, sourceB, ".gitea/scoped_workflows/b.yaml", `on:
  schedule:
    - cron: '* * * * *'
jobs:
  job:
    runs-on: ubuntu-latest
    steps:
      - run: echo b
`)
		require.NoError(t, actions_model.AddScopedWorkflowSource(t.Context(), user.ID, sourceB.ID))
		consumerAPI := createActionsTestRepo(t, token, "scoped-cron-partial-consumer", false)
		consumer := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: consumerAPI.ID})
		const nativePath = ".gitea/workflows/native.yaml"
		nativeFile := createWorkflowFile(t, token, consumer.OwnerName, consumer.Name, nativePath,
			getWorkflowCreateFileOptions(user, consumer.DefaultBranch, "create native cron", `on:
  schedule:
    - cron: '* * * * *'
jobs:
  job:
    runs-on: ubuntu-latest
    steps:
      - run: echo native
`))
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		planA := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{RepoID: consumer.ID, WorkflowRepoID: sourceA.ID, WorkflowID: "a.yaml", IsScopedRun: true})
		planB := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{RepoID: consumer.ID, WorkflowRepoID: sourceB.ID, WorkflowID: "b.yaml", IsScopedRun: true})
		nativePlan := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{RepoID: consumer.ID, WorkflowID: "native.yaml", IsScopedRun: false})
		for _, plan := range []*actions_model.ActionSchedule{planA, planB, nativePlan} {
			spec := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{RepoID: consumer.ID, ScheduleID: plan.ID})
			spec.Next = timeutil.TimeStampNow()
			require.NoError(t, actions_model.UpdateScheduleSpec(t.Context(), spec, "next"))
		}
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		runA := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: consumer.ID, ScheduleID: planA.ID})
		runB := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: consumer.ID, ScheduleID: planB.ID})
		nativeRun := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: consumer.ID, ScheduleID: nativePlan.ID})
		jobA := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: runA.ID})
		jobB := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: runB.ID})
		nativeJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: nativeRun.ID})
		specB := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{ScheduleID: planB.ID})
		nativeSpec := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{ScheduleID: nativePlan.ID})
		specB.Next = timeutil.TimeStampNow() + 3600
		nativeSpec.Next = specB.Next
		require.NoError(t, actions_model.UpdateScheduleSpec(t.Context(), specB, "next"))
		require.NoError(t, actions_model.UpdateScheduleSpec(t.Context(), nativeSpec, "next"))
		require.Equal(t, actions_model.StatusWaiting, runA.Status)
		require.Equal(t, actions_model.StatusWaiting, runB.Status)
		require.Equal(t, actions_model.StatusWaiting, nativeRun.Status)

		createRepoWorkflowFile(t, user, token, sourceA, "version.txt", "source a changed")
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		unittest.AssertNotExistsBean(t, &actions_model.ActionSchedule{ID: planA.ID})
		require.Equal(t, actions_model.StatusCancelled, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runA.ID}).Status)
		require.Equal(t, actions_model.StatusCancelled, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: jobA.ID}).Status)
		require.Equal(t, planB.ID, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{RepoID: consumer.ID, WorkflowRepoID: sourceB.ID, WorkflowID: "b.yaml", IsScopedRun: true}).ID)
		require.Equal(t, nativePlan.ID, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{RepoID: consumer.ID, WorkflowID: "native.yaml", IsScopedRun: false}).ID)
		require.Equal(t, specB.Next, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{ID: specB.ID}).Next)
		require.Equal(t, nativeSpec.Next, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{ID: nativeSpec.ID}).Next)
		require.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: runB.ID}).Status)
		require.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: jobB.ID}).Status)
		require.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: nativeRun.ID}).Status)
		require.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: nativeJob.ID}).Status)

		newPlanA := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{RepoID: consumer.ID, WorkflowRepoID: sourceA.ID, WorkflowID: "a.yaml", IsScopedRun: true})
		require.NotEqual(t, planA.ID, newPlanA.ID)
		newSpecA := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{ScheduleID: newPlanA.ID})
		newSpecA.Next = timeutil.TimeStampNow()
		require.NoError(t, actions_model.UpdateScheduleSpec(t.Context(), newSpecA, "next"))
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		newRunA := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: consumer.ID, ScheduleID: newPlanA.ID})
		require.NoError(t, actions_model.RemoveScopedWorkflowSource(t.Context(), user.ID, sourceA.ID))
		require.NoError(t, actions_model.RemoveScopedWorkflowSource(t.Context(), user.ID, sourceB.ID))
		deleteNative := NewRequestWithJSON(t, "DELETE", "/api/v1/repos/"+consumer.OwnerName+"/"+consumer.Name+"/contents/"+nativePath,
			&api.DeleteFileOptions{SHA: nativeFile.Content.SHA, FileOptions: api.FileOptions{
				BranchName: consumer.DefaultBranch, Message: "delete native cron",
				Author: api.Identity{Name: user.Name, Email: user.Email}, Committer: api.Identity{Name: user.Name, Email: user.Email},
			}}).AddTokenAuth(token)
		MakeRequest(t, deleteNative, http.StatusOK)
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		unittest.AssertNotExistsBean(t, &actions_model.ActionSchedule{RepoID: consumer.ID})
		for _, run := range []*actions_model.ActionRun{runB, nativeRun, newRunA} {
			require.Equal(t, actions_model.StatusCancelled, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: run.ID}).Status)
		}
	})
}

func TestScopedScheduleSourceFailureDoesNotBlockPush(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
		sourceAPI := createActionsTestRepo(t, token, "scoped-cron-broken-source", false)
		source := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: sourceAPI.ID})
		createRepoWorkflowFile(t, user, token, source, ".gitea/scoped_workflows/ancestor.yaml", `on:
  schedule:
    - cron: '* * * * *'
jobs:
  job:
    runs-on: ubuntu-latest
    steps:
      - run: echo scoped
`)
		require.NoError(t, actions_model.AddScopedWorkflowSource(t.Context(), user.ID, source.ID))
		consumerAPI := createActionsTestRepo(t, token, "scoped-cron-push-consumer", false)
		consumer := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: consumerAPI.ID})
		createRepoWorkflowFile(t, user, token, consumer, ".gitea/workflows/push.yaml", `on:
  push:
  schedule:
    - cron: '* * * * *'
jobs:
  job:
    runs-on: ubuntu-latest
    steps:
      - run: echo local
`)
		branch, err := git_model.GetBranch(t.Context(), source.ID, source.DefaultBranch)
		require.NoError(t, err)
		_, err = db.GetEngine(t.Context()).ID(branch.ID).Cols("commit_id").Update(&git_model.Branch{CommitID: strings.Repeat("a", 40)})
		require.NoError(t, err)
		before := unittest.GetCount(t, &actions_model.ActionRun{RepoID: consumer.ID, WorkflowID: "push.yaml"})
		createRepoWorkflowFile(t, user, token, consumer, "trigger.txt", "trigger local push")
		require.Equal(t, before+1, unittest.GetCount(t, &actions_model.ActionRun{RepoID: consumer.ID, WorkflowID: "push.yaml"}))
		consumerBranch, err := git_model.GetBranch(t.Context(), consumer.ID, consumer.DefaultBranch)
		require.NoError(t, err)
		plan := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{RepoID: consumer.ID, WorkflowID: "push.yaml", IsScopedRun: false})
		require.Equal(t, consumerBranch.CommitID, plan.CommitSHA)
		spec := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{RepoID: consumer.ID, ScheduleID: plan.ID})
		spec.Next = timeutil.TimeStampNow()
		require.NoError(t, actions_model.UpdateScheduleSpec(t.Context(), spec, "next"))
		require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
		unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: consumer.ID, ScheduleID: plan.ID, TriggerEvent: "schedule"})
	})
}
