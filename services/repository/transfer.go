// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/organization"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/globallock"
	"gitea.dev/modules/log"
	"gitea.dev/modules/util"
	governance_service "gitea.dev/services/governance"
	notify_service "gitea.dev/services/notify"
)

type LimitReachedError struct{ Limit int }

func (LimitReachedError) Error() string {
	return "Repository limit has been reached"
}

func IsRepositoryLimitReached(err error) bool {
	_, ok := err.(LimitReachedError)
	return ok
}

func getRepoWorkingLockKey(repoID int64) string {
	return fmt.Sprintf("repo_working_%d", repoID)
}

func repositoryRequestActor(ctx context.Context, doer *user_model.User) governance_model.Actor {
	actor := governance_model.AuditActor(ctx)
	if actor.EffectiveUserID() <= 0 {
		return governance_service.RequestActor(doer, "", "native")
	}
	return actor
}

// AcceptTransferOwnership transfers all corresponding setting from old user to new one.
func AcceptTransferOwnership(ctx context.Context, repo *repo_model.Repository, doer *user_model.User) error {
	releaser, err := globallock.Lock(ctx, getRepoWorkingLockKey(repo.ID))
	if err != nil {
		log.Error("lock.Lock(): %v", err)
		return fmt.Errorf("lock.Lock: %w", err)
	}
	defer releaser()

	repoTransfer, err := repo_model.GetPendingRepositoryTransfer(ctx, repo)
	if err != nil {
		return err
	}

	oldOwnerName := repo.OwnerName

	acceptActor := repositoryRequestActor(ctx, doer)
	if err := governance_service.WithActorWrite(ctx, acceptActor, []string{governance_model.Resource("repository", repo.ID), governance_model.Resource("group", repoTransfer.RecipientID)}, func(ctx context.Context) error {
		return db.WithTx(ctx, func(ctx context.Context) error {
			freshDoer, err := user_model.GetUserByID(ctx, acceptActor.EffectiveUserID())
			if err != nil {
				return err
			}
			freshRepo, err := repo_model.GetRepositoryByID(ctx, repo.ID)
			if err != nil {
				return err
			}
			freshTransfer, err := repo_model.GetPendingRepositoryTransfer(ctx, freshRepo)
			if err != nil {
				return err
			}
			if err := freshTransfer.LoadAttributes(ctx); err != nil {
				return err
			}

			if !freshDoer.CanCreateRepoIn(freshTransfer.Recipient) {
				return LimitReachedError{Limit: freshTransfer.Recipient.MaxCreationLimit()}
			}

			if !freshTransfer.CanUserAcceptOrRejectTransfer(ctx, freshDoer) {
				return util.ErrPermissionDenied
			}

			if err := freshRepo.LoadOwner(ctx); err != nil {
				return err
			}
			for _, team := range freshTransfer.Teams {
				if freshTransfer.Recipient.ID != team.OrgID {
					return fmt.Errorf("team %d does not belong to organization", team.ID)
				}
			}

			sourceDoer, err := user_model.GetUserByID(ctx, freshTransfer.DoerID)
			if err != nil {
				return err
			}
			if !(sourceDoer.IsOrganization() && sourceDoer.ID == freshRepo.OwnerID) {
				if _, err := governance_service.CheckRepositoryOwnerMutation(ctx, sourceDoer.ID, freshRepo.ID); err != nil {
					return err
				}
			}
			if err := transferOwnershipLocked(ctx, sourceDoer, freshTransfer.Recipient, freshRepo, freshTransfer.Teams); err != nil {
				return err
			}
			*repo = *freshRepo
			return nil
		})
	}); err != nil {
		return err
	}
	releaser()

	notify_service.TransferRepository(ctx, doer, repo, oldOwnerName)

	return nil
}

// isRepositoryModelOrDirExist returns true if the repository with given name under user has already existed.
func isRepositoryModelOrDirExist(ctx context.Context, u *user_model.User, repoName string) (bool, error) {
	has, err := repo_model.IsRepositoryModelExist(ctx, u, repoName)
	if err != nil {
		return false, err
	}
	repo := repo_model.StorageRepo(repo_model.RelativePath(u.Name, repoName))
	isExist, err := gitrepo.IsRepositoryExist(ctx, repo)
	return has || isExist, err
}

