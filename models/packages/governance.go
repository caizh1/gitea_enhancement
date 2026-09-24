// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package packages

import (
	"context"
	"errors"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/httplib"
	"gitea.dev/modules/util"
)

type packageTaskUserContextKey struct{}

// PackageTaskUserContextKey 为包协议请求保留经过认证的任务身份。
var PackageTaskUserContextKey = packageTaskUserContextKey{}

// TaskPackageAccessMode 将当前任务、源仓和任务权限上限约束到同一包所有者。
func TaskPackageAccessMode(ctx context.Context, ownerID int64, doer *user_model.User) (perm.AccessMode, error) {
	taskID, ok := user_model.GetActionsUserTaskID(doer)
	if !ok || taskID <= 0 {
		return perm.AccessModeNone, nil
	}
	task, err := actions_model.GetTaskByID(ctx, taskID)
	if errors.Is(err, util.ErrNotExist) {
		return perm.AccessModeNone, nil
	}
	if err != nil {
		return perm.AccessModeNone, err
	}
	valid, err := actions_model.TaskCredentialValid(ctx, task)
	if err != nil || !valid {
		return perm.AccessModeNone, err
	}
	valid, err = access_model.TaskTriggererCanReadSource(ctx, task)
	if err != nil || !valid {
		return perm.AccessModeNone, err
	}
	repo, err := repo_model.GetRepositoryByID(ctx, task.RepoID)
	if err != nil {
		return perm.AccessModeNone, err
	}
	if repo.OwnerID != ownerID {
		return perm.AccessModeNone, nil
	}
	permissions, err := actions_model.ComputeTaskTokenPermissions(ctx, task, repo)
	if err != nil {
		return perm.AccessModeNone, err
	}
	mode := permissions.UnitAccessModes[unit.TypePackages]
	run, err := actions_model.GetRunByRepoAndID(ctx, task.RepoID, task.Job.RunID)
	if err != nil {
		return perm.AccessModeNone, err
	}
	triggerUserID, err := actions_model.TaskEffectiveTriggerUserID(ctx, task.Job, run)
	if err != nil {
		return perm.AccessModeNone, err
	}
	actorMode, err := packageActorAccessMode(ctx, ownerID, triggerUserID)
	if err != nil {
		return perm.AccessModeNone, err
	}
	mode = min(mode, actorMode)
	if task.Status != actions_model.StatusRunning {
		mode = min(mode, perm.AccessModeRead)
	}
	return mode, nil
}

func packageActorAccessMode(ctx context.Context, ownerID, actorID int64) (perm.AccessMode, error) {
	if actorID <= 0 {
		return perm.AccessModeNone, nil
	}
	actor, err := user_model.GetUserByID(ctx, actorID)
	if err != nil {
		return perm.AccessModeNone, err
	}
	if !actor.IsActive || actor.ProhibitLogin || actor.IsGiteaActions() || actor.IsOrganization() {
		return perm.AccessModeNone, nil
	}
	owner, err := user_model.GetUserByID(ctx, ownerID)
	if err != nil {
		return perm.AccessModeNone, err
	}
	if actor.IsAdmin || actor.ID == ownerID {
		return perm.AccessModeWrite, nil
	}
	if !owner.IsOrganization() {
		if owner.Visibility.IsPublic() || owner.Visibility.IsLimited() && !actor.IsRestricted {
			return perm.AccessModeRead, nil
		}
		return perm.AccessModeNone, nil
	}
	abilities, err := organization.GovernanceGroupAbilities(ctx, ownerID, actorID)
	if err != nil {
		return perm.AccessModeNone, err
	}
	mode := perm.AccessModeNone
	if abilities[governance_model.WritePackages] {
		mode = perm.AccessModeWrite
	} else if abilities[governance_model.ReadPackages] {
		mode = perm.AccessModeRead
	}
	teams, err := organization.GetUserOrgTeams(ctx, ownerID, actorID)
	if err != nil {
		return perm.AccessModeNone, err
	}
	for _, team := range teams {
		teamMode := team.UnitAccessMode(ctx, unit.TypePackages)
		if team.HasAdminAccess() {
			teamMode = team.AccessMode
		}
		if teamMode > mode {
			mode = teamMode
		}
	}
	if mode == perm.AccessModeNone && organization.HasOrgOrUserVisible(ctx, owner, actor) {
		mode = perm.AccessModeRead
	}
	return mode, nil
}

