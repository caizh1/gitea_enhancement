// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"context"
	"fmt"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	access_model "gitea.dev/models/perm/access"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	system_model "gitea.dev/models/system"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/git"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/log"
	"gitea.dev/modules/storage"
	"gitea.dev/modules/util"
	notify_service "gitea.dev/services/notify"
)

// NewIssue creates new issue with labels for repository.
func NewIssue(ctx context.Context, repo *repo_model.Repository, issue *issues_model.Issue, labelIDs []int64, uuids []string, assigneeIDs, projectIDs []int64) error {
	if err := access_model.CheckAuditorParticipation(ctx, repo.ID, issue.PosterID, unit.TypeIssues); err != nil {
		return err
	}
	if err := issue.LoadPoster(ctx); err != nil {
		return err
	}

	if user_model.IsUserBlockedBy(ctx, issue.Poster, repo.OwnerID) || user_model.IsUserBlockedBy(ctx, issue.Poster, assigneeIDs...) {
		return user_model.ErrBlockedUser
	}

	assigneeCommentMap := make(map[int64]*issues_model.Comment)
	assignees := make(map[int64]*user_model.User)
	if err := db.WithTx(ctx, func(ctx context.Context) error {
		if err := issues_model.NewIssue(ctx, repo, issue, labelIDs, uuids); err != nil {
			return err
		}
		for _, assigneeID := range assigneeIDs {
			assignee, err := user_model.GetUserByID(ctx, assigneeID)
			if err != nil {
				log.Error("GetUserByID: %v", err)
				continue
			}
			assignees[assigneeID] = assignee
			comment, err := AddAssigneeIfNotAssigned(ctx, issue, issue.Poster, assignee)
			if err != nil {
				return err
			}
			assigneeCommentMap[assigneeID] = comment
		}
		if len(projectIDs) > 0 {
			err := issues_model.IssueAssignOrRemoveProject(ctx, issue, issue.Poster, projectIDs)
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	mentions, err := issues_model.FindAndUpdateIssueMentions(ctx, issue, issue.Poster, issue.Content)
	if err != nil {
		return err
	}

	notify_service.NewIssue(ctx, issue, mentions)
	if len(issue.Labels) > 0 {
		notify_service.IssueChangeLabels(ctx, issue.Poster, issue, issue.Labels, nil)
	}
	if issue.Milestone != nil {
		notify_service.IssueChangeMilestone(ctx, issue.Poster, issue, 0)
	}

	if len(assigneeIDs) > 0 {
		for _, assignee := range assignees {
			notify_service.IssueChangeAssignee(ctx, issue.Poster, issue, assignee, false, assigneeCommentMap[assignee.ID])
		}
	}

	return nil
}

// ChangeTitle changes the title of this issue, as the given user.
func ChangeTitle(ctx context.Context, issue *issues_model.Issue, doer *user_model.User, title string) error {
	oldTitle := issue.Title
	issue.Title = title

	if oldTitle == title {
		return nil
	}

	if err := issue.LoadRepo(ctx); err != nil {
		return err
	}

	if user_model.IsUserBlockedBy(ctx, doer, issue.PosterID, issue.Repo.OwnerID) {
		if isAdmin, _ := access_model.IsUserRepoAdmin(ctx, issue.Repo, doer); !isAdmin {
			return user_model.ErrBlockedUser
		}
	}

	if err := issues_model.ChangeIssueTitle(ctx, issue, doer, oldTitle); err != nil {
		return err
	}

	var reviewNotifiers []*ReviewRequestNotifier
	if issue.IsPull && issues_model.HasWorkInProgressPrefix(oldTitle) && !issues_model.HasWorkInProgressPrefix(title) {
		if err := issue.LoadPullRequest(ctx); err != nil {
			return err
		}

		var err error
		reviewNotifiers, err = PullRequestCodeOwnersReview(ctx, issue.PullRequest)
		if err != nil {
			log.Error("PullRequestCodeOwnersReview: %v", err)
		}
	}

	notify_service.IssueChangeTitle(ctx, doer, issue, oldTitle)
	ReviewRequestNotify(ctx, issue, issue.Poster, reviewNotifiers)
	if issue.IsPull && issues_model.HasWorkInProgressPrefix(oldTitle) && !issues_model.HasWorkInProgressPrefix(title) {
		SyncGovernanceReviewRequests(ctx, issue.PullRequest, doer)
	}

	return nil
}

// ChangeTimeEstimate changes the time estimate of this issue, as the given user.
func ChangeTimeEstimate(ctx context.Context, issue *issues_model.Issue, doer *user_model.User, timeEstimate int64) (err error) {
	issue.TimeEstimate = timeEstimate

	return issues_model.ChangeIssueTimeEstimate(ctx, issue, doer, timeEstimate)
}

// ChangeIssueRef changes the branch of this issue, as the given user.
func ChangeIssueRef(ctx context.Context, issue *issues_model.Issue, doer *user_model.User, ref string) error {
	oldRef := issue.Ref
	issue.Ref = ref

	if err := issues_model.ChangeIssueRef(ctx, issue, doer, oldRef); err != nil {
		return err
	}

	notify_service.IssueChangeRef(ctx, doer, issue, oldRef)

	return nil
}

// DeleteIssue deletes an issue
func DeleteIssue(ctx context.Context, doer *user_model.User, issue *issues_model.Issue) error {
	// load issue before deleting it
	if err := issue.LoadAttributes(ctx); err != nil {
		return err
	}
	if err := issue.LoadPullRequest(ctx); err != nil {
		return err
	}

	var attachmentPaths []string
	err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", issue.RepoID), governance_model.Resource("issue", issue.ID)}, func(ctx context.Context) error {
		var err error
		attachmentPaths, err = deleteIssue(ctx, issue)
		return err
	})
	if err != nil {
		return err
	}
	for _, attachmentPath := range attachmentPaths {
		system_model.RemoveStorageWithNotice(ctx, storage.Attachments, "Delete issue attachment", attachmentPath)
	}

	// delete pull request related git data
	if issue.IsPull {
		if err := issue.PullRequest.LoadBaseRepo(ctx); err != nil {
			return err
		}
		if err := gitrepo.RemoveRef(ctx, issue.PullRequest.BaseRepo, issue.PullRequest.GetGitHeadRefName()); err != nil {
			return err
		}
	}

	notify_service.DeleteIssue(ctx, doer, issue)

	return nil
}

// GetRefEndNamesAndURLs retrieves the ref end names (e.g. refs/heads/branch-name -> branch-name)
// and their respective URLs.
func GetRefEndNamesAndURLs(issues []*issues_model.Issue, repoLink string) (map[int64]string, map[int64]string) {
	issueRefEndNames := make(map[int64]string, len(issues))
	issueRefURLs := make(map[int64]string, len(issues))
	for _, issue := range issues {
		if issue.Ref != "" {
			ref := git.RefName(issue.Ref)
			issueRefEndNames[issue.ID] = ref.ShortName()
			issueRefURLs[issue.ID] = repoLink + "/src/" + ref.RefWebLinkPath()
		}
	}
	return issueRefEndNames, issueRefURLs
}

// deleteIssue deletes the issue
func deleteIssue(ctx context.Context, issue *issues_model.Issue) ([]string, error) {
	return db.WithTx2(ctx, func(ctx context.Context) ([]string, error) {
		freshIssue, err := issues_model.GetIssueByID(ctx, issue.ID)
		if err != nil {
			return nil, err
		}
		if _, err := db.GetEngine(ctx).ID(issue.ID).NoAutoCondition().Delete(issue); err != nil {
			return nil, err
		}

		if err := issues_model.DecrRepoIssueNumbers(ctx, issue.RepoID, issue.IsPull, true, issue.IsClosed); err != nil {
			return nil, err
		}

		if err := issues_model.UpdateMilestoneCounters(ctx, issue.MilestoneID); err != nil {
			return nil, fmt.Errorf("error updating counters for milestone id %d: %w",
				issue.MilestoneID, err)
		}

		if err := activities_model.DeleteIssueActions(ctx, issue.RepoID, issue.ID, issue.Index); err != nil {
			return nil, err
		}

		var comments []*issues_model.Comment
		if err := db.GetEngine(ctx).Where("issue_id = ?", issue.ID).Find(&comments); err != nil {
			return nil, err
		}
		for _, comment := range comments {
			if comment.Type == issues_model.CommentTypeComment || comment.Type == issues_model.CommentTypeCode {
				if err := issues_model.AppendContentAudit(ctx, "comment.deleted", freshIssue, "comment", comment.ID, map[string]any{"comment_type": comment.Type.String(), "issue_id": issue.ID}); err != nil {
					return nil, err
				}
			}
		}
		var attachments []*repo_model.Attachment
		if err := db.GetEngine(ctx).Where("issue_id = ?", issue.ID).Find(&attachments); err != nil {
			return nil, err
		}
		if _, err := repo_model.DeleteAttachments(ctx, attachments, true); err != nil {
			return nil, err
		}
		if !freshIssue.IsPull {
			if err := issues_model.AppendContentAudit(ctx, "issue.deleted", freshIssue, "issue", freshIssue.ID, map[string]any{"before": map[string]any{"state": util.Iif(freshIssue.IsClosed, "closed", "open"), "title": freshIssue.Title}}); err != nil {
				return nil, err
			}
		}

		// delete all database data still assigned to this issue
		if err := db.DeleteBeans(ctx,
			&issues_model.ContentHistory{IssueID: issue.ID},
			&issues_model.Comment{IssueID: issue.ID},
			&issues_model.IssueLabel{IssueID: issue.ID},
			&issues_model.IssueDependency{IssueID: issue.ID},
			&issues_model.IssueAssignees{IssueID: issue.ID},
			&issues_model.IssueUser{IssueID: issue.ID},
			&activities_model.Notification{IssueID: issue.ID},
			&issues_model.Reaction{IssueID: issue.ID},
			&issues_model.IssueWatch{IssueID: issue.ID},
			&issues_model.Stopwatch{IssueID: issue.ID},
			&issues_model.TrackedTime{IssueID: issue.ID},
			&project_model.ProjectIssue{IssueID: issue.ID},
			&issues_model.PullRequest{IssueID: issue.ID},
			&issues_model.Comment{RefIssueID: issue.ID},
			&issues_model.IssueDependency{DependencyID: issue.ID},
			&issues_model.Comment{DependentIssueID: issue.ID},
			&issues_model.IssuePin{IssueID: issue.ID},
		); err != nil {
			return nil, err
		}

		return nil, nil
	})
}

// DeleteOrphanedIssues delete issues without a repo
func DeleteOrphanedIssues(ctx context.Context) error {
	var attachmentPaths []string
	err := db.WithTx(ctx, func(ctx context.Context) error {
		repoIDs, err := issues_model.GetOrphanedIssueRepoIDs(ctx)
		if err != nil {
			return err
		}
		for i := range repoIDs {
			paths, err := DeleteIssuesByRepoID(ctx, repoIDs[i])
			if err != nil {
				return err
			}
			attachmentPaths = append(attachmentPaths, paths...)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Remove issue attachment files.
	for i := range attachmentPaths {
		system_model.RemoveStorageWithNotice(ctx, storage.Attachments, "Delete issue attachment", attachmentPaths[i])
	}
	return nil
}

// DeleteIssuesByRepoID deletes issues by repositories id
func DeleteIssuesByRepoID(ctx context.Context, repoID int64) (attachmentPaths []string, err error) {
	for {
		issues := make([]*issues_model.Issue, 0, db.DefaultMaxInSize)
		if err := db.GetEngine(ctx).
			Where("repo_id = ?", repoID).
			OrderBy("id").
			Limit(db.DefaultMaxInSize).
			Find(&issues); err != nil {
			return nil, err
		}

		if len(issues) == 0 {
			break
		}

		for _, issue := range issues {
			issueAttachPaths, err := deleteIssue(ctx, issue)
			if err != nil {
				return nil, fmt.Errorf("deleteIssue [issue_id: %d]: %w", issue.ID, err)
			}

			attachmentPaths = append(attachmentPaths, issueAttachPaths...)
		}
	}

	return attachmentPaths, err
}
