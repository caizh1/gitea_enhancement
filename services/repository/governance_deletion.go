// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
)

type DeletionOption struct {
	ConfirmationPath string `json:"confirmation_path"`
	// DueUnix 绑定具体删除计划，旧页面不能永久删除后来重新计划的项目。
	DueUnix int64 `json:"due_unix"`
}

func repositoryDeletionAncestors(ctx context.Context, repo *repo_model.Repository) ([]int64, error) {
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, group := range chain {
		if group.DeleteAfter != 0 {
			return nil, governance_model.ErrConflict
		}
		if group.Kind == "group" {
			ids = append(ids, group.ID)
		}
	}
	return ids, nil
}

func repositoryDeletionAudit(ctx context.Context, actor governance_model.Actor, repo *repo_model.Repository, kind string, ancestors []int64, details map[string]any) error {
	data, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: kind, Actor: actor, ScopeType: "repository", ScopeID: repo.ID, AncestorIDs: ancestors, ObjectType: "repository", ObjectID: repo.ID, ObjectPath: repo.FullPath(), Result: "success", Details: data})
}

func renameDeletionRepository(ctx context.Context, repo *repo_model.Repository, name string) error {
	ownerPath, err := governance_model.ChangeRepositoryLifecyclePath(ctx, repo.ID, repo.OwnerID, name)
	if err != nil {
		return err
	}
	if repo.GovernanceStorageName == "" {
		repo.GovernanceStorageName = repo.LowerName
	}
	repo.Name, repo.LowerName, repo.OwnerNamespace = name, strings.ToLower(name), ownerPath
	// 生命周期入口已经核对并锁定，普通改名入口会正确拒绝归档项目。
	_, err = db.GetEngine(ctx).ID(repo.ID).Cols("name", "lower_name", "owner_namespace", "governance_storage_name").Update(repo)
	return err
}

func ScheduleRepositoryDeletion(ctx context.Context, actor governance_model.Actor, id int64, option DeletionOption) (*governance_model.RepositoryDeletion, error) {
	ctx = governance_model.WithAuditActor(ctx, actor)
	var schedule *governance_model.RepositoryDeletion
	err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", id)}, func(ctx context.Context) error {
		repo, err := repo_model.GetRepositoryByID(ctx, id)
		if err != nil {
			return err
		}
		if err := checkRepositoryDeletion(ctx, deletionAuthority{UserID: actor.EffectiveUserID(), OwnerID: repo.OwnerID}, repo); err != nil {
			return err
		}
		if option.ConfirmationPath != repo.FullPath() {
			return governance_model.ErrConflict
		}
		ancestors, err := repositoryDeletionAncestors(ctx, repo)
		if err != nil {
			return err
		}
		exists, err := db.ExistByID[governance_model.RepositoryDeletion](ctx, id)
		if err != nil {
			return err
		}
		if exists {
			return governance_model.ErrConflict
		}
		days := setting.Governance.DeletionRetentionDays
		if days < 1 || days > 90 {
			return governance_model.ErrInvalid
		}
		schedule = &governance_model.RepositoryDeletion{RepositoryID: id, OwnerID: repo.OwnerID, Actor: actor, OriginalName: repo.Name, Archived: repo.IsArchived, ArchivedUnix: int64(repo.ArchivedUnix), DueUnix: time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix()}
		if err := repo_model.SetArchiveRepoState(ctx, repo, true); err != nil {
			return err
		}
		if err := actions_model.CancelPreviousJobsForRepositoryLifecycle(ctx, id); err != nil {
			return err
		}
		suffix := fmt.Sprintf("-deletion-%d-%d", id, time.Now().UnixNano())
		name := string([]rune(repo.Name)[:min(len([]rune(repo.Name)), 100-len(suffix))]) + suffix
		if err := renameDeletionRepository(ctx, repo, name); err != nil {
			return err
		}
		if err := db.Insert(ctx, schedule); err != nil {
			return err
		}
		return repositoryDeletionAudit(ctx, actor, repo, "repository.deletion_scheduled", ancestors, map[string]any{"due_unix": schedule.DueUnix, "retention_days": days, "original_name": schedule.OriginalName})
	})
	return schedule, err
}

func restoreRepositoryDeletion(ctx context.Context, actor governance_model.Actor, repo *repo_model.Repository, schedule *governance_model.RepositoryDeletion, ancestors []int64, reason string) error {
	if err := renameDeletionRepository(ctx, repo, schedule.OriginalName); err != nil {
		return err
	}
	repo.IsArchived, repo.ArchivedUnix = schedule.Archived, timeutil.TimeStamp(schedule.ArchivedUnix)
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		return err
	}
	for _, parent := range chain {
		if parent.Archived {
			repo.IsArchived = true
			if repo.ArchivedUnix == 0 {
				repo.ArchivedUnix = timeutil.TimeStampNow()
			}
		}
	}
	if _, err := db.GetEngine(ctx).ID(repo.ID).Cols("is_archived", "archived_unix").Update(repo); err != nil {
		return err
	}
	if _, err := db.GetEngine(ctx).ID(repo.ID).Delete(new(governance_model.RepositoryDeletion)); err != nil {
		return err
	}
	return repositoryDeletionAudit(ctx, actor, repo, "repository.restored", ancestors, map[string]any{"reason": reason})
}

