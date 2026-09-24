// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"context"
	"errors"
	"fmt"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	org_model "gitea.dev/models/organization"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	governance_service "gitea.dev/services/governance"
	repo_service "gitea.dev/services/repository"
)

func CanBlockUser(ctx context.Context, doer, blocker, blockee *user_model.User) bool {
	var err error
	doer, blocker, blockee, err = currentBlockUsers(ctx, doer, blocker, blockee)
	if err != nil {
		return false
	}
	return canBlockCurrent(ctx, doer, blocker, blockee)
}

func canBlockCurrent(ctx context.Context, doer, blocker, blockee *user_model.User) bool {
	if blocker.ID == blockee.ID {
		return false
	}
	if doer.ID == blockee.ID {
		return false
	}

	if blockee.IsOrganization() {
		return false
	}

	if user_model.IsUserBlockedBy(ctx, blockee, blocker.ID) {
		return false
	}

	if blocker.IsOrganization() {
		org := org_model.OrgFromUser(blocker)
		if isMember, err := org.IsOrgMember(ctx, blockee.ID); err != nil || isMember {
			return false
		}
	}
	if !canManageBlocker(ctx, doer, blocker) {
		return false
	}

	return true
}

func CanUnblockUser(ctx context.Context, doer, blocker, blockee *user_model.User) bool {
	var err error
	doer, blocker, blockee, err = currentBlockUsers(ctx, doer, blocker, blockee)
	if err != nil {
		return false
	}
	return canUnblockCurrent(ctx, doer, blocker, blockee)
}

func canUnblockCurrent(ctx context.Context, doer, blocker, blockee *user_model.User) bool {
	if doer.ID == blockee.ID {
		return false
	}

	if !user_model.IsUserBlockedBy(ctx, blockee, blocker.ID) {
		return false
	}

	if !canManageBlocker(ctx, doer, blocker) {
		return false
	}
	if blocker.IsOrganization() {
		if checkBlockerLifecycle(ctx, blocker.ID) != nil {
			return false
		}
	}

	return true
}

func canManageBlocker(ctx context.Context, doer, blocker *user_model.User) bool {
	if doer.IsAdmin {
		return true
	}
	if !blocker.IsOrganization() {
		return doer.ID == blocker.ID
	}
	owned, err := org_model.OrgFromUser(blocker).IsOwnedBy(ctx, doer.ID)
	return err == nil && owned
}

func currentBlockUsers(ctx context.Context, doer, blocker, blockee *user_model.User) (*user_model.User, *user_model.User, *user_model.User, error) {
	if doer == nil || blocker == nil || blockee == nil {
		return nil, nil, nil, user_model.ErrCanNotBlock
	}
	currentDoer, err := user_model.GetUserByID(ctx, doer.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	if !currentDoer.IsActive || currentDoer.ProhibitLogin || currentDoer.IsOrganization() || currentDoer.IsGiteaActions() || currentDoer.IsGhost() {
		return nil, nil, nil, user_model.ErrCanNotBlock
	}
	currentBlocker, err := user_model.GetUserByID(ctx, blocker.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	currentBlockee, err := user_model.GetUserByID(ctx, blockee.ID)
	return currentDoer, currentBlocker, currentBlockee, err
}

func checkBlockerLifecycle(ctx context.Context, groupID int64) error {
	ancestors, err := governance_model.Ancestors(ctx, groupID)
	if errors.Is(err, governance_model.ErrNotFound) {
		return nil // 原生旧组织可能尚无治理命名空间。
	}
	if err != nil {
		return err
	}
	if ancestors[0].Kind != "group" {
		return governance_model.ErrNotFound
	}
	for _, ancestor := range ancestors {
		if ancestor.Archived || ancestor.DeleteAfter != 0 {
			return governance_model.ErrConflict
		}
	}
	return nil
}

func withBlockWrite(ctx context.Context, doer, blocker, blockee *user_model.User, write func(context.Context) error) error {
	actor := governance_model.AuditActor(ctx)
	if actor.EffectiveUserID() > 0 && actor.EffectiveUserID() != doer.ID {
		return governance_model.ErrForbidden
	}
	return governance_service.WithActorWrite(ctx, actor, []string{governance_model.Resource("group", blocker.ID), governance_model.Resource("user", doer.ID), governance_model.Resource("user", blockee.ID)}, write)
}

func appendBlockAudit(ctx context.Context, blocker, blockee *user_model.User, eventType string, before, after, noteChanged bool) error {
	scopeType := "user"
	var ancestors []int64
	if blocker.IsOrganization() {
		scopeType = "group"
		chain, err := governance_model.Ancestors(ctx, blocker.ID)
		if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
			return err
		}
		for _, group := range chain {
			ancestors = append(ancestors, group.ID)
		}
	}
	details, err := json.Marshal(map[string]any{"blocked_before": before, "blocked_after": after, "note_changed": noteChanged})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{
		Type: eventType, Actor: governance_model.AuditActor(ctx), ScopeType: scopeType, ScopeID: blocker.ID,
		AncestorIDs: ancestors, ObjectType: "user", ObjectID: blockee.ID, ObjectPath: blockee.Name,
		Result: "success", Details: details,
	})
}

