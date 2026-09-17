// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"fmt"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	packages_model "gitea.dev/models/packages"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	repo_module "gitea.dev/modules/repository"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	webhook_module "gitea.dev/modules/webhook"
)

type GroupDeletionOption struct {
	Revision         int64  `json:"revision"`
	ConfirmationPath string `json:"confirmation_path"`
}

func deletionResources(groups []*governance_model.Namespace, repos []*repo_model.Repository) []string {
	resources := make([]string, 0, len(groups)+len(repos))
	for _, group := range groups {
		resources = append(resources, governance_model.Resource("group", group.ID))
	}
	for _, repo := range repos {
		resources = append(resources, governance_model.Resource("repository", repo.ID))
	}
	return resources
}

func checkDeletionAncestors(ctx context.Context, id int64) error {
	chain, err := governance_model.Ancestors(ctx, id)
	if err != nil {
		return err
	}
	for _, n := range chain {
		if n.ID != id && n.DeleteAfter != 0 {
			return governance_model.ErrConflict
		}
	}
	return nil
}

func checkDeletionActor(ctx context.Context, actor governance_model.Actor) error {
	if actor.ActingAsID == 0 {
		return nil
	}
	authenticated, err := activeActor(ctx, actor.ID)
	if err != nil {
		return err
	}
	if !authenticated.IsAdmin {
		return governance_model.ErrForbidden
	}
	return nil
}

func renameDeletionGroup(ctx context.Context, group *governance_model.Namespace, slug, name string, actor governance_model.Actor, groups []*governance_model.Namespace) error {
	if err := governance_model.RenameNamespaceForDeletion(ctx, group.ID, group.Revision, slug, actor, func(context.Context, *governance_model.Namespace, *governance_model.Namespace) error { return nil }); err != nil {
		return err
	}
	for _, previous := range groups {
		n, err := governance_model.GetNamespace(ctx, previous.ID)
		if err != nil {
			return err
		}
		if err := governance_model.SyncNativeNamespacePath(ctx, n); err != nil {
			return err
		}
	}
	_, err := db.GetEngine(ctx).ID(group.ID).Cols("full_name").Update(&user_model.User{FullName: name})
	return err
}

