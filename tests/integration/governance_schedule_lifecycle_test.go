// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	actions_model "gitea.dev/models/actions"
	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"
	actions_service "gitea.dev/services/actions"
	governance_service "gitea.dev/services/governance"
	repo_service "gitea.dev/services/repository"

	"github.com/stretchr/testify/require"
)

func TestScheduleRunCancelledAcrossArchiveAndPendingDeletion(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		token := getUserToken(t, user.Name, auth_model.AccessTokenScopeAll)
		response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups",
			governance_service.GroupOption{Name: "定时生命周期父组", Path: "schedule-lifecycle-root", Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
		root := DecodeJSON(t, response, &governance_service.GroupState{})
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups",
			governance_service.GroupOption{Name: "子组", Path: "child", ParentID: root.ID, Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
		child := DecodeJSON(t, response, &governance_service.GroupState{})

		createWaitingRun := func(name string) (*repo_model.Repository, *actions_model.ActionRun, *actions_model.ActionRunJob) {
			response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/orgs/"+child.InternalName+"/repos",
				api.CreateRepoOption{Name: name, Private: true, AutoInit: true}).AddTokenAuth(token), http.StatusCreated)
			created := DecodeJSON(t, response, &api.Repository{})
			repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: created.ID})
			createRepoWorkflowFile(t, user, token, repo, ".gitea/workflows/schedule.yml", `on:
  schedule:
    - cron: '* * * * *'
jobs:
  job:
    runs-on: isolated-lifecycle-test
    steps:
      - run: echo lifecycle
`)
			require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
			plan := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionSchedule{RepoID: repo.ID, WorkflowID: "schedule.yml"})
			spec := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionScheduleSpec{RepoID: repo.ID, ScheduleID: plan.ID})
			spec.Next = timeutil.TimeStampNow()
			require.NoError(t, actions_model.UpdateScheduleSpec(t.Context(), spec, "next"))
			require.NoError(t, actions_service.StartScheduleTasks(t.Context()))
			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{RepoID: repo.ID, ScheduleID: plan.ID})
			job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID})
			require.Equal(t, "refs/heads/"+repo.DefaultBranch, run.Ref)
			require.Equal(t, actions_model.StatusWaiting, run.Status)
			require.Equal(t, actions_model.StatusWaiting, job.Status)
			return repo, run, job
		}
		assertCancelled := func(run *actions_model.ActionRun, job *actions_model.ActionRunJob) {
			require.Equal(t, actions_model.StatusCancelled, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: run.ID}).Status)
			require.Equal(t, actions_model.StatusCancelled, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID}).Status)
		}
		archiveEndpoint := fmt.Sprintf("/api/v1/governance/groups/%d/archive", root.ID)
		_, oldRun, oldJob := createWaitingRun("archive-job")
		response = MakeRequest(t, NewRequest(t, "GET", archiveEndpoint).AddTokenAuth(token), http.StatusOK)
		impact := DecodeJSON(t, response, &governance_service.GroupArchiveImpact{})
		response = MakeRequest(t, NewRequestWithJSON(t, "PUT", archiveEndpoint,
			governance_service.GroupArchiveOption{Archived: true, Revision: impact.Revision}).AddTokenAuth(token), http.StatusOK)
		root = DecodeJSON(t, response, &governance_service.GroupState{})
		assertCancelled(oldRun, oldJob)
		response = MakeRequest(t, NewRequestWithJSON(t, "PUT", archiveEndpoint,
			governance_service.GroupArchiveOption{Archived: false, Revision: root.Revision}).AddTokenAuth(token), http.StatusOK)
		root = DecodeJSON(t, response, &governance_service.GroupState{})
		assertCancelled(oldRun, oldJob)

		pendingRepo, pendingRun, pendingJob := createWaitingRun("pending-job")
		deletionEndpoint := fmt.Sprintf("/api/v1/governance/repositories/%d/deletion", pendingRepo.ID)
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", deletionEndpoint,
			repo_service.DeletionOption{ConfirmationPath: pendingRepo.FullPath()}).AddTokenAuth(token), http.StatusOK)
		deletion := DecodeJSON(t, response, &repo_service.DeletionState{})
		require.NotNil(t, deletion.Pending)
		assertCancelled(pendingRun, pendingJob)
		response = MakeRequest(t, NewRequestWithJSON(t, "PUT", archiveEndpoint,
			governance_service.GroupArchiveOption{Archived: true, Revision: root.Revision}).AddTokenAuth(token), http.StatusOK)
		root = DecodeJSON(t, response, &governance_service.GroupState{})
		response = MakeRequest(t, NewRequestWithJSON(t, "PUT", archiveEndpoint,
			governance_service.GroupArchiveOption{Archived: false, Revision: root.Revision}).AddTokenAuth(token), http.StatusOK)
		DecodeJSON(t, response, &governance_service.GroupState{})
		response = MakeRequest(t, NewRequest(t, "GET", deletionEndpoint).AddTokenAuth(token), http.StatusOK)
		deletion = DecodeJSON(t, response, &repo_service.DeletionState{})
		require.NotNil(t, deletion.Pending)
		require.True(t, deletion.Archived)
		assertCancelled(oldRun, oldJob)
		assertCancelled(pendingRun, pendingJob)
	})
}

