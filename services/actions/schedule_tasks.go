// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	perm_model "gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/actions/jobparser"
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"
	"gitea.dev/modules/timeutil"
	webhook_module "gitea.dev/modules/webhook"
	"gitea.dev/services/convert"
)

// StartScheduleTasks start the task
func StartScheduleTasks(ctx context.Context) error {
	return startTasks(ctx)
}

// startTasks retrieves specifications in pages, creates a schedule task for each specification,
// and updates the specification's next run time and previous run time.
// The function returns an error if there's an issue with finding or updating the specifications.
func startTasks(ctx context.Context) error {
	if err := refreshScopedSchedulePlans(ctx); err != nil {
		return err
	}
	const pageSize = 50
	now := time.Now()
	var beforeID int64
	for {
		specs, _, err := actions_model.FindSpecs(ctx, actions_model.FindSpecOptions{
			ListOptions: db.ListOptions{
				Page:     1,
				PageSize: pageSize,
			},
			Next: now.Unix(), BeforeID: beforeID,
		})
		if err != nil {
			return fmt.Errorf("find specs: %w", err)
		}

		if err := specs.LoadRepos(ctx); err != nil {
			return fmt.Errorf("LoadRepos: %w", err)
		}

		for _, row := range specs {
			beforeID = row.ID
			if row.Schedule == nil || row.Repo == nil || row.Schedule.ScopeRevision != row.Repo.ActionsScopeRevision || row.Schedule.OwnerID != row.Repo.OwnerID {
				continue
			}
			if row.Repo.IsArchived {
				// Skip if the repo is archived
				continue
			}
			current, err := actions_model.ScheduleBranchCurrent(ctx, row.Repo, row.Schedule.Ref, row.Schedule.CommitSHA)
			if err != nil {
				return fmt.Errorf("ScheduleBranchCurrent: %w", err)
			}
			if !current {
				continue
			}

			cfg, err := row.Repo.GetUnit(ctx, unit.TypeActions)
			if err != nil {
				if repo_model.IsErrUnitTypeNotExist(err) {
					// Skip the actions unit of this repo is disabled.
					continue
				}
				return fmt.Errorf("GetUnit: %w", err)
			}
			if !row.Schedule.IsScopedRun && cfg.ActionsConfig().IsWorkflowDisabled(row.Schedule.WorkflowID) {
				continue
			}
			if row.Schedule.IsScopedRun {
				optedOut, err := actions_model.IsScopedWorkflowOptedOut(ctx, cfg.ActionsConfig(), row.Repo.OwnerID, row.Schedule.WorkflowRepoID, row.Schedule.WorkflowID)
				if err != nil {
					return err
				}
				if optedOut {
					continue
				}
			}
			if err := startScheduleSpec(ctx, row, now); err != nil {
				if errors.Is(err, governance_model.ErrConflict) {
					continue
				}
				log.Error("startScheduleSpec: %v", err)
				return err
			}
		}

		// Stop if all specs have been retrieved
		if len(specs) < pageSize {
			break
		}
	}

	return nil
}

func refreshScopedSchedulePlans(ctx context.Context) error {
	hasSources, err := db.GetEngine(ctx).Exist(new(actions_model.ActionScopedWorkflowSource))
	if err != nil {
		return err
	}
	hasPlans, err := db.GetEngine(ctx).Where("is_scoped_run = ?", true).Exist(new(actions_model.ActionSchedule))
	if err != nil || !hasSources && !hasPlans {
		return err
	}
	var beforeID int64
	for {
		repos := make([]*repo_model.Repository, 0, 50)
		query := db.GetEngine(ctx).Desc("id").Limit(50)
		if beforeID > 0 {
			query = query.Where("id < ?", beforeID)
		}
		if err := query.Find(&repos); err != nil {
			return err
		}
		if len(repos) == 0 {
			return nil
		}
		for _, repo := range repos {
			beforeID = repo.ID
			if repo.IsEmpty || repo.IsArchived || repo.Status != repo_model.RepositoryReady {
				continue
			}
			if _, err := repo.GetUnit(ctx, unit.TypeActions); err != nil {
				if repo_model.IsErrUnitTypeNotExist(err) {
					continue
				}
				return err
			}
			need, err := scopedScheduleNeedsRefresh(ctx, repo)
			if err != nil {
				log.Error("scoped schedule refresh check for repo %d: %v", repo.ID, err)
				continue
			}
			if need {
				if err := DetectAndHandleSchedules(ctx, repo); err != nil && !errors.Is(err, governance_model.ErrConflict) {
					log.Error("scoped schedule refresh for repo %d: %v", repo.ID, err)
				}
			}
		}
		if len(repos) < 50 {
			return nil
		}
	}
}

