// Copyright 2019 The Gitea Authors.
// All rights reserved.
// SPDX-License-Identifier: MIT

package pull

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/commitstatus"
	"gitea.dev/modules/container"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/glob"
	"gitea.dev/modules/log"
)

// MergeRequiredContextsCommitStatus returns a commit status state for given required contexts
func MergeRequiredContextsCommitStatus(commitStatuses []*git_model.CommitStatus, requiredContexts []string) commitstatus.CommitStatusState {
	if len(commitStatuses) == 0 {
		return commitstatus.CommitStatusPending
	}

	if len(requiredContexts) == 0 {
		return git_model.CalcCommitStatus(commitStatuses).State
	}

	requiredContextsGlob := make(map[string]glob.Glob, len(requiredContexts))
	for _, ctx := range requiredContexts {
		if gp, err := glob.Compile(ctx); err != nil {
			log.Error("glob.Compile %s failed. Error: %v", ctx, err)
		} else {
			requiredContextsGlob[ctx] = gp
		}
	}

	requiredCommitStatuses := make([]*git_model.CommitStatus, 0, len(commitStatuses))
	allRequiredContextsMatched := true
	for _, gp := range requiredContextsGlob {
		requiredContextMatched := false
		for _, commitStatus := range commitStatuses {
			if gp.Match(commitStatus.Context) {
				requiredCommitStatuses = append(requiredCommitStatuses, commitStatus)
				requiredContextMatched = true
			}
		}
		allRequiredContextsMatched = allRequiredContextsMatched && requiredContextMatched
	}
	if len(requiredCommitStatuses) == 0 {
		return commitstatus.CommitStatusPending
	}

	returnedStatus := git_model.CalcCommitStatus(requiredCommitStatuses).State
	if allRequiredContextsMatched {
		return returnedStatus
	}

	if returnedStatus == commitstatus.CommitStatusFailure {
		return commitstatus.CommitStatusFailure
	}
	// even if part of success, return pending
	return commitstatus.CommitStatusPending
}

// IsPullCommitStatusPass returns if all required status checks PASS
func IsPullCommitStatusPass(ctx context.Context, pr *issues_model.PullRequest) (bool, error) {
	pb, err := git_model.GetFirstMatchProtectedBranchRule(ctx, pr.BaseRepoID, pr.BaseBranch)
	if err != nil {
		return false, fmt.Errorf("GetFirstMatchProtectedBranchRule: %w", err)
	}
	if pb == nil || !pb.EnableStatusCheck {
		if err := pr.LoadBaseRepo(ctx); err != nil {
			return false, err
		}
		sources, err := actions_model.GetEffectiveScopedWorkflowSources(ctx, pr.BaseRepo.OwnerID)
		if err != nil {
			return false, err
		}
		required := false
		for _, source := range sources {
			for _, cfg := range source.WorkflowConfigs {
				required = required || cfg != nil && cfg.Required
			}
		}
		if !required {
			return true, nil
		}
	}

	state, err := GetPullRequestCommitStatusState(ctx, pr)
	if err != nil {
		return false, err
	}
	return state.IsSuccess(), nil
}

