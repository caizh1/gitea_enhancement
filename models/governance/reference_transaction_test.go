// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gitea.dev/models/db"
	json_module "gitea.dev/modules/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditDetailsMarshalJSON(t *testing.T) {
	for _, details := range []AuditDetails{nil, {}} {
		encoded, err := json.Marshal(details)
		require.NoError(t, err)
		assert.JSONEq(t, `{}`, string(encoded))
	}

	_, err := json.Marshal(AuditDetails(`{"broken"`))
	require.Error(t, err, "非空非法 JSON 不能被静默修正")
}

func TestPlannedReferenceBusinessOperationIsClaimedByMatchingHook(t *testing.T) {
	ctx := testDatabase(t)
	zero, target := strings.Repeat("0", 40), strings.Repeat("b", 40)
	planned := &ReferenceTransaction{RepoID: 1, BusinessOperation: "release_tag_create", BusinessOldBranch: "v1", Actor: Actor{ID: 2, Name: "发布者", Kind: "user", Transport: "web"}, BusinessPayload: json_module.Value(`{"action":"create"}`), Changes: []ReferenceChange{{Ref: "refs/tags/v1", Old: zero, New: zero}}}
	require.NoError(t, PlanReferenceBusinessOperation(ctx, planned))

	claim := &ReferenceTransaction{RepoID: 1, BusinessOperationID: planned.BusinessOperationID, BusinessOperation: planned.BusinessOperation, BusinessOldBranch: "v1", WriterID: "writer", Actor: Actor{ID: 2, Name: "Git 内部身份", Kind: "user", Transport: "internal_git"}, Changes: []ReferenceChange{{Ref: "refs/tags/v1", Old: zero, New: target}}}
	require.NoError(t, PrepareReferenceTransaction(ctx, claim, nil, func(context.Context) error { return nil }))
	require.Equal(t, planned.ID, claim.ID)
	require.JSONEq(t, `{"action":"create"}`, string(claim.BusinessPayload))

	fresh, err := GetReferenceBusinessOperation(ctx, planned.BusinessOperationID)
	require.NoError(t, err)
	require.Equal(t, "prepared", fresh.State)
	require.Equal(t, target, fresh.Changes[0].New)
	require.Equal(t, planned.Actor, fresh.Actor, "Hook 认领不能覆盖规划阶段的请求身份")
}

func TestUnclaimedPlannedOperationNeverTreatsWildcardAsCommitted(t *testing.T) {
	ctx := testDatabase(t)
	zero, target := strings.Repeat("0", 40), strings.Repeat("c", 40)
	planned := &ReferenceTransaction{RepoID: 1, BusinessOperation: "release_tag_create", BusinessOldBranch: "v2", Actor: Actor{ID: 2, Name: "发布者", Kind: "user", Transport: "web"}, BusinessPayload: json_module.Value(`{"action":"create"}`), Changes: []ReferenceChange{{Ref: "refs/tags/v2", Old: zero, New: zero}}}
	require.NoError(t, PlanReferenceBusinessOperation(ctx, planned))
	state, err := ReconcileReferenceTransaction(ctx, planned.ID, map[string]string{"refs/tags/v2": target}, nil)
	require.NoError(t, err)
	require.Equal(t, "unknown", state)
	state, err = ReconcileReferenceTransaction(ctx, planned.ID, map[string]string{"refs/tags/v2": zero}, nil)
	require.NoError(t, err)
	require.Equal(t, "aborted", state)
}

