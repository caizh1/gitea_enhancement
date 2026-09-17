// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"context"
	"fmt"

	auth_model "gitea.dev/models/auth"
	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
	password_module "gitea.dev/modules/auth/password"
	"gitea.dev/modules/optional"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/structs"
	"gitea.dev/modules/util"
)

type UpdateOptionField[T any] struct {
	FieldValue T
	FromSync   bool
}

func UpdateOptionFieldFromValue[T any](value T) optional.Option[UpdateOptionField[T]] {
	return optional.Some(UpdateOptionField[T]{FieldValue: value})
}

func UpdateOptionFieldFromSync[T any](value T) optional.Option[UpdateOptionField[T]] {
	return optional.Some(UpdateOptionField[T]{FieldValue: value, FromSync: true})
}

func UpdateOptionFieldFromPtr[T any](value *T) optional.Option[UpdateOptionField[T]] {
	if value == nil {
		return optional.None[UpdateOptionField[T]]()
	}
	return UpdateOptionFieldFromValue(*value)
}

type UpdateOptions struct {
	KeepEmailPrivate             optional.Option[bool]
	FullName                     optional.Option[string]
	Website                      optional.Option[string]
	Location                     optional.Option[string]
	Description                  optional.Option[string]
	AllowGitHook                 optional.Option[bool]
	AllowImportLocal             optional.Option[bool]
	MaxRepoCreation              optional.Option[int]
	IsRestricted                 optional.Option[bool]
	IsAuditor                    optional.Option[bool]
	Visibility                   optional.Option[structs.VisibleType]
	KeepActivityPrivate          optional.Option[bool]
	Language                     optional.Option[string]
	Theme                        optional.Option[string]
	DiffViewStyle                optional.Option[string]
	AllowCreateOrganization      optional.Option[bool]
	IsActive                     optional.Option[bool]
	IsAdmin                      optional.Option[UpdateOptionField[bool]]
	EmailNotificationsPreference optional.Option[string]
	SetLastLogin                 bool
	RepoAdminChangeTeamAccess    optional.Option[bool]
}