func TestWorkflowDispatchQueuedRunInvalidatedByArchive(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		token := getUserToken(t, user.Name, auth_model.AccessTokenScopeAll)
		response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups",
			governance_service.GroupOption{Name: "派发生命周期组", Path: "dispatch-lifecycle-root", Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
		root := DecodeJSON(t, response, &governance_service.GroupState{})
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups",
			governance_service.GroupOption{Name: "子组", Path: "child", ParentID: root.ID, Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
		child := DecodeJSON(t, response, &governance_service.GroupState{})

		createWaitingDispatch := func(name string) (*repo_model.Repository, *actions_model.ActionRun, *actions_model.ActionRunJob) {
			response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/orgs/"+child.InternalName+"/repos",
				api.CreateRepoOption{Name: name, Private: true, AutoInit: true}).AddTokenAuth(token), http.StatusCreated)
			created := DecodeJSON(t, response, &api.Repository{})
			repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: created.ID})
			createRepoWorkflowFile(t, user, token, repo, ".gitea/workflows/dispatch.yml", `on:
  workflow_dispatch:
jobs:
  job:
    runs-on: isolated-lifecycle-test
    steps:
      - run: echo lifecycle
`)
			response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/"+child.InternalName+"/"+name+
				"/actions/workflows/dispatch.yml/dispatches?return_run_details=true",
				api.CreateActionWorkflowDispatch{Ref: repo.DefaultBranch}).AddTokenAuth(token), http.StatusOK)
			details := DecodeJSON(t, response, &api.RunDetails{})
			run := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: details.WorkflowRunID})
			job := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{RunID: run.ID})
			require.Equal(t, actions_model.StatusWaiting, run.Status)
			require.Equal(t, actions_model.StatusWaiting, job.Status)
			require.Zero(t, job.TaskID)
			return repo, run, job
		}
		assertCancelled := func(run *actions_model.ActionRun, job *actions_model.ActionRunJob) {
			require.Equal(t, actions_model.StatusCancelled, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: run.ID}).Status)
			freshJob := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID})
			require.Equal(t, actions_model.StatusCancelled, freshJob.Status)
			require.Zero(t, freshJob.TaskID)
		}

		groupRepo, groupRun, groupJob := createWaitingDispatch("group-archive")
		archiveEndpoint := fmt.Sprintf("/api/v1/governance/groups/%d/archive", root.ID)
		response = MakeRequest(t, NewRequest(t, "GET", archiveEndpoint).AddTokenAuth(token), http.StatusOK)
		impact := DecodeJSON(t, response, &governance_service.GroupArchiveImpact{})
		response = MakeRequest(t, NewRequestWithJSON(t, "PUT", archiveEndpoint,
			governance_service.GroupArchiveOption{Archived: true, Revision: impact.Revision}).AddTokenAuth(token), http.StatusOK)
		root = DecodeJSON(t, response, &governance_service.GroupState{})
		assertCancelled(groupRun, groupJob)
		response = MakeRequest(t, NewRequestWithJSON(t, "PUT", archiveEndpoint,
			governance_service.GroupArchiveOption{Archived: false, Revision: root.Revision}).AddTokenAuth(token), http.StatusOK)
		root = DecodeJSON(t, response, &governance_service.GroupState{})
		assertCancelled(groupRun, groupJob)
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/"+child.InternalName+
			"/group-archive/actions/workflows/dispatch.yml/dispatches?return_run_details=true",
			api.CreateActionWorkflowDispatch{Ref: groupRepo.DefaultBranch}).AddTokenAuth(token), http.StatusOK)
		fresh := DecodeJSON(t, response, &api.RunDetails{})
		require.NotEqual(t, groupRun.ID, fresh.WorkflowRunID)
		require.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: fresh.WorkflowRunID}).Status)
		assertCancelled(groupRun, groupJob)

		_, directRun, directJob := createWaitingDispatch("direct-archive")
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/"+child.InternalName+"/direct-archive",
			api.EditRepoOption{Archived: new(true)}).AddTokenAuth(token), http.StatusOK)
		assertCancelled(directRun, directJob)
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/"+child.InternalName+"/direct-archive",
			api.EditRepoOption{Archived: new(false)}).AddTokenAuth(token), http.StatusOK)
		assertCancelled(directRun, directJob)

		pendingRepo, pendingRun, pendingJob := createWaitingDispatch("pending-delete")
		MakeRequest(t, NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/governance/repositories/%d/deletion", pendingRepo.ID),
			repo_service.DeletionOption{ConfirmationPath: pendingRepo.FullPath()}).AddTokenAuth(token), http.StatusOK)
		assertCancelled(pendingRun, pendingJob)

		_, groupDeleteRun, groupDeleteJob := createWaitingDispatch("group-delete")
		MakeRequest(t, NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/governance/groups/%d/deletion", root.ID),
			governance_service.GroupDeletionOption{Revision: root.Revision, ConfirmationPath: root.FullPath}).AddTokenAuth(token), http.StatusOK)
		assertCancelled(groupDeleteRun, groupDeleteJob)
	})
}