func TestReferenceTransactionRetainsUnknownAndCommitsEvidenceAtomically(t *testing.T) {
	ctx := testDatabase(t)
	old, newID, other := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	operation := &ReferenceTransaction{RepoID: 1, Actor: Actor{ID: 2, Name: "审批者", Kind: "user", Transport: "git_http"}, Changes: []ReferenceChange{{Ref: "refs/heads/main", Old: old, New: newID}}}
	require.NoError(t, PrepareReferenceTransaction(ctx, operation, []string{Resource("pull", 9)}, func(context.Context) error { return nil }))
	assert.ErrorIs(t, WithWrite(ctx, []string{Resource("pull", 9)}, func(context.Context) error { t.Fatal("占用期间不能更改批准"); return nil }), ErrConflict)
	state, err := ReconcileReferenceTransaction(ctx, operation.ID, map[string]string{"refs/heads/main": other}, nil)
	require.NoError(t, err)
	assert.Equal(t, "unknown", state)
	assert.ErrorIs(t, WithWrite(ctx, []string{Resource("repository", 1)}, func(context.Context) error { return nil }), ErrConflict)
	failure := errors.New("模拟证据落库失败")
	_, err = ReconcileReferenceTransaction(ctx, operation.ID, map[string]string{"refs/heads/main": newID}, func(ctx context.Context, _ *ReferenceTransaction) error {
		require.NoError(t, WithWrite(ctx, []string{Resource("pull", 9)}, func(ctx context.Context) error {
			return db.Insert(ctx, &PullVersion{PullID: 9, Head: newID, BaseBranch: "main", PatchID: "差异", Generation: 1})
		}))
		return failure
	})
	require.ErrorIs(t, err, failure)
	version, has, err := db.GetByID[PullVersion](ctx, 9)
	require.NoError(t, err)
	assert.False(t, has)
	assert.Nil(t, version)
	var calls int
	apply := func(ctx context.Context, _ *ReferenceTransaction) error {
		calls++
		_, err := AdvancePullVersion(ctx, PullVersion{PullID: 9, Head: newID, BaseBranch: "main", PatchID: "差异"}, "", true)
		return err
	}
	state, err = ReconcileReferenceTransaction(ctx, operation.ID, map[string]string{"refs/heads/main": newID}, apply)
	require.NoError(t, err)
	assert.Equal(t, "committed", state)
	_, err = ReconcileReferenceTransaction(ctx, operation.ID, map[string]string{"refs/heads/main": newID}, apply)
	require.NoError(t, err)
	assert.Equal(t, 1, calls, "回执重放不能重复推进差异版本")
	require.NoError(t, WithWrite(ctx, []string{Resource("pull", 9)}, func(context.Context) error { return nil }))
}