// transferOwnership transfers all corresponding repository items from old user to new one.
func transferOwnership(ctx context.Context, doer, requestedOwner *user_model.User, repo *repo_model.Repository, teams []*organization.Team) error {
	actor := repositoryRequestActor(ctx, doer)
	resources := []string{
		governance_model.Resource("repository", repo.ID),
		governance_model.Resource("group", repo.OwnerID),
		governance_model.Resource("group", requestedOwner.ID),
	}
	write := func(ctx context.Context) error {
		freshRepo, err := repo_model.GetRepositoryByID(ctx, repo.ID)
		if err != nil {
			return err
		}
		if freshRepo.OwnerID != repo.OwnerID || freshRepo.Name != repo.Name || freshRepo.Status != repo.Status {
			return governance_model.ErrConflict
		}
		if doer.IsOrganization() && doer.ID == repo.OwnerID {
			if err := freshRepo.LoadOwner(ctx); err != nil {
				return err
			}
			if err := transferOwnershipLocked(ctx, doer, requestedOwner, freshRepo, teams); err != nil {
				return err
			}
			*repo = *freshRepo
			return nil
		}
		freshDoer, err := user_model.GetUserByID(ctx, actor.EffectiveUserID())
		if err != nil {
			return err
		}
		freshRepo, err = governance_service.CheckRepositoryOwnerMutation(ctx, freshDoer.ID, repo.ID)
		if err != nil {
			return err
		}
		if freshRepo.OwnerID != repo.OwnerID || freshRepo.Name != repo.Name || freshRepo.Status != repo.Status {
			return governance_model.ErrConflict
		}
		newOwner, err := user_model.GetUserByID(ctx, requestedOwner.ID)
		if err != nil {
			return err
		}
		if err := freshRepo.LoadOwner(ctx); err != nil {
			return err
		}
		if err := transferOwnershipLocked(ctx, freshDoer, newOwner, freshRepo, teams); err != nil {
			return err
		}
		*repo = *freshRepo
		return nil
	}
	if doer.IsOrganization() && doer.ID == repo.OwnerID {
		// 旧转移记录可能用组织自身表示来源；治理锁与状态复核仍必须执行。
		return governance_model.WithWrite(ctx, resources, write)
	}
	return governance_service.WithActorWrite(ctx, actor, resources, write)
}

