// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"

	governance_model "gitea.dev/models/governance"
)

// RequiredScopedRunnerAllowed reports whether a runner may execute this scoped run.
func RequiredScopedRunnerAllowed(ctx context.Context, run *ActionRun, runner *ActionRunner) (bool, error) {
	if run == nil || runner == nil {
		return false, nil
	}
	if !run.IsScopedRun {
		return true, nil
	}
	sources, err := GetEffectiveScopedWorkflowSources(ctx, run.OwnerID)
	if err != nil {
		return false, err
	}
	for _, source := range sources {
		if source.SourceRepoID != run.WorkflowRepoID || !source.IsWorkflowRequired(run.WorkflowID) {
			continue
		}
		if runner.RepoID != 0 || source.OwnerID == 0 && runner.OwnerID != 0 {
			return false, nil
		}
		if runner.OwnerID == 0 || runner.OwnerID == source.OwnerID {
			continue
		}
		chain, err := governance_model.Ancestors(ctx, source.OwnerID)
		if errors.Is(err, governance_model.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		trusted := false
		for _, ancestor := range chain {
			trusted = trusted || ancestor.ID == runner.OwnerID
		}
		if !trusted {
			return false, nil
		}
	}
	return true, nil
}
