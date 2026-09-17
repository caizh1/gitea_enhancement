// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package org

import (
	"context"
	"fmt"
	"strings"

	actions_model "gitea.dev/models/actions"
	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	org_model "gitea.dev/models/organization"
	packages_model "gitea.dev/models/packages"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	secret_model "gitea.dev/models/secret"
	user_model "gitea.dev/models/user"
	"gitea.dev/models/webhook"
	issue_indexer "gitea.dev/modules/indexer/issues"
	"gitea.dev/modules/log"
	"gitea.dev/modules/structs"
	repo_service "gitea.dev/services/repository"
)

// deleteOrganization deletes models associated to an organization.
func deleteOrganization(ctx context.Context, org *org_model.Organization) error {
	if org.Type != user_model.UserTypeOrganization {
		return fmt.Errorf("%s is a user not an organization", org.Name)
	}
	if err := webhook.DeleteOwnerWebhooks(ctx, org.ID); err != nil {
		return err
	}

	if err := db.DeleteBeans(ctx,
		&org_model.Team{OrgID: org.ID},
		&org_model.OrgUser{OrgID: org.ID},
		&org_model.TeamUser{OrgID: org.ID},
		&org_model.TeamUnit{OrgID: org.ID},
		&org_model.TeamInvite{OrgID: org.ID},
		&secret_model.Secret{OwnerID: org.ID},
		&user_model.Blocking{BlockerID: org.ID},
		&actions_model.ActionRunner{OwnerID: org.ID},
		&actions_model.ActionRunnerToken{OwnerID: org.ID},
		&actions_model.ActionScopedWorkflowSource{OwnerID: org.ID},
	); err != nil {
		return fmt.Errorf("DeleteBeans: %w", err)
	}

	if err := governance_model.DeleteNativeNamespace(ctx, org.ID); err != nil {
		return err
	}
	if _, err := db.GetEngine(ctx).ID(org.ID).Delete(new(user_model.User)); err != nil {
		return fmt.Errorf("Delete: %w", err)
	}

	return nil
}

