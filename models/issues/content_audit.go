// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package issues

import (
	"context"
	"strconv"

	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/json"
)

// AppendRepositoryContentAudit 以当前仓库快照记录不含正文的公开元数据。
func AppendRepositoryContentAudit(ctx context.Context, eventType string, repoID int64, objectType string, objectID int64, objectPathSuffix string, details map[string]any) error {
	if repoID <= 0 || objectID <= 0 {
		return governance_model.ErrInvalid
	}
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	fromSnapshot := false
	if err != nil {
		repo = repo_model.ContentAuditRepository(ctx, repoID)
		if repo == nil {
			return err
		}
		fromSnapshot = true
	}
	ancestors, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		if err != governance_model.ErrNotFound {
			return err
		}
		ancestors = nil
		if !fromSnapshot {
			if err := repo.LoadOwner(ctx); err != nil {
				return err
			}
			if repo.Owner.IsOrganization() {
				ancestors = []*governance_model.Namespace{{ID: repo.OwnerID, Kind: "group"}}
			}
		}
	}
	ancestorIDs := make([]int64, 0, len(ancestors))
	for _, ancestor := range ancestors {
		if ancestor.Kind == "group" {
			ancestorIDs = append(ancestorIDs, ancestor.ID)
		}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{
		Type: eventType, Actor: governance_model.AuditActor(ctx), ScopeType: "repository", ScopeID: repoID,
		AncestorIDs: ancestorIDs, ObjectType: objectType, ObjectID: objectID,
		ObjectPath: repo.FullPath() + objectPathSuffix, Result: "success", Details: raw,
	})
}

// AppendContentAudit 仅记录内容对象的公开元数据，禁止调用者传入正文。
func AppendContentAudit(ctx context.Context, eventType string, issue *Issue, objectType string, objectID int64, details map[string]any) error {
	if issue == nil || issue.RepoID <= 0 || objectID <= 0 {
		return governance_model.ErrInvalid
	}
	return AppendRepositoryContentAudit(ctx, eventType, issue.RepoID, objectType, objectID, "#"+strconv.FormatInt(issue.Index, 10), details)
}

func appendIssueAudit(ctx context.Context, action string, issue *Issue, details map[string]any) error {
	objectType, objectID, eventType := "issue", issue.ID, "issue."+action
	if issue.IsPull {
		if err := issue.LoadPullRequest(ctx); err != nil {
			return err
		}
		objectType, objectID, eventType = "pull", issue.PullRequest.ID, "pull."+action
	}
	return AppendContentAudit(ctx, eventType, issue, objectType, objectID, details)
}

// AppendIssueUpdatedAudit 按对象真实类型记录 Issue 或 PR 的公开元数据变化。
func AppendIssueUpdatedAudit(ctx context.Context, issue *Issue, details map[string]any) error {
	return appendIssueAudit(ctx, "updated", issue, details)
}

func contentAuditResources(issue *Issue) []string {
	return []string{governance_model.Resource("repository", issue.RepoID), governance_model.Resource("issue", issue.ID)}
}
