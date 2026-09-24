// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// ActionScopedWorkflowSource registers a repository as a source of scoped workflows, either for an owner (user/org) or for the whole instance.
type ActionScopedWorkflowSource struct {
	ID int64 `xorm:"pk autoincr"`

	// OwnerID is the scope the source applies to: a user/org ID (applies to that owner's repos), or 0 for instance-level (applies to every repo).
	OwnerID int64 `xorm:"UNIQUE(owner_repo) NOT NULL DEFAULT 0"`
	// SourceRepoID is the source repository providing the workflow files; always non-zero.
	SourceRepoID        int64 `xorm:"INDEX UNIQUE(owner_repo) NOT NULL DEFAULT 0"`
	SourceScopeRevision int64 `xorm:"NOT NULL DEFAULT 0"`
	ConfigRevision      int64 `xorm:"NOT NULL DEFAULT 1"`

	// WorkflowConfigs maps a workflow ID (entry name) to its merge-gate config.
	WorkflowConfigs map[string]*ScopedWorkflowConfig `xorm:"JSON TEXT 'workflow_configs'"`

	CreatedUnix timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
}

// ScopedWorkflowConfig is one scoped workflow's config within a source registration.
type ScopedWorkflowConfig struct {
	Required bool     `json:"required"`
	Patterns []string `json:"patterns"` // the status-check patterns that must be present and pass, only effective when Required is true
}

func init() {
	db.RegisterModel(new(ActionScopedWorkflowSource))
}

// IsWorkflowRequired reports whether the given workflow ID (entry name) is marked required in this source.
func (s *ActionScopedWorkflowSource) IsWorkflowRequired(workflowID string) bool {
	c, ok := s.WorkflowConfigs[workflowID]
	return ok && c.Required
}

type FindScopedWorkflowSourceOpts struct {
	db.ListOptions
	OwnerIDs     []int64
	SourceRepoID int64
}

func (opts FindScopedWorkflowSourceOpts) ToConds() builder.Cond {
	cond := builder.NewCond()
	if len(opts.OwnerIDs) > 0 {
		cond = cond.And(builder.In("owner_id", opts.OwnerIDs))
	}
	if opts.SourceRepoID != 0 {
		cond = cond.And(builder.Eq{"source_repo_id": opts.SourceRepoID})
	}
	return cond
}

// GetEffectiveScopedWorkflowSources returns the scoped-workflow sources effective for a repo owned by repoOwnerID:
// the owner's ancestor sources plus instance-level (owner_id=0) sources.
func GetEffectiveScopedWorkflowSources(ctx context.Context, repoOwnerID int64) ([]*ActionScopedWorkflowSource, error) {
	owners, err := scopedWorkflowOwnerIDs(ctx, repoOwnerID)
	if err != nil {
		return nil, err
	}
	return db.Find[ActionScopedWorkflowSource](ctx, FindScopedWorkflowSourceOpts{OwnerIDs: owners})
}

func scopedWorkflowOwnerIDs(ctx context.Context, repoOwnerID int64) ([]int64, error) {
	owners := []int64{0}
	if repoOwnerID == 0 {
		return owners, nil
	}
	chain, err := governance_model.Ancestors(ctx, repoOwnerID)
	if errors.Is(err, governance_model.ErrNotFound) {
		return append(owners, repoOwnerID), nil // legacy native owner before governance migration
	}
	if err != nil {
		return nil, err
	}
	for _, owner := range chain {
		owners = append(owners, owner.ID)
	}
	return owners, nil
}

// IsScopedWorkflowSourceEffective reports whether sourceRepoID is a scoped-workflow source effective for a repo owned by repoOwnerID.
func IsScopedWorkflowSourceEffective(ctx context.Context, repoOwnerID, sourceRepoID int64) (bool, error) {
	owners, err := scopedWorkflowOwnerIDs(ctx, repoOwnerID)
	if err != nil {
		return false, err
	}
	return db.Exist[ActionScopedWorkflowSource](ctx, FindScopedWorkflowSourceOpts{OwnerIDs: owners, SourceRepoID: sourceRepoID}.ToConds())
}