// DeleteOrganization completely and permanently deletes everything of organization.
func DeleteOrganization(ctx context.Context, org *org_model.Organization, purge bool) error {
	cleanup := &governance_model.ResourceCleanup{Kind: "group", ResourceID: org.ID, Actor: governance_model.AuditActor(ctx), ScopeType: "group", ScopeID: org.ID, ObjectPath: org.Name}
	cleanup.Objects = append(cleanup.Objects, governance_model.CleanupObject{Kind: "git", Path: strings.ToLower(org.Name)})
	if org.Avatar != "" {
		cleanup.Objects = append(cleanup.Objects, governance_model.CleanupObject{Kind: "avatar", Path: org.CustomAvatarRelativePath()})
	}
	if err := db.WithTx(ctx, func(ctx context.Context) error {
		// 在任何仓库文件删除前锁定并检查子群组；同一事务阻止并发创建子组。
		if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("group", org.ID)}, func(ctx context.Context) error {
			actor := governance_model.AuditActor(ctx)
			if actor.EffectiveUserID() > 0 {
				user, err := user_model.GetUserByID(ctx, actor.EffectiveUserID())
				if err != nil {
					return err
				}
				if !user.IsActive || user.ProhibitLogin {
					return governance_model.ErrForbidden
				}
				if !user.IsAdmin {
					owner, err := org.IsOwnedBy(ctx, user.ID)
					if err != nil {
						return err
					}
					if !owner {
						return governance_model.ErrForbidden
					}
				}
				if actor.ActingAsID > 0 {
					original, err := user_model.GetUserByID(ctx, actor.ID)
					if err != nil {
						return err
					}
					if !original.IsActive || original.ProhibitLogin || !original.IsAdmin {
						return governance_model.ErrForbidden
					}
				}
			}
			children, err := db.GetEngine(ctx).Where("parent_id = ?", org.ID).Exist(new(governance_model.Namespace))
			if err != nil {
				return err
			}
			if children {
				return governance_model.ErrConflict
			}
			return nil
		}); err != nil {
			return err
		}

		if purge {
			err := repo_service.DeleteOwnerRepositoriesDirectly(ctx, org.AsUser())
			if err != nil {
				return err
			}
		}

		// Check ownership of repository.
		count, err := repo_model.CountRepositories(ctx, repo_model.CountRepositoryOptions{OwnerID: org.ID})
		if err != nil {
			return fmt.Errorf("GetRepositoryCount: %w", err)
		} else if count > 0 {
			return repo_model.ErrUserOwnRepos{UID: org.ID}
		}

		// Check ownership of packages.
		if ownsPackages, err := packages_model.HasOwnerPackages(ctx, org.ID); err != nil {
			return fmt.Errorf("HasOwnerPackages: %w", err)
		} else if ownsPackages {
			return packages_model.ErrUserOwnPackages{UID: org.ID}
		}

		if namespace, err := governance_model.GetNamespace(ctx, org.ID); err == nil {
			cleanup.ObjectPath = namespace.FullPath
		} else if err != governance_model.ErrNotFound {
			return err
		}
		ancestors, err := governance_model.Ancestors(ctx, org.ID)
		if err != nil && err != governance_model.ErrNotFound {
			return err
		}
		for _, ancestor := range ancestors {
			if ancestor.Kind == "group" {
				cleanup.AncestorIDs = append(cleanup.AncestorIDs, ancestor.ID)
			}
		}
		if err := repo_service.QueueResourceCleanup(ctx, cleanup); err != nil {
			return err
		}
		if err := deleteOrganization(ctx, org); err != nil {
			return fmt.Errorf("DeleteOrganization: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	if !db.InTransaction(ctx) {
		if err := repo_service.RunResourceCleanup(ctx, cleanup.ID); err != nil {
			log.Error("群组数据库已删除，存储清理任务 %d 等待恢复：%v", cleanup.ID, err)
		}
	}

	return nil
}

func updateRepoForVisibilityChanged(ctx context.Context, repo *repo_model.Repository, makePrivate bool) error {
	if err := repo.LoadOwner(ctx); err != nil {
		return fmt.Errorf("LoadOwner: %w", err)
	}

	// Organization repository need to recalculate access table when visibility is changed.
	if err := access_model.RecalculateAccesses(ctx, repo); err != nil {
		return fmt.Errorf("RecalculateAccesses: %w", err)
	}

	if makePrivate {
		if _, err := db.GetEngine(ctx).Where("repo_id = ?", repo.ID).Cols("is_private").Update(&activities_model.Action{
			IsPrivate: true,
		}); err != nil {
			return err
		}

		// the repo is no longer publicly visible, so drop stars and watches from users who can no longer
		// see it, matching the direct repository-private transition (see services/repository)
		if err := repo_model.ClearRepoStars(ctx, repo.ID); err != nil {
			return err
		}
		if err := repo_model.ClearRepoWatches(ctx, repo.ID); err != nil {
			return err
		}
	}

	// Create/Remove git-daemon-export-ok for git-daemon...
	if err := repo_service.CheckDaemonExportOK(ctx, repo); err != nil {
		return err
	}

	// If visibility is changed, we need to update the issue indexer.
	// Since the data in the issue indexer have field to indicate if the repo is public or not.
	// FIXME: it should check organization visibility instead of repository visibility only.
	issue_indexer.UpdateRepoIndexer(ctx, repo.ID)

	forkRepos, err := repo_model.GetRepositoriesByForkID(ctx, repo.ID)
	if err != nil {
		return fmt.Errorf("getRepositoriesByForkID: %w", err)
	}
	for i := range forkRepos {
		if err := updateRepoForVisibilityChanged(ctx, forkRepos[i], makePrivate); err != nil {
			return fmt.Errorf("updateRepoForVisibilityChanged[%s]: %w", forkRepos[i].FullName(), err)
		}
	}
	return nil
}

func ChangeOrganizationVisibility(ctx context.Context, org *org_model.Organization, visibility structs.VisibleType) error {
	if org.Visibility == visibility {
		return nil
	}

	org.Visibility = visibility
	// FIXME: If it's a big forks network(forks and sub forks), the database transaction will be too long to fail.
	return db.WithTx(ctx, func(ctx context.Context) error {
		if err := user_model.UpdateUserColsNoAutoTime(ctx, org.AsUser(), "visibility"); err != nil {
			return err
		}

		repos, _, err := repo_model.GetUserRepositories(ctx, repo_model.SearchRepoOptions{
			Actor: org.AsUser(), Private: true, ListOptions: db.ListOptionsAll,
		})
		if err != nil {
			return err
		}
		for _, repo := range repos {
			if err := updateRepoForVisibilityChanged(ctx, repo, visibility == structs.VisibleTypePrivate); err != nil {
				return fmt.Errorf("updateRepoForVisibilityChanged: %w", err)
			}
		}
		return nil
	})
}

// UpdateOrgEmailAddress validates and updates the organization's contact email.
// A nil email means no change.
func UpdateOrgEmailAddress(ctx context.Context, org *org_model.Organization, email *string) error {
	if email == nil {
		return nil
	}

	if *email != "" {
		if err := user_model.ValidateEmail(*email); err != nil {
			return err
		}
	}

	org.Email = *email
	return user_model.UpdateUserCols(ctx, org.AsUser(), "email")
}
