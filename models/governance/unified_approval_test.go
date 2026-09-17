// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"gitea.dev/models/db"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestUnifiedApprovalCountsDistinctPeopleAndNativeEvidence(t *testing.T) {
	version := PullVersion{PullID: 1, Generation: 2}
	total := ApprovalRule{ID: 1, Name: "总人数", Required: 3, ScopeType: "repository", ScopeID: 1, BranchMode: "all", Enabled: true}
	dev := total
	dev.ID = 2
	dev.Name = "研发"
	dev.Required = 1
	qa := dev
	qa.ID = 3
	qa.Name = "质量"
	candidates := []RuleCandidates{{Rule: total, UserIDs: []int64{1, 2, 3, 3}}, {Rule: dev, UserIDs: []int64{1}}, {Rule: qa, UserIDs: []int64{1}}}
	evidence := []ApprovalEvidence{{PullID: 1, UserID: 1, Generation: 2}, {PullID: 1, UserID: 2, Generation: 2}, {PullID: 1, UserID: 2, Generation: 2}}
	state, err := CountApprovalRules(version, candidates, evidence, false)
	require.NoError(t, err)
	require.False(t, state.Satisfied)
	require.Equal(t, 1, state.Rules[0].Missing)
	require.True(t, state.Rules[1].Satisfied)
	require.True(t, state.Rules[2].Satisfied)
	evidence = append(evidence, ApprovalEvidence{PullID: 1, UserID: 3, Generation: 2})
	state, err = CountApprovalRules(version, candidates, evidence, false)
	require.NoError(t, err)
	require.True(t, state.Satisfied)
	native := dev
	native.NativeProtectionID = 7
	version.Generation = 1
	state, err = CountApprovalRules(version, []RuleCandidates{{Rule: native, UserIDs: []int64{1}, NativeApprovedIDs: []int64{1}}}, nil, false)
	require.NoError(t, err)
	require.True(t, state.Satisfied, "兼容评审不伪造差异代次证据")
	for _, scenario := range []struct {
		generation     int64
		reauthenticate bool
		evidence       []ApprovalEvidence
	}{
		{2, false, nil},
		{1, true, nil},
		{2, false, []ApprovalEvidence{{PullID: 1, UserID: 1, Generation: 1}}},
		{1, true, []ApprovalEvidence{{PullID: 1, UserID: 1, Generation: 1}}},
	} {
		version.Generation = scenario.generation
		state, err = CountApprovalRules(version, []RuleCandidates{{Rule: native, UserIDs: []int64{1}, NativeApprovedIDs: []int64{1}}}, scenario.evidence, scenario.reauthenticate)
		require.NoError(t, err)
		require.False(t, state.Satisfied, "历史票不能绕过差异变化或重新认证")
	}
}

type approvalMigrationPull struct {
	ID         int64 `xorm:"pk"`
	BaseRepoID int64
}

type approvalMigrationUser struct {
	ID int64 `xorm:"pk"`
}

func (*approvalMigrationUser) TableName() string { return "user" }

type approvalMigrationTeam struct {
	ID int64 `xorm:"pk"`
}

func (*approvalMigrationTeam) TableName() string { return "team" }

func (*approvalMigrationPull) TableName() string { return "pull_request" }

func TestUnifiedApprovalMigrationPreservesHistoryAndIsIdempotent(t *testing.T) {
	ctx := testDatabase(t)
	engine := db.GetXORMEngineForTesting()
	require.NoError(t, engine.Sync(new(NativeBranchApproval), new(approvalMigrationPull), new(approvalMigrationUser), new(approvalMigrationTeam)))
	require.NoError(t, db.Insert(ctx, &approvalMigrationUser{ID: 5}, &approvalMigrationTeam{ID: 8}))
	branch := &NativeBranchApproval{ID: 11, RepoID: 2, RuleName: "release/*", RequiredApprovals: 101, EnableApprovalsWhitelist: true, ApprovalsWhitelistUserIDs: []int64{5}, ApprovalsWhitelistTeamIDs: []int64{8}, IgnoreStaleApprovals: true}
	require.NoError(t, db.Insert(ctx, branch))
	original := ApprovalRule{Name: "质量", ScopeType: "repository", ScopeID: 2, Required: 1, Enabled: true, BranchMode: "protection_rules", Branches: []string{"release/*"}, Revision: 3}
	require.NoError(t, db.Insert(ctx, &original))
	require.NoError(t, db.Insert(ctx, &approvalMigrationPull{ID: 9, BaseRepoID: 2}, &PullRuleVersion{PullID: 9, Revision: 1, Rules: []ApprovalRule{original}, CreatedAt: time.Now()}))
	require.NoError(t, AddUnifiedBranchApprovals(engine))
	require.NoError(t, AddUnifiedBranchApprovals(engine))
	var native ApprovalRule
	has, err := engine.Where("native_protection_id = ?", 11).Get(&native)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, 101, native.Required)
	require.Equal(t, []int64{5}, native.UserIDs)
	require.True(t, native.NativeIgnoreStale)
	migrated, has, err := db.GetByID[ApprovalRule](ctx, original.ID)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, []int64{11}, migrated.ProtectionIDs)
	latest, has, err := LatestPullRuleVersion(ctx, 9)
	require.NoError(t, err)
	require.True(t, has)
	require.EqualValues(t, 2, latest.Revision)
	require.Equal(t, []int64{11}, latest.Rules[0].ProtectionIDs)
	var first PullRuleVersion
	_, err = engine.Where("pull_id = ? AND revision = ?", 9, 1).Get(&first)
	require.NoError(t, err)
	require.Equal(t, []string{"release/*"}, first.Rules[0].Branches)
	native.UserIDs = []int64{7}
	native.TeamIDs = []int64{4}
	native.Required = 4
	require.NoError(t, WriteNativeApprovalProjection(ctx, &native))
	var projected NativeBranchApproval
	_, err = engine.ID(11).Get(&projected)
	require.NoError(t, err)
	require.Equal(t, []int64{7}, projected.ApprovalsWhitelistUserIDs)
	require.Equal(t, []int64{4}, projected.ApprovalsWhitelistTeamIDs)
}

func TestUnifiedApprovalMigrationRejectsMissingAssociationAtomically(t *testing.T) {
	ctx := testDatabase(t)
	engine := db.GetXORMEngineForTesting()
	require.NoError(t, engine.Sync(new(NativeBranchApproval), new(approvalMigrationPull)))
	require.NoError(t, db.Insert(ctx, &NativeBranchApproval{ID: 11, RepoID: 2, RuleName: "main", RequiredApprovals: 2},
		&ApprovalRule{Name: "关联失效", ScopeType: "repository", ScopeID: 2, Required: 1, Enabled: true, BranchMode: "protection_rules", Branches: []string{"missing"}}))
	require.ErrorContains(t, AddUnifiedBranchApprovals(engine), "不存在的保护规则")
	count, err := engine.Where("native_protection_id = ?", 11).Count(new(ApprovalRule))
	require.NoError(t, err)
	require.Zero(t, count, "迁移阻断时不能留下部分统一规则")
}
