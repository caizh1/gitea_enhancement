// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"
	"time"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	organization_model "gitea.dev/models/organization"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApprovalCandidatesUseDirectMembersAndCurrentAccess(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	child, err := CreateGroup(ctx, owner, GroupOption{Path: "approval-pool", ParentID: 3, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, owner, 3, GroupMemberOption{UserID: 4, Role: governance_model.Developer, Revision: 1}, false))
	require.NoError(t, SetGroupMember(ctx, owner, child.ID, GroupMemberOption{UserID: 5, Role: governance_model.Developer, Revision: 1}, false))
	repo := &repo_model.Repository{OwnerID: child.ID, OwnerName: child.InternalName, Name: "approval", LowerName: "approval", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypePullRequests}))
	rule := governance_model.ApprovalRule{ScopeType: "repository", ScopeID: repo.ID, Name: "指定审批组", Required: 1, GroupIDs: []int64{child.ID}, BranchMode: "all", Enabled: true}
	settings := governance_model.ApprovalSettings{PreventAuthor: true}
	candidates, err := ApprovalCandidates(ctx, repo, "main", 2, nil, settings, rule)
	require.NoError(t, err)
	assert.Equal(t, []int64{5}, candidates.UserIDs, "只选直接成员，不纳入父组的继承成员")
	rule.UserIDs = []int64{4, 4, 99999}
	candidates, err = ApprovalCandidates(ctx, repo, "main", 2, nil, settings, rule)
	require.NoError(t, err)
	assert.Equal(t, []int64{4, 5}, candidates.UserIDs)
	total := governance_model.ApprovalRule{ScopeType: "repository", ScopeID: repo.ID, Name: "总人数", Required: 2, AllEligible: true, BranchMode: "all", Enabled: true}
	pools, err := ApplicableApprovalCandidates(ctx, repo, "main", 2, nil, settings, []governance_model.ApprovalRule{total, rule})
	require.NoError(t, err)
	require.Len(t, pools, 2)
	assert.Equal(t, []int64{1, 4, 5}, pools[0].UserIDs, "总人数包含合格开发者，并按人员去重")
	version := governance_model.PullVersion{PullID: 1, Generation: 1}
	state, err := governance_model.CountApprovalRules(version, pools, []governance_model.ApprovalEvidence{{PullID: 1, UserID: 4, Generation: 1}, {PullID: 1, UserID: 5, Generation: 1}}, false)
	require.NoError(t, err)
	assert.True(t, state.Satisfied)
	otherBranch := rule
	otherBranch.BranchMode, otherBranch.Branches = "branches", []string{"release"}
	pools, err = ApplicableApprovalCandidates(ctx, repo, "main", 2, nil, settings, []governance_model.ApprovalRule{total, otherBranch})
	require.NoError(t, err)
	require.Len(t, pools, 1)
	assert.Equal(t, []int64{1, 4, 5}, pools[0].UserIDs, "开发者原有审批资格不依赖其他分支的显式规则")
	settings.PreventCommitter = true
	candidates, err = ApprovalCandidates(ctx, repo, "main", 2, []int64{4}, settings, rule)
	require.NoError(t, err)
	assert.Equal(t, []int64{5}, candidates.UserIDs)
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "group", child.ID, 5).Cols("expires_unix").Update(&governance_model.Membership{ExpiresUnix: time.Now().Unix() - 1})
	require.NoError(t, err)
	candidates, err = ApprovalCandidates(ctx, repo, "main", 2, []int64{4}, settings, rule)
	require.NoError(t, err)
	assert.Empty(t, candidates.UserIDs)
	settings.PreventCommitter = false
	_, err = db.GetEngine(ctx).ID(4).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: true})
	require.NoError(t, err)
	candidates, err = ApprovalCandidates(ctx, repo, "main", 2, nil, settings, rule)
	require.NoError(t, err)
	assert.Empty(t, candidates.UserIDs)
}

func TestApprovalBranchRulesUseTargetProtection(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, db.Insert(ctx, &git_model.ProtectedBranch{RepoID: 1, RuleName: "release/*"}))
	rule := governance_model.ApprovalRule{ScopeType: "repository", ScopeID: 1, Name: "发布审批", Required: 1, BranchMode: "protection_rules", Branches: []string{"release/*"}, Enabled: true}
	applies, err := ApprovalRuleApplies(ctx, 1, "release/v1", &rule)
	require.NoError(t, err)
	assert.True(t, applies)
	applies, err = ApprovalRuleApplies(ctx, 2, "release/v1", &rule)
	require.NoError(t, err)
	assert.False(t, applies, "源项目的保护规则不能代替目标项目")
	rule.BranchMode, rule.Branches = "branches", []string{"unprotected"}
	applies, err = ApprovalRuleApplies(ctx, 1, "unprotected", &rule)
	require.NoError(t, err)
	assert.False(t, applies)
}

