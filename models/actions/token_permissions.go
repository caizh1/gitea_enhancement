// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/perm"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
)

func ownerTokenPolicies(ctx context.Context, ownerID int64) ([]OwnerActionsConfig, repo_model.ActionsTokenPermissions, error) {
	chain, err := governance_model.Ancestors(ctx, ownerID)
	if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
		return nil, repo_model.ActionsTokenPermissions{}, err
	}
	ownerIDs := []int64{ownerID} // Legacy installations may not have namespace rows yet.
	if err == nil {
		ownerIDs = make([]int64, 0, len(chain))
		for _, namespace := range chain {
			ownerIDs = append(ownerIDs, namespace.ID)
		}
	}

	policies := make([]OwnerActionsConfig, 0, len(ownerIDs))
	defaultPermissions := repo_model.MakeActionsTokenPermissions(perm.AccessModeWrite)
	hasDefault := false
	for _, id := range ownerIDs {
		raw, err := user_model.GetUserSetting(ctx, id, user_model.SettingsKeyActionsConfig)
		if err != nil {
			return nil, repo_model.ActionsTokenPermissions{}, err
		}
		var cfg OwnerActionsConfig
		if err := cfg.FromDB([]byte(raw)); err != nil {
			return nil, repo_model.ActionsTokenPermissions{}, err
		}
		policies = append(policies, cfg)
		if hasDefault || raw == "" {
			continue
		}
		var declared struct {
			TokenPermissionMode repo_model.ActionsTokenPermissionMode `json:"token_permission_mode"`
		}
		if err := json.Unmarshal([]byte(raw), &declared); err != nil {
			return nil, repo_model.ActionsTokenPermissions{}, err
		}
		if declared.TokenPermissionMode == repo_model.ActionsTokenPermissionModePermissive || declared.TokenPermissionMode == repo_model.ActionsTokenPermissionModeRestricted {
			defaultPermissions = cfg.GetDefaultTokenPermissions()
			hasDefault = true
		}
	}
	return policies, defaultPermissions, nil
}

// ClampTaskTokenPermissionsForRepo applies the repository and every owner ancestor's absolute ceilings.
func ClampTaskTokenPermissionsForRepo(ctx context.Context, permissions repo_model.ActionsTokenPermissions, repo *repo_model.Repository) (repo_model.ActionsTokenPermissions, error) {
	policies, _, err := ownerTokenPolicies(ctx, repo.OwnerID)
	if err != nil {
		return repo_model.ActionsTokenPermissions{}, err
	}
	permissions = repo.MustGetUnit(ctx, unit.TypeActions).ActionsConfig().ClampPermissions(permissions)
	for _, policy := range policies {
		permissions = policy.ClampPermissions(permissions)
	}
	return permissions, nil
}

// ComputeTaskTokenPermissions computes the effective permissions for a job token against the target repository.
// It uses the job's stored permissions (if any), then applies org/repo clamps and fork/cross-repo restrictions.
// Note: target repository access policy checks are enforced in GetActionsUserRepoPermission; this function only computes the job token's effective permission ceiling.
func ComputeTaskTokenPermissions(ctx context.Context, task *ActionTask, targetRepo *repo_model.Repository) (ret repo_model.ActionsTokenPermissions, err error) {
	if err := task.LoadJob(ctx); err != nil {
		return ret, err
	}
	if err := task.Job.LoadRepo(ctx); err != nil {
		return ret, err
	}
	runRepo := task.Job.Repo

	repoActionsCfg := runRepo.MustGetUnit(ctx, unit.TypeActions).ActionsConfig()
	policies, inheritedDefault, err := ownerTokenPolicies(ctx, runRepo.OwnerID)
	if err != nil {
		return ret, err
	}

	var jobDeclaredPerms repo_model.ActionsTokenPermissions
	if task.Job.TokenPermissions != nil {
		jobDeclaredPerms = *task.Job.TokenPermissions
	} else if repoActionsCfg.OverrideOwnerConfig {
		jobDeclaredPerms = repoActionsCfg.GetDefaultTokenPermissions()
	} else {
		jobDeclaredPerms = inheritedDefault
	}

	effectivePerms := repoActionsCfg.ClampPermissions(jobDeclaredPerms)
	for _, policy := range policies {
		effectivePerms = policy.ClampPermissions(effectivePerms)
	}

	// Cross-repository access and fork pull requests are strictly read-only for security.
	// This ensures a "task repo" cannot gain write access to other repositories via CrossRepoAccess settings.
	isSameRepo := task.Job.RepoID == targetRepo.ID
	restrictCrossRepoAccess := task.IsForkPullRequest || !isSameRepo
	if restrictCrossRepoAccess {
		effectivePerms = repo_model.ClampActionsTokenPermissions(effectivePerms, repo_model.MakeRestrictedPermissions())
	}

	return effectivePerms, nil
}