func transferOwnershipLocked(ctx context.Context, doer, newOwner *user_model.User, repo *repo_model.Repository, teams []*organization.Team) (err error) {
	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()

	sess := db.GetEngine(ctx)
	newOwnerName := newOwner.Name

	// Check if new owner has repository with same name.
	if has, err := isRepositoryModelOrDirExist(ctx, newOwner, repo.Name); err != nil {
		return fmt.Errorf("IsRepositoryExist: %w", err)
	} else if has {
		return repo_model.ErrRepoAlreadyExist{
			Uname: newOwnerName,
			Name:  repo.Name,
		}
	}

	oldOwner := repo.Owner
	newPath, err := governance_model.ChangeNativeRepositoryPath(ctx, repo.ID, newOwner.ID, repo.Name)
	if err != nil {
		return err
	}

	// Note: we have to set value here to make sure recalculate accesses is based on
	// new owner.
	repo.OwnerID = newOwner.ID
	repo.Owner = newOwner
	repo.OwnerName = newOwner.Name
	repo.OwnerNamespace = strings.TrimSuffix(newPath, "/"+repo.Name)
	if newOwner.IsOrganization() {
		if namespace, loadErr := governance_model.GetNamespace(ctx, newOwner.ID); loadErr == nil && namespace.Visibility > repo.EffectiveVisibility() {
			repo.Visibility = namespace.Visibility
			repo.IsPrivate = namespace.Visibility != repo_model.VisibilityPublic
		}
	}
	if repo.GovernanceStorageOwner == "" {
		repo.GovernanceStorageOwner = oldOwner.Name
	}
	if repo.GovernanceStorageName == "" {
		repo.GovernanceStorageName = repo.Name
	}

	// Update repository.
	if err := repo_model.UpdateRepositoryColsNoAutoTime(ctx, repo, "owner_id", "owner_name", "owner_namespace", "governance_storage_owner", "governance_storage_name", "visibility", "is_private"); err != nil {
		return fmt.Errorf("update owner: %w", err)
	}
	if err := governance_service.RemapTransferredRepositoryCustomRoles(ctx, repositoryRequestActor(ctx, doer), repo.ID, oldOwner.ID, newOwner.ID, newPath); err != nil {
		return fmt.Errorf("remap repository governance roles: %w", err)
	}

	// Remove redundant collaborators.
	collaborators, _, err := repo_model.GetCollaborators(ctx, &repo_model.FindCollaborationOptions{RepoID: repo.ID})
	if err != nil {
		return fmt.Errorf("GetCollaborators: %w", err)
	}

	// Dummy object.
	collaboration := &repo_model.Collaboration{RepoID: repo.ID}
	for _, c := range collaborators {
		if c.IsGhost() {
			collaboration.ID = c.Collaboration.ID
			if _, err := sess.Delete(collaboration); err != nil {
				return fmt.Errorf("remove collaborator '%d': %w", c.ID, err)
			}
			collaboration.ID = 0
		}

		if c.ID != newOwner.ID {
			isMember, err := organization.IsOrganizationMember(ctx, newOwner.ID, c.ID)
			if err != nil {
				return fmt.Errorf("IsOrgMember: %w", err)
			} else if !isMember {
				continue
			}
		}
		collaboration.UserID = c.ID
		if _, err := sess.Delete(collaboration); err != nil {
			return fmt.Errorf("remove collaborator '%d': %w", c.ID, err)
		}
		collaboration.UserID = 0
	}

	if oldOwner.IsOrganization() {
		// Remove old team-repository relations.
		if err := organization.RemoveOrgRepo(ctx, oldOwner.ID, repo.ID); err != nil {
			return fmt.Errorf("removeOrgRepo: %w", err)
		}

		// Remove project's issues that belong to old organization's projects
		projects, err := project_model.GetAllProjectsIDsByOwnerIDAndType(ctx, oldOwner.ID, project_model.TypeOrganization)
		if err != nil {
			return fmt.Errorf("Unable to find old org projects: %w", err)
		}
		issues, err := issues_model.GetIssueIDsByRepoID(ctx, repo.ID)
		if err != nil {
			return fmt.Errorf("Unable to find repo's issues: %w", err)
		}
		err = project_model.DeleteAllProjectIssueByIssueIDsAndProjectIDs(ctx, issues, projects)
		if err != nil {
			return fmt.Errorf("Unable to delete project's issues: %w", err)
		}
	}

	if newOwner.IsOrganization() {
		teams, err := organization.FindOrgTeams(ctx, newOwner.ID)
		if err != nil {
			return fmt.Errorf("LoadTeams: %w", err)
		}
		for _, t := range teams {
			if t.IncludesAllRepositories {
				if err := addRepositoryToTeam(ctx, t, repo); err != nil {
					return fmt.Errorf("AddRepository: %w", err)
				}
			}
		}
	} else if err := access_model.RecalculateAccesses(ctx, repo); err != nil {
		// Organization called this in addRepository method.
		return fmt.Errorf("recalculateAccesses: %w", err)
	}

	// Remove repository from old owner's Actions AllowedCrossRepoIDs if present
	if oldActionsCfg, err := actions_model.GetOwnerActionsConfig(ctx, oldOwner.ID); err == nil {
		newAllowedCrossRepoIDs := util.SliceRemoveAll(oldActionsCfg.AllowedCrossRepoIDs, repo.ID)
		if len(newAllowedCrossRepoIDs) != len(oldActionsCfg.AllowedCrossRepoIDs) {
			oldActionsCfg.AllowedCrossRepoIDs = newAllowedCrossRepoIDs
			if err := actions_model.SetOwnerActionsConfig(ctx, oldOwner.ID, oldActionsCfg); err != nil {
				return fmt.Errorf("SetOwnerActionsConfig: %w", err)
			}
		}
	} else {
		return fmt.Errorf("GetOwnerActionsConfig: %w", err)
	}

	// Update repository count.
	if _, err := sess.Exec("UPDATE `user` SET num_repos=num_repos+1 WHERE id=?", newOwner.ID); err != nil {
		return fmt.Errorf("increase new owner repository count: %w", err)
	} else if _, err := sess.Exec("UPDATE `user` SET num_repos=num_repos-1 WHERE id=?", oldOwner.ID); err != nil {
		return fmt.Errorf("decrease old owner repository count: %w", err)
	}

	if err := repo_model.WatchRepo(ctx, doer, repo, true); err != nil {
		return fmt.Errorf("watchRepo: %w", err)
	}

	if oldOwner.IsOrganization() {
		// Remove watch for organization.
		if err := repo_model.WatchRepo(ctx, oldOwner, repo, false); err != nil {
			return fmt.Errorf("watchRepo [false]: %w", err)
		}

		// Delete labels that belong to the old organization and comments that added these labels
		if _, err := sess.Exec(`DELETE FROM issue_label WHERE issue_label.id IN (
			SELECT il_too.id FROM (
				SELECT il_too_too.id
					FROM issue_label AS il_too_too
						INNER JOIN label ON il_too_too.label_id = label.id
						INNER JOIN issue on issue.id = il_too_too.issue_id
					WHERE
						issue.repo_id = ? AND ((label.org_id = 0 AND issue.repo_id != label.repo_id) OR (label.repo_id = 0 AND label.org_id != ?))
		) AS il_too )`, repo.ID, newOwner.ID); err != nil {
			return fmt.Errorf("Unable to remove old org labels: %w", err)
		}

		if _, err := sess.Exec(`DELETE FROM comment WHERE comment.id IN (
			SELECT il_too.id FROM (
				SELECT com.id
					FROM comment AS com
						INNER JOIN label ON com.label_id = label.id
						INNER JOIN issue ON issue.id = com.issue_id
					WHERE
						com.type = ? AND issue.repo_id = ? AND ((label.org_id = 0 AND issue.repo_id != label.repo_id) OR (label.repo_id = 0 AND label.org_id != ?))
		) AS il_too)`, issues_model.CommentTypeLabel, repo.ID, newOwner.ID); err != nil {
			return fmt.Errorf("Unable to remove old org label comments: %w", err)
		}
	}

	if err := repo_model.DeleteRepositoryTransfer(ctx, repo.ID); err != nil {
		return fmt.Errorf("deleteRepositoryTransfer: %w", err)
	}
	repo.Status = repo_model.RepositoryReady
	if err := repo_model.UpdateRepositoryColsNoAutoTime(ctx, repo, "status"); err != nil {
		return err
	}

	// If there was previously a redirect at this location, remove it.
	if err := repo_model.DeleteRedirect(ctx, newOwner.ID, repo.Name); err != nil {
		return fmt.Errorf("delete repo redirect: %w", err)
	}

	if err := repo_model.NewRedirect(ctx, oldOwner.ID, repo.ID, repo.Name, repo.Name); err != nil {
		return fmt.Errorf("repo_model.NewRedirect: %w", err)
	}

	newRepo, err := repo_model.GetRepositoryByID(ctx, repo.ID)
	if err != nil {
		return err
	}

	for _, team := range teams {
		if err := addRepositoryToTeam(ctx, team, newRepo); err != nil {
			return err
		}
	}

	if err := governance_model.EnsurePermanentRepositoryOwner(ctx, repo.ID, 0); err != nil {
		return err
	}
	return committer.Commit()
}

