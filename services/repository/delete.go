// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"
	"fmt"

	actions_model "gitea.dev/models/actions"
	activities_model "gitea.dev/models/activities"
	admin_model "gitea.dev/models/admin"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/organization"
	packages_model "gitea.dev/models/packages"
	access_model "gitea.dev/models/perm/access"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	secret_model "gitea.dev/models/secret"
	user_model "gitea.dev/models/user"
	"gitea.dev/models/webhook"
	"gitea.dev/modules/lfs"
	"gitea.dev/modules/log"
	actions_service "gitea.dev/services/actions"
	asymkey_service "gitea.dev/services/asymkey"
	issue_service "gitea.dev/services/issue"

	"xorm.io/builder"
)

func deleteDBRepository(ctx context.Context, repoID int64) error {
	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repoID)}, func(context.Context) error { return nil }); err != nil {
		return err
	}
	if cnt, err := db.GetEngine(ctx).ID(repoID).Delete(&repo_model.Repository{}); err != nil {
		return err
	} else if cnt != 1 {
		return repo_model.ErrRepoNotExist{
			ID:        repoID,
			OwnerName: "",
			Name:      "",
		}
	}
	return nil
}

// DeleteRepository deletes a repository for a user or organization.
// make sure if you call this func to close open sessions (sqlite will otherwise get a deadlock)
func DeleteRepositoryDirectly(ctx context.Context, repoID int64, ignoreOrgTeams ...bool) error {
	originalCtx := ctx
	ctx, committer, err := db.TxContext(ctx)
	if err != nil {
		return err
	}
	defer committer.Close()
	sess := db.GetEngine(ctx)

	repo := &repo_model.Repository{}
	has, err := sess.ID(repoID).Get(repo)
	if err != nil {
		return err
	} else if !has {
		return repo_model.ErrRepoNotExist{
			ID:        repoID,
			OwnerName: "",
			Name:      "",
		}
	}

	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
		if err != nil {
			return err
		}
		if authority, ok := ctx.Value(deletionAuthorityKey{}).(deletionAuthority); ok {
			if err := checkRepositoryDeletion(ctx, authority, fresh); err != nil {
				return err
			}
		}
		repo = fresh
		return nil
	}); err != nil {
		return err
	}

	if err := closePullsFromDeletedRepository(ctx, repo.ID); err != nil {
		return err
	}
	ctx = repo_model.WithContentAuditRepositorySnapshot(ctx, repo)

	// Query the action tasks of this repo, they will be needed after they have been deleted to remove the logs
	tasks, err := db.Find[actions_model.ActionTask](ctx, actions_model.FindTaskOptions{RepoID: repoID})
	if err != nil {
		return fmt.Errorf("find actions tasks of repo %v: %w", repoID, err)
	}

	// Query the artifacts of this repo, they will be needed after they have been deleted to remove artifacts files in ObjectStorage
	artifacts, err := db.Find[actions_model.ActionArtifact](ctx, actions_model.FindArtifactsOptions{RepoID: repoID})
	if err != nil {
		return fmt.Errorf("list actions artifacts of repo %v: %w", repoID, err)
	}

	// In case owner is a organization, we have to change repo specific teams
	// if ignoreOrgTeams is not true
	var org *user_model.User
	if len(ignoreOrgTeams) == 0 || !ignoreOrgTeams[0] {
		if org, err = user_model.GetUserByID(ctx, repo.OwnerID); err != nil {
			return err
		}
	}

	// Delete Deploy Keys
	deleted, err := asymkey_service.DeleteRepoDeployKeys(ctx, repoID)
	if err != nil {
		return err
	}
	needRewriteKeysFile := deleted > 0

	if err := git_model.DeleteRepositoryProtectionRules(ctx, repoID); err != nil {
		return err
	}
	if err := webhook.AppendRepositoryWebhookDeletionAudits(ctx, repoID); err != nil {
		return err
	}

	if err := deleteDBRepository(ctx, repoID); err != nil {
		return err
	}

	if org != nil && org.IsOrganization() {
		teams, err := organization.FindOrgTeams(ctx, org.ID)
		if err != nil {
			return err
		}
		for _, t := range teams {
			if !organization.HasTeamRepo(ctx, t.OrgID, t.ID, repoID) {
				continue
			} else if err = removeRepositoryFromTeam(ctx, t, repo, false); err != nil {
				return err
			}
		}
	}

	attachments := make([]*repo_model.Attachment, 0, 20)
	if err = sess.Join("INNER", "`release`", "`release`.id = `attachment`.release_id").
		Where("`release`.repo_id = ?", repoID).
		Find(&attachments); err != nil {
		return err
	}
	releaseAttachments := make([]string, 0, len(attachments))
	for i := 0; i < len(attachments); i++ {
		releaseAttachments = append(releaseAttachments, attachments[i].RelativePath())
	}

	if _, err := db.Exec(ctx, "UPDATE `user` SET num_stars=num_stars-1 WHERE id IN (SELECT `uid` FROM `star` WHERE repo_id = ?)", repo.ID); err != nil {
		return err
	}

	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		hooks, err := db.Find[webhook.Webhook](ctx, webhook.ListWebhookOptions{RepoID: repo.ID})
		if err != nil {
			return err
		}
		for _, hook := range hooks {
			if err := webhook.RedactDeletedHookTasks(ctx, hook.ID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// CleanupEphemeralRunnersByPickedTaskOfRepo deletes ephemeral global/org/user that have started any task of this repo
	// The cannot pick a second task hardening for ephemeral runners expect that task objects remain available until runner deletion
	// This method will delete affected ephemeral global/org/user runners
	// &actions_model.ActionRunner{RepoID: repoID} does only handle ephemeral repository runners
	if err := actions_service.CleanupEphemeralRunnersByPickedTaskOfRepo(ctx, repoID); err != nil {
		return fmt.Errorf("cleanupEphemeralRunners: %w", err)
	}

	if err := db.DeleteBeans(ctx,
		&access_model.Access{RepoID: repo.ID},
		&activities_model.Action{RepoID: repo.ID},
		&repo_model.Collaboration{RepoID: repoID},
		&issues_model.Comment{RefRepoID: repoID},
		&git_model.CommitStatus{RepoID: repoID},
		&git_model.CommitStatusIndex{RepoID: repoID},
		&git_model.CommitStatusSummary{RepoID: repoID},
		&git_model.Branch{RepoID: repoID},
		&git_model.RenamedBranch{RepoID: repoID},
		&git_model.LFSLock{RepoID: repoID},
		&repo_model.LanguageStat{RepoID: repoID},
		&repo_model.RepoLicense{RepoID: repoID},
		&issues_model.Milestone{RepoID: repoID},
		&repo_model.Mirror{RepoID: repoID},
		&activities_model.Notification{RepoID: repoID},
		&repo_model.PushMirror{RepoID: repoID},
		&repo_model.Release{RepoID: repoID},
		&repo_model.RepoIndexerStatus{RepoID: repoID},
		&repo_model.Redirect{RedirectRepoID: repoID},
		&repo_model.RepoTransfer{RepoID: repoID}, // this column doesn't have index, maybe it's fine since the table shouldn't be too large.
		&repo_model.RepoUnit{RepoID: repoID},
		&repo_model.Star{RepoID: repoID},
		&admin_model.Task{RepoID: repoID},
		&repo_model.Watch{RepoID: repoID},
		&webhook.Webhook{RepoID: repoID},
		&secret_model.Secret{RepoID: repoID},
		&actions_model.ActionVariable{RepoID: repoID},
		&actions_model.ActionTaskStep{RepoID: repoID},
		&actions_model.ActionTask{RepoID: repoID},
		&actions_model.ActionRunJob{RepoID: repoID},
		&actions_model.ActionRun{RepoID: repoID},
		&actions_model.ActionRunAttempt{RepoID: repoID},
		&actions_model.ActionRunner{RepoID: repoID},
		&actions_model.ActionScheduleSpec{RepoID: repoID},
		&actions_model.ActionSchedule{RepoID: repoID},
		&actions_model.ActionArtifact{RepoID: repoID},
		&actions_model.ActionRunJobSummary{RepoID: repoID},
		&actions_model.ActionRunnerToken{RepoID: repoID},
		&actions_model.ActionTasksVersion{RepoID: repoID},
		&actions_model.ActionScopedWorkflowSource{SourceRepoID: repoID},
		&issues_model.IssuePin{RepoID: repoID},
	); err != nil {
		return fmt.Errorf("deleteBeans: %w", err)
	}

	// Delete Labels and related objects
	if err := issues_model.DeleteLabelsByRepoID(ctx, repoID); err != nil {
		return err
	}

	// Delete Pulls and related objects
	if err := issues_model.DeletePullsByBaseRepoID(ctx, repoID); err != nil {
		return err
	}

	// Delete Issues and related objects
	var attachmentPaths []string
	if attachmentPaths, err = issue_service.DeleteIssuesByRepoID(ctx, repoID); err != nil {
		return err
	}

	// Delete issue index
	if err := db.DeleteResourceIndex(ctx, "issue_index", repoID); err != nil {
		return err
	}

	if repo.IsFork {
		if _, err := db.Exec(ctx, "UPDATE `repository` SET num_forks=num_forks-1 WHERE id=?", repo.ForkID); err != nil {
			return fmt.Errorf("decrease fork count: %w", err)
		}
	}

	if _, err := db.Exec(ctx, "UPDATE `user` SET num_repos=num_repos-1 WHERE id=?", repo.OwnerID); err != nil {
		return err
	}

	if len(repo.Topics) > 0 {
		if err := repo_model.RemoveTopicsFromRepo(ctx, repo.ID); err != nil {
			return err
		}
	}

	if err := project_model.DeleteProjectByRepoID(ctx, repoID); err != nil {
		return fmt.Errorf("unable to delete projects for repo[%d]: %w", repoID, err)
	}

	// Remove LFS objects
	var lfsObjects []*git_model.LFSMetaObject
	if err = sess.Where("repository_id=?", repoID).Find(&lfsObjects); err != nil {
		return err
	}

	lfsPaths := make([]string, 0, len(lfsObjects))
	for _, v := range lfsObjects {
		count, err := db.CountByBean(ctx, &git_model.LFSMetaObject{Pointer: lfs.Pointer{Oid: v.Oid}})
		if err != nil {
			return err
		}
		if count > 1 {
			continue
		}

		lfsPaths = append(lfsPaths, v.RelativePath())
	}

	if _, err := db.DeleteByBean(ctx, &git_model.LFSMetaObject{RepositoryID: repoID}); err != nil {
		return err
	}

	// Remove archives
	var archives []*repo_model.RepoArchiver
	if err = sess.Where("repo_id=?", repoID).Find(&archives); err != nil {
		return err
	}

	archivePaths := make([]string, 0, len(archives))
	for _, v := range archives {
		archivePaths = append(archivePaths, v.RelativePath())
	}

	if _, err := db.DeleteByBean(ctx, &repo_model.RepoArchiver{RepoID: repoID}); err != nil {
		return err
	}

	if repo.NumForks > 0 {
		if _, err = sess.Exec("UPDATE `repository` SET fork_id=0,is_fork=? WHERE fork_id=?", false, repo.ID); err != nil {
			log.Error("reset 'fork_id' and 'is_fork': %v", err)
		}
	}

	// Get all attachments with both issue_id and release_id are zero
	var newAttachments []*repo_model.Attachment
	if err := sess.Where(builder.Eq{
		"repo_id":    repo.ID,
		"issue_id":   0,
		"release_id": 0,
	}).Find(&newAttachments); err != nil {
		return err
	}

	newAttachmentPaths := make([]string, 0, len(newAttachments))
	for _, attach := range newAttachments {
		newAttachmentPaths = append(newAttachmentPaths, attach.RelativePath())
	}

	if _, err := sess.Where("repo_id=?", repo.ID).Delete(new(repo_model.Attachment)); err != nil {
		return err
	}

	// unlink packages linked to this repository
	if err = packages_model.UnlinkRepositoryFromAllPackages(ctx, repoID); err != nil {
		return err
	}

	cleanup := &governance_model.ResourceCleanup{Kind: "repository", ResourceID: repo.ID, RewriteKeys: needRewriteKeysFile, Actor: governance_model.AuditActor(ctx), ScopeType: "repository", ScopeID: repo.ID, ObjectPath: repo.FullPath()}
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil && err != governance_model.ErrNotFound {
		return err
	}
	for _, ancestor := range chain {
		if ancestor.Kind == "group" {
			cleanup.AncestorIDs = append(cleanup.AncestorIDs, ancestor.ID)
		}
	}
	cleanup.Objects = append(cleanup.Objects, governance_model.CleanupObject{Kind: "git", Path: repo.RelativePath()}, governance_model.CleanupObject{Kind: "git", Path: repo.WikiStorageRepo().RelativePath()})
	for kind, paths := range map[string][]string{"archive": archivePaths, "lfs": lfsPaths, "attachment": append(append(attachmentPaths, releaseAttachments...), newAttachmentPaths...)} {
		for _, path := range paths {
			cleanup.Objects = append(cleanup.Objects, governance_model.CleanupObject{Kind: kind, Path: path})
		}
	}
	if repo.Avatar != "" {
		cleanup.Objects = append(cleanup.Objects, governance_model.CleanupObject{Kind: "repo_avatar", Path: repo.CustomAvatarRelativePath()})
	}
	for _, task := range tasks {
		cleanup.Objects = append(cleanup.Objects, governance_model.CleanupObject{Kind: "action_log", Path: task.LogFilename, InStorage: task.LogInStorage})
	}
	for _, art := range artifacts {
		cleanup.Objects = append(cleanup.Objects, governance_model.CleanupObject{Kind: "artifact", Path: art.StoragePath})
	}
	if _, err := db.GetEngine(ctx).ID(repo.ID).Delete(new(governance_model.RepositoryDeletion)); err != nil {
		return err
	}
	if err := db.DeleteBeans(ctx,
		&governance_model.Membership{ScopeType: "repository", ScopeID: repo.ID},
		&governance_model.Share{ScopeType: "repository", ScopeID: repo.ID},
		&governance_model.Invitation{ScopeType: "repository", ScopeID: repo.ID},
		&governance_model.AccessRequest{ScopeType: "repository", ScopeID: repo.ID},
		&governance_model.AccessRequestSetting{ScopeType: "repository", ScopeID: repo.ID},
	); err != nil {
		return err
	}
	if err := QueueResourceCleanup(ctx, cleanup); err != nil {
		return err
	}
	if err = committer.Commit(); err != nil {
		return err
	}
	committer.Close()
	if !db.InTransaction(originalCtx) {
		if err := RunResourceCleanup(originalCtx, cleanup.ID); err != nil {
			log.Error("仓库数据库已删除，存储清理任务 %d 等待恢复：%v", cleanup.ID, err)
		}
	}

	return nil
}

// DeleteOwnerRepositoriesDirectly calls DeleteRepositoryDirectly for all repos of the given owner
func DeleteOwnerRepositoriesDirectly(ctx context.Context, owner *user_model.User) error {
	for {
		repos, _, err := repo_model.GetUserRepositories(ctx, repo_model.SearchRepoOptions{
			ListOptions: db.ListOptions{
				PageSize: repo_model.RepositoryListDefaultPageSize,
				Page:     1,
			},
			Private: true,
			OwnerID: owner.ID,
			Actor:   owner,
		})
		if err != nil {
			return fmt.Errorf("GetUserRepositories: %w", err)
		}
		if len(repos) == 0 {
			break
		}
		for _, repo := range repos {
			if err := DeleteRepositoryDirectly(ctx, repo.ID); err != nil {
				return fmt.Errorf("unable to delete repository %s for %s[%d]. Error: %w", repo.Name, owner.Name, owner.ID, err)
			}
		}
	}
	return nil
}

// closePullsFromDeletedRepository 在最终删除事务中关闭以本项目为来源的外部 PR。
// 先跳过已关闭项，避免可处理的已关闭回执提前关闭外层事务。
func closePullsFromDeletedRepository(ctx context.Context, repoID int64) error {
	var pulls []*issues_model.PullRequest
	if err := db.GetEngine(ctx).Where("head_repo_id = ? AND base_repo_id <> ? AND has_merged = ?", repoID, repoID, false).Find(&pulls); err != nil {
		return err
	}
	actor := governance_model.AuditActor(ctx)
	doer := user_model.NewGhostUser()
	if actor.EffectiveUserID() > 0 {
		var err error
		doer, err = user_model.GetUserByID(ctx, actor.EffectiveUserID())
		if err != nil {
			return err
		}
	}
	for _, pull := range pulls {
		if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", pull.BaseRepoID)}, func(ctx context.Context) error {
			if err := pull.LoadIssue(ctx); err != nil {
				return err
			}
			if pull.Issue.IsClosed {
				return nil
			}
			if err := issue_service.CloseIssue(ctx, pull.Issue, doer, ""); err != nil {
				return err
			}
			chain, err := governance_model.Ancestors(ctx, pull.Issue.Repo.OwnerID)
			if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
				return err
			}
			var ancestors []int64
			for _, group := range chain {
				if group.Kind == "group" {
					ancestors = append(ancestors, group.ID)
				}
			}
			return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "pull.closed_by_repository_deletion", Actor: actor, ScopeType: "repository", ScopeID: pull.BaseRepoID, AncestorIDs: ancestors, ObjectType: "pull", ObjectID: pull.ID, ObjectPath: pull.Issue.Repo.FullPath(), Result: "success"})
		}); err != nil {
			return err
		}
	}
	return nil
}