// ScopedWorkflowSourceValid checks the source's current lifecycle and registration scope.
func ScopedWorkflowSourceValid(ctx context.Context, consumerOwnerID, sourceRepoID int64) (bool, error) {
	sources, err := GetEffectiveScopedWorkflowSources(ctx, consumerOwnerID)
	if err != nil {
		return false, err
	}
	for _, source := range sources {
		if source.SourceRepoID != sourceRepoID {
			continue
		}
		valid, err := ScopedWorkflowRegistrationValid(ctx, source)
		if err != nil || valid {
			return valid, err
		}
	}
	return false, nil
}

// ScopedWorkflowRegistrationValid checks this registration, not another registration of the same source.
func ScopedWorkflowRegistrationValid(ctx context.Context, registration *ActionScopedWorkflowSource) (bool, error) {
	source, err := repo_model.GetRepositoryByID(ctx, registration.SourceRepoID)
	if err != nil {
		if errors.Is(err, util.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if source.IsArchived || source.IsEmpty || source.Status != repo_model.RepositoryReady {
		return false, nil
	}
	active, err := ownerActionsActive(ctx, source.OwnerID)
	if err != nil || !active {
		return false, err
	}
	// Owner-level registrations stop applying when their source moves away.
	return registration.SourceScopeRevision == source.ActionsScopeRevision && (registration.OwnerID == 0 || registration.OwnerID == source.OwnerID), nil
}

// ScopedWorkflowRunValid rejects a run whose original source registration was removed or changed scope.
func ScopedWorkflowRunValid(ctx context.Context, run *ActionRun) (bool, error) {
	if len(run.ScopedConfigRevisions) == 0 {
		return false, nil
	}
	sources, err := GetEffectiveScopedWorkflowSources(ctx, run.OwnerID)
	if err != nil {
		return false, err
	}
	registered := false
	for _, source := range sources {
		if source.SourceRepoID != run.WorkflowRepoID {
			continue
		}
		if _, ok := run.ScopedConfigRevisions[strconv.FormatInt(source.ID, 10)]; !ok {
			continue
		}
		valid, err := ScopedWorkflowRegistrationValid(ctx, source)
		if err != nil {
			return false, err
		}
		if valid {
			registered = true
			break
		}
	}
	if !registered {
		return false, nil
	}
	source, err := repo_model.GetRepositoryByID(ctx, run.WorkflowRepoID)
	if err != nil {
		if errors.Is(err, util.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return source.ActionsScopeRevision == run.WorkflowSourceScopeRevision, nil
}

// IsWorkflowRequiredInSources reports whether workflowID from sourceRepoID is required by any of the given sources.
func IsWorkflowRequiredInSources(sources []*ActionScopedWorkflowSource, sourceRepoID int64, workflowID string) bool {
	for _, s := range sources {
		if s.SourceRepoID == sourceRepoID && s.IsWorkflowRequired(workflowID) {
			return true
		}
	}
	return false
}

// ScopedStatusContextPrefix identifies the source without exposing a private repository name in commit statuses.
func ScopedStatusContextPrefix(_ context.Context, sourceRepoID int64) string {
	return fmt.Sprintf("scoped:%d", sourceRepoID)
}

// LegacyScopedStatusContextPrefix is used only when matching configs saved with the former source-name prefix.
func LegacyScopedStatusContextPrefix(ctx context.Context, sourceRepoID int64) string {
	if sourceRepo, err := repo_model.GetRepositoryByID(ctx, sourceRepoID); err == nil {
		return sourceRepo.FullName()
	}
	return ""
}

// IsScopedWorkflowRequired reports whether workflowID from sourceRepoID is required for a repo owned by consumerOwnerID.
func IsScopedWorkflowRequired(ctx context.Context, consumerOwnerID, sourceRepoID int64, workflowID string) (bool, error) {
	sources, err := GetEffectiveScopedWorkflowSources(ctx, consumerOwnerID)
	if err != nil {
		return false, err
	}
	return IsWorkflowRequiredInSources(sources, sourceRepoID, workflowID), nil
}

// IsScopedWorkflowOptedOutloads the consumer's effective sources then calls ScopedWorkflowOptedOut
func IsScopedWorkflowOptedOut(ctx context.Context, cfg *repo_model.ActionsConfig, consumerOwnerID, sourceRepoID int64, workflowID string) (bool, error) {
	if !cfg.IsScopedWorkflowDisabled(sourceRepoID, workflowID) {
		return false, nil
	}
	sources, err := GetEffectiveScopedWorkflowSources(ctx, consumerOwnerID)
	if err != nil {
		return false, err
	}
	return ScopedWorkflowOptedOut(cfg, sources, sourceRepoID, workflowID), nil
}

// ScopedWorkflowOptedOut reports whether a consumer's opt-out of (sourceRepoID, workflowID) is in effect.
func ScopedWorkflowOptedOut(cfg *repo_model.ActionsConfig, sources []*ActionScopedWorkflowSource, sourceRepoID int64, workflowID string) bool {
	return !IsWorkflowRequiredInSources(sources, sourceRepoID, workflowID) && cfg.IsScopedWorkflowDisabled(sourceRepoID, workflowID)
}

// GetScopedWorkflowSourcesByOwner returns the sources an owner (user/org, or 0 for instance) registered.
func GetScopedWorkflowSourcesByOwner(ctx context.Context, ownerID int64) ([]*ActionScopedWorkflowSource, error) {
	return db.Find[ActionScopedWorkflowSource](ctx, FindScopedWorkflowSourceOpts{OwnerIDs: []int64{ownerID}})
}

// GetScopedWorkflowSource returns the (owner, repo) source registration or a NotExist error.
func GetScopedWorkflowSource(ctx context.Context, ownerID, repoID int64) (*ActionScopedWorkflowSource, error) {
	src := &ActionScopedWorkflowSource{}
	has, err := db.GetEngine(ctx).Where("owner_id = ? AND source_repo_id = ?", ownerID, repoID).Get(src)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, util.NewNotExistErrorf("scoped workflow source (owner %d, repo %d) does not exist", ownerID, repoID)
	}
	return src, nil
}

// AddScopedWorkflowSource registers repoID as a source for ownerID (no-op if already registered).
func AddScopedWorkflowSource(ctx context.Context, ownerID, repoID int64) error {
	source, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return err
	}
	exists, err := db.GetEngine(ctx).Where("owner_id = ? AND source_repo_id = ?", ownerID, repoID).Exist(new(ActionScopedWorkflowSource))
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if err := db.Insert(ctx, &ActionScopedWorkflowSource{OwnerID: ownerID, SourceRepoID: repoID, SourceScopeRevision: source.ActionsScopeRevision, ConfigRevision: 1}); err != nil {
		// Re-check and treat an already-present row as the intended no-op.
		if exists, existErr := db.GetEngine(ctx).Where("owner_id = ? AND source_repo_id = ?", ownerID, repoID).Exist(new(ActionScopedWorkflowSource)); existErr == nil && exists {
			return nil
		}
		return err
	}
	return nil
}

// SetScopedWorkflowSourceConfigs replaces the per-workflow merge-gate configs (workflow ID -> config).
func SetScopedWorkflowSourceConfigs(ctx context.Context, ownerID, repoID int64, configs map[string]*ScopedWorkflowConfig) error {
	_, err := db.GetEngine(ctx).Where("owner_id = ? AND source_repo_id = ?", ownerID, repoID).
		Cols("workflow_configs").Incr("config_revision").
		Update(&ActionScopedWorkflowSource{WorkflowConfigs: configs})
	return err
}

// RemoveScopedWorkflowSource removes the (owner, repo) source registration.
func RemoveScopedWorkflowSource(ctx context.Context, ownerID, repoID int64) error {
	_, err := db.GetEngine(ctx).Where("owner_id = ? AND source_repo_id = ?", ownerID, repoID).Delete(new(ActionScopedWorkflowSource))
	return err
}
