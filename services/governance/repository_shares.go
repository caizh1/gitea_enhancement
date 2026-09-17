// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/json"
)

type RepositorySharesState struct {
	FullPath string                    `json:"full_path"`
	Revision int64                     `json:"revision"`
	Shares   []*governance_model.Share `json:"shares"`
}

func checkRepositoryShareOwner(ctx context.Context, actorID, repoID int64) (*repo_model.Repository, error) {
	doer, err := activeActor(ctx, actorID)
	if err != nil {
		return nil, err
	}
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if repo_model.IsErrRepoNotExist(err) {
		return nil, governance_model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, doer)
	if err != nil {
		return nil, err
	}
	if !permission.IsOwner() {
		return nil, governance_model.ErrNotFound
	}
	return repo, nil
}

func ListRepositoryShares(ctx context.Context, actorID, repoID, afterID int64) (*RepositorySharesState, error) {
	repo, err := checkRepositoryShareOwner(ctx, actorID, repoID)
	if err != nil {
		return nil, err
	}
	if afterID < 0 {
		return nil, governance_model.ErrInvalid
	}
	namespace, err := governance_model.GetNamespace(ctx, repo.OwnerID)
	if err != nil {
		return nil, err
	}
	result := &RepositorySharesState{FullPath: repo.FullPath(), Revision: namespace.Revision, Shares: []*governance_model.Share{}}
	err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND id > ?", "repository", repoID, afterID).Asc("id").Limit(100).Find(&result.Shares)
	return result, err
}

// SetRepositoryShare 使用拥有者命名空间修订号，转移或并发治理变更后必须重新读取预览。
func SetRepositoryShare(ctx context.Context, actor governance_model.Actor, repoID int64, option GroupShareOption, remove bool) error {
	return withActorWrite(ctx, actor, []string{governance_model.Resource("repository", repoID), governance_model.Resource("group", option.GroupID)}, func(ctx context.Context) error {
		repo, err := checkRepositoryShareOwner(ctx, actor.EffectiveUserID(), repoID)
		if err != nil {
			return err
		}
		chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
		if err != nil {
			return err
		}
		if chain[0].Revision != option.Revision || repo.IsArchived {
			return governance_model.ErrConflict
		}
		ancestors := make([]int64, 0, len(chain))
		for _, n := range chain {
			if n.Archived || n.DeleteAfter != 0 {
				return governance_model.ErrConflict
			}
			if n.Kind == "group" {
				ancestors = append(ancestors, n.ID)
			}
		}
		var previous governance_model.Share
		exists, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND group_id = ?", "repository", repoID, option.GroupID).Get(&previous)
		if err != nil {
			return err
		}
		if exists {
			if err := auditGrantExpiry(ctx, previous.ScopeType, previous.ScopeID, previous.ExpiresUnix, "share.expired", previous, time.Now()); err != nil {
				return err
			}
		}
		next := &governance_model.Share{ID: previous.ID, ScopeType: "repository", ScopeID: repoID, GroupID: option.GroupID, MaxRole: option.MaxRole, ExpiresUnix: option.ExpiresUnix}
		if remove {
			if !exists {
				return governance_model.ErrNotFound
			}
			if _, err := db.GetEngine(ctx).ID(previous.ID).Delete(new(governance_model.Share)); err != nil {
				return err
			}
		} else {
			invited, err := CheckGroupAccess(ctx, actor.EffectiveUserID(), option.GroupID, governance_model.ReadGroup)
			if err != nil {
				return err
			}
			if invited.Archived || invited.DeleteAfter != 0 {
				return governance_model.ErrConflict
			}
			if !exists {
				if err := checkExternalShareRestriction(ctx, repo.OwnerID, option.GroupID); err != nil {
					return err
				}
			}
			// 项目共享要求操作者是被邀请群组成员；仅能看到公开组不够。
			member, err := organization.IsOrganizationMember(ctx, option.GroupID, actor.EffectiveUserID())
			if err != nil {
				return err
			}
			if !member {
				return governance_model.ErrNotFound
			}
			if _, err := governance_model.AbilitiesFor(option.MaxRole, nil); err != nil {
				return err
			}
			if option.MaxRole == governance_model.Owner || option.ExpiresUnix != 0 && option.ExpiresUnix <= time.Now().Unix() {
				return governance_model.ErrInvalid
			}
			if exists {
				if _, err := db.GetEngine(ctx).ID(previous.ID).Cols("max_role", "expires_unix").Update(next); err != nil {
					return err
				}
			} else if err := db.Insert(ctx, next); err != nil {
				return err
			}
		}
		if _, err := db.GetEngine(ctx).ID(repo.OwnerID).Incr("revision").Update(new(governance_model.Namespace)); err != nil {
			return err
		}
		details, err := json.Marshal(map[string]any{"before": previous, "after": next, "removed": remove})
		if err != nil {
			return err
		}
		kind := "share.updated"
		if !exists {
			kind = "share.created"
		} else if remove {
			kind = "share.removed"
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: kind, Actor: actor, ScopeType: "repository", ScopeID: repoID, AncestorIDs: ancestors, ObjectType: "repository", ObjectID: repoID, ObjectPath: repo.FullPath(), Result: "success", Details: details})
	})
}