// changeRepositoryName changes all corresponding setting from old repository name to new one.
func changeRepositoryName(ctx context.Context, repo *repo_model.Repository, newRepoName string) (err error) {
	oldRepoName := repo.Name
	newRepoName = strings.ToLower(newRepoName)
	if err = repo_model.IsUsableRepoName(newRepoName); err != nil {
		return err
	}

	if err := repo.LoadOwner(ctx); err != nil {
		return err
	}

	has, err := isRepositoryModelOrDirExist(ctx, repo.Owner, newRepoName)
	if err != nil {
		return fmt.Errorf("IsRepositoryExist: %w", err)
	} else if has {
		return repo_model.ErrRepoAlreadyExist{
			Uname: repo.OwnerName,
			Name:  newRepoName,
		}
	}

	if err := governance_model.PrepareNativeRepositoryPath(ctx, repo.ID, repo.OwnerID, newRepoName); err != nil {
		return err
	}
	return db.WithTx(ctx, func(ctx context.Context) error {
		if repo.GovernanceStorageOwner == "" {
			repo.GovernanceStorageOwner = repo.OwnerName
		}
		if repo.GovernanceStorageName == "" {
			repo.GovernanceStorageName = oldRepoName
		}
		if err := repo_model.UpdateRepositoryColsNoAutoTime(ctx, repo, "governance_storage_owner", "governance_storage_name"); err != nil {
			return err
		}
		return repo_model.NewRedirect(ctx, repo.Owner.ID, repo.ID, oldRepoName, newRepoName)
	})
}

