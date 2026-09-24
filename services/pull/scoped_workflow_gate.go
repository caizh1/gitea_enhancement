// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pull

import (
	"context"
	"fmt"
	"strconv"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	actions_module "gitea.dev/modules/actions"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/glob"
)

type requiredScopedWorkflowKey struct {
	sourceID   int64
	workflowID string
}

type requiredScopedSourceProof struct {
	repoID, ownerID, scopeRevision, configRevision, referenceRevision int64
	defaultBranch, gitSHA                                             string
}

type requiredScopedSourceSnapshot struct {
	sources   map[int64]*requiredScopedSourceProof
	workflows map[requiredScopedWorkflowKey]bool
}

// CheckRequiredScopedWorkflowReadiness previews the final trusted-run gate for a fixed pull head.
// The merge authorization repeats this check with source reservations and revision validation.
func CheckRequiredScopedWorkflowReadiness(ctx context.Context, repo *repo_model.Repository, head string) error {
	snapshot, err := captureRequiredScopedSourceSnapshot(ctx, repo)
	if err != nil {
		return err
	}
	_, err = checkRequiredScopedRunsAtSnapshot(ctx, repo, head, snapshot)
	return err
}

// captureRequiredScopedSourceSnapshot reads Git outside the final authorization transaction.
func captureRequiredScopedSourceSnapshot(ctx context.Context, repo *repo_model.Repository) (*requiredScopedSourceSnapshot, error) {
	snapshot := &requiredScopedSourceSnapshot{sources: make(map[int64]*requiredScopedSourceProof), workflows: make(map[requiredScopedWorkflowKey]bool)}
	if err := governance_model.WithStableRead(ctx, func(ctx context.Context) error {
		sources, err := actions_model.GetEffectiveScopedWorkflowSources(ctx, repo.OwnerID)
		if err != nil {
			return err
		}
		for _, source := range sources {
			for workflowID, config := range source.WorkflowConfigs {
				if config == nil || !config.Required {
					continue
				}
				if snapshot.sources[source.ID] == nil {
					sourceRepo, err := repo_model.GetRepositoryByID(ctx, source.SourceRepoID)
					if err != nil {
						return fmt.Errorf("%w: scoped source %d is unavailable", ErrNotReadyToMerge, source.SourceRepoID)
					}
					revision, err := governance_model.ReadReferenceRevision(ctx, source.SourceRepoID)
					if err != nil {
						return err
					}
					pending, err := db.GetEngine(ctx).Where("resource = ?", governance_model.Resource("repository", source.SourceRepoID)).Exist(new(governance_model.ReferenceReservation))
					if err != nil {
						return err
					}
					if pending {
						return fmt.Errorf("%w: scoped source %d has a pending reference update", ErrNotReadyToMerge, source.SourceRepoID)
					}
					snapshot.sources[source.ID] = &requiredScopedSourceProof{repoID: source.SourceRepoID, ownerID: source.OwnerID, scopeRevision: source.SourceScopeRevision, configRevision: source.ConfigRevision, referenceRevision: revision, defaultBranch: sourceRepo.DefaultBranch}
				}
				snapshot.workflows[requiredScopedWorkflowKey{source.ID, workflowID}] = true
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, proof := range snapshot.sources {
		sourceRepo, err := repo_model.GetRepositoryByID(ctx, proof.repoID)
		if err != nil {
			return nil, fmt.Errorf("%w: scoped source %d is unavailable", ErrNotReadyToMerge, proof.repoID)
		}
		installed, err := gitrepo.ReferenceTransactionHookInstalled(sourceRepo)
		if err != nil {
			return nil, fmt.Errorf("%w: scoped source %d reference hook cannot be checked", ErrNotReadyToMerge, proof.repoID)
		}
		if !installed {
			return nil, fmt.Errorf("%w: scoped source %d needs a verified reference-transaction hook; save its required configuration again", ErrNotReadyToMerge, proof.repoID)
		}
		actualBranch, err := gitrepo.GetDefaultBranch(ctx, sourceRepo)
		if err != nil || actualBranch != proof.defaultBranch {
			return nil, fmt.Errorf("%w: scoped source %d Git default branch differs from repository settings", ErrNotReadyToMerge, proof.repoID)
		}
		proof.gitSHA, err = gitrepo.GetBranchCommitID(ctx, sourceRepo, proof.defaultBranch)
		if err != nil {
			return nil, fmt.Errorf("%w: scoped source %d default branch is unavailable", ErrNotReadyToMerge, proof.repoID)
		}
	}
	return snapshot, nil
}

// checkRequiredScopedRuns reads the current policy and the run proof in the final merge transaction.
func checkRequiredScopedRuns(ctx context.Context, repo *repo_model.Repository, head string) error {
	_, err := checkRequiredScopedRunsAtSnapshot(ctx, repo, head, nil)
	return err
}

// requiredScopedSourceDependencies validates the Git snapshot while holding source reference reservations out.
func requiredScopedSourceDependencies(ctx context.Context, repo *repo_model.Repository, head string, snapshot *requiredScopedSourceSnapshot) ([]string, error) {
	if snapshot == nil {
		return nil, governance_model.ErrConflict
	}
	resources := make([]string, 0, len(snapshot.sources))
	for _, source := range snapshot.sources {
		resources = append(resources, governance_model.Resource("repository", source.repoID))
	}
	var dependencies []string
	err := governance_model.WithWrite(ctx, resources, func(ctx context.Context) error {
		var err error
		dependencies, err = checkRequiredScopedRunsAtSnapshot(ctx, repo, head, snapshot)
		return err
	})
	return dependencies, err
}

func checkRequiredScopedRunsAtSnapshot(ctx context.Context, repo *repo_model.Repository, head string, snapshot *requiredScopedSourceSnapshot) ([]string, error) {
	sources, err := actions_model.GetEffectiveScopedWorkflowSources(ctx, repo.OwnerID)
	if err != nil {
		return nil, err
	}
	var dependencies []string
	if snapshot != nil {
		currentWorkflows := make(map[requiredScopedWorkflowKey]bool)
		for _, source := range sources {
			for workflowID, cfg := range source.WorkflowConfigs {
				if cfg == nil || !cfg.Required {
					continue
				}
				key := requiredScopedWorkflowKey{source.ID, workflowID}
				currentWorkflows[key] = true
				proof := snapshot.sources[source.ID]
				if proof == nil || !snapshot.workflows[key] || proof.repoID != source.SourceRepoID || proof.ownerID != source.OwnerID || proof.scopeRevision != source.SourceScopeRevision || proof.configRevision != source.ConfigRevision {
					return nil, fmt.Errorf("%w: scoped workflow source or configuration changed during merge authorization", ErrNotReadyToMerge)
				}
			}
		}
		if len(currentWorkflows) != len(snapshot.workflows) {
			return nil, fmt.Errorf("%w: required scoped workflows changed during merge authorization", ErrNotReadyToMerge)
		}
		for _, source := range sources {
			proof := snapshot.sources[source.ID]
			if proof == nil {
				continue
			}
			sourceRepo, err := repo_model.GetRepositoryByID(ctx, source.SourceRepoID)
			if err != nil {
				return nil, err
			}
			if sourceRepo.DefaultBranch != proof.defaultBranch {
				return nil, fmt.Errorf("%w: scoped source %d default branch changed", ErrNotReadyToMerge, source.SourceRepoID)
			}
			current, err := governance_model.ReadReferenceRevision(ctx, source.SourceRepoID)
			if err != nil {
				return nil, err
			}
			if current != proof.referenceRevision {
				return nil, fmt.Errorf("%w: scoped source %d reference changed during merge authorization", ErrNotReadyToMerge, source.SourceRepoID)
			}
			dependencies = append(dependencies, governance_model.Resource("repository", source.SourceRepoID))
		}
	}
	for _, source := range sources {
		for workflowID, cfg := range source.WorkflowConfigs {
			if cfg == nil || !cfg.Required {
				continue
			}
			valid, err := actions_model.ScopedWorkflowRegistrationValid(ctx, source)
			if err != nil {
				return nil, err
			}
			if !valid {
				return nil, fmt.Errorf("%w: scoped source %d is unavailable or requires reconfirmation", ErrNotReadyToMerge, source.SourceRepoID)
			}
			if source.ConfigRevision < 1 {
				return nil, fmt.Errorf("%w: scoped source %d has no trusted configuration revision", ErrNotReadyToMerge, source.SourceRepoID)
			}
			if len(cfg.Patterns) == 0 {
				return nil, fmt.Errorf("%w: scoped workflow %s has no required job pattern", ErrNotReadyToMerge, workflowID)
			}
			runs, err := db.Find[actions_model.ActionRun](ctx, actions_model.FindRunOptions{
				RepoID: repo.ID, WorkflowRepoID: source.SourceRepoID, WorkflowID: workflowID,
				CommitSHA: head,
			})
			if err != nil {
				return nil, err
			}
			var latest *actions_model.ActionRun
			for _, run := range runs {
				if run.OwnerID != repo.OwnerID || !run.IsScopedRun || run.ScopeInvalidated ||
					run.WorkflowSourceScopeRevision != source.SourceScopeRevision || run.WorkflowCommitSHA == "" ||
					run.ScopedConfigRevisions[strconv.FormatInt(source.ID, 10)] != source.ConfigRevision {
					continue
				}
				latest = run
				break
			}
			if latest == nil {
				return nil, fmt.Errorf("%w: scoped workflow %s from source %d needs a run for commit %s at configuration revision %d", ErrNotReadyToMerge, workflowID, source.SourceRepoID, head, source.ConfigRevision)
			}
			if snapshot != nil && latest.WorkflowCommitSHA != snapshot.sources[source.ID].gitSHA {
				return nil, fmt.Errorf("%w: scoped workflow %s from source %d needs a run at current source commit %s", ErrNotReadyToMerge, workflowID, source.SourceRepoID, snapshot.sources[source.ID].gitSHA)
			}
			if latest.LatestAttemptID != 0 {
				attempt, err := actions_model.GetRunAttemptByRepoAndID(ctx, latest.RepoID, latest.LatestAttemptID)
				if err != nil {
					return nil, err
				}
				if attempt.RunID == latest.ID {
					status := attempt.Status
					if status == actions_model.StatusSuccess {
						status = latest.Status
					}
					switch status {
					case actions_model.StatusWaiting, actions_model.StatusRunning, actions_model.StatusBlocked, actions_model.StatusCancelling:
						return nil, fmt.Errorf("%w: required scoped run is pending", ErrNotReadyToMerge)
					case actions_model.StatusFailure:
						return nil, fmt.Errorf("%w: required scoped run failed", ErrNotReadyToMerge)
					case actions_model.StatusCancelled:
						return nil, fmt.Errorf("%w: required scoped run was cancelled", ErrNotReadyToMerge)
					}
				}
			}
			for _, pattern := range cfg.Patterns {
				matcher, err := glob.Compile(pattern)
				if err != nil {
					return nil, fmt.Errorf("%w: scoped workflow %s from source %d has an invalid required pattern", ErrNotReadyToMerge, workflowID, source.SourceRepoID)
				}
				found, passed, err := scopedRunPatternResult(ctx, latest, matcher)
				if err != nil {
					return nil, err
				}
				if !found || !passed {
					return nil, fmt.Errorf("%w: scoped workflow %s from source %d needs a successful run for commit %s at configuration revision %d", ErrNotReadyToMerge, workflowID, source.SourceRepoID, head, source.ConfigRevision)
				}
			}
			if snapshot != nil {
				trusted, err := scopedRunRunnerProof(ctx, latest)
				if err != nil {
					return nil, err
				}
				if !trusted {
					return nil, fmt.Errorf("%w: scoped workflow %s needs a successful job on a runner managed by its required source scope", ErrNotReadyToMerge, workflowID)
				}
			}
		}
	}
	return dependencies, nil
}

func scopedRunRunnerProof(ctx context.Context, run *actions_model.ActionRun) (bool, error) {
	jobs, err := actions_model.GetRunJobsByRunAndAttemptID(ctx, run.ID, run.LatestAttemptID)
	if err != nil {
		return false, err
	}
	checked, trustedRunners := 0, make(map[int64]bool)
	for _, job := range jobs {
		if job.IsReusableCaller || job.Status != actions_model.StatusSuccess {
			continue
		}
		checked++
		if job.EffectiveTaskID() == 0 {
			return false, nil
		}
		task, err := actions_model.GetTaskByID(ctx, job.EffectiveTaskID())
		if err != nil {
			return false, nil
		}
		if task.RepoID != run.RepoID || task.OwnerID != run.OwnerID || task.Status != actions_model.StatusSuccess || task.RunnerID == 0 {
			return false, nil
		}
		if job.TaskID == 0 {
			original, err := actions_model.GetRunJobByRepoAndID(ctx, run.RepoID, task.JobID)
			if err != nil || original.RunID != run.ID || original.TaskID != task.ID {
				return false, nil
			}
		} else if task.JobID != job.ID {
			return false, nil
		}
		if trusted, seen := trustedRunners[task.RunnerID]; seen {
			if !trusted {
				return false, nil
			}
			continue
		}
		runner, err := actions_model.GetRunnerByID(ctx, task.RunnerID)
		if err != nil {
			return false, nil
		}
		trusted, err := actions_model.RequiredScopedRunnerAllowed(ctx, run, runner)
		if err != nil {
			return false, err
		}
		trustedRunners[task.RunnerID] = trusted
		if !trusted {
			return false, nil
		}
	}
	return checked > 0, nil
}

func scopedRunPatternResult(ctx context.Context, run *actions_model.ActionRun, matcher glob.Glob) (bool, bool, error) {
	if run.LatestAttemptID == 0 {
		return true, false, nil
	}
	attempt, err := actions_model.GetRunAttemptByRepoAndID(ctx, run.RepoID, run.LatestAttemptID)
	if err != nil {
		return false, false, err
	}
	if attempt.RunID != run.ID {
		return true, false, nil
	}
	jobs, err := actions_model.GetRunJobsByRunAndAttemptID(ctx, run.ID, attempt.ID)
	if err != nil {
		return false, false, err
	}
	if len(jobs) == 0 {
		return true, false, nil
	}
	prefix := actions_model.ScopedStatusContextPrefix(ctx, run.WorkflowRepoID)
	legacyPrefix := actions_model.LegacyScopedStatusContextPrefix(ctx, run.WorkflowRepoID)
	matched := false
	passed := run.Status == actions_model.StatusSuccess && attempt.Status == actions_model.StatusSuccess
	for _, job := range jobs {
		contextName := actions_module.ScopedWorkflowStatusContextName(prefix, actions_module.WorkflowDisplayName(run.WorkflowID, job.WorkflowPayload), job.Name, run.TriggerEvent)
		legacyName := actions_module.ScopedWorkflowStatusContextName(legacyPrefix, actions_module.WorkflowDisplayName(run.WorkflowID, job.WorkflowPayload), job.Name, run.TriggerEvent)
		if !matcher.Match(contextName) && (legacyPrefix == "" || !matcher.Match(legacyName)) {
			continue
		}
		matched = true
		if job.Status != actions_model.StatusSuccess {
			passed = false
		}
	}
	return matched, matched && passed, nil
}
