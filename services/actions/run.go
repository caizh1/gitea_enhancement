// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"fmt"
	"strconv"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/actions/jobparser"
	"gitea.dev/modules/log"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	act_model "gitea.com/gitea/runner/act/model"
	"go.yaml.in/yaml/v4"
)

// PrepareRunAndInsert prepares a run and inserts it into the database
// It parses the workflow content, evaluates concurrency if needed, and inserts the run and its jobs into the database.
// The title will be cut off at 255 characters if it's longer than 255 characters.
func PrepareRunAndInsert(ctx context.Context, content []byte, run *actions_model.ActionRun, inputsWithDefaults map[string]any) error {
	if run.WorkflowRepoID == 0 {
		return fmt.Errorf("WorkflowRepoID must be set before insert (repo %d, workflow %q)", run.RepoID, run.WorkflowID)
	}

	if err := run.LoadAttributes(ctx); err != nil {
		return fmt.Errorf("LoadAttributes: %w", err)
	}

	vars, err := actions_model.GetVariablesOfRun(ctx, run)
	if err != nil {
		return fmt.Errorf("GetVariablesOfRun: %w", err)
	}

	wfRawConcurrency, err := jobparser.ReadWorkflowRawConcurrency(content)
	if err != nil {
		return fmt.Errorf("ReadWorkflowRawConcurrency: %w", err)
	}

	if err = InsertRun(ctx, run, content, vars, inputsWithDefaults, wfRawConcurrency); err != nil {
		return fmt.Errorf("InsertRun: %w", err)
	}

	// Load the newly inserted jobs with all fields from database (the job models in InsertRun are partial, so load again)
	allJobs, err := db.Find[actions_model.ActionRunJob](ctx, actions_model.FindRunJobOptions{RunID: run.ID})
	if err != nil {
		return fmt.Errorf("FindRunJob: %w", err)
	}

	CreateCommitStatusForRunJobs(ctx, run, allJobs...)

	NotifyWorkflowJobsAndRunsStatusUpdate(ctx, allJobs)

	return nil
}

