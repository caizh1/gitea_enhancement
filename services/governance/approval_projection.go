// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"slices"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
)

func visibleApprovalSubjects(ctx context.Context, actorID int64, repo *repo_model.Repository, userIDs, groupIDs, teamIDs []int64) (map[int64]bool, map[int64]bool, map[int64]bool, error) {
	users, groups, teams := map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	if actorID <= 0 {
		return users, groups, teams, nil
	}
	actor, err := activeActor(ctx, actorID)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, id := range slices.Compact(slices.Sorted(slices.Values(userIDs))) {
		user, err := user_model.GetUserByID(ctx, id)
		if user_model.IsErrUserNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, nil, err
		}
		permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
		if err != nil {
			return nil, nil, nil, err
		}
		users[id] = user_model.IsUserVisibleToViewer(ctx, user, actor) && permission.CanRead(unit.TypePullRequests)
	}
	for _, id := range slices.Compact(slices.Sorted(slices.Values(groupIDs))) {
		_, err := CheckGroupAccess(ctx, actorID, id, governance_model.ReadGroup)
		if err == nil {
			groups[id] = true
		} else if !errors.Is(err, governance_model.ErrNotFound) {
			return nil, nil, nil, err
		}
	}
	for _, id := range slices.Compact(slices.Sorted(slices.Values(teamIDs))) {
		team, err := organization.GetTeamByID(ctx, id)
		if organization.IsErrTeamNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, nil, err
		}
		visible, err := canReadApprovalTeam(ctx, actorID, team)
		if err != nil {
			return nil, nil, nil, err
		}
		teams[id] = visible
	}
	return users, groups, teams, nil
}

func approvalRuleSubjectIDs(rules []*governance_model.ApprovalRule) (users, groups, teams []int64) {
	for _, rule := range rules {
		users = append(users, rule.UserIDs...)
		groups = append(groups, rule.GroupIDs...)
		teams = append(teams, rule.TeamIDs...)
		if rule.ScopeType == "group" {
			groups = append(groups, rule.ScopeID)
		}
	}
	return users, groups, teams
}

func projectApprovalRule(rule governance_model.ApprovalRule, users, groups, teams map[int64]bool) governance_model.ApprovalRule {
	filter := func(ids []int64, visible map[int64]bool) []int64 {
		result := make([]int64, 0, len(ids))
		for _, id := range ids {
			if visible[id] {
				result = append(result, id)
			} else {
				rule.SubjectsHidden = true
			}
		}
		return result
	}
	rule.UserIDs = filter(slices.Clone(rule.UserIDs), users)
	rule.GroupIDs = filter(slices.Clone(rule.GroupIDs), groups)
	rule.TeamIDs = filter(slices.Clone(rule.TeamIDs), teams)
	if rule.ScopeType == "group" && !groups[rule.ScopeID] {
		rule.ScopeID = 0
		rule.SubjectsHidden = true
	}
	return rule
}

func preserveHiddenApprovalSubjects(ctx context.Context, actorID int64, repo *repo_model.Repository, next *governance_model.ApprovalRule, previous *governance_model.ApprovalRule) error {
	if previous == nil {
		return nil
	}
	users, groups, teams, err := visibleApprovalSubjects(ctx, actorID, repo, previous.UserIDs, previous.GroupIDs, previous.TeamIDs)
	if err != nil {
		return err
	}
	preserve := func(nextIDs *[]int64, previousIDs []int64, visible map[int64]bool) {
		for _, id := range previousIDs {
			if !visible[id] && !slices.Contains(*nextIDs, id) {
				*nextIDs = append(*nextIDs, id)
			}
		}
		slices.Sort(*nextIDs)
		*nextIDs = slices.Compact(*nextIDs)
	}
	preserve(&next.UserIDs, previous.UserIDs, users)
	preserve(&next.GroupIDs, previous.GroupIDs, groups)
	preserve(&next.TeamIDs, previous.TeamIDs, teams)
	return nil
}

// ProjectRepositoryApprovalRules 只生成响应副本，不改变内部计票或保存对象。
func ProjectRepositoryApprovalRules(ctx context.Context, actorID, repoID int64, source *RepositoryApprovalRules) (*RepositoryApprovalRules, error) {
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return nil, err
	}
	userIDs, groupIDs, teamIDs := approvalRuleSubjectIDs(source.Rules)
	users, groups, teams, err := visibleApprovalSubjects(ctx, actorID, repo, userIDs, groupIDs, teamIDs)
	if err != nil {
		return nil, err
	}
	result := *source
	result.Rules = make([]*governance_model.ApprovalRule, 0, len(source.Rules))
	for _, rule := range source.Rules {
		projected := projectApprovalRule(*rule, users, groups, teams)
		result.Rules = append(result.Rules, &projected)
	}
	return &result, nil
}

func ProjectPullApprovalRules(ctx context.Context, actorID, repoID int64, source *PullApprovalRules) (*PullApprovalRules, error) {
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return nil, err
	}
	rules := slices.Clone(source.Policies)
	for i := range source.Version.Rules {
		rules = append(rules, &source.Version.Rules[i])
	}
	userIDs, groupIDs, teamIDs := approvalRuleSubjectIDs(rules)
	users, groups, teams, err := visibleApprovalSubjects(ctx, actorID, repo, userIDs, groupIDs, teamIDs)
	if err != nil {
		return nil, err
	}
	result := *source
	version := *source.Version
	version.Rules = make([]governance_model.ApprovalRule, 0, len(source.Version.Rules))
	for _, rule := range source.Version.Rules {
		version.Rules = append(version.Rules, projectApprovalRule(rule, users, groups, teams))
	}
	result.Version = &version
	result.Policies = make([]*governance_model.ApprovalRule, 0, len(source.Policies))
	for _, rule := range source.Policies {
		projected := projectApprovalRule(*rule, users, groups, teams)
		result.Policies = append(result.Policies, &projected)
	}
	return &result, nil
}

func ProjectPullApprovalResult(ctx context.Context, actorID, pullID, repoID int64, source *PullApprovalResult) (*PullApprovalResult, error) {
	rules, err := ListPullApprovalRules(ctx, actorID, pullID)
	if err != nil {
		return nil, err
	}
	projected, err := ProjectPullApprovalRules(ctx, actorID, repoID, rules)
	if err != nil {
		return nil, err
	}
	hidden := map[int64]bool{}
	for _, rule := range projected.Policies {
		hidden[rule.ID] = rule.SubjectsHidden
	}
	for _, rule := range projected.Version.Rules {
		hidden[rule.ID] = rule.SubjectsHidden
	}
	result := *source
	result.State = source.State
	result.State.Rules = slices.Clone(source.State.Rules)
	for i := range result.State.Rules {
		if hidden[result.State.Rules[i].ID] {
			result.State.Rules[i].SubjectsHidden = true
			if result.State.Rules[i].ScopeType == "group" {
				result.State.Rules[i].ScopeID = 0
			}
			result.State.Rules[i].ApprovedUserIDs = []int64{}
			result.State.Rules[i].EligibleCount = 0
		}
	}
	return &result, nil
}