func UpdateUser(ctx context.Context, u *user_model.User, opts *UpdateOptions) error {
	cols := make([]string, 0, 20)
	if opts.IsAuditor.Has() {
		u.IsAuditor = opts.IsAuditor.Value()
		cols = append(cols, "is_auditor")
	}

	if opts.KeepEmailPrivate.Has() {
		u.KeepEmailPrivate = opts.KeepEmailPrivate.Value()

		cols = append(cols, "keep_email_private")
	}

	if opts.FullName.Has() {
		u.FullName = opts.FullName.Value()

		cols = append(cols, "full_name")
	}
	if opts.Website.Has() {
		u.Website = opts.Website.Value()

		cols = append(cols, "website")
	}
	if opts.Location.Has() {
		u.Location = opts.Location.Value()

		cols = append(cols, "location")
	}
	if opts.Description.Has() {
		u.Description = opts.Description.Value()

		cols = append(cols, "description")
	}
	if opts.Language.Has() {
		u.Language = opts.Language.Value()

		cols = append(cols, "language")
	}
	if opts.Theme.Has() {
		u.Theme = opts.Theme.Value()

		cols = append(cols, "theme")
	}
	if opts.DiffViewStyle.Has() {
		u.DiffViewStyle = opts.DiffViewStyle.Value()

		cols = append(cols, "diff_view_style")
	}

	if opts.AllowGitHook.Has() {
		u.AllowGitHook = opts.AllowGitHook.Value()

		cols = append(cols, "allow_git_hook")
	}
	if opts.AllowImportLocal.Has() {
		u.AllowImportLocal = opts.AllowImportLocal.Value()

		cols = append(cols, "allow_import_local")
	}

	if opts.MaxRepoCreation.Has() {
		u.MaxRepoCreation = opts.MaxRepoCreation.Value()

		cols = append(cols, "max_repo_creation")
	}

	if opts.IsActive.Has() {
		u.IsActive = opts.IsActive.Value()

		cols = append(cols, "is_active")
	}
	if opts.IsRestricted.Has() {
		u.IsRestricted = opts.IsRestricted.Value()

		cols = append(cols, "is_restricted")
	}
	if opts.IsAdmin.Has() {
		if opts.IsAdmin.Value().FieldValue /* true */ {
			u.IsAdmin = opts.IsAdmin.Value().FieldValue // set IsAdmin=true
			cols = append(cols, "is_admin")
		} else if !user_model.IsLastAdminUser(ctx, u) /* not the last admin */ {
			u.IsAdmin = opts.IsAdmin.Value().FieldValue // it's safe to change it from false to true (not the last admin)
			cols = append(cols, "is_admin")
		} else /* IsAdmin=false but this is the last admin user */ { //nolint:gocritic // make it easier to read
			if !opts.IsAdmin.Value().FromSync {
				return user_model.ErrDeleteLastAdminUser{UID: u.ID}
			}
			// else: syncing from external-source, this user is the last admin, so skip the "IsAdmin=false" change
		}
	}

	// only validate and persist the visibility when it actually changes
	if opts.Visibility.Has() && opts.Visibility.Value() != u.Visibility {
		if !u.IsOrganization() && !setting.Service.AllowedUserVisibilityModesSlice.IsAllowedVisibility(opts.Visibility.Value()) {
			return fmt.Errorf("visibility mode not allowed: %s", opts.Visibility.Value().String())
		}
		u.Visibility = opts.Visibility.Value()

		cols = append(cols, "visibility")
	}
	if opts.KeepActivityPrivate.Has() {
		u.KeepActivityPrivate = opts.KeepActivityPrivate.Value()

		cols = append(cols, "keep_activity_private")
	}

	if opts.AllowCreateOrganization.Has() {
		u.AllowCreateOrganization = opts.AllowCreateOrganization.Value()

		cols = append(cols, "allow_create_organization")
	}
	if opts.RepoAdminChangeTeamAccess.Has() {
		u.RepoAdminChangeTeamAccess = opts.RepoAdminChangeTeamAccess.Value()

		cols = append(cols, "repo_admin_change_team_access")
	}

	if opts.EmailNotificationsPreference.Has() {
		u.EmailNotificationsPreference = opts.EmailNotificationsPreference.Value()

		cols = append(cols, "email_notifications_preference")
	}

	if opts.SetLastLogin {
		u.SetLastLogin()

		cols = append(cols, "last_login_unix")
	}

	return governance_model.WithWrite(ctx, []string{governance_model.Resource("user", u.ID)}, func(ctx context.Context) error {
		fresh, err := user_model.GetUserByID(ctx, u.ID)
		if err != nil {
			return err
		}
		if fresh.IsActive && opts.IsActive.Has() && !opts.IsActive.Value() {
			if err := governance_model.EnsureUserCanLoseOwnerAccess(ctx, u.ID); err != nil {
				return err
			}
		}
		return user_model.UpdateUserCols(ctx, u, cols...)
	})
}

type UpdateAuthOptions struct {
	RecoveryCode       optional.Option[string]
	LoginSource        optional.Option[int64]
	LoginName          optional.Option[string]
	Password           optional.Option[string]
	MustChangePassword optional.Option[bool]
	ProhibitLogin      optional.Option[bool]
}

func UpdateAuth(ctx context.Context, u *user_model.User, opts *UpdateAuthOptions) error {
	prepared := *u
	if err := prepareAuth(ctx, &prepared, opts); err != nil {
		return err
	}
	err := governance_model.WithWrite(ctx, []string{governance_model.Resource("user", u.ID)}, func(ctx context.Context) error {
		current, err := user_model.GetUserByID(ctx, u.ID)
		if err != nil {
			return err
		}
		if current.LoginType != u.LoginType || current.LoginSource != u.LoginSource || (opts.Password.Has() && current.Passwd != u.Passwd) {
			return governance_model.ErrConflict
		}
		if !current.ProhibitLogin && opts.ProhibitLogin.Has() && opts.ProhibitLogin.Value() {
			if err := governance_model.EnsureUserCanLoseOwnerAccess(ctx, u.ID); err != nil {
				return err
			}
		}
		if opts.RecoveryCode.Has() {
			tf, err := auth_model.GetTwoFactorByUID(ctx, u.ID)
			if err != nil {
				return err
			}
			used, err := tf.ConsumeScratchToken(ctx, opts.RecoveryCode.Value())
			if err != nil {
				return err
			} else if !used {
				return util.NewInvalidArgumentErrorf("恢复码无效或已使用")
			}
		}
		return saveAuth(ctx, &prepared, opts)
	})
	if err == nil {
		*u = prepared
	}
	return err
}