func BlockUser(ctx context.Context, doer, blocker, blockee *user_model.User, note string) error {
	if doer == nil || blocker == nil || blockee == nil {
		return user_model.ErrCanNotBlock
	}
	if blockee.IsOrganization() {
		return user_model.ErrBlockOrganization
	}
	return withBlockWrite(ctx, doer, blocker, blockee, func(ctx context.Context) error {
		var err error
		doer, blocker, blockee, err = currentBlockUsers(ctx, doer, blocker, blockee)
		if err != nil || !canBlockCurrent(ctx, doer, blocker, blockee) {
			return user_model.ErrCanNotBlock
		}
		// unfollow each other
		if err := user_model.UnfollowUser(ctx, blocker.ID, blockee.ID); err != nil {
			return err
		}
		if err := user_model.UnfollowUser(ctx, blockee.ID, blocker.ID); err != nil {
			return err
		}

		// unstar each other
		if err := unstarRepos(ctx, blocker, blockee); err != nil {
			return err
		}
		if err := unstarRepos(ctx, blockee, blocker); err != nil {
			return err
		}

		// unwatch each others repositories
		if err := unwatchRepos(ctx, blocker, blockee); err != nil {
			return err
		}
		if err := unwatchRepos(ctx, blockee, blocker); err != nil {
			return err
		}

		// unassign each other from issues
		if err := unassignIssues(ctx, blocker, blockee); err != nil {
			return err
		}
		if err := unassignIssues(ctx, blockee, blocker); err != nil {
			return err
		}

		// remove each other from repository collaborations
		if err := removeCollaborations(ctx, blocker, blockee); err != nil {
			return err
		}
		if err := removeCollaborations(ctx, blockee, blocker); err != nil {
			return err
		}

		// cancel each other repository transfers
		if err := cancelRepositoryTransfers(ctx, doer, blocker, blockee); err != nil {
			return err
		}
		if err := cancelRepositoryTransfers(ctx, doer, blockee, blocker); err != nil {
			return err
		}

		if err := db.Insert(ctx, &user_model.Blocking{
			BlockerID: blocker.ID,
			BlockeeID: blockee.ID,
			Note:      note,
		}); err != nil {
			return err
		}
		return appendBlockAudit(ctx, blocker, blockee, "user.blocked", false, true, false)
	})
}

func unstarRepos(ctx context.Context, starrer, repoOwner *user_model.User) error {
	seen := make(map[int64]bool)
	opts := &repo_model.StarredReposOptions{
		ListOptions: db.ListOptions{
			Page:     1,
			PageSize: 25,
		},
		StarrerID:   starrer.ID,
		RepoOwnerID: repoOwner.ID,
	}

	for {
		repos, err := repo_model.GetStarredRepos(ctx, opts)
		if err != nil {
			return err
		}

		if len(repos) == 0 {
			return nil
		}

		for _, repo := range repos {
			if seen[repo.ID] {
				return fmt.Errorf("starring repository %d did not advance", repo.ID)
			}
			seen[repo.ID] = true
			if err := repo_model.StarRepo(ctx, starrer, repo, false); err != nil {
				return err
			}
		}
	}
}

func unwatchRepos(ctx context.Context, watcher, repoOwner *user_model.User) error {
	seen := make(map[int64]bool)
	opts := &repo_model.WatchedReposOptions{
		ListOptions: db.ListOptions{
			Page:     1,
			PageSize: 25,
		},
		WatcherID:   watcher.ID,
		RepoOwnerID: repoOwner.ID,
	}

	for {
		repos, _, err := repo_model.GetWatchedRepos(ctx, opts)
		if err != nil {
			return err
		}

		if len(repos) == 0 {
			return nil
		}

		for _, repo := range repos {
			if seen[repo.ID] {
				return fmt.Errorf("watching repository %d did not advance", repo.ID)
			}
			seen[repo.ID] = true
			if err := repo_model.WatchRepo(ctx, watcher, repo, false); err != nil {
				return err
			}
		}
	}
}

func cancelRepositoryTransfers(ctx context.Context, doer, sender, recipient *user_model.User) error {
	transfers, err := repo_model.GetPendingRepositoryTransfers(ctx, &repo_model.PendingRepositoryTransferOptions{
		SenderID:    sender.ID,
		RecipientID: recipient.ID,
	})
	if err != nil {
		return err
	}

	for _, transfer := range transfers {
		if err := repo_service.CancelRepositoryTransfer(ctx, transfer, doer); err != nil {
			return err
		}
	}

	return nil
}