// InsertRun inserts a run
// The title will be cut off at 255 characters if it's longer than 255 characters.
func InsertRun(ctx context.Context, run *actions_model.ActionRun, content []byte, vars map[string]string, inputs map[string]any, wfRawConcurrency *act_model.RawConcurrency) error {
	var organizationTrigger bool
	if run.TriggerUserID > 0 {
		trigger, err := user_model.GetUserByID(ctx, run.TriggerUserID)
		if err != nil {
			return err
		}
		organizationTrigger = trigger.IsOrganization()
	}
	preparedRepo := run.Repo
	if preparedRepo == nil {
		var err error
		preparedRepo, err = repo_model.GetRepositoryByID(ctx, run.RepoID)
		if err != nil {
			return err
		}
	}
	ownerID, ownerNamespace, scopeRevision := preparedRepo.OwnerID, preparedRepo.OwnerNamespace, preparedRepo.ActionsScopeRevision
	var cancelledConcurrencyJobs []*actions_model.ActionRunJob
	var needPostCommitEmit bool
	if err := db.WithTx(ctx, func(ctx context.Context) error {
		index, err := db.GetNextResourceIndex(ctx, "action_run_index", run.RepoID)
		if err != nil {
			return err
		}
		run.Index = index
		run.Title = util.EllipsisDisplayString(run.Title, 255)
		run.Status = actions_model.StatusWaiting
		if organizationTrigger {
			run.Status = actions_model.StatusCancelled
			run.Stopped = timeutil.TimeStampNow()
		}
		if run.IsScopedRun {
			sources, err := actions_model.GetEffectiveScopedWorkflowSources(ctx, run.OwnerID)
			if err != nil {
				return err
			}
			run.ScopedConfigRevisions = make(map[string]int64)
			for _, source := range sources {
				if source.SourceRepoID == run.WorkflowRepoID && source.SourceScopeRevision == run.WorkflowSourceScopeRevision {
					run.ScopedConfigRevisions[strconv.FormatInt(source.ID, 10)] = source.ConfigRevision
				}
			}
		}

		if wfRawConcurrency != nil {
			rawConcurrency, err := yaml.Marshal(wfRawConcurrency)
			if err != nil {
				return fmt.Errorf("marshal raw concurrency: %w", err)
			}
			run.RawConcurrency = string(rawConcurrency)
		}

		// Insert before parsing jobs or evaluating workflow-level concurrency
		// so that run.ID is populated. Expressions referencing github.run_id —
		// in run-name, job names, runs-on, or a workflow-level concurrency
		// group like `${{ github.head_ref || github.run_id }}` — would otherwise
		// interpolate to an empty string.
		if err := db.Insert(ctx, run); err != nil {
			return err
		}

		runAttempt := &actions_model.ActionRunAttempt{
			RepoID:        run.RepoID,
			RunID:         run.ID,
			Attempt:       1,
			TriggerUserID: run.TriggerUserID,
			Status:        actions_model.StatusWaiting,
		}
		if organizationTrigger {
			runAttempt.Status = actions_model.StatusCancelled
			runAttempt.Stopped = run.Stopped
		}

		if wfRawConcurrency != nil {
			if !organizationTrigger {
				if err := EvaluateRunConcurrencyFillModel(ctx, run, runAttempt, wfRawConcurrency, vars, inputs); err != nil {
					return fmt.Errorf("EvaluateRunConcurrencyFillModel: %w", err)
				}
				// check run (workflow-level) concurrency
				var jobsToCancel []*actions_model.ActionRunJob
				runAttempt.Status, jobsToCancel, err = PrepareToStartRunWithConcurrency(ctx, runAttempt)
				if err != nil {
					return err
				}
				cancelledConcurrencyJobs = append(cancelledConcurrencyJobs, jobsToCancel...)
			}
		}

		if err := db.Insert(ctx, runAttempt); err != nil {
			return err
		}
		run.LatestAttemptID = runAttempt.ID

		giteaCtx := GenerateGiteaContext(ctx, run, runAttempt, nil)
		jobs, err := jobparser.Parse(content, jobparser.WithVars(vars), jobparser.WithGitContext(giteaCtx.ToGitHubContext()), jobparser.WithInputs(inputs))
		if err != nil {
			return fmt.Errorf("parse workflow: %w", err)
		}
		titleChanged := len(jobs) > 0 && jobs[0].RunName != ""
		if titleChanged {
			run.Title = util.EllipsisDisplayString(jobs[0].RunName, 255)
		}

		cols := []string{"latest_attempt_id"}
		if titleChanged {
			cols = append(cols, "title")
		}
		if err := actions_model.UpdateRun(ctx, run, cols...); err != nil {
			return err
		}

		runJobs := make([]*actions_model.ActionRunJob, 0, len(jobs))
		var hasWaitingJobs bool
		for _, v := range jobs {
			runJob, jobsToCancel, jobNeedsPostCommitEmit, err := insertRunJob(ctx, run, runAttempt, v, vars, inputs, organizationTrigger)
			if err != nil {
				return err
			}
			cancelledConcurrencyJobs = append(cancelledConcurrencyJobs, jobsToCancel...)
			needPostCommitEmit = needPostCommitEmit || jobNeedsPostCommitEmit
			// A reusable caller is never dispatched to a runner, so it must not drive the task-version bump.
			hasWaitingJobs = hasWaitingJobs || (runJob.Status == actions_model.StatusWaiting && !runJob.IsReusableCaller)
			runJobs = append(runJobs, runJob)
		}

		if !organizationTrigger || len(runJobs) > 0 {
			runAttempt.Status = actions_model.AggregateJobStatus(runJobs)
		}
		if err := actions_model.UpdateRunAttempt(ctx, runAttempt, "status"); err != nil {
			return err
		}

		// if there is a job in the waiting status, increase tasks version.
		if hasWaitingJobs {
			if err := actions_model.IncreaseTaskVersion(ctx, run.OwnerID, run.RepoID); err != nil {
				return err
			}
		}
		if err := actions_model.AppendRunAudit(ctx, run, "actions.run_triggered"); err != nil {
			return err
		}

		return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
			freshRepo, err := repo_model.GetRepositoryByID(ctx, run.RepoID)
			if err != nil {
				return err
			}
			if freshRepo.OwnerID != ownerID || run.OwnerID != ownerID || freshRepo.OwnerNamespace != ownerNamespace || freshRepo.ActionsScopeRevision != scopeRevision || freshRepo.IsArchived {
				return fmt.Errorf("%w：工作流准备期间仓库归属或生命周期已变化，请重新触发", governance_model.ErrConflict)
			}
			if run.IsScopedRun {
				valid, err := actions_model.ScopedWorkflowRunValid(ctx, run)
				if err != nil {
					return err
				}
				if !valid {
					return fmt.Errorf("%w：工作流来源已失效，请重新触发", governance_model.ErrConflict)
				}
			}
			if run.ScheduleID != 0 {
				valid, err := actions_model.ScheduledRunValidForWrite(ctx, run, freshRepo)
				if err != nil {
					return err
				}
				if !valid {
					return fmt.Errorf("%w：定时计划的仓库范围已变化，请重新检测并触发工作流", governance_model.ErrConflict)
				}
			}
			return nil
		})
	}); err != nil {
		return err
	}

	NotifyWorkflowJobsAndRunsStatusUpdate(ctx, cancelledConcurrencyJobs)
	EmitJobsIfReadyByJobs(cancelledConcurrencyJobs)

	// Post-commit kick: let the job emitter resolve jobs if needed
	if needPostCommitEmit {
		if err := EmitJobsIfReadyByRun(run.ID); err != nil {
			log.Error("emit run %d after InsertRun: %v", run.ID, err)
		}
	}

	return nil
}

