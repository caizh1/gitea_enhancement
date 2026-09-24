// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"context"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/organization"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/log"
	governance_service "gitea.dev/services/governance"
	notify_service "gitea.dev/services/notify"
)

// ReviewRequest add or remove a review request from a user for this PR, and make comment for it.
func ReviewRequest(ctx context.Context, issue *issues_model.Issue, doer *user_model.User, permDoer *access_model.Permission, reviewer *user_model.User, isAdd bool) (comment *issues_model.Comment, err error) {
	err = isValidReviewRequest(ctx, reviewer, doer, isAdd, issue, permDoer)
	if err != nil {
		return nil, err
	}

	if isAdd {
		comment, err = issues_model.AddReviewRequest(ctx, issue, reviewer, doer, false)
	} else {
		comment, err = issues_model.RemoveReviewRequest(ctx, issue, reviewer, doer)
	}

	if err != nil {
		return nil, err
	}

	if comment != nil {
		notify_service.PullRequestReviewRequest(ctx, doer, issue, reviewer, isAdd, comment)
	}

	return comment, err
}

// GovernanceReviewRequest 添加审批规则产生的原生待办；重复请求由模型层幂等忽略。
func GovernanceReviewRequest(ctx context.Context, issue *issues_model.Issue, doer, reviewer *user_model.User) error {
	comment, err := issues_model.AddGovernanceReviewRequest(ctx, issue, reviewer, doer)
	if err != nil || comment == nil {
		return err
	}
	if comment.Assignee != nil {
		reviewer = comment.Assignee
	}
	notify_service.PullRequestReviewRequest(ctx, doer, issue, reviewer, true, comment)
	return nil
}

// SyncGovernanceReviewRequests 为新增的合格审批人补原生待办，不删除任何人工或历史请求。
func SyncGovernanceReviewRequests(ctx context.Context, pr *issues_model.PullRequest, doer *user_model.User) {
	if pr == nil || doer == nil || pr.HasMerged {
		return
	}
	if err := pr.LoadIssue(ctx); err != nil {
		log.Error("Load pull issue for governance review requests: %v", err)
		return
	}
	if pr.Issue.IsClosed || pr.IsWorkInProgress(ctx) {
		return
	}
	ids, err := governance_service.PullApprovalRequestUserIDs(ctx, doer.ID, pr.ID)
	if err != nil {
		log.Error("PullApprovalRequestUserIDs: %v", err)
		return
	}
	for _, id := range ids {
		if id == pr.Issue.PosterID {
			continue
		}
		reviewer, err := user_model.GetUserByID(ctx, id)
		if err != nil {
			log.Error("Get governance approval reviewer %d: %v", id, err)
			continue
		}
		if err := GovernanceReviewRequest(ctx, pr.Issue, doer, reviewer); err != nil {
			log.Error("GovernanceReviewRequest: %v", err)
		}
	}
}

// SyncRepositoryGovernanceReviewRequests 为项目规则或设置变化后的开放 PR 补发新出现的原生待办。
func SyncRepositoryGovernanceReviewRequests(ctx context.Context, repoID int64, doer *user_model.User) {
	if repoID <= 0 || doer == nil {
		return
	}
	var pulls []*issues_model.PullRequest
	if err := db.GetEngine(ctx).Where("base_repo_id = ? AND has_merged = ?", repoID, false).Find(&pulls); err != nil {
		log.Error("Load repository pull requests for governance review requests: %v", err)
		return
	}
	for _, pr := range pulls {
		SyncGovernanceReviewRequests(ctx, pr, doer)
	}
}

// SyncScopeGovernanceReviewRequests 为实例或组策略变化后的后代项目补发原生待办。
func SyncScopeGovernanceReviewRequests(ctx context.Context, scope string, scopeID int64, doer *user_model.User) {
	if doer == nil {
		return
	}
	var repos []*repo_model.Repository
	query := db.GetEngine(ctx)
	if scope == "group" {
		namespace, err := governance_model.GetNamespace(ctx, scopeID)
		if err != nil {
			log.Error("Load governance scope for review requests: %v", err)
			return
		}
		query = query.Where("owner_namespace = ? OR owner_namespace LIKE ?", namespace.FullPath, namespace.FullPath+"/%")
	} else if scope != "instance" {
		return
	}
	if err := query.Find(&repos); err != nil {
		log.Error("Load governance scope repositories for review requests: %v", err)
		return
	}
	for _, repo := range repos {
		SyncRepositoryGovernanceReviewRequests(ctx, repo.ID, doer)
	}
}

