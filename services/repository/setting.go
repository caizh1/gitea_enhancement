// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"net/url"
	"slices"
	"strconv"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/modules/log"
	actions_service "gitea.dev/services/actions"
)

// UpdateRepositoryUnits updates a repository's units
func UpdateRepositoryUnits(ctx context.Context, repo *repo_model.Repository, units []repo_model.RepoUnit, deleteUnitTypes []unit.Type) (err error) {
	return db.WithTx(ctx, func(ctx context.Context) error {
		return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
			before, err := repositoryUnitAuditValues(ctx, repo.ID)
			if err != nil {
				return err
			}
			// Delete existing settings of units before adding again
			for _, u := range units {
				deleteUnitTypes = append(deleteUnitTypes, u.Type)
			}

			if slices.Contains(deleteUnitTypes, unit.TypeActions) {
				if err := actions_service.CleanRepoScheduleTasks(ctx, repo); err != nil {
					log.Error("CleanRepoScheduleTasks: %v", err)
				}
			}

			for _, u := range units {
				if u.Type == unit.TypeActions {
					if err := actions_service.DetectAndHandleSchedules(ctx, repo); err != nil {
						log.Error("DetectAndHandleSchedules: %v", err)
					}
					break
				}
			}

			if _, err = db.GetEngine(ctx).Where("repo_id = ?", repo.ID).In("type", deleteUnitTypes).Delete(new(repo_model.RepoUnit)); err != nil {
				return err
			}

			if len(units) > 0 {
				if err = db.Insert(ctx, units); err != nil {
					return err
				}
			}
			after, err := repositoryUnitAuditValues(ctx, repo.ID)
			if err != nil {
				return err
			}
			return appendRepositoryAudit(ctx, repo, repo, "repository.updated", map[string]any{"units_before": before, "units_after": after})
		})
	})
}

func repositoryUnitAuditValues(ctx context.Context, repoID int64) (map[string]any, error) {
	var units []*repo_model.RepoUnit
	if err := db.GetEngine(ctx).Where("repo_id = ?", repoID).Find(&units); err != nil {
		return nil, err
	}
	values := make(map[string]any, len(units))
	for _, repoUnit := range units {
		value := map[string]any{"anonymous_access": repoUnit.AnonymousAccessMode, "everyone_access": repoUnit.EveryoneAccessMode}
		switch config := repoUnit.Config.(type) {
		case *repo_model.IssuesConfig:
			value["config"] = map[string]any{"time_tracking": config.EnableTimetracker, "contributors_only_time_tracking": config.AllowOnlyContributorsToTrackTime, "dependencies": config.EnableDependencies}
		case *repo_model.PullRequestsConfig:
			value["config"] = config
		case *repo_model.ProjectsConfig:
			value["config"] = map[string]any{"mode": config.GetProjectsMode()}
		case *repo_model.ActionsConfig:
			value["config"] = map[string]any{"token_permission_mode": config.TokenPermissionMode, "max_token_permissions": config.MaxTokenPermissions, "override_owner_config": config.OverrideOwnerConfig, "disabled_workflow_count": len(config.DisabledWorkflows), "disabled_scoped_workflow_count": len(config.DisabledScopedWorkflows), "collaborative_owner_ids": config.CollaborativeOwnerIDs}
		case *repo_model.ExternalWikiConfig:
			value["config"] = map[string]any{"url": safeUnitURL(config.ExternalWikiURL)}
		case *repo_model.ExternalTrackerConfig:
			value["config"] = map[string]any{"url": safeUnitURL(config.ExternalTrackerURL), "format": config.ExternalTrackerFormat, "style": config.ExternalTrackerStyle, "regexp": config.ExternalTrackerRegexpPattern}
		}
		values[strconv.Itoa(int(repoUnit.Type))] = value
	}
	return values, nil
}

func safeUnitURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
	return parsed.String()
}
