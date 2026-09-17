// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	packages_model "gitea.dev/models/packages"
	repo_model "gitea.dev/models/repo"
	system_model "gitea.dev/models/system"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/eventsource"
	"gitea.dev/modules/log"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/storage"
	"gitea.dev/modules/structs"
	"gitea.dev/services/agit"
	asymkey_service "gitea.dev/services/asymkey"
	governance_service "gitea.dev/services/governance"
	org_service "gitea.dev/services/org"
	"gitea.dev/services/packages"
	container_service "gitea.dev/services/packages/container"
	repo_service "gitea.dev/services/repository"
)

// RenameUser renames a user
func RenameUser(ctx context.Context, u *user_model.User, newUserName string, doer *user_model.User) error {
	if u.IsOrganization() {
		n, err := governance_model.GetNamespace(ctx, u.ID)
		if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
			return err
		}
		if err == nil {
			moved, err := governance_service.MoveGroup(ctx, governance_service.RequestActor(doer, "", "native"), u.ID, governance_service.GroupOption{Path: newUserName, ParentID: n.ParentID, Revision: n.Revision})
			if err != nil {
				return err
			}
			u.NamespacePath = moved.FullPath
			return nil
		}
	}

	if newUserName == u.Name {
		return nil
	}

	// Non-local users are not allowed to change their own username, but admins are
	isExternalUser := !u.IsOrganization() && !u.IsLocal()
	if isExternalUser && !doer.IsAdmin {
		return user_model.ErrUserIsNotLocal{UID: u.ID, Name: u.Name}
	}

	if err := user_model.IsUsableUsername(newUserName); err != nil {
		return err
	}

	onlyCapitalization := strings.EqualFold(newUserName, u.Name)
	oldUserName := u.Name

	actor := governance_service.RequestActor(doer, "", "native")
	if onlyCapitalization {
		err := governance_model.WithWrite(ctx, []string{governance_model.Resource("user", u.ID), governance_model.Resource("group", u.ID)}, func(ctx context.Context) error {
			return db.WithTx(ctx, func(ctx context.Context) error {
				if err := repo_model.FreezeRepositoryStorageByOwnerID(ctx, u.ID); err != nil {
					return err
				}
				if err := governance_model.RenameNativePersonalNamespace(ctx, u.ID, newUserName, actor); err != nil {
					return err
				}
				u.Name = newUserName
				if err := user_model.UpdateUserCols(ctx, u, "name"); err != nil {
					return err
				}
				return repo_model.UpdateRepositoryOwnerNames(ctx, u.ID, newUserName)
			})
		})
		if err != nil {
			u.Name = oldUserName
			return err
		}
		if u.NamespacePath != "" {
			u.NamespacePath = newUserName
		}
		return nil
	}

	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()

	isExist, err := user_model.IsUserExist(ctx, u.ID, newUserName)
	if err != nil {
		return err
	}
	if isExist {
		return user_model.ErrUserAlreadyExist{
			Name: newUserName,
		}
	}

	if err := governance_model.RenameNativePersonalNamespace(ctx, u.ID, newUserName, actor); err != nil {
		return err
	}
	if err := repo_model.FreezeRepositoryStorageByOwnerID(ctx, u.ID); err != nil {
		return err
	}
	if err = repo_model.UpdateRepositoryOwnerName(ctx, oldUserName, newUserName); err != nil {
		return err
	}

	if err = user_model.NewUserRedirect(ctx, u.ID, oldUserName, newUserName); err != nil {
		return err
	}

	if err := agit.UserNameChanged(ctx, u, newUserName); err != nil {
		return err
	}
	if err := container_service.UpdateRepositoryNames(ctx, u, newUserName); err != nil {
		return err
	}

	u.Name = newUserName
	u.LowerName = strings.ToLower(newUserName)
	if err := user_model.UpdateUserCols(ctx, u, "name", "lower_name"); err != nil {
		u.Name = oldUserName
		u.LowerName = strings.ToLower(oldUserName)
		return err
	}

	if err = committer.Commit(); err != nil {
		u.Name = oldUserName
		u.LowerName = strings.ToLower(oldUserName)
		return err
	}
	if u.NamespacePath != "" {
		u.NamespacePath = newUserName
	}
	return nil
}