func scopedScheduleNeedsRefresh(ctx context.Context, repo *repo_model.Repository) (bool, error) {
	current, err := db.Find[actions_model.ActionSchedule](ctx, actions_model.FindScheduleOptions{RepoID: repo.ID})
	if err != nil {
		return false, err
	}
	branch, err := git_model.GetBranch(ctx, repo.ID, repo.DefaultBranch)
	if err != nil {
		return false, err
	}
	for _, plan := range current {
		if plan.CommitSHA != branch.CommitID || plan.ScopeRevision != repo.ActionsScopeRevision || plan.OwnerID != repo.OwnerID {
			return true, nil
		}
	}
	sources, err := actions_model.GetEffectiveScopedWorkflowSources(ctx, repo.OwnerID)
	if err != nil {
		return false, err
	}
	unit, err := repo.GetUnit(ctx, unit.TypeActions)
	if err != nil {
		return false, err
	}
	registrations := make(map[int64][]*actions_model.ActionScopedWorkflowSource)
	for _, source := range sources {
		valid, err := actions_model.ScopedWorkflowRegistrationValid(ctx, source)
		if err != nil {
			return false, err
		}
		if valid {
			registrations[source.SourceRepoID] = append(registrations[source.SourceRepoID], source)
		}
	}
	count := 0
	for sourceID, sourceRegistrations := range registrations {
		sourceRepo, err := repo_model.GetRepositoryByID(ctx, sourceID)
		if err != nil {
			return false, err
		}
		sha, parsed, err := LoadParsedScopedWorkflows(ctx, sourceRepo)
		if err != nil {
			for _, plan := range current {
				if plan.IsScopedRun && plan.WorkflowRepoID == sourceID {
					return true, nil
				}
			}
			log.Error("scoped schedule source %d: %v", sourceID, err)
			continue
		}
		revisions := make(map[string]int64, len(sourceRegistrations))
		for _, registration := range sourceRegistrations {
			revisions[strconv.FormatInt(registration.ID, 10)] = registration.ConfigRevision
		}
		for _, workflow := range parsed {
			if actions_model.ScopedWorkflowOptedOut(unit.ActionsConfig(), sourceRegistrations, sourceID, workflow.EntryName) {
				continue
			}
			parsedWorkflow, err := jobparser.ReadWorkflow(workflow.Content)
			if err != nil || len(parsedWorkflow.OnSchedule()) == 0 {
				continue
			}
			count++
			found := false
			for _, plan := range current {
				if plan.IsScopedRun && plan.WorkflowRepoID == sourceID && plan.WorkflowID == workflow.EntryName &&
					plan.WorkflowCommitSHA == sha && plan.WorkflowSourceScopeRevision == sourceRepo.ActionsScopeRevision &&
					maps.Equal(plan.ScopedConfigRevisions, revisions) && plan.CommitSHA == branch.CommitID {
					found = true
					break
				}
			}
			if !found {
				return true, nil
			}
		}
	}
	for _, plan := range current {
		if plan.IsScopedRun {
			count--
		}
	}
	return count != 0, nil
}