// prepareAuth 密码检查可能访问外部服务，必须在治理写锁之外完成。
func prepareAuth(ctx context.Context, u *user_model.User, opts *UpdateAuthOptions) error {
	if opts.LoginSource.Has() {
		source, err := auth_model.GetSourceByID(ctx, opts.LoginSource.Value())
		if err != nil {
			return err
		}

		u.LoginType = source.Type
		u.LoginSource = source.ID
	}
	if opts.LoginName.Has() {
		u.LoginName = opts.LoginName.Value()
	}

	if opts.Password.Has() && (u.IsLocal() || u.IsOAuth2()) {
		password := opts.Password.Value()

		if len(password) < setting.MinPasswordLength {
			return password_module.ErrMinLength
		}
		if !password_module.IsComplexEnough(password) {
			return password_module.ErrComplexity
		}
		if err := password_module.IsPwned(ctx, password); err != nil {
			return err
		}

		if err := u.SetPassword(password); err != nil {
			return err
		}
	}

	if opts.MustChangePassword.Has() {
		u.MustChangePassword = opts.MustChangePassword.Value()
	}
	if opts.ProhibitLogin.Has() {
		u.ProhibitLogin = opts.ProhibitLogin.Value()
	}

	return nil
}

func saveAuth(ctx context.Context, u *user_model.User, opts *UpdateAuthOptions) error {
	cols := make([]string, 0, 9)
	if opts.LoginSource.Has() {
		cols = append(cols, "login_type", "login_source")
	}
	if opts.LoginName.Has() {
		cols = append(cols, "login_name")
	}
	passwordChanged := opts.Password.Has() && (u.IsLocal() || u.IsOAuth2())
	if passwordChanged {
		cols = append(cols, "passwd", "passwd_hash_algo", "salt")
	}
	if opts.MustChangePassword.Has() {
		cols = append(cols, "must_change_password")
	}
	if opts.ProhibitLogin.Has() {
		cols = append(cols, "prohibit_login")
	}
	if len(cols) == 0 {
		return nil
	}
	if err := user_model.UpdateUserCols(ctx, u, cols...); err != nil {
		return err
	}

	if passwordChanged {
		if err := auth_model.DeleteAuthTokensByUserID(ctx, u.ID); err != nil {
			return err
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{
			Type: "credential.password_changed", Actor: governance_model.AuditActor(ctx),
			ScopeType: "user", ScopeID: u.ID, ObjectType: "user", ObjectID: u.ID,
			ObjectPath: u.Name, Result: "success",
		})
	}
	return nil
}

// UpdateAdminUser 将同次管理操作的数据库变更作为一个事务保存。
func UpdateAdminUser(ctx context.Context, u *user_model.User, authOpts *UpdateAuthOptions, opts *UpdateOptions, email optional.Option[string], reset2FA bool) error {
	prepared := *u
	if err := prepareAuth(ctx, &prepared, authOpts); err != nil {
		return err
	}
	err := governance_model.WithWrite(ctx, []string{governance_model.Resource("user", u.ID), governance_model.Resource("group", u.ID)}, func(ctx context.Context) error {
		actor := governance_model.AuditActor(ctx)
		if actor.Kind != "system" {
			for _, id := range []int64{actor.ID, actor.EffectiveUserID()} {
				admin, err := user_model.GetUserByID(ctx, id)
				if err != nil {
					return err
				}
				if !admin.IsAdmin || !admin.IsActive || admin.ProhibitLogin {
					return governance_model.ErrForbidden
				}
			}
		}
		current, err := user_model.GetUserByID(ctx, u.ID)
		if err != nil {
			return err
		}
		if current.LoginType != u.LoginType || current.LoginSource != u.LoginSource || (authOpts.Password.Has() && current.Passwd != u.Passwd) {
			return governance_model.ErrConflict
		}
		if err := saveAuth(ctx, &prepared, authOpts); err != nil {
			return err
		}
		current, err = user_model.GetUserByID(ctx, u.ID)
		if err != nil {
			return err
		}
		prepared = *current
		if email.Has() {
			if err := ReplacePrimaryEmailAddress(ctx, &prepared, email.Value()); err != nil {
				return err
			}
		}
		if err := UpdateUser(ctx, &prepared, opts); err != nil {
			return err
		}
		if reset2FA {
			return auth_model.DeleteUserMFA(ctx, u.ID)
		}
		return nil
	})
	if err == nil {
		*u = prepared
	}
	return err
}