// DeleteUser completely and permanently deletes everything of a user,
// but issues/comments/pulls will be kept and shown as someone has been deleted,
// unless the user is younger than USER_DELETE_WITH_COMMENTS_MAX_DAYS.
func DeleteUser(ctx context.Context, u *user_model.User, purge bool) error {
	if u.IsOrganization() {
		return fmt.Errorf("%s is an organization not a user", u.Name)
	}

	if u.IsActive && user_model.IsLastAdminUser(ctx, u) {
		return user_model.ErrDeleteLastAdminUser{UID: u.ID}
	}

	if purge {
		// Disable the user first
		// NOTE: This is deliberately not within a transaction as it must disable the user immediately to prevent any further action by the user to be purged.
		if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("user", u.ID)}, func(ctx context.Context) error {
			if err := governance_model.EnsureUserCanLoseOwnerAccess(ctx, u.ID); err != nil {
				return err
			}
			return user_model.UpdateUserCols(ctx, &user_model.User{
				ID: u.ID, IsActive: false, IsRestricted: true, IsAdmin: false, ProhibitLogin: true,
				Passwd: "", Salt: "", PasswdHashAlgo: "", MaxRepoCreation: 0,
			}, "is_active", "is_restricted", "is_admin", "prohibit_login", "max_repo_creation", "passwd", "salt", "passwd_hash_algo")
		}); err != nil {
			return fmt.Errorf("unable to disable user: %s[%d] prior to purge. UpdateUserCols: %w", u.Name, u.ID, err)
		}

		// Force any logged in sessions to log out
		// FIXME: We also need to tell the session manager to log them out too.
		eventsource.GetManager().SendMessage(u.ID, &eventsource.Event{
			Name: "logout",
		})

		// Delete all repos belonging to this user
		// Now this is not within a transaction because there are internal transactions within the DeleteRepository
		// BUT: the db will still be consistent even if a number of repos have already been deleted.
		// And in fact we want to capture any repositories that are being created in other transactions in the meantime
		//
		// An alternative option here would be write a DeleteAllRepositoriesForUserID function which would delete all of the repos
		// but such a function would likely get out of date
		err := repo_service.DeleteOwnerRepositoriesDirectly(ctx, u)
		if err != nil {
			return err
		}

		// Remove from Organizations and delete last owner organizations
		// Now this is not within a transaction because there are internal transactions within the DeleteOrganization
		// BUT: the db will still be consistent even if a number of organizations memberships and organizations have already been deleted
		// And in fact we want to capture any organization additions that are being created in other transactions in the meantime
		//
		// An alternative option here would be write a function which would delete all organizations but it seems
		// but such a function would likely get out of date
		for {
			orgs, err := db.Find[organization.Organization](ctx, organization.FindOrgOptions{
				ListOptions: db.ListOptions{
					PageSize: repo_model.RepositoryListDefaultPageSize,
					Page:     1,
				},
				UserID:            u.ID,
				IncludeVisibility: structs.VisibleTypePrivate,
			})
			if err != nil {
				return fmt.Errorf("unable to find org list for %s[%d]. Error: %w", u.Name, u.ID, err)
			}
			if len(orgs) == 0 {
				break
			}
			for _, org := range orgs {
				if err := org_service.RemoveOrgUser(ctx, org, u); err != nil {
					if organization.IsErrLastOrgOwner(err) {
						err = org_service.DeleteOrganization(ctx, org, true)
						if err != nil {
							return fmt.Errorf("unable to delete organization %d: %w", org.ID, err)
						}
					}
					if err != nil {
						return fmt.Errorf("unable to remove user %s[%d] from org %s[%d]. Error: %w", u.Name, u.ID, org.Name, org.ID, err)
					}
				}
			}
		}

		// Delete Packages
		if setting.Packages.Enabled {
			if _, err := packages.RemoveAllPackages(ctx, u.ID); err != nil {
				return err
			}
		}
	}

	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("user", u.ID)}, func(ctx context.Context) error {
		if err := governance_model.EnsureUserCanLoseOwnerAccess(ctx, u.ID); err != nil {
			return err
		}
		// Note: A user owns any repository or belongs to any organization
		//	cannot perform delete operation. This causes a race with the purge above
		//  however consistency requires that we ensure that this is the case

		// Check ownership of repository.
		count, err := repo_model.CountRepositories(ctx, repo_model.CountRepositoryOptions{OwnerID: u.ID})
		if err != nil {
			return fmt.Errorf("GetRepositoryCount: %w", err)
		} else if count > 0 {
			return repo_model.ErrUserOwnRepos{UID: u.ID}
		}

		// Check membership of organization.
		count, err = organization.GetOrganizationCount(ctx, u)
		if err != nil {
			return fmt.Errorf("GetOrganizationCount: %w", err)
		} else if count > 0 {
			return organization.ErrUserHasOrgs{UID: u.ID}
		}

		// Check ownership of packages.
		if ownsPackages, err := packages_model.HasOwnerPackages(ctx, u.ID); err != nil {
			return fmt.Errorf("HasOwnerPackages: %w", err)
		} else if ownsPackages {
			return packages_model.ErrUserOwnPackages{UID: u.ID}
		}

		if err := deleteUser(ctx, u, purge); err != nil {
			return fmt.Errorf("DeleteUser: %w", err)
		}

		// Finally delete any unlinked attachments, this will also delete the attached files
		if err := deleteUserUnlinkedAttachments(ctx, u); err != nil {
			return fmt.Errorf("deleteUserUnlinkedAttachments: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	if err := asymkey_service.RewriteAllPublicKeys(ctx); err != nil {
		return err
	}
	if err := asymkey_service.RewriteAllPrincipalKeys(ctx); err != nil {
		return err
	}

	// Note: There are something just cannot be roll back, so just keep error logs of those operations.
	path := user_model.UserPath(u.Name)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		// 非空目录可能仍承载已经转出的稳定仓库正文，必须保留。
		log.Warn("保留用户 %q 的非空仓库目录 %s: %v", u.Name, path, err)
	}

	if u.Avatar != "" {
		avatarPath := u.CustomAvatarRelativePath()
		if err := storage.Avatars.Delete(avatarPath); err != nil {
			err = fmt.Errorf("failed to remove %s: %w", avatarPath, err)
			_ = system_model.CreateNotice(ctx, system_model.NoticeTask, fmt.Sprintf("delete user '%s': %v", u.Name, err))
		}
	}

	return nil
}