// ChangeRepositoryName changes all corresponding setting from old repository name to new one.
func ChangeRepositoryName(ctx context.Context, doer *user_model.User, repo *repo_model.Repository, newRepoName string) error {
	log.Trace("ChangeRepositoryName: %s/%s -> %s", doer.Name, repo.Name, newRepoName)

	oldRepoName := repo.Name

	// Change repository directory name. We must lock the local copy of the
	// repo so that we can automatically rename the repo path and updates the
	// local copy's origin accordingly.

	releaser, err := globallock.Lock(ctx, getRepoWorkingLockKey(repo.ID))
	if err != nil {
		log.Error("lock.Lock(): %v", err)
		return fmt.Errorf("lock.Lock: %w", err)
	}
	defer releaser()

	actor := repositoryRequestActor(ctx, doer)
	if err := governance_service.WithActorWrite(ctx, actor, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		freshDoer, err := user_model.GetUserByID(ctx, actor.EffectiveUserID())
		if err != nil {
			return err
		}
		fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
		if err != nil {
			return err
		}
		if fresh.OwnerID != repo.OwnerID || fresh.Name != repo.Name || fresh.OwnerName != repo.OwnerName {
			return governance_model.ErrConflict
		}
		if _, err := governance_service.CheckRepositoryMutationAbility(ctx, freshDoer.ID, fresh.ID, governance_model.ManageProject); err != nil {
			if !errors.Is(err, governance_model.ErrNotFound) {
				return err
			}
			// 兼容尚未初始化治理命名空间的原生仓库；改名仍要求原生 Admin。
			permission, permissionErr := access_model.GetIndividualUserRepoPermission(ctx, fresh, freshDoer)
			if permissionErr != nil || !permission.IsAdmin() {
				return err
			}
		}
		if err := fresh.LoadOwner(ctx); err != nil {
			return err
		}
		if err := changeRepositoryName(ctx, fresh, newRepoName); err != nil {
			return err
		}
		// 改名在此提交；后续保存普通设置不能被当成过期路径，也不能撤销这次改名。
		renamed := *fresh
		renamed.Name, renamed.LowerName = newRepoName, strings.ToLower(newRepoName)
		if err := repo_model.UpdateRepositoryColsNoAutoTime(ctx, &renamed, "name", "lower_name"); err != nil {
			return err
		}
		*repo = renamed
		return nil
	}); err != nil {
		return err
	}
	releaser()

	repo.Name = newRepoName
	notify_service.RenameRepository(ctx, doer, repo, oldRepoName)

	return nil
}

// StartRepositoryTransfer transfer a repo from one owner to a new one.
// it make repository into pending transfer state, if doer can not create repo for new owner.
func StartRepositoryTransfer(ctx context.Context, doer, newOwner *user_model.User, repo *repo_model.Repository, teams []*organization.Team) error {
	return startRepositoryTransfer(ctx, doer, newOwner, repo, teams, nil)
}