// ScheduleGroupDeletion 只提交可恢复状态；文件删除由到期执行和提交后清理分别处理。
func ScheduleGroupDeletion(ctx context.Context, actor governance_model.Actor, id int64, option GroupDeletionOption) (*GroupState, error) {
	ctx = governance_model.WithAuditActor(ctx, actor)
	err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		group, groups, repos, err := groupArchiveTree(ctx, actor.EffectiveUserID(), id)
		if err != nil {
			return err
		}
		if group.Revision != option.Revision || group.DeleteAfter != 0 || option.ConfirmationPath != group.FullPath {
			return governance_model.ErrConflict
		}
		if err := checkDeletionActor(ctx, actor); err != nil {
			return err
		}
		if err := checkDeletionAncestors(ctx, id); err != nil {
			return err
		}
		doer, err := activeActor(ctx, actor.EffectiveUserID())
		if err != nil {
			return err
		}
		for _, repo := range repos {
			allowed, err := repo_module.CanUserDelete(ctx, repo, doer)
			if err != nil {
				return err
			}
			if !allowed {
				return governance_model.ErrForbidden
			}
		}
		return governance_model.WithWrite(ctx, deletionResources(groups, repos), func(ctx context.Context) error {
			days := setting.Governance.DeletionRetentionDays
			if days < 1 || days > 90 {
				return governance_model.ErrInvalid
			}
			schedule := &governance_model.GroupDeletion{GroupID: id, Actor: actor, DueUnix: time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix(), OriginalSlug: group.Slug, OriginalName: group.Name}
			ids := make([]int64, 0, len(groups))
			for _, n := range groups {
				ids = append(ids, n.ID)
				schedule.Groups = append(schedule.Groups, governance_model.DeletionGroupState{ID: n.ID, Archived: n.Archived, DeleteAfter: n.DeleteAfter, DeleteActorID: n.DeleteActorID})
			}
			for _, repo := range repos {
				schedule.Repositories = append(schedule.Repositories, governance_model.DeletionRepositoryState{ID: repo.ID, Archived: repo.IsArchived, ArchivedUnix: int64(repo.ArchivedUnix)})
				if err := repo_model.SetArchiveRepoState(ctx, repo, true); err != nil {
					return err
				}
				if _, err := actions_model.CancelPreviousJobs(ctx, repo.ID, repo.DefaultBranch, "", webhook_module.HookEventSchedule); err != nil {
					return err
				}
			}
			// 容器包坐标不能因待删除重命名而失效，与固定版本 GitLab 的例外一致。
			container, err := db.GetEngine(ctx).In("owner_id", ids).Where("type = ?", packages_model.TypeContainer).Exist(new(packages_model.Package))
			if err != nil {
				return err
			}
			if !container {
				suffix := fmt.Sprintf("-deletion-%d-%d", id, time.Now().Unix())
				slug := group.Slug[:min(len(group.Slug), 100-len(suffix))] + suffix
				name := string([]rune(group.Name)[:min(len([]rune(group.Name)), 100-len(suffix))]) + suffix
				if err := renameDeletionGroup(ctx, group.Namespace, slug, name, actor, groups); err != nil {
					return err
				}
			}
			if _, err := db.GetEngine(ctx).In("id", ids).Cols("archived", "delete_after", "delete_actor_id").Incr("revision").Update(&governance_model.Namespace{Archived: true, DeleteAfter: schedule.DueUnix, DeleteActorID: actor.EffectiveUserID()}); err != nil {
				return err
			}
			if err := db.Insert(ctx, schedule); err != nil {
				return err
			}
			return groupAudit(ctx, actor, group.Namespace, "group.deletion_scheduled", map[string]any{"due_unix": schedule.DueUnix, "retention_days": days, "groups": len(groups), "repositories": len(repos)})
		})
	})
	if err != nil {
		return nil, err
	}
	return CheckGroupAccess(ctx, actor.EffectiveUserID(), id, governance_model.ReadGroup)
}

func RestoreGroupDeletion(ctx context.Context, actor governance_model.Actor, id int64, option GroupDeletionOption) (*GroupState, error) {
	ctx = governance_model.WithAuditActor(ctx, actor)
	err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		group, groups, repos, err := groupArchiveTree(ctx, actor.EffectiveUserID(), id)
		if err != nil {
			return err
		}
		if group.Revision != option.Revision || option.ConfirmationPath != group.FullPath {
			return governance_model.ErrConflict
		}
		if err := checkDeletionActor(ctx, actor); err != nil {
			return err
		}
		if err := checkDeletionAncestors(ctx, id); err != nil {
			return err
		}
		schedule, has, err := db.GetByID[governance_model.GroupDeletion](ctx, id)
		if err != nil {
			return err
		}
		if !has {
			return governance_model.ErrConflict
		}
		return restoreDeletionState(ctx, actor, group.Namespace, groups, repos, schedule, "owner_requested")
	})
	if err != nil {
		return nil, err
	}
	return CheckGroupAccess(ctx, actor.EffectiveUserID(), id, governance_model.ReadGroup)
}