func unassignIssues(ctx context.Context, assignee, repoOwner *user_model.User) error {
	seen := make(map[int64]bool)
	opts := &issues_model.AssignedIssuesOptions{
		ListOptions: db.ListOptions{
			Page:     1,
			PageSize: 25,
		},
		AssigneeID:  assignee.ID,
		RepoOwnerID: repoOwner.ID,
	}

	for {
		issues, _, err := issues_model.GetAssignedIssues(ctx, opts)
		if err != nil {
			return err
		}

		if len(issues) == 0 {
			return nil
		}

		for _, issue := range issues {
			if seen[issue.ID] {
				return fmt.Errorf("assigned issue %d did not advance", issue.ID)
			}
			seen[issue.ID] = true
			if err := issue.LoadAssignees(ctx); err != nil {
				return err
			}

			if _, _, err := issues_model.ToggleIssueAssignee(ctx, issue, assignee, assignee.ID); err != nil {
				return err
			}
		}
	}
}

func removeCollaborations(ctx context.Context, repoOwner, collaborator *user_model.User) error {
	seen := make(map[int64]bool)
	opts := &repo_model.FindCollaborationOptions{
		ListOptions: db.ListOptions{
			Page:     1,
			PageSize: 25,
		},
		CollaboratorID: collaborator.ID,
		RepoOwnerID:    repoOwner.ID,
	}

	for {
		collaborations, _, err := repo_model.GetCollaborators(ctx, opts)
		if err != nil {
			return err
		}

		if len(collaborations) == 0 {
			return nil
		}

		for _, collaboration := range collaborations {
			if seen[collaboration.Collaboration.ID] {
				return fmt.Errorf("collaboration %d did not advance", collaboration.Collaboration.ID)
			}
			seen[collaboration.Collaboration.ID] = true
			repo, err := repo_model.GetRepositoryByID(ctx, collaboration.Collaboration.RepoID)
			if err != nil {
				return err
			}

			if err := repo_service.DeleteCollaboration(ctx, repo, collaborator); err != nil {
				return err
			}
		}
	}
}

func UnblockUser(ctx context.Context, doer, blocker, blockee *user_model.User) error {
	if doer == nil || blocker == nil || blockee == nil {
		return user_model.ErrCanNotUnblock
	}
	if blockee.IsOrganization() {
		return user_model.ErrBlockOrganization
	}
	return withBlockWrite(ctx, doer, blocker, blockee, func(ctx context.Context) error {
		var err error
		doer, blocker, blockee, err = currentBlockUsers(ctx, doer, blocker, blockee)
		if err != nil {
			return user_model.ErrCanNotUnblock
		}
		if !canManageBlocker(ctx, doer, blocker) {
			return user_model.ErrCanNotUnblock
		}
		if blocker.IsOrganization() {
			if err := checkBlockerLifecycle(ctx, blocker.ID); err != nil {
				return err
			}
		}
		if !canUnblockCurrent(ctx, doer, blocker, blockee) {
			return user_model.ErrCanNotUnblock
		}
		block, err := user_model.GetBlocking(ctx, blocker.ID, blockee.ID)
		if err != nil {
			return err
		}
		if block != nil {
			deleted, err := db.DeleteByID[user_model.Blocking](ctx, block.ID)
			if err != nil {
				return err
			}
			if deleted == 0 {
				return user_model.ErrCanNotUnblock
			}
			return appendBlockAudit(ctx, blocker, blockee, "user.unblocked", true, false, false)
		}
		return nil
	})
}

// UpdateBlockingNote updates an existing record after checking current blocker ownership.
func UpdateBlockingNote(ctx context.Context, doer, blocker, blockee *user_model.User, note string) error {
	if doer == nil || blocker == nil || blockee == nil {
		return user_model.ErrCanNotBlock
	}
	return withBlockWrite(ctx, doer, blocker, blockee, func(ctx context.Context) error {
		doer, blocker, blockee, err := currentBlockUsers(ctx, doer, blocker, blockee)
		if err != nil || !canManageBlocker(ctx, doer, blocker) {
			return user_model.ErrCanNotBlock
		}
		block, err := user_model.GetBlocking(ctx, blocker.ID, blockee.ID)
		if err != nil {
			return err
		}
		if block.Note == note {
			return nil
		}
		if err := user_model.UpdateBlockingNote(ctx, block.ID, note); err != nil {
			return err
		}
		return appendBlockAudit(ctx, blocker, blockee, "user.block_note_updated", true, true, true)
	})
}