// isValidReviewRequest Check permission for ReviewRequest
func isValidReviewRequest(ctx context.Context, reviewer, doer *user_model.User, isAdd bool, issue *issues_model.Issue, permDoer *access_model.Permission) error {
	if reviewer.IsOrganization() {
		return issues_model.ErrNotValidReviewRequest{
			Reason: "Organization can't be added as reviewer",
			UserID: doer.ID,
			RepoID: issue.Repo.ID,
		}
	}
	if doer.IsOrganization() {
		return issues_model.ErrNotValidReviewRequest{
			Reason: "Organization can't be doer to add reviewer",
			UserID: doer.ID,
			RepoID: issue.Repo.ID,
		}
	}

	permReviewer, err := access_model.GetIndividualUserRepoPermission(ctx, issue.Repo, reviewer)
	if err != nil {
		return err
	}

	if permDoer == nil {
		permDoer = new(access_model.Permission)
		*permDoer, err = access_model.GetDoerRepoPermission(ctx, issue.Repo, doer)
		if err != nil {
			return err
		}
	}

	lastReview, err := issues_model.GetReviewByIssueIDAndUserID(ctx, issue.ID, reviewer.ID)
	if err != nil && !issues_model.IsErrReviewNotExist(err) {
		return err
	}

	canDoerChangeReviewRequests := CanDoerChangeReviewRequests(ctx, doer, issue.Repo, issue.PosterID)

	if isAdd {
		if !permReviewer.CanAccessAny(perm.AccessModeRead, unit.TypePullRequests) {
			return issues_model.ErrNotValidReviewRequest{
				Reason: "Reviewer can't read",
				UserID: doer.ID,
				RepoID: issue.Repo.ID,
			}
		}

		if reviewer.ID == issue.PosterID && issue.OriginalAuthorID == 0 {
			return issues_model.ErrNotValidReviewRequest{
				Reason: "poster of pr can't be reviewer",
				UserID: doer.ID,
				RepoID: issue.Repo.ID,
			}
		}

		if canDoerChangeReviewRequests {
			return nil
		}

		if doer.ID == issue.PosterID && issue.OriginalAuthorID == 0 && lastReview != nil && lastReview.Type != issues_model.ReviewTypeRequest {
			return nil
		}

		return issues_model.ErrNotValidReviewRequest{
			Reason: "Doer can't choose reviewer",
			UserID: doer.ID,
			RepoID: issue.Repo.ID,
		}
	}

	if canDoerChangeReviewRequests {
		return nil
	}

	if lastReview != nil && lastReview.Type == issues_model.ReviewTypeRequest && lastReview.ReviewerID == doer.ID {
		return nil
	}

	return issues_model.ErrNotValidReviewRequest{
		Reason: "Doer can't remove reviewer",
		UserID: doer.ID,
		RepoID: issue.Repo.ID,
	}
}

// isValidTeamReviewRequest Check permission for ReviewRequest Team
func isValidTeamReviewRequest(ctx context.Context, reviewer *organization.Team, doer *user_model.User, isAdd bool, issue *issues_model.Issue) error {
	if doer.IsOrganization() {
		return issues_model.ErrNotValidReviewRequest{
			Reason: "Organization can't be doer to add reviewer",
			UserID: doer.ID,
			RepoID: issue.Repo.ID,
		}
	}

	canDoerChangeReviewRequests := CanDoerChangeReviewRequests(ctx, doer, issue.Repo, issue.PosterID)

	if isAdd {
		if issue.Repo.IsPrivate {
			hasTeam := organization.HasTeamRepo(ctx, reviewer.OrgID, reviewer.ID, issue.RepoID)

			if !hasTeam {
				return issues_model.ErrNotValidReviewRequest{
					Reason: "Reviewing team can't read repo",
					UserID: doer.ID,
					RepoID: issue.Repo.ID,
				}
			}
		}

		if canDoerChangeReviewRequests {
			return nil
		}

		return issues_model.ErrNotValidReviewRequest{
			Reason: "Doer can't choose reviewer",
			UserID: doer.ID,
			RepoID: issue.Repo.ID,
		}
	}

	if canDoerChangeReviewRequests {
		return nil
	}

	return issues_model.ErrNotValidReviewRequest{
		Reason: "Doer can't remove reviewer",
		UserID: doer.ID,
		RepoID: issue.Repo.ID,
	}
}