func restoreDeletionState(ctx context.Context, actor governance_model.Actor, group *governance_model.Namespace, groups []*governance_model.Namespace, repos []*repo_model.Repository, schedule *governance_model.GroupDeletion, reason string) error {
	return governance_model.WithWrite(ctx, deletionResources(groups, repos), func(ctx context.Context) error {
		if group.Slug != schedule.OriginalSlug {
			if err := renameDeletionGroup(ctx, group, schedule.OriginalSlug, schedule.OriginalName, actor, groups); err != nil {
				return err
			}
		}
		for _, saved := range schedule.Groups {
			if _, err := db.GetEngine(ctx).ID(saved.ID).Cols("archived", "delete_after", "delete_actor_id").Incr("revision").Update(&governance_model.Namespace{Archived: saved.Archived, DeleteAfter: saved.DeleteAfter, DeleteActorID: saved.DeleteActorID}); err != nil {
				return err
			}
		}
		for _, saved := range schedule.Repositories {
			// 原生归档入口会拒绝归档父组，故在已锁定整树的恢复事务内准确还原快照。
			if _, err := db.GetEngine(ctx).ID(saved.ID).Cols("is_archived", "archived_unix").Update(&repo_model.Repository{IsArchived: saved.Archived, ArchivedUnix: timeutil.TimeStamp(saved.ArchivedUnix)}); err != nil {
				return err
			}
		}
		if _, err := db.GetEngine(ctx).ID(group.ID).Delete(new(governance_model.GroupDeletion)); err != nil {
			return err
		}
		return groupAudit(ctx, actor, group, "group.restored", map[string]any{"groups": len(groups), "repositories": len(repos), "reason": reason})
	})
}

// ProcessGroupDeletion 在同一事务中复查权限并执行数据库删除；回调禁止删除文件或独立提交。
// actor 为空表示到期任务，否则必须通过当前所有者的路径确认。
func ProcessGroupDeletion(ctx context.Context, id int64, now time.Time, actor *governance_model.Actor, option GroupDeletionOption, remove func(context.Context, []*governance_model.Namespace) error) error {
	if remove == nil {
		return governance_model.ErrInvalid
	}
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		schedule, has, err := db.GetByID[governance_model.GroupDeletion](ctx, id)
		if err != nil {
			return err
		}
		if !has {
			return governance_model.ErrNotFound
		}
		if actor == nil && schedule.DueUnix > now.Unix() {
			return governance_model.ErrConflict
		}
		if err := checkDeletionAncestors(ctx, id); err != nil {
			return err
		}
		initiator := schedule.Actor
		if actor != nil {
			initiator = *actor
		}
		group, groups, repos, authErr := groupArchiveTree(ctx, initiator.EffectiveUserID(), id)
		if authErr == nil {
			authErr = checkDeletionActor(ctx, initiator)
		}
		if authErr != nil {
			if actor != nil || (!errors.Is(authErr, governance_model.ErrNotFound) && !errors.Is(authErr, governance_model.ErrForbidden)) {
				return authErr
			}
			n, err := governance_model.GetNamespace(ctx, id)
			if err != nil {
				return err
			}
			groups, repos, err := groupResourceTree(ctx, n)
			if err != nil {
				return err
			}
			return restoreDeletionState(ctx, governance_model.Actor{Kind: "system", Name: "群组删除任务", Transport: "background"}, n, groups, repos, schedule, "initiator_permission_revoked")
		}
		if actor != nil && (group.Revision != option.Revision || group.FullPath != option.ConfirmationPath) {
			return governance_model.ErrConflict
		}
		doer, err := activeActor(ctx, initiator.EffectiveUserID())
		if err != nil {
			return err
		}
		for _, repo := range repos {
			allowed, err := repo_module.CanUserDelete(ctx, repo, doer)
			if err != nil {
				return err
			}
			if !allowed {
				return governance_model.ErrForbidden
			}
		}
		return governance_model.WithWrite(ctx, deletionResources(groups, repos), func(ctx context.Context) error {
			ctx = governance_model.WithAuditActor(ctx, initiator)
			if err := remove(ctx, groups); err != nil {
				return err
			}
			ids := make([]int64, 0, len(groups))
			for _, group := range groups {
				ids = append(ids, group.ID)
			}
			_, err := db.GetEngine(ctx).In("group_id", ids).Delete(new(governance_model.GroupDeletion))
			return err
		})
	})
}
