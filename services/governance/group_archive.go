// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"strings"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
)

type GroupArchiveOption struct {
	Archived bool  `json:"archived"`
	Revision int64 `json:"revision"`
}

type GroupArchiveImpact struct {
	AncestorDeletionID             int64 `json:"ancestor_deletion_id"`
	AncestorArchivedID             int64 `json:"ancestor_archived_id"`
	GroupID                        int64 `json:"group_id"`
	Revision                       int64 `json:"revision"`
	Groups                         int   `json:"groups"`
	Repositories                   int   `json:"repositories"`
	PreviouslyArchivedRepositories int   `json:"previously_archived_repositories"`
}

func groupArchiveTree(ctx context.Context, actorID, id int64) (*GroupState, []*governance_model.Namespace, []*repo_model.Repository, error) {
	group, err := CheckGroupAccess(ctx, actorID, id, governance_model.ManageGroup)
	if err != nil {
		return nil, nil, nil, err
	}
	actor, err := activeActor(ctx, actorID)
	if err != nil {
		return nil, nil, nil, err
	}
	if !actor.IsAdmin && !governance_model.HasOwnerGrant(group.Grants) {
		return nil, nil, nil, governance_model.ErrNotFound
	}
	namespaces, repositories, err := groupResourceTree(ctx, group.Namespace)
	return group, namespaces, repositories, err
}

func groupResourceTree(ctx context.Context, group *governance_model.Namespace) ([]*governance_model.Namespace, []*repo_model.Repository, error) {
	prefix := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(group.LowerPath) + "/%"
	var namespaces []*governance_model.Namespace
	if err := db.GetEngine(ctx).Where("id = ? OR lower_path LIKE ? ESCAPE '!'", group.ID, prefix).Asc("id").Find(&namespaces); err != nil {
		return nil, nil, err
	}
	ids := make([]int64, 0, len(namespaces))
	for _, n := range namespaces {
		ids = append(ids, n.ID)
	}
	var repositories []*repo_model.Repository
	if err := db.GetEngine(ctx).In("owner_id", ids).Asc("id").Find(&repositories); err != nil {
		return nil, nil, err
	}
	return namespaces, repositories, nil
}

func PreviewGroupArchive(ctx context.Context, actorID, id int64) (*GroupArchiveImpact, error) {
	var impact *GroupArchiveImpact
	err := governance_model.WithStableRead(ctx, func(ctx context.Context) error {
		group, namespaces, repositories, err := groupArchiveTree(ctx, actorID, id)
		if err != nil {
			return err
		}
		impact = &GroupArchiveImpact{GroupID: id, Revision: group.Revision, Groups: len(namespaces), Repositories: len(repositories)}
		ancestors, err := governance_model.Ancestors(ctx, id)
		if err != nil {
			return err
		}
		for _, ancestor := range ancestors {
			if ancestor.ID != id && ancestor.Archived && impact.AncestorArchivedID == 0 {
				impact.AncestorArchivedID = ancestor.ID
			}
			if ancestor.ID != id && ancestor.DeleteAfter != 0 {
				impact.AncestorDeletionID = ancestor.ID
			}
		}

		for _, repo := range repositories {
			if repo.IsArchived {
				impact.PreviouslyArchivedRepositories++
			}
		}
		return nil
	})
	return impact, err
}

// SetGroupArchiveState 统一归档或恢复后代；不保留后代原有独立归档标记。
func SetGroupArchiveState(ctx context.Context, actor governance_model.Actor, id int64, option GroupArchiveOption) (*GroupState, error) {
	ctx = governance_model.WithAuditActor(ctx, actor)
	err := withActorWrite(ctx, actor, nil, func(ctx context.Context) error {
		group, namespaces, repositories, err := groupArchiveTree(ctx, actor.EffectiveUserID(), id)
		if err != nil {
			return err
		}
		if group.Revision != option.Revision || group.Archived == option.Archived {
			return governance_model.ErrConflict
		}
		ancestors, err := governance_model.Ancestors(ctx, id)
		if err != nil {
			return err
		}
		for _, ancestor := range ancestors {
			if ancestor.DeleteAfter != 0 || ancestor.ID != id && ancestor.Archived {
				return governance_model.ErrConflict
			}
		}
		resources := make([]string, 0, len(namespaces)+len(repositories))
		ids := make([]int64, 0, len(namespaces))
		for _, n := range namespaces {
			if n.DeleteAfter != 0 {
				return fmt.Errorf("%w：后代群组正在等待删除", governance_model.ErrConflict)
			}
			resources = append(resources, governance_model.Resource("group", n.ID))
			ids = append(ids, n.ID)
		}
		for _, repo := range repositories {
			resources = append(resources, governance_model.Resource("repository", repo.ID))
		}
		return governance_model.WithWrite(ctx, resources, func(ctx context.Context) error {
			// 过渡状态只存在于当前事务，允许共用原生项目归档入口；失败全部回滚。
			if _, err := db.GetEngine(ctx).In("id", ids).Cols("archived").Update(&governance_model.Namespace{Archived: false}); err != nil {
				return err
			}
			previouslyArchived := 0
			for _, repo := range repositories {
				pending, err := db.ExistByID[governance_model.RepositoryDeletion](ctx, repo.ID)
				if err != nil {
					return err
				}
				if pending {
					continue
				} // 独立项目删除计划保留只读状态，不受父组解除归档影响。
				if repo.IsArchived {
					previouslyArchived++
				}
				if err := repo_model.SetArchiveRepoState(ctx, repo, option.Archived); err != nil {
					return err
				}
				if option.Archived {
					// 恢复后重新触发；归档前的未完成任务不能再次领取。
					if err := actions_model.CancelPreviousJobsForRepositoryLifecycle(ctx, repo.ID); err != nil {
						return err
					}
				}
			}
			if _, err := db.GetEngine(ctx).In("id", ids).Cols("archived").Incr("revision").Update(&governance_model.Namespace{Archived: option.Archived}); err != nil {
				return err
			}
			kind := "group.archived"
			if !option.Archived {
				kind = "group.unarchived"
			}
			return groupAudit(ctx, actor, group.Namespace, kind, map[string]any{"archived": option.Archived, "groups": len(namespaces), "repositories": len(repositories), "previously_archived_repositories": previouslyArchived, "descendant_archive_policy": "restore_descendants"})
		})
	})
	if err != nil {
		return nil, err
	}
	return CheckGroupAccess(ctx, actor.EffectiveUserID(), id, governance_model.ReadGroup)
}