func checkPackageWriteActor(ctx context.Context, ownerID, actorID int64) error {
	var mode perm.AccessMode
	var err error
	if actorID == user_model.ActionsUserID {
		doer, ok := ctx.Value(PackageTaskUserContextKey).(*user_model.User)
		if !ok {
			return governance_model.ErrForbidden
		}
		mode, err = TaskPackageAccessMode(ctx, ownerID, doer)
	} else {
		mode, err = packageActorAccessMode(ctx, ownerID, actorID)
	}
	if err != nil {
		return err
	}
	if mode < perm.AccessModeWrite {
		return governance_model.ErrForbidden
	}
	return nil
}

// WithAuthenticatedOwnerWrite 在短事务内复核当前包写权与来源生命周期。
func WithAuthenticatedOwnerWrite(ctx context.Context, ownerID int64, doer *user_model.User, apply func(context.Context) error) error {
	if doer != nil && doer.IsGiteaActions() {
		ctx = context.WithValue(ctx, PackageTaskUserContextKey, doer)
	}
	return withPackageOwnerWrite(ctx, ownerID, func(ctx context.Context) error {
		if doer == nil {
			return governance_model.ErrForbidden
		}
		if requestActor, ok := ctx.Value(governance_model.AuditActorContextKey).(governance_model.Actor); ok && requestActor.EffectiveUserID() != doer.ID {
			return governance_model.ErrForbidden
		}
		if err := checkPackageWriteActor(ctx, ownerID, doer.ID); err != nil {
			return err
		}
		return apply(ctx)
	})
}

// withPackageOwnerWrite 与群组归档、删除共用事务锁，拒绝旧请求在所有者消失后新增软件包引用。
func withPackageOwnerWrite(ctx context.Context, ownerID int64, apply func(context.Context) error) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("group", ownerID)}, func(ctx context.Context) error {
		var owner struct{ ID int64 }
		has, err := db.GetEngine(ctx).Table("user").ID(ownerID).Get(&owner)
		if err != nil {
			return err
		}
		if !has {
			return governance_model.ErrNotFound
		}
		chain, err := governance_model.Ancestors(ctx, ownerID)
		if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
			return err
		}
		for _, n := range chain {
			if n.Archived || n.DeleteAfter != 0 {
				return governance_model.ErrConflict
			}
		}
		if actor, ok := ctx.Value(governance_model.AuditActorContextKey).(governance_model.Actor); ok {
			if err := checkPackageWriteActor(ctx, ownerID, actor.EffectiveUserID()); err != nil {
				return err
			}
		} else if ctx.Value(httplib.RequestContextKey) != nil {
			return governance_model.ErrForbidden
		}
		return apply(ctx)
	})
}

func withPackageWrite(ctx context.Context, packageID int64, apply func(context.Context) error) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		pkg, err := GetPackageByID(ctx, packageID)
		if err != nil {
			return err
		}
		return withPackageOwnerWrite(ctx, pkg.OwnerID, apply)
	})
}

func withPackageVersionWrite(ctx context.Context, versionID int64, apply func(context.Context) error) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		version, err := GetVersionByID(ctx, versionID)
		if err != nil {
			return err
		}
		return withPackageWrite(ctx, version.PackageID, apply)
	})
}

func withPackageReferenceWrite(ctx context.Context, refType PropertyType, refID int64, apply func(context.Context) error) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		switch refType {
		case PropertyTypePackage:
			return withPackageWrite(ctx, refID, apply)
		case PropertyTypeVersion:
			return withPackageVersionWrite(ctx, refID, apply)
		case PropertyTypeFile:
			file, has, err := db.GetByID[PackageFile](ctx, refID)
			if err != nil {
				return err
			}
			if !has {
				return ErrPackageFileNotExist
			}
			return withPackageVersionWrite(ctx, file.VersionID, apply)
		default:
			return governance_model.ErrInvalid
		}
	})
}