// insertRunJob builds a single run job from a parsed workflow job, evaluates its
// job-level concurrency, inserts it, and — for a ready no-needs reusable caller —
// inline-expands (or skips) it. It returns the inserted job, any jobs cancelled by
// job concurrency, and whether a post-commit emitter pass is needed to resolve the
// caller's dependents.
func insertRunJob(ctx context.Context, run *actions_model.ActionRun, runAttempt *actions_model.ActionRunAttempt, workflowJob *jobparser.SingleWorkflow, vars map[string]string, inputs map[string]any, organizationTrigger bool) (*actions_model.ActionRunJob, []*actions_model.ActionRunJob, bool, error) {
	id, job := workflowJob.Job()
	needs := job.Needs()
	if err := workflowJob.SetJob(id, job.EraseNeeds()); err != nil {
		return nil, nil, false, err
	}
	payload, _ := workflowJob.Marshal()

	isReusableWorkflowCaller := job.Uses != ""
	shouldBlockJob := runAttempt.Status == actions_model.StatusBlocked || len(needs) > 0 || run.NeedApproval

	attemptJobID, err := actions_model.GetNextAttemptJobID(ctx, run.ID)
	if err != nil {
		return nil, nil, false, fmt.Errorf("alloc attempt_job_id: %w", err)
	}

	job.Name = util.EllipsisDisplayString(job.Name, 255)
	runJob := &actions_model.ActionRunJob{
		RunID:                   run.ID,
		RunAttemptID:            runAttempt.ID,
		RepoID:                  run.RepoID,
		OwnerID:                 run.OwnerID,
		CommitSHA:               run.CommitSHA,
		IsForkPullRequest:       run.IsForkPullRequest,
		Name:                    job.Name,
		Attempt:                 runAttempt.Attempt,
		WorkflowPayload:         payload,
		JobID:                   id,
		AttemptJobID:            attemptJobID,
		Needs:                   needs,
		RunsOn:                  job.RunsOn(),
		Status:                  util.Iif(shouldBlockJob, actions_model.StatusBlocked, actions_model.StatusWaiting),
		WorkflowSourceRepoID:    run.WorkflowRepoID,
		WorkflowSourceCommitSHA: run.WorkflowCommitSHA,
		ContinueOnError:         job.GetContinueOnError(),
	}
	if organizationTrigger {
		runJob.Status = actions_model.StatusCancelled
		runJob.Stopped = runAttempt.Stopped
	}
	// Parse workflow/job permissions (no clamping here)
	if perms := ExtractJobPermissionsFromWorkflow(workflowJob, job); perms != nil {
		runJob.TokenPermissions = perms
	}

	if isReusableWorkflowCaller {
		runJob.IsReusableCaller = true
		runJob.CallUses = job.Uses
	}

	var cancelledConcurrencyJobs []*actions_model.ActionRunJob
	// check job concurrency
	if job.RawConcurrency != nil {
		rawConcurrency, err := yaml.Marshal(job.RawConcurrency)
		if err != nil {
			return nil, nil, false, fmt.Errorf("marshal raw concurrency: %w", err)
		}
		runJob.RawConcurrency = string(rawConcurrency)

		// do not evaluate job concurrency when it requires `needs`, the jobs with `needs` will be evaluated later by job emitter
		if len(needs) == 0 && !organizationTrigger {
			if err := EvaluateJobConcurrencyFillModel(ctx, run, runAttempt, runJob, vars, inputs); err != nil {
				return nil, nil, false, fmt.Errorf("evaluate job concurrency: %w", err)
			}
		}

		// If a job needs other jobs ("needs" is not empty), its status is set to StatusBlocked at the entry of the loop
		// No need to check job concurrency for a blocked job (it will be checked by job emitter later)
		if runJob.Status == actions_model.StatusWaiting {
			var jobsToCancel []*actions_model.ActionRunJob
			runJob.Status, jobsToCancel, err = PrepareToStartJobWithConcurrency(ctx, runJob)
			if err != nil {
				return nil, nil, false, fmt.Errorf("prepare to start job with concurrency: %w", err)
			}
			cancelledConcurrencyJobs = append(cancelledConcurrencyJobs, jobsToCancel...)
		}
	}

	if err := db.Insert(ctx, runJob); err != nil {
		return nil, nil, false, err
	}

	// expand reusable caller
	var needPostCommitEmit bool
	if isReusableWorkflowCaller && runJob.Status == actions_model.StatusWaiting {
		if err := processInlineReusableCaller(ctx, run, runAttempt, runJob, vars); err != nil {
			return nil, nil, false, err
		}
		// A processed caller always needs a resolver pass:
		//   - if the caller is expanded, resolve its children jobs;
		//   - if the caller is skipped, propagate its state to its dependents
		needPostCommitEmit = true
	}

	return runJob, cancelledConcurrencyJobs, needPostCommitEmit, nil
}