func deleteUserUnlinkedAttachments(ctx context.Context, u *user_model.User) error {
	attachments, err := repo_model.GetUnlinkedAttachmentsByUserID(ctx, u.ID)
	if err != nil {
		return fmt.Errorf("GetUnlinkedAttachmentsByUserID: %w", err)
	}
	for _, attach := range attachments {
		if err := repo_model.DeleteAttachment(ctx, attach, true); err != nil {
			return fmt.Errorf("DeleteAttachment ID[%d]: %w", attach.ID, err)
		}
	}
	return nil
}

// DeleteInactiveUsers deletes all inactive users and their email addresses.
func DeleteInactiveUsers(ctx context.Context, olderThan time.Duration) error {
	inactiveUsers, err := user_model.GetInactiveUsers(ctx, olderThan)
	if err != nil {
		return err
	}

	// FIXME: should only update authorized_keys file once after all deletions.
	for _, u := range inactiveUsers {
		if err = DeleteUser(ctx, u, false); err != nil {
			// Ignore inactive users that were ever active but then were set inactive by admin
			if repo_model.IsErrUserOwnRepos(err) || organization.IsErrUserHasOrgs(err) || packages_model.IsErrUserOwnPackages(err) {
				log.Warn("Inactive user %q has repositories, organizations or packages, skipping deletion: %v", u.Name, err)
				continue
			}
			select {
			case <-ctx.Done():
				return db.ErrCancelledf("when deleting inactive user %q", u.Name)
			default:
				return err
			}
		}
	}
	return nil // TODO: there could be still inactive users left, and the number would increase gradually
}