// StartRepositoryTransferAfterPreview 要求确认提交与刚才展示的稳定身份一致。
// 预览本身不是授权凭据，锁内仍会重新计算操作者权限和目标资格。
func StartRepositoryTransferAfterPreview(ctx context.Context, doer, newOwner *user_model.User, repo *repo_model.Repository, teams []*organization.Team, impact *RepositoryTransferImpact) error {
	return startRepositoryTransfer(ctx, doer, newOwner, repo, teams, impact)
}

func startRepositoryTransfer(ctx context.Context, doer, newOwner *user_model.User, repo *repo_model.Repository, teams []*organization.Team, impact *RepositoryTransferImpact) error {
	releaser, err := globallock.Lock(ctx, getRepoWorkingLockKey(repo.ID))
	if err != nil {
		return fmt.Errorf("lock.Lock: %w", err)
	}
	defer releaser()

	if err := repo_model.TestRepositoryReadyForTransfer(repo.Status); err != nil {
		return err
	}

	if !doer.CanForkRepoIn(newOwner) {
		return LimitReachedError{Limit: newOwner.MaxCreationLimit()}
	}

	var isDirectTransfer bool
	oldOwnerName := repo.OwnerName
	requestedRepo := repo
	actor := repositoryRequestActor(ctx, doer)
	if err := governance_service.WithActorWrite(ctx, actor, []string{governance_model.Resource("repository", repo.ID), governance_model.Resource("group", repo.OwnerID), governance_model.Resource("group", newOwner.ID)}, func(ctx context.Context) error {
		return db.WithTx(ctx, func(ctx context.Context) error {
			freshDoer, err := user_model.GetUserByID(ctx, actor.EffectiveUserID())
			if err != nil {
				return err
			}
			freshRepo, err := governance_service.CheckRepositoryOwnerMutation(ctx, freshDoer.ID, repo.ID)
			if err != nil {
				return err
			}
			freshOwner, err := user_model.GetUserByID(ctx, newOwner.ID)
			if err != nil {
				return err
			}
			if impact != nil {
				if err := impact.Validate(ctx, freshRepo, freshOwner); err != nil {
					return err
				}
			}
			if freshRepo.OwnerID != repo.OwnerID || freshRepo.Name != repo.Name || freshRepo.Status != repo.Status {
				return governance_model.ErrConflict
			}
			if err := freshRepo.LoadOwner(ctx); err != nil {
				return err
			}
			doer, newOwner, repo = freshDoer, freshOwner, freshRepo
			if err := repo_model.TestRepositoryReadyForTransfer(repo.Status); err != nil {
				return err
			}
			if !doer.CanForkRepoIn(newOwner) {
				return LimitReachedError{Limit: newOwner.MaxCreationLimit()}
			}
			// Admin is always allowed to transfer || user transfer repo back to his account,
			// then it will transfer directly without acceptance.
			if doer.IsAdmin || doer.ID == newOwner.ID {
				isDirectTransfer = true
				return transferOwnershipLocked(ctx, doer, newOwner, repo, teams)
			}

			if user_model.IsUserBlockedBy(ctx, doer, newOwner.ID) {
				return user_model.ErrBlockedUser
			}

			// If new owner is an org and user can create repos he can transfer directly too
			if newOwner.IsOrganization() {
				allowed, err := organization.CanCreateOrgRepo(ctx, newOwner.ID, doer.ID)
				if err != nil {
					return err
				}
				if allowed {
					isDirectTransfer = true
					return transferOwnershipLocked(ctx, doer, newOwner, repo, teams)
				}
			}

			// In case the new owner would not have sufficient access to the repo, give access rights for read
			hasAccess, err := access_model.HasAnyUnitAccess(ctx, newOwner.ID, repo)
			if err != nil {
				return err
			}
			if !hasAccess {
				if err := AddOrUpdateCollaborator(ctx, repo, newOwner, perm.AccessModeRead); err != nil {
					return err
				}
			}

			// Make repo as pending for transfer
			repo.Status = repo_model.RepositoryPendingTransfer
			if err := repo_model.CreatePendingRepositoryTransfer(ctx, doer, newOwner, repo.ID, teams); err != nil {
				return err
			}
			*requestedRepo = *repo
			return nil
		})
	}); err != nil {
		return err
	}
	*requestedRepo = *repo

	if isDirectTransfer {
		notify_service.TransferRepository(ctx, doer, repo, oldOwnerName)
	} else {
		// notify users who are able to accept / reject transfer
		notify_service.RepoPendingTransfer(ctx, doer, newOwner, repo)
	}

	return nil
}