// processInlineReusableCaller evaluates a no-needs reusable caller's own `if:` and
// either inline-expands it into child jobs or marks it skipped.
// (A caller with needs is Blocked and gets its `if:` evaluated by the job emitter instead.)
func processInlineReusableCaller(ctx context.Context, run *actions_model.ActionRun, runAttempt *actions_model.ActionRunAttempt, caller *actions_model.ActionRunJob, vars map[string]string) error {
	shouldStart, err := evaluateJobIf(ctx, run, runAttempt, caller, vars, true)
	if err != nil {
		return fmt.Errorf("evaluate caller %d if: %w", caller.ID, err)
	}
	if shouldStart {
		if err := expandReusableWorkflowCaller(ctx, run, runAttempt, caller, vars); err != nil {
			return fmt.Errorf("inline trigger caller %d ready: %w", caller.ID, err)
		}
		// refresh the caller status
		if err := actions_model.RefreshReusableCallerStatus(ctx, caller); err != nil {
			return fmt.Errorf("refresh caller %d status: %w", caller.ID, err)
		}
		return nil
	}
	caller.Status = actions_model.StatusSkipped
	if _, err := actions_model.UpdateRunJob(ctx, caller, nil, "status"); err != nil {
		return fmt.Errorf("skip caller %d: %w", caller.ID, err)
	}
	return nil
}
