// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRule(id int64, name string, required int, users ...int64) RuleCandidates {
	return RuleCandidates{Rule: ApprovalRule{
		ID: id, Name: name, Required: required, Enabled: true,
		ScopeType: "repository", ScopeID: 99, BranchMode: "all", Revision: 1,
	}, UserIDs: users}
}

func TestApprovalMultipleRulesAndUniqueTotal(t *testing.T) {
	rules := []RuleCandidates{
		testRule(1, "张三必批", 1, 11), testRule(2, "李四必批", 1, 12),
		testRule(3, "安全组一人", 1, 11, 13), testRule(4, "总人数三人", 3, 11, 12, 13, 14),
		testRule(5, "可选咨询", 0),
	}
	version := PullVersion{PullID: 7, Generation: 2, Head: "当前提交"}
	approvals := []ApprovalEvidence{
		{ReviewID: 1, PullID: 7, UserID: 11, Generation: 2},
		{ReviewID: 2, PullID: 7, UserID: 11, Generation: 2},
		{ReviewID: 3, PullID: 7, UserID: 12, Generation: 2},
		{ReviewID: 4, PullID: 7, UserID: 13, Generation: 1},
		{ReviewID: 5, PullID: 8, UserID: 14, Generation: 2},
	}
	state, err := CountApprovalRules(version, rules, approvals, false)
	require.NoError(t, err)
	assert.False(t, state.Satisfied)
	assert.True(t, state.Rules[0].Satisfied)
	assert.True(t, state.Rules[2].Satisfied)
	assert.Equal(t, 1, state.Rules[3].Missing, "重复批准、失效批准和其他 PR 的批准均不能凑总人数")
	assert.True(t, state.Rules[4].Satisfied)
	approvals = append(approvals, ApprovalEvidence{ReviewID: 6, PullID: 7, UserID: 14, Generation: 2})
	state, err = CountApprovalRules(version, rules, approvals, false)
	require.NoError(t, err)
	assert.True(t, state.Satisfied)
	rules[0].UserIDs = nil
	state, err = CountApprovalRules(version, rules, approvals, false)
	require.NoError(t, err)
	assert.False(t, state.Satisfied)
	assert.True(t, state.Rules[0].NeedsAttention, "指定人失去资格时阻断，不能由其他票替代")
	state, err = CountApprovalRules(version, rules[1:2], approvals, true)
	require.NoError(t, err)
	assert.False(t, state.Satisfied, "再次认证开启后未认证票不计入")
}

func TestApprovalGenerationDoesNotResurrect(t *testing.T) {
	ctx := testDatabase(t)
	a := PullVersion{PullID: 7, Head: "A", BaseBranch: "main", PatchID: "差异A"}
	version, err := AdvancePullVersion(ctx, a, "", true)
	require.NoError(t, err)
	approval := ApprovalEvidence{PullID: 7, ReviewID: 1, UserID: 11, Generation: version.Generation}
	rebased := a
	rebased.Head = "A-rebase"
	version, err = AdvancePullVersion(ctx, rebased, "A", true)
	require.NoError(t, err)
	assert.Equal(t, approval.Generation, version.Generation, "差异不变的 rebase 保留批准")
	b := PullVersion{PullID: 7, Head: "B", BaseBranch: "main", PatchID: "差异B"}
	version, err = AdvancePullVersion(ctx, b, "A-rebase", true)
	require.NoError(t, err)
	assert.Greater(t, version.Generation, approval.Generation)
	version, err = AdvancePullVersion(ctx, a, "B", true)
	require.NoError(t, err)
	state, err := CountApprovalRules(*version, []RuleCandidates{testRule(1, "张三必批", 1, 11)}, []ApprovalEvidence{approval}, false)
	require.NoError(t, err)
	assert.False(t, state.Satisfied, "回到 A 不能复活 A 的旧票")
	_, err = AdvancePullVersion(ctx, b, "旧页面的提交", true)
	assert.ErrorIs(t, err, ErrConflict)
}

func TestApprovalInvalidationAuditAndRollback(t *testing.T) {
	ctx := testDatabase(t)
	actor := Actor{ID: 7, Kind: "user", Name: "推送者", Transport: "git_http", RequestID: "代码变更请求"}
	ctx = WithAuditActor(ctx, actor)
	scope := &PullVersionAuditScope{RepoID: 99, Path: "acme/target", AncestorIDs: []int64{42}}
	a := PullVersion{PullID: 7, Head: "A", BaseBranch: "main", PatchID: "差异A"}
	_, err := AdvancePullVersion(ctx, a, "", true, scope)
	require.NoError(t, err)
	rebase := a
	rebase.Head = "rebase-A"
	_, err = AdvancePullVersion(ctx, rebase, "A", true, scope)
	require.NoError(t, err)
	count, err := db.GetEngine(ctx).Where("type = ?", "approval.invalidated").Count(new(AuditEvent))
	require.NoError(t, err)
	require.Zero(t, count, "相同差异的重排不记录批准失效")
	b := a
	b.Head, b.PatchID = "B", "差异B"
	invalidActor := actor
	invalidActor.Transport = ""
	_, err = AdvancePullVersion(WithAuditActor(ctx, invalidActor), b, "rebase-A", true, scope)
	require.ErrorIs(t, err, ErrInvalid)
	previous, _, err := db.GetByID[PullVersion](ctx, 7)
	require.NoError(t, err)
	require.Equal(t, "rebase-A", previous.Head, "审计失败不能推进批准代次")
	_, err = AdvancePullVersion(ctx, b, "rebase-A", true, scope)
	require.NoError(t, err)
	_, err = AdvancePullVersion(ctx, a, "B", true, scope)
	require.NoError(t, err)
	var events []AuditEvent
	require.NoError(t, db.GetEngine(ctx).Where("type = ?", "approval.invalidated").OrderBy("id").Find(&events))
	require.Len(t, events, 2, "A→B→A 必须保留两次失效证据")
	for _, event := range events {
		require.Equal(t, actor, event.Actor)
		require.EqualValues(t, 99, event.ScopeID)
		require.Equal(t, scope.Path, event.ObjectPath)
		require.Equal(t, scope.AncestorIDs, event.AncestorIDs)
	}
}
