// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	secret_model "gitea.dev/models/secret"
	webhook_model "gitea.dev/models/webhook"
	"gitea.dev/modules/json"
)

// LifecycleSourceImpact exposes only source counts; credential values and webhook endpoints stay internal.
type LifecycleSourceImpact struct {
	SourceID          int64  `json:"source_id"`
	SourcePath        string `json:"source_path,omitempty"`
	Restricted        bool   `json:"restricted"`
	Runners           int    `json:"runners,omitempty"`
	Variables         int    `json:"variables,omitempty"`
	Secrets           int    `json:"secrets,omitempty"`
	Hooks             int    `json:"hooks,omitempty"`
	Labels            int    `json:"labels,omitempty"`
	BranchProtections int    `json:"branch_protections,omitempty"`
	WorkflowSources   int    `json:"workflow_sources,omitempty"`
	RequiredWorkflows int    `json:"required_workflows,omitempty"`
}

// LifecycleSourcePaths keeps hidden source names out of existing path summaries.
func LifecycleSourcePaths(sources []LifecycleSourceImpact) []string {
	paths := make([]string, 0, len(sources))
	for _, source := range sources {
		if source.Restricted {
			paths = append(paths, fmt.Sprintf("受限来源 #%d", source.SourceID))
		} else {
			paths = append(paths, source.SourcePath)
		}
	}
	return paths
}

// LifecycleSourceImpacts returns display-safe source changes and a separate opaque digest for commit revalidation.
func LifecycleSourceImpacts(ctx context.Context, actorID int64, groups []*governance_model.Namespace) ([]LifecycleSourceImpact, string, error) {
	result := make([]LifecycleSourceImpact, 0, len(groups))
	hashes := make([]string, 0, len(groups))
	for _, group := range groups {
		item, hash, err := lifecycleSourceImpact(ctx, actorID, group)
		if err != nil {
			return nil, "", err
		}
		result = append(result, item)
		hashes = append(hashes, hash)
	}
	encoded, err := json.Marshal(hashes)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(encoded)
	return result, hex.EncodeToString(sum[:]), nil
}

func lifecycleSourceImpact(ctx context.Context, actorID int64, group *governance_model.Namespace) (LifecycleSourceImpact, string, error) {
	id := group.ID
	e := db.GetEngine(ctx)
	var runners []*actions_model.ActionRunner
	var variables []*actions_model.ActionVariable
	var secrets []*secret_model.Secret
	var hooks []*webhook_model.Webhook
	var labels []*issues_model.Label
	var branches []*git_model.GroupProtectedBranch
	var workflows []*actions_model.ActionScopedWorkflowSource
	for _, query := range []struct {
		where string
		rows  any
	}{
		{"owner_id = ? AND repo_id = 0", &runners},
		{"owner_id = ? AND repo_id = 0", &variables},
		{"owner_id = ? AND repo_id = 0", &secrets},
		{"owner_id = ? AND repo_id = 0", &hooks},
		{"org_id = ? AND repo_id = 0", &labels},
		{"group_id = ?", &branches},
		{"owner_id = ?", &workflows},
	} {
		if err := e.Where(query.where, id).Asc("id").Find(query.rows); err != nil {
			return LifecycleSourceImpact{}, "", err
		}
	}
	item := LifecycleSourceImpact{SourceID: id}
	canManage := group.Kind == "user" && actorID == id
	if group.Kind == "user" {
		if actorID != id {
			item.Restricted = true
			return item, lifecycleImpactDigest(item), nil
		}
		item.SourcePath = group.FullPath
	} else {
		visible, err := CheckGroupAccess(ctx, actorID, id, governance_model.ReadGroup)
		if errors.Is(err, governance_model.ErrNotFound) {
			item.Restricted = true
			return item, lifecycleImpactDigest(item), nil
		}
		if err != nil {
			return LifecycleSourceImpact{}, "", err
		}
		item.SourcePath = visible.FullPath
		_, err = CheckGroupAccess(ctx, actorID, id, governance_model.ManageGroup)
		if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
			return LifecycleSourceImpact{}, "", err
		}
		canManage = err == nil
	}
	item = fillVisibleLifecycleSource(item, runners, variables, secrets, hooks, labels, branches, workflows)
	if !canManage {
		return item, lifecycleImpactDigest(item), nil
	}
	// Only a source manager's digest includes configuration details. Read-only previews cannot probe hidden changes.
	for _, runner := range runners {
		runner.LastOnline, runner.LastActive, runner.Updated = 0, 0, 0
	}
	for _, hook := range hooks {
		hook.LastStatus = 0
	}
	for _, label := range labels {
		label.NumIssues, label.NumClosedIssues = 0, 0
	}
	encoded, err := json.Marshal(struct {
		Group     *governance_model.Namespace
		Runners   []*actions_model.ActionRunner
		Variables []*actions_model.ActionVariable
		Secrets   []*secret_model.Secret
		Hooks     []*webhook_model.Webhook
		Labels    []*issues_model.Label
		Branches  []*git_model.GroupProtectedBranch
		Workflows []*actions_model.ActionScopedWorkflowSource
	}{group, runners, variables, secrets, hooks, labels, branches, workflows})
	if err != nil {
		return LifecycleSourceImpact{}, "", err
	}
	sum := sha256.Sum256(encoded)
	return item, hex.EncodeToString(sum[:]), nil
}

func lifecycleImpactDigest(item LifecycleSourceImpact) string {
	encoded, _ := json.Marshal(item)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func fillVisibleLifecycleSource(item LifecycleSourceImpact, runners []*actions_model.ActionRunner, variables []*actions_model.ActionVariable, secrets []*secret_model.Secret, hooks []*webhook_model.Webhook, labels []*issues_model.Label, branches []*git_model.GroupProtectedBranch, workflows []*actions_model.ActionScopedWorkflowSource) LifecycleSourceImpact {
	item.Runners, item.Variables, item.Secrets = len(runners), len(variables), len(secrets)
	item.Hooks, item.Labels, item.BranchProtections, item.WorkflowSources = len(hooks), len(labels), len(branches), len(workflows)
	for _, source := range workflows {
		for _, config := range source.WorkflowConfigs {
			if config != nil && config.Required {
				item.RequiredWorkflows++
			}
		}
	}
	return item
}
