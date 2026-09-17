// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	governance_service "gitea.dev/services/governance"
)

// RepositoryTransferImpact 是一次项目转移的确定性预览。确认时仍会在治理写锁内重查这些身份字段。
type RepositoryTransferImpact struct {
	RepositoryID             int64
	SourceOwnerID            int64
	TargetOwnerID            int64
	SourceStatus             repo_model.RepositoryStatus
	SourceName               string
	TargetOwnerName          string
	ImpactFingerprint        string
	OldPath                  string
	NewPath                  string
	RemovedAncestorPaths     []string
	AddedAncestorPaths       []string
	RemovedMembershipSources int64
	AddedMembershipSources   int64
	RemovedShareSources      int64
	AddedShareSources        int64
	RemovedApprovalRules     int64
	AddedApprovalRules       int64
	RemovedApprovalSettings  int64
	AddedApprovalSettings    int64
	RemovedAuditStreams      int64
	AddedAuditStreams        int64
	DirectSharesRetained     int64
	DirectApprovalRules      int64
	CompatibilityAliasKept   bool
}

// PreviewRepositoryTransfer 只返回操作者有权转移且有权看见的来源和目标信息。
func PreviewRepositoryTransfer(ctx context.Context, actorID, repoID, targetOwnerID int64) (*RepositoryTransferImpact, error) {
	var impact *RepositoryTransferImpact
	err := governance_model.WithStableRead(ctx, func(ctx context.Context) error {
		var err error
		impact, err = previewRepositoryTransfer(ctx, actorID, repoID, targetOwnerID)
		return err
	})
	return impact, err
}

func previewRepositoryTransfer(ctx context.Context, actorID, repoID, targetOwnerID int64) (*RepositoryTransferImpact, error) {
	repo, err := governance_service.CheckRepositoryOwnerMutation(ctx, actorID, repoID)
	if err != nil {
		return nil, err
	}
	if err := repo_model.TestRepositoryReadyForTransfer(repo.Status); err != nil {
		return nil, err
	}
	if err := repo.LoadOwner(ctx); err != nil {
		return nil, err
	}
	target, err := user_model.GetUserByID(ctx, targetOwnerID)
	if err != nil {
		return nil, err
	}
	if target.IsOrganization() {
		if _, err := governance_service.CheckGroupAccess(ctx, actorID, target.ID, governance_model.CreateProject); err != nil {
			return nil, governance_model.ErrNotFound
		}
	}
	oldChain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		return nil, err
	}
	newChain, err := governance_model.Ancestors(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	removed, added := transferNamespaceDifference(oldChain, newChain)
	impact := &RepositoryTransferImpact{
		RepositoryID: repo.ID, SourceOwnerID: repo.OwnerID, TargetOwnerID: target.ID,
		SourceStatus: repo.Status, SourceName: repo.Name, TargetOwnerName: target.Name,
		OldPath: repo.FullPath(), NewPath: target.NamespacePath + "/" + repo.Name,
		RemovedAncestorPaths: transferNamespacePaths(removed), AddedAncestorPaths: transferNamespacePaths(added),
		CompatibilityAliasKept: true,
	}
	if target.NamespacePath == "" {
		impact.NewPath = target.Name + "/" + repo.Name
	}
	impact.ImpactFingerprint, err = repositoryTransferFingerprint(ctx, repo.ID, oldChain, newChain)
	if err != nil {
		return nil, err
	}
	if err := fillRepositoryTransferScopes(ctx, impact, removed, added); err != nil {
		return nil, err
	}
	if impact.DirectSharesRetained, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repo.ID).Count(new(governance_model.Share)); err != nil {
		return nil, err
	}
	impact.DirectApprovalRules, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND enabled = ?", "repository", repo.ID, true).Count(new(governance_model.ApprovalRule))
	return impact, err
}

func transferNamespaceDifference(oldChain, newChain []*governance_model.Namespace) (removed, added []*governance_model.Namespace) {
	oldIDs, newIDs := map[int64]bool{}, map[int64]bool{}
	for _, item := range oldChain {
		oldIDs[item.ID] = true
	}
	for _, item := range newChain {
		newIDs[item.ID] = true
	}
	for _, item := range oldChain {
		if !newIDs[item.ID] {
			removed = append(removed, item)
		}
	}
	for _, item := range newChain {
		if !oldIDs[item.ID] {
			added = append(added, item)
		}
	}
	return removed, added
}

func transferNamespacePaths(items []*governance_model.Namespace) []string {
	paths := make([]string, 0, len(items))
	for _, item := range items {
		paths = append(paths, item.FullPath)
	}
	return paths
}

func fillRepositoryTransferScopes(ctx context.Context, impact *RepositoryTransferImpact, removed, added []*governance_model.Namespace) error {
	var err error
	impact.RemovedMembershipSources, impact.RemovedShareSources, impact.RemovedApprovalRules, impact.RemovedApprovalSettings, impact.RemovedAuditStreams, err = countRepositoryTransferScopes(ctx, removed)
	if err != nil {
		return err
	}
	impact.AddedMembershipSources, impact.AddedShareSources, impact.AddedApprovalRules, impact.AddedApprovalSettings, impact.AddedAuditStreams, err = countRepositoryTransferScopes(ctx, added)
	return err
}