func TestApprovalCandidatesRespectCurrentNativePoolAndTeamMembership(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo := &repo_model.Repository{OwnerID: 3, OwnerName: "org3", Name: "native-pool", LowerName: "native-pool"}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx,
		&repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode},
		&repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypePullRequests},
		&git_model.ProtectedBranch{RepoID: repo.ID, RuleName: "main", EnableApprovalsWhitelist: true, ApprovalsWhitelistUserIDs: []int64{2, 4}},
	))
	rule := governance_model.ApprovalRule{
		ScopeType: "repository", ScopeID: repo.ID, Name: "兼容原生团队", Required: 1,
		TeamIDs: []int64{2}, BranchMode: "all", Enabled: true, RespectNativeApprovalPool: true,
	}

	candidates, err := ApprovalCandidates(ctx, repo, "main", 1, nil, governance_model.ApprovalSettings{}, rule)
	require.NoError(t, err)
	require.Equal(t, []int64{2, 4}, candidates.UserIDs)

	deleted, err := db.GetEngine(ctx).Where("team_id = ? AND uid = ?", 2, 4).Delete(new(organization_model.TeamUser))
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	candidates, err = ApprovalCandidates(ctx, repo, "main", 1, nil, governance_model.ApprovalSettings{}, rule)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, candidates.UserIDs, "离开团队后必须立即失去资格，不能依赖导入时成员快照")

	protected, err := git_model.GetFirstMatchProtectedBranchRule(ctx, repo.ID, "main")
	require.NoError(t, err)
	protected.ApprovalsWhitelistUserIDs = []int64{4}
	_, err = db.GetEngine(ctx).ID(protected.ID).Cols("approvals_whitelist_user_i_ds").Update(protected)
	require.NoError(t, err)
	candidates, err = ApprovalCandidates(ctx, repo, "main", 1, nil, governance_model.ApprovalSettings{}, rule)
	require.NoError(t, err)
	require.Empty(t, candidates.UserIDs, "离开原生审批池后必须立即失去资格")

	total := rule
	total.TeamIDs, total.AllEligible, total.Name = nil, true, "兼容原生总人数"
	unrestricted := rule
	unrestricted.TeamIDs, unrestricted.UserIDs = nil, []int64{2}
	unrestricted.RespectNativeApprovalPool = false
	pools, err := ApplicableApprovalCandidates(ctx, repo, "main", 1, nil, governance_model.ApprovalSettings{}, []governance_model.ApprovalRule{total, unrestricted})
	require.NoError(t, err)
	require.Equal(t, []int64{4}, pools[0].UserIDs, "无约束显式规则不能把原生白名单外人员重新加入兼容总人数池")
}

func TestReporterApprovalRequiresSharedGroupAndSpecificProtectedBranch(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	group, err := CreateGroup(ctx, actor, GroupOption{Path: "reporter-approval", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, group.ID, GroupMemberOption{UserID: 5, Role: governance_model.Planner, Revision: group.Revision}, false))
	repo := &repo_model.Repository{OwnerID: 3, OwnerName: "org3", Name: "approval-shared", LowerName: "approval-shared", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypePullRequests}, &git_model.ProtectedBranch{RepoID: repo.ID, RuleName: "main"}))
	state, err := ListRepositoryShares(ctx, 2, repo.ID, 0)
	require.NoError(t, err)
	require.NoError(t, SetRepositoryShare(ctx, actor, repo.ID, GroupShareOption{GroupID: group.ID, MaxRole: governance_model.Reporter, Revision: state.Revision}, false))
	rule := governance_model.ApprovalRule{ScopeType: "repository", ScopeID: repo.ID, Name: "只读审批", Required: 1, UserIDs: []int64{5}, BranchMode: "all", Enabled: true}
	settings := governance_model.ApprovalSettings{PreventAuthor: true}
	pools, err := ApplicableApprovalCandidates(ctx, repo, "main", 2, nil, settings, []governance_model.ApprovalRule{rule})
	require.NoError(t, err)
	require.Empty(t, pools[0].UserIDs, "单独指定只读人员不能扩大审批资格")
	rule.UserIDs, rule.GroupIDs = nil, []int64{group.ID}
	pools, err = ApplicableApprovalCandidates(ctx, repo, "main", 2, nil, settings, []governance_model.ApprovalRule{rule})
	require.NoError(t, err)
	require.Empty(t, pools[0].UserIDs, "全部分支规则不启用 Reporter/Planner 审批")
	rule.BranchMode, rule.Branches = "branches", []string{"main"}
	pools, err = ApplicableApprovalCandidates(ctx, repo, "main", 2, nil, settings, []governance_model.ApprovalRule{rule})
	require.NoError(t, err)
	require.Equal(t, []int64{5}, pools[0].UserIDs)
	total := governance_model.ApprovalRule{ScopeType: "repository", ScopeID: repo.ID, Name: "去重总人数", Required: 1, AllEligible: true, BranchMode: "all", Enabled: true}
	pools, err = ApplicableApprovalCandidates(ctx, repo, "main", 2, nil, settings, []governance_model.ApprovalRule{total, rule})
	require.NoError(t, err)
	require.Contains(t, pools[0].UserIDs, int64(5), "适用的显式共享群组规则允许只读人员计入去重总人数")
	pools, err = ApplicableApprovalCandidates(ctx, repo, "other", 2, nil, settings, []governance_model.ApprovalRule{total, rule})
	require.NoError(t, err)
	require.Len(t, pools, 1)
	require.NotContains(t, pools[0].UserIDs, int64(5), "其他分支规则不能扩大当前分支的只读审批资格")
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND group_id = ?", "repository", repo.ID, group.ID).Cols("expires_unix").Update(&governance_model.Share{ExpiresUnix: time.Now().Unix() - 1})
	require.NoError(t, err)
	pools, err = ApplicableApprovalCandidates(ctx, repo, "main", 2, nil, settings, []governance_model.ApprovalRule{rule})
	require.NoError(t, err)
	require.Empty(t, pools[0].UserIDs)
}