func TestReferenceTransactionRejectsPartialResultsAndFailedPreparation(t *testing.T) {
	ctx := testDatabase(t)
	old, newID := strings.Repeat("a", 40), strings.Repeat("b", 40)
	operation := &ReferenceTransaction{RepoID: 2, Actor: Actor{ID: 2, Name: "用户", Kind: "user", Transport: "api"}, Changes: []ReferenceChange{{Ref: "refs/heads/a", Old: old, New: newID}, {Ref: "refs/heads/b", Old: old, New: newID}}}
	reject := errors.New("审批未满足")
	assert.ErrorIs(t, PrepareReferenceTransaction(ctx, operation, nil, func(context.Context) error { return reject }), reject)
	count, err := db.GetEngine(ctx).Count(new(ReferenceTransaction))
	require.NoError(t, err)
	assert.Zero(t, count)
	require.NoError(t, PrepareReferenceTransaction(ctx, operation, nil, func(context.Context) error { return nil }))
	state, err := ReconcileReferenceTransaction(ctx, operation.ID, map[string]string{"refs/heads/a": newID, "refs/heads/b": old}, nil)
	require.NoError(t, err)
	assert.Equal(t, "unknown", state)
	state, err = ReconcileReferenceTransaction(ctx, operation.ID, map[string]string{"refs/heads/a": old, "refs/heads/b": old}, nil)
	require.NoError(t, err)
	assert.Equal(t, "aborted", state)
	require.NoError(t, WithWrite(ctx, []string{Resource("repository", 2)}, func(context.Context) error { return nil }))
	_, err = ReferenceFingerprint(append(operation.Changes, operation.Changes[0]))
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestWikiReferenceTransactionCommitsContentEventOnce(t *testing.T) {
	ctx := testDatabase(t)
	old, newID := strings.Repeat("a", 40), strings.Repeat("b", 40)
	details := AuditDetails(`{"after":{"name":"Home"}}`)
	operation := &ReferenceTransaction{RepoID: 1, IsWiki: true, Actor: Actor{ID: 2, Name: "维护者", Kind: "user", Transport: "web"}, ObjectPath: "owner/repo", ContentEvent: "wiki.created", ContentObjectPath: "owner/repo/wiki/Home", ContentDetails: details, Changes: []ReferenceChange{{Ref: "refs/heads/master", Old: old, New: newID}}}
	require.NoError(t, PrepareReferenceTransaction(ctx, operation, []string{Resource("wiki_repository", 1)}, func(context.Context) error { return nil }))
	state, err := ReconcileReferenceTransaction(ctx, operation.ID, map[string]string{"refs/heads/master": newID}, nil)
	require.NoError(t, err)
	require.Equal(t, "committed", state)
	_, err = ReconcileReferenceTransaction(ctx, operation.ID, map[string]string{"refs/heads/master": newID}, nil)
	require.NoError(t, err)
	count, err := db.GetEngine(ctx).Where("type = ? AND object_path = ?", "wiki.created", "owner/repo/wiki/Home").Count(new(AuditEvent))
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestReferenceTransactionPersistsApprovalGenerations(t *testing.T) {
	ctx := testDatabase(t)
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	before := PullVersion{PullID: 15, Head: a, BaseBranch: "main", PatchID: "差异甲"}
	after := PullVersion{PullID: 15, Head: b, BaseBranch: "main", PatchID: "差异乙"}
	prepare := func(from, to PullVersion) *ReferenceTransaction {
		t.Helper()
		operation := &ReferenceTransaction{RepoID: 1, Actor: Actor{ID: 2, Name: "开发者", Kind: "user", Transport: "git"}, Changes: []ReferenceChange{{Ref: "refs/heads/feature", Old: from.Head, New: to.Head}}, PullChanges: []ReferencePullChange{{Before: from, After: to, ResetOnChange: true}}}
		require.NoError(t, PrepareReferenceTransaction(ctx, operation, nil, func(context.Context) error { return nil }))
		return operation
	}
	read := func() *PullVersion {
		t.Helper()
		result, has, err := db.GetByID[PullVersion](ctx, 15)
		require.NoError(t, err)
		require.True(t, has)
		return result
	}
	op := prepare(before, after)
	require.EqualValues(t, 1, read().Generation)
	require.ErrorIs(t, WithWrite(ctx, []string{Resource("pull", 15)}, func(context.Context) error { return nil }), ErrConflict)
	_, err := ReconcileReferenceTransaction(ctx, op.ID, map[string]string{"refs/heads/feature": b}, func(context.Context, *ReferenceTransaction) error { return errors.New("模拟后置失败") })
	require.Error(t, err)
	require.EqualValues(t, 1, read().Generation, "版本和引用回执必须共同回滚")
	_, err = ReconcileReferenceTransaction(ctx, op.ID, map[string]string{"refs/heads/feature": b}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, read().Generation)
	op = prepare(after, before)
	_, err = ReconcileReferenceTransaction(ctx, op.ID, map[string]string{"refs/heads/feature": a}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 3, read().Generation, "A→B→A 不能恢复第一代批准")
	rebased := before
	rebased.Head = c
	op = prepare(before, rebased)
	_, err = ReconcileReferenceTransaction(ctx, op.ID, map[string]string{"refs/heads/feature": c}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 3, read().Generation, "同差异重排提交保留当前批准")
	_, err = ReconcileReferenceTransaction(ctx, op.ID, map[string]string{"refs/heads/feature": c}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 3, read().Generation, "回执重放不能推进版本")
	op = prepare(rebased, after)
	_, err = ReconcileReferenceTransaction(ctx, op.ID, map[string]string{"refs/heads/feature": c}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 3, read().Generation, "失败写入不取消已有效批准")
}

func TestStableApprovalReadDoesNotAdvanceGovernanceRevision(t *testing.T) {
	ctx := testDatabase(t)
	before, has, err := db.GetByID[WriteLock](ctx, 1)
	require.NoError(t, err)
	require.True(t, has)
	require.NoError(t, WithStableRead(ctx, func(ctx context.Context) error {
		current, has, err := db.GetByID[WriteLock](ctx, 1)
		require.NoError(t, err)
		require.True(t, has)
		require.Equal(t, before.Revision, current.Revision)
		return nil
	}))
	after, has, err := db.GetByID[WriteLock](ctx, 1)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, before.Revision, after.Revision, "查询不得使无关 Git 写入快照失效")
	require.NoError(t, WithWrite(ctx, nil, func(context.Context) error { return nil }))
	after, _, err = db.GetByID[WriteLock](ctx, 1)
	require.NoError(t, err)
	require.Equal(t, before.Revision+1, after.Revision)
}