func TestClaimedTaskTokenStaysRevokedAfterArchiveRestore(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		token := getUserToken(t, user.Name, auth_model.AccessTokenScopeAll)
		response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/groups",
			governance_service.GroupOption{Name: "任务令牌恢复边界", Path: "task-token-archive-root", Visibility: 2}).AddTokenAuth(token), http.StatusCreated)
		root := DecodeJSON(t, response, &governance_service.GroupState{})
		response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/orgs/"+root.InternalName+"/repos",
			api.CreateRepoOption{Name: "task-token", Private: true, AutoInit: true}).AddTokenAuth(token), http.StatusCreated)
		created := DecodeJSON(t, response, &api.Repository{})
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: created.ID})
		createRepoWorkflowFile(t, user, token, repo, ".gitea/workflows/dispatch.yml", `on:
  workflow_dispatch:
jobs:
  job:
    runs-on: isolated-lifecycle-token
    steps:
      - run: echo lifecycle
`)
		MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/"+root.InternalName+
			"/task-token/actions/workflows/dispatch.yml/dispatches", api.CreateActionWorkflowDispatch{Ref: repo.DefaultBranch}).AddTokenAuth(token), http.StatusNoContent)
		runner := &actions_model.ActionRunner{
			UUID: "task-token-archive-runner", Name: "task-token-archive-runner",
			OwnerID: root.ID, AgentLabels: []string{"isolated-lifecycle-token"}, HasCancellingSupport: true,
		}
		runner.GenerateAndFillToken()
		require.NoError(t, db.Insert(t.Context(), runner))
		task, claimed, err := actions_model.CreateTaskForRunner(t.Context(), runner)
		require.NoError(t, err)
		require.True(t, claimed)
		require.NotNil(t, task)
		oldToken := task.Token
		valid, err := actions_service.TaskCredentialValid(t.Context(), task)
		require.NoError(t, err)
		require.True(t, valid)
		_, err = actions_model.GetRunningTaskByToken(t.Context(), oldToken)
		require.NoError(t, err)

		archiveEndpoint := fmt.Sprintf("/api/v1/governance/groups/%d/archive", root.ID)
		response = MakeRequest(t, NewRequest(t, "GET", archiveEndpoint).AddTokenAuth(token), http.StatusOK)
		impact := DecodeJSON(t, response, &governance_service.GroupArchiveImpact{})
		response = MakeRequest(t, NewRequestWithJSON(t, "PUT", archiveEndpoint,
			governance_service.GroupArchiveOption{Archived: true, Revision: impact.Revision}).AddTokenAuth(token), http.StatusOK)
		root = DecodeJSON(t, response, &governance_service.GroupState{})
		task = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.ID})
		require.Equal(t, actions_model.StatusCancelling, task.Status)
		valid, err = actions_service.TaskCredentialValid(t.Context(), task)
		require.NoError(t, err)
		require.False(t, valid)

		MakeRequest(t, NewRequestWithJSON(t, "PUT", archiveEndpoint,
			governance_service.GroupArchiveOption{Archived: false, Revision: root.Revision}).AddTokenAuth(token), http.StatusOK)
		task = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: task.ID})
		require.Equal(t, actions_model.StatusCancelling, task.Status)
		valid, err = actions_service.TaskCredentialValid(t.Context(), task)
		require.NoError(t, err)
		require.False(t, valid, "旧任务令牌不能因为归档/恢复 ABA 再次获得授权")
		_, err = actions_model.GetRunningTaskByToken(t.Context(), oldToken)
		require.ErrorIs(t, err, util.ErrNotExist)

		MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/"+root.InternalName+
			"/task-token/actions/workflows/dispatch.yml/dispatches", api.CreateActionWorkflowDispatch{Ref: repo.DefaultBranch}).AddTokenAuth(token), http.StatusNoContent)
		directTask, claimed, err := actions_model.CreateTaskForRunner(t.Context(), runner)
		require.NoError(t, err)
		require.True(t, claimed)
		require.NotEqual(t, task.ID, directTask.ID)
		directToken := directTask.Token
		valid, err = actions_service.TaskCredentialValid(t.Context(), directTask)
		require.NoError(t, err)
		require.True(t, valid)
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/"+root.InternalName+"/task-token",
			api.EditRepoOption{Archived: new(true)}).AddTokenAuth(token), http.StatusOK)
		directTask = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: directTask.ID})
		require.Equal(t, actions_model.StatusCancelling, directTask.Status)
		valid, err = actions_service.TaskCredentialValid(t.Context(), directTask)
		require.NoError(t, err)
		require.False(t, valid)
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/"+root.InternalName+"/task-token",
			api.EditRepoOption{Archived: new(false)}).AddTokenAuth(token), http.StatusOK)
		directTask = unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: directTask.ID})
		valid, err = actions_service.TaskCredentialValid(t.Context(), directTask)
		require.NoError(t, err)
		require.False(t, valid, "直接仓库归档/恢复也不能复活旧任务凭据")
		_, err = actions_model.GetRunningTaskByToken(t.Context(), directToken)
		require.ErrorIs(t, err, util.ErrNotExist)
	})
}