func countRepositoryTransferScopes(ctx context.Context, items []*governance_model.Namespace) (memberships, shares, rules, settings, streams int64, err error) {
	if len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if memberships, err = db.GetEngine(ctx).Where("scope_type = ?", "group").In("scope_id", ids).Count(new(governance_model.Membership)); err != nil {
		return
	}
	if shares, err = db.GetEngine(ctx).Where("scope_type = ?", "group").In("scope_id", ids).Count(new(governance_model.Share)); err != nil {
		return
	}
	if rules, err = db.GetEngine(ctx).Where("scope_type = ? AND enabled = ?", "group", true).In("scope_id", ids).Count(new(governance_model.ApprovalRule)); err != nil {
		return
	}
	if settings, err = db.GetEngine(ctx).Where("scope_type = ? AND inherit = ?", "group", false).In("scope_id", ids).Count(new(governance_model.ApprovalSettings)); err != nil {
		return
	}
	streams, err = db.GetEngine(ctx).Where("scope_type = ? AND enabled = ?", "group", true).In("scope_id", ids).Count(new(governance_model.AuditStream))
	return
}

func (impact *RepositoryTransferImpact) Validate(ctx context.Context, repo *repo_model.Repository, target *user_model.User) error {
	if impact == nil || repo.ID != impact.RepositoryID || repo.OwnerID != impact.SourceOwnerID || repo.Name != impact.SourceName || repo.Status != impact.SourceStatus || target.ID != impact.TargetOwnerID || target.Name != impact.TargetOwnerName {
		return fmt.Errorf("%w：项目或目标命名空间已变化，请重新预览", governance_model.ErrConflict)
	}
	oldChain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		return err
	}
	newChain, err := governance_model.Ancestors(ctx, target.ID)
	if err != nil {
		return err
	}
	fingerprint, err := repositoryTransferFingerprint(ctx, repo.ID, oldChain, newChain)
	if err != nil {
		return err
	}
	if fingerprint != impact.ImpactFingerprint {
		return fmt.Errorf("%w：继承权限、共享、审批策略或审计外送已变化，请重新预览", governance_model.ErrConflict)
	}
	return nil
}

func repositoryTransferFingerprint(ctx context.Context, repoID int64, chains ...[]*governance_model.Namespace) (string, error) {
	type version struct {
		ID, Revision int64
		Enabled      bool
	}
	ids, seen := []int64{}, map[int64]bool{}
	namespaces := []*governance_model.Namespace{}
	for _, chain := range chains {
		for _, item := range chain {
			if !seen[item.ID] {
				seen[item.ID] = true
				ids = append(ids, item.ID)
				namespaces = append(namespaces, item)
			}
		}
	}
	memberships, shares := []*governance_model.Membership{}, []*governance_model.Share{}
	rules, settings, streams := []*governance_model.ApprovalRule{}, []*governance_model.ApprovalSettings{}, []*governance_model.AuditStream{}
	for _, scope := range []struct {
		kind string
		id   int64
	}{{"repository", repoID}} {
		if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", scope.kind, scope.id).Asc("id").Find(&memberships); err != nil {
			return "", err
		}
		if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", scope.kind, scope.id).Asc("id").Find(&shares); err != nil {
			return "", err
		}
		if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", scope.kind, scope.id).Asc("id").Find(&rules); err != nil {
			return "", err
		}
		if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", scope.kind, scope.id).Asc("id").Find(&settings); err != nil {
			return "", err
		}
		if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", scope.kind, scope.id).Asc("id").Find(&streams); err != nil {
			return "", err
		}
	}
	if len(ids) > 0 {
		if err := db.GetEngine(ctx).Where("scope_type = ?", "group").In("scope_id", ids).Asc("id").Find(&memberships); err != nil {
			return "", err
		}
		if err := db.GetEngine(ctx).Where("scope_type = ?", "group").In("scope_id", ids).Asc("id").Find(&shares); err != nil {
			return "", err
		}
		if err := db.GetEngine(ctx).Where("scope_type = ?", "group").In("scope_id", ids).Asc("id").Find(&rules); err != nil {
			return "", err
		}
		if err := db.GetEngine(ctx).Where("scope_type = ?", "group").In("scope_id", ids).Asc("id").Find(&settings); err != nil {
			return "", err
		}
		if err := db.GetEngine(ctx).Where("scope_type = ?", "group").In("scope_id", ids).Asc("id").Find(&streams); err != nil {
			return "", err
		}
	}
	streamVersions := make([]version, 0, len(streams))
	for _, item := range streams {
		streamVersions = append(streamVersions, version{item.ID, item.Revision, item.Enabled})
	}
	payload, err := json.Marshal(struct {
		Namespaces  []*governance_model.Namespace
		Memberships []*governance_model.Membership
		Shares      []*governance_model.Share
		Rules       []*governance_model.ApprovalRule
		Settings    []*governance_model.ApprovalSettings
		Streams     []version
	}{namespaces, memberships, shares, rules, settings, streamVersions})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