// GetPullRequestCommitStatusState returns pull request merged commit status state
func GetPullRequestCommitStatusState(ctx context.Context, pr *issues_model.PullRequest) (commitstatus.CommitStatusState, error) {
	// Ensure HeadRepo is loaded
	if err := pr.LoadHeadRepo(ctx); err != nil {
		return "", fmt.Errorf("LoadHeadRepo: %w", err)
	}

	// check if all required status checks are successful
	headGitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, pr.HeadRepo)
	if err != nil {
		return "", fmt.Errorf("OpenRepository: %w", err)
	}
	defer closer.Close()

	if pr.Flow == issues_model.PullRequestFlowGithub {
		if exist, err := git_model.IsBranchExist(ctx, pr.HeadRepo.ID, pr.HeadBranch); err != nil {
			return "", fmt.Errorf("IsBranchExist: %w", err)
		} else if !exist {
			return "", errors.New("Head branch does not exist, can not merge")
		}
	}
	if pr.Flow == issues_model.PullRequestFlowAGit && !gitrepo.IsReferenceExist(ctx, pr.HeadRepo, pr.GetGitHeadRefName()) {
		return "", errors.New("Head branch does not exist, can not merge")
	}

	var sha string
	if pr.Flow == issues_model.PullRequestFlowGithub {
		sha, err = headGitRepo.GetBranchCommitID(pr.HeadBranch)
	} else {
		sha, err = headGitRepo.GetRefCommitID(pr.GetGitHeadRefName())
	}
	if err != nil {
		return "", err
	}

	if err := pr.LoadBaseRepo(ctx); err != nil {
		return "", fmt.Errorf("LoadBaseRepo: %w", err)
	}

	pb, err := git_model.GetFirstMatchProtectedBranchRule(ctx, pr.BaseRepoID, pr.BaseBranch)
	if err != nil {
		return "", fmt.Errorf("LoadProtectedBranch: %w", err)
	}
	scopedSnapshot, err := captureRequiredScopedSourceSnapshot(ctx, pr.BaseRepo)
	if err != nil {
		if errors.Is(err, ErrNotReadyToMerge) {
			return commitstatus.CommitStatusPending, nil
		}
		return "", err
	}
	if _, err := checkRequiredScopedRunsAtSnapshot(ctx, pr.BaseRepo, sha, scopedSnapshot); err != nil {
		if errors.Is(err, ErrNotReadyToMerge) {
			return commitstatus.CommitStatusPending, nil
		}
		return "", err
	}
	if pb == nil || !pb.EnableStatusCheck {
		return commitstatus.CommitStatusSuccess, nil
	}
	commitStatuses, err := git_model.GetLatestCommitStatus(ctx, pr.BaseRepo.ID, sha, db.ListOptionsAll)
	if err != nil {
		return "", fmt.Errorf("GetLatestCommitStatus: %w", err)
	}
	return MergeRequiredContextsCommitStatus(commitStatuses, pb.StatusCheckContexts), nil
}

// EffectiveRequiredContexts returns contexts for status display and scoped workflow filtering, drawn from:
//  1. every required scoped workflow's configured job patterns
//  2. each given protected branch rule's own configured contexts, only when that rule's status check is enabled
//
// Final merge authorization checks scoped runs directly; these patterns are not proof of a run.
// Passing no rule or a single nil rule yields nothing, not even scoped patterns.
// A single rule yields that rule's effective contexts.
// Passing several rules unions their effective contexts; this is used when the governing rule is not yet known.
func EffectiveRequiredContexts(ctx context.Context, repo *repo_model.Repository, pbs ...*git_model.ProtectedBranch) ([]string, error) {
	// No protection rule in effect: nothing is required.
	if len(pbs) == 0 || (len(pbs) == 1 && pbs[0] == nil) {
		return nil, nil
	}

	sources, err := actions_model.GetEffectiveScopedWorkflowSources(ctx, repo.OwnerID)
	if err != nil {
		return nil, fmt.Errorf("GetEffectiveScopedWorkflowSources: %w", err)
	}

	required := make(container.Set[string])

	// Keep scoped patterns for filtered-workflow status creation and settings previews.
	for _, source := range sources {
		for _, cfg := range source.WorkflowConfigs {
			if cfg == nil || !cfg.Required {
				continue
			}
			legacyPrefix := actions_model.LegacyScopedStatusContextPrefix(ctx, source.SourceRepoID)
			if legacyPrefix == "" {
				required.Add(actions_model.ScopedStatusContextPrefix(ctx, source.SourceRepoID) + ": unavailable")
				continue
			}
			for _, pattern := range cfg.Patterns {
				pattern = strings.ReplaceAll(pattern, legacyPrefix, actions_model.ScopedStatusContextPrefix(ctx, source.SourceRepoID))
				required.Add(pattern)
			}
		}
	}

	// Union the configured contexts of every rule whose own status check is enabled (a disabled rule contributes none)
	for _, pb := range pbs {
		if pb == nil || !pb.EnableStatusCheck {
			continue
		}
		required.AddMultiple(pb.StatusCheckContexts...)
	}

	values := required.Values()
	slices.Sort(values) // stable output
	return values, nil
}
