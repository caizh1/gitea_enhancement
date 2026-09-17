// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package repo

import (
	"context"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
)

type contentAuditRepositoryKey struct{}

// WithContentAuditRepositorySnapshot 保留删除事务开始前、已在治理锁内核对的仓库路径快照。
func WithContentAuditRepositorySnapshot(ctx context.Context, repo *Repository) context.Context {
	return context.WithValue(ctx, contentAuditRepositoryKey{}, repo)
}

// ContentAuditRepository 返回与指定 ID 匹配的删除前快照。
func ContentAuditRepository(ctx context.Context, repoID int64) *Repository {
	repo, _ := ctx.Value(contentAuditRepositoryKey{}).(*Repository)
	if repo != nil && repo.ID == repoID {
		return repo
	}
	return nil
}

// ContentAuditRepositoryScope 返回治理锁内的仓库路径与祖先快照，供审计事件和延迟清理共同使用。
func ContentAuditRepositoryScope(ctx context.Context, repoID int64) (*Repository, []int64, error) {
	repo, err := GetRepositoryByID(ctx, repoID)
	fromSnapshot := false
	if err != nil {
		repo = ContentAuditRepository(ctx, repoID)
		if repo == nil {
			return nil, nil, err
		}
		fromSnapshot = true
	}
	ancestors, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		if err != governance_model.ErrNotFound {
			return nil, nil, err
		}
		ancestors = nil
		if !fromSnapshot {
			if err := repo.LoadOwner(ctx); err != nil {
				return nil, nil, err
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
	return repo, ancestorIDs, nil
}

// AppendContentAudit 记录仓库内容对象的公开元数据，调用者负责排除正文与秘密。
func AppendContentAudit(ctx context.Context, eventType string, repoID int64, objectType string, objectID int64, suffix string, details map[string]any) error {
	repo, ancestorIDs, err := ContentAuditRepositoryScope(ctx, repoID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{
		Type: eventType, Actor: governance_model.AuditActor(ctx), ScopeType: "repository", ScopeID: repoID,
		AncestorIDs: ancestorIDs, ObjectType: objectType, ObjectID: objectID,
		ObjectPath: repo.FullPath() + suffix, Result: "success", Details: raw,
	})
}