// RejectRepositoryTransfer marks the repository as ready and remove pending transfer entry,
// thus cancel the transfer process.
// The accepter can reject the transfer.
func RejectRepositoryTransfer(ctx context.Context, repo *repo_model.Repository, doer *user_model.User) error {
	actor := repositoryRequestActor(ctx, doer)
	return governance_service.WithActorWrite(ctx, actor, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		return db.WithTx(ctx, func(ctx context.Context) error {
			freshRepo, err := repo_model.GetRepositoryByID(ctx, repo.ID)
			if err != nil {
				return err
			}
			freshDoer, err := user_model.GetUserByID(ctx, actor.EffectiveUserID())
			if err != nil {
				return err
			}
			repoTransfer, err := repo_model.GetPendingRepositoryTransfer(ctx, freshRepo)
			if err != nil {
				return err
			}

			if err := repoTransfer.LoadAttributes(ctx); err != nil {
				return err
			}

			if !repoTransfer.CanUserAcceptOrRejectTransfer(ctx, freshDoer) {
				return util.ErrPermissionDenied
			}

			freshRepo.Status = repo_model.RepositoryReady
			if err := repo_model.UpdateRepositoryColsNoAutoTime(ctx, freshRepo, "status"); err != nil {
				return err
			}

			return repo_model.DeleteRepositoryTransfer(ctx, freshRepo.ID)
		})
	})
}

func canUserCancelTransfer(ctx context.Context, r *repo_model.RepoTransfer, u *user_model.User) bool {
	if u.IsAdmin || u.ID == r.DoerID {
		return true
	}

	if err := r.LoadAttributes(ctx); err != nil {
		log.Error("LoadAttributes: %v", err)
		return false
	}

	if err := r.Repo.LoadOwner(ctx); err != nil {
		log.Error("LoadOwner: %v", err)
		return false
	}

	if !r.Repo.Owner.IsOrganization() {
		return r.Repo.OwnerID == u.ID
	}

	perm, err := access_model.GetIndividualUserRepoPermission(ctx, r.Repo, u)
	if err != nil {
		log.Error("GetIndividualUserRepoPermission: %v", err)
		return false
	}
	return perm.IsOwner()
}

// CancelRepositoryTransfer cancels the repository transfer process. The sender or
// the users who have admin permission of the original repository can cancel the transfer
func CancelRepositoryTransfer(ctx context.Context, repoTransfer *repo_model.RepoTransfer, doer *user_model.User) error {
	actor := repositoryRequestActor(ctx, doer)
	return governance_service.WithActorWrite(ctx, actor, []string{governance_model.Resource("repository", repoTransfer.RepoID)}, func(ctx context.Context) error {
		return db.WithTx(ctx, func(ctx context.Context) error {
			freshDoer, err := user_model.GetUserByID(ctx, actor.EffectiveUserID())
			if err != nil {
				return err
			}
			freshTransfer, err := repo_model.GetPendingRepositoryTransfer(ctx, &repo_model.Repository{ID: repoTransfer.RepoID})
			if err != nil {
				return err
			}
			if err := freshTransfer.LoadAttributes(ctx); err != nil {
				return err
			}

			if !canUserCancelTransfer(ctx, freshTransfer, freshDoer) {
				return util.ErrPermissionDenied
			}

			freshTransfer.Repo.Status = repo_model.RepositoryReady
			if err := repo_model.UpdateRepositoryColsNoAutoTime(ctx, freshTransfer.Repo, "status"); err != nil {
				return err
			}

			return repo_model.DeleteRepositoryTransfer(ctx, freshTransfer.RepoID)
		})
	})
}