func RestoreRepositoryDeletion(ctx context.Context, actor governance_model.Actor, id int64, option DeletionOption) error {
	ctx = governance_model.WithAuditActor(ctx, actor)
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", id)}, func(ctx context.Context) error {
		repo, err := repo_model.GetRepositoryByID(ctx, id)
		if err != nil {
			return err
		}
		if err := checkRepositoryDeletion(ctx, deletionAuthority{UserID: actor.EffectiveUserID(), OwnerID: repo.OwnerID}, repo); err != nil {
			return err
		}
		ancestors, err := repositoryDeletionAncestors(ctx, repo)
		if err != nil {
			return err
		}
		schedule, has, err := db.GetByID[governance_model.RepositoryDeletion](ctx, id)
		if err != nil {
			return err
		}
		if !has {
			return governance_model.ErrNotFound
		}
		if option.ConfirmationPath != repo.FullPath() || option.DueUnix != schedule.DueUnix {
			return governance_model.ErrConflict
		}
		return restoreRepositoryDeletion(ctx, actor, repo, schedule, ancestors, "owner_requested")
	})
}

// DeleteScheduledRepository 在提交后清理正文，恢复未知结果不会重复删除新引用。
func DeleteScheduledRepository(ctx context.Context, id int64, now time.Time, actor *governance_model.Actor, option DeletionOption) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", id)}, func(ctx context.Context) error {
		schedule, has, err := db.GetByID[governance_model.RepositoryDeletion](ctx, id)
		if err != nil {
			return err
		}
		if !has {
			return governance_model.ErrNotFound
		}
		if actor == nil && schedule.DueUnix > now.Unix() {
			return governance_model.ErrConflict
		}
		repo, err := repo_model.GetRepositoryByID(ctx, id)
		if err != nil {
			return err
		}
		ancestors, err := repositoryDeletionAncestors(ctx, repo)
		if err != nil {
			return err
		}
		initiator := schedule.Actor
		if actor != nil {
			initiator = *actor
		}
		ctx = governance_model.WithAuditActor(ctx, initiator)
		authority := deletionAuthority{UserID: initiator.EffectiveUserID(), OwnerID: schedule.OwnerID}
		if err := checkRepositoryDeletion(ctx, authority, repo); err != nil {
			if actor != nil || (!errors.Is(err, governance_model.ErrForbidden) && !user_model.IsErrUserNotExist(err)) {
				return err
			}
			return restoreRepositoryDeletion(ctx, governance_model.Actor{Kind: "system", Name: "项目删除任务", Transport: "background"}, repo, schedule, ancestors, "initiator_permission_revoked")
		}
		if actor != nil && (option.DueUnix != schedule.DueUnix || option.ConfirmationPath != repo.FullPath()) {
			return governance_model.ErrConflict
		}
		// 使用已通过最终权限检查的原生数据库删除，避免提前发送不可回滚通知。
		ctx = context.WithValue(ctx, deletionAuthorityKey{}, authority)
		return DeleteRepositoryDirectly(ctx, id)
	})
}

func RunScheduledRepositoryDeletions(ctx context.Context) error {
	var schedules []*governance_model.RepositoryDeletion
	now := time.Now()
	if err := db.GetEngine(ctx).Where("due_unix <= ? AND next_attempt_unix <= ?", now.Unix(), now.Unix()).Asc("due_unix").Limit(100).Find(&schedules); err != nil {
		return err
	}
	var failures []error
	for _, schedule := range schedules {
		err := DeleteScheduledRepository(ctx, schedule.RepositoryID, now, nil, DeletionOption{})
		if err == nil || errors.Is(err, governance_model.ErrNotFound) {
			continue
		}
		_, saveErr := db.GetEngine(ctx).ID(schedule.RepositoryID).Cols("next_attempt_unix").Incr("failures").Update(&governance_model.RepositoryDeletion{NextAttemptUnix: now.Add(time.Minute).Unix()})
		if saveErr != nil {
			failures = append(failures, saveErr)
		}
		if !errors.Is(err, governance_model.ErrConflict) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// RepositoryDeletionState 只展示经当前删除权限核对的状态，不暴露原始审计身份快照。
type DeletionState struct {
	RepositoryID       int64                                `json:"repository_id"`
	FullPath           string                               `json:"full_path"`
	Archived           bool                                 `json:"archived"`
	AncestorDeletionID int64                                `json:"ancestor_deletion_id"`
	Pending            *governance_model.RepositoryDeletion `json:"pending"`
	RetentionDays      int                                  `json:"retention_days"`
}

func GetRepositoryDeletionState(ctx context.Context, actor governance_model.Actor, id int64) (*DeletionState, error) {
	ctx = governance_model.WithAuditActor(ctx, actor)
	var state *DeletionState
	err := governance_model.WithStableRead(ctx, func(ctx context.Context) error {
		repo, err := repo_model.GetRepositoryByID(ctx, id)
		if err != nil {
			return err
		}
		if err := checkRepositoryDeletion(ctx, deletionAuthority{UserID: actor.EffectiveUserID(), OwnerID: repo.OwnerID}, repo); err != nil {
			return err
		}
		state = &DeletionState{RepositoryID: id, FullPath: repo.FullPath(), Archived: repo.IsArchived, RetentionDays: setting.Governance.DeletionRetentionDays}
		chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
		if err != nil {
			return err
		}
		for _, group := range chain {
			if group.DeleteAfter != 0 {
				state.AncestorDeletionID = group.ID
			}
		}
		pending, has, err := db.GetByID[governance_model.RepositoryDeletion](ctx, id)
		if err != nil {
			return err
		}
		if has {
			state.Pending = pending
		}
		return nil
	})
	return state, err
}