func startScheduleSpec(ctx context.Context, row *actions_model.ActionScheduleSpec, now time.Time) error {
	next, err := nextScheduleTime(row, now)
	if err != nil {
		return err
	}
	row.Prev, row.Next = row.Next, next
	return CreateScheduleTask(ctx, row)
}

func nextScheduleTime(row *actions_model.ActionScheduleSpec, now time.Time) (timeutil.TimeStamp, error) {
	schedule, err := row.Parse()
	if err != nil {
		return 0, err
	}
	return timeutil.TimeStamp(schedule.Next(now).Unix()), nil
}

// CreateScheduleTask creates a scheduled task from a cron action schedule spec.
// It creates an action run based on the schedule, inserts it into the database, and creates commit statuses for each job.
func CreateScheduleTask(ctx context.Context, spec *actions_model.ActionScheduleSpec) error {
	cron := spec.Schedule

	// Scheduled runs carry no webhook payload; synthesize what github.event.* expects.
	if err := spec.Repo.LoadOwner(ctx); err != nil {
		return fmt.Errorf("LoadOwner: %w", err)
	}
	fields := map[string]any{
		"repository": convert.ToRepo(ctx, spec.Repo, access_model.Permission{AccessMode: perm_model.AccessModeRead}),
		"sender":     convert.ToUser(ctx, user_model.NewActionsUser(), nil),
	}
	if spec.Repo.Owner.IsOrganization() {
		fields["organization"] = convert.ToOrganization(ctx, organization.OrgFromUser(spec.Repo.Owner))
	}
	eventPayload := withScheduleInEventPayload(cron.EventPayload, spec.Spec, fields)

	// Create a new action run based on the schedule
	run := &actions_model.ActionRun{
		Title:                       cron.Title,
		RepoID:                      cron.RepoID,
		OwnerID:                     cron.OwnerID,
		WorkflowID:                  cron.WorkflowID,
		TriggerUserID:               cron.TriggerUserID,
		Ref:                         cron.Ref,
		CommitSHA:                   cron.CommitSHA,
		Event:                       cron.Event,
		EventPayload:                eventPayload,
		TriggerEvent:                string(webhook_module.HookEventSchedule),
		ScheduleID:                  cron.ID,
		Status:                      actions_model.StatusWaiting,
		WorkflowRepoID:              cron.RepoID,
		WorkflowCommitSHA:           cron.CommitSHA,
		WorkflowSourceScopeRevision: cron.WorkflowSourceScopeRevision,
		ScopedConfigRevisions:       cron.ScopedConfigRevisions,
		IsScopedRun:                 cron.IsScopedRun,
	}
	if cron.IsScopedRun {
		run.WorkflowRepoID = cron.WorkflowRepoID
		run.WorkflowCommitSHA = cron.WorkflowCommitSHA
	}

	// FIXME cron.Content might be outdated if the workflow file has been changed.
	// Load the latest sha from default branch
	// Insert the action run and its associated jobs into the database
	if err := prepareRunAndInsert(ctx, cron.Content, run, nil, spec); err != nil {
		return err
	}

	// Return nil if no errors occurred
	return nil
}

func withScheduleInEventPayload(eventPayload, schedule string, fields map[string]any) string {
	if schedule == "" {
		return eventPayload
	}

	// eventPayload originates from json.Marshal(input.Payload) in handleSchedules,
	// so a nil payload is stored as the literal "null" and pre-existing rows may be
	// empty. Both cases start from a fresh map so the schedule field can still be set.
	var event map[string]any
	if eventPayload != "" {
		if err := json.Unmarshal([]byte(eventPayload), &event); err != nil {
			log.Error("withScheduleInEventPayload: unmarshal: %v", err)
			return eventPayload
		}
	}
	if event == nil {
		event = map[string]any{}
	}

	maps.Copy(event, fields)
	event["schedule"] = schedule
	updatedPayload, err := json.Marshal(event)
	if err != nil {
		log.Error("withScheduleInEventPayload: marshal: %v", err)
		return eventPayload
	}

	return string(updatedPayload)
}