// TeamReviewRequest add or remove a review request from a team for this PR, and make comment for it.
func TeamReviewRequest(ctx context.Context, issue *issues_model.Issue, doer *user_model.User, reviewer *organization.Team, isAdd bool) (comment *issues_model.Comment, err error) {
	err = isValidTeamReviewRequest(ctx, reviewer, doer, isAdd, issue)
	if err != nil {
		return nil, err
	}
	if isAdd {
		comment, err = issues_model.AddTeamReviewRequest(ctx, issue, reviewer, doer, false)
	} else {
		comment, err = issues_model.RemoveTeamReviewRequest(ctx, issue, reviewer, doer)
	}

	if err != nil {
		return nil, err
	}

	if comment == nil || !isAdd {
		return nil, nil //nolint:nilnil // return nil because no comment was created or it is a removal
	}

	return comment, teamReviewRequestNotify(ctx, issue, doer, reviewer, isAdd, comment)
}

func ReviewRequestNotify(ctx context.Context, issue *issues_model.Issue, doer *user_model.User, reviewNotifiers []*ReviewRequestNotifier) {
	for _, reviewNotifier := range reviewNotifiers {
		if reviewNotifier.Reviewer != nil {
			notify_service.PullRequestReviewRequest(ctx, issue.Poster, issue, reviewNotifier.Reviewer, reviewNotifier.IsAdd, reviewNotifier.Comment)
		} else if reviewNotifier.ReviewTeam != nil {
			if err := teamReviewRequestNotify(ctx, issue, issue.Poster, reviewNotifier.ReviewTeam, reviewNotifier.IsAdd, reviewNotifier.Comment); err != nil {
				log.Error("teamReviewRequestNotify: %v", err)
			}
		}
	}
}

// teamReviewRequestNotify notify all user in this team
func teamReviewRequestNotify(ctx context.Context, issue *issues_model.Issue, doer *user_model.User, reviewer *organization.Team, isAdd bool, comment *issues_model.Comment) error {
	// notify all user in this team
	if err := comment.LoadIssue(ctx); err != nil {
		return err
	}

	members, err := organization.GetTeamMembers(ctx, &organization.SearchMembersOptions{
		TeamID: reviewer.ID,
	})
	if err != nil {
		return err
	}

	for _, member := range members {
		if member.ID == comment.Issue.PosterID {
			continue
		}
		comment.AssigneeID = member.ID
		notify_service.PullRequestReviewRequest(ctx, doer, issue, member, isAdd, comment)
	}

	return err
}

// CanDoerChangeReviewRequests returns if the doer can add/remove review requests of a PR
func CanDoerChangeReviewRequests(ctx context.Context, doer *user_model.User, repo *repo_model.Repository, posterID int64) bool {
	if repo.IsArchived {
		return false
	}
	// The poster of the PR can change the reviewers
	if doer.ID == posterID {
		return true
	}

	// The owner of the repo can change the reviewers
	if doer.ID == repo.OwnerID {
		return true
	}

	// Collaborators of the repo can change the reviewers
	isCollaborator, err := repo_model.IsCollaborator(ctx, repo.ID, doer.ID)
	if err != nil {
		log.Error("IsCollaborator: %v", err)
		return false
	}
	if isCollaborator {
		return true
	}

	canWritePulls, err := access_model.HasGovernanceAbility(ctx, repo, doer, governance_model.WritePulls)
	if err != nil {
		log.Error("HasGovernanceAbility: %v", err)
		return false
	}
	if canWritePulls {
		canWrite, err := access_model.HasAccessUnit(ctx, doer, repo, unit.TypePullRequests, perm.AccessModeWrite)
		if err != nil {
			log.Error("HasAccessUnit: %v", err)
			return false
		}
		return canWrite
	}

	// If the repo's owner is an organization, members of teams with read permission on pull requests can change reviewers
	if repo.Owner.IsOrganization() {
		teams, err := organization.GetTeamsWithAccessToAnyRepoUnit(ctx, repo.OwnerID, repo.ID, perm.AccessModeRead, unit.TypePullRequests)
		if err != nil {
			log.Error("GetTeamsWithAccessToRepo: %v", err)
			return false
		}
		for _, team := range teams {
			if !team.UnitEnabled(ctx, unit.TypePullRequests) {
				continue
			}
			isMember, err := organization.IsTeamMember(ctx, repo.OwnerID, team.ID, doer.ID)
			if err != nil {
				log.Error("IsTeamMember: %v", err)
				continue
			}
			if isMember {
				return true
			}
		}
	}

	return false
}
