// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestGroupBranchProtectionFollowsCurrentAncestryAndReservations(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	root, err := CheckGroupAccess(ctx, actor.ID, 3, governance_model.ManageGroup)
	require.NoError(t, err)
	saved, err := SaveGroupBranchProtection(ctx, actor, GroupBranchProtectionInput{GroupID: 3, Revision: root.Revision, RuleName: "release/*", PushRole: governance_model.Maintainer})
	require.NoError(t, err)
	require.Positive(t, saved.ID)
	require.False(t, saved.Match("Release/v1"))
	require.True(t, saved.Match("release/v1"))
	_, err = SaveGroupBranchProtection(ctx, actor, GroupBranchProtectionInput{GroupID: 3, Revision: root.Revision, RuleName: "main"})
	require.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = SaveGroupBranchProtection(ctx, actor, GroupBranchProtectionInput{GroupID: 3, Revision: root.Revision + 1, RuleName: "release/*"})
	require.ErrorIs(t, err, governance_model.ErrConflict, "a duplicate must not become a server error")
	_, err = SaveGroupBranchProtection(ctx, actor, GroupBranchProtectionInput{GroupID: 3, Revision: root.Revision + 1, RuleName: "bad["})
	require.ErrorIs(t, err, governance_model.ErrInvalid)
	_, err = SaveGroupBranchProtection(ctx, governance_model.Actor{ID: 4, Kind: "user", Transport: "api"}, GroupBranchProtectionInput{GroupID: 3, Revision: root.Revision + 1, RuleName: "main"})
	require.Error(t, err)
	protection, err := git_model.EvaluateEffectiveBranchProtection(ctx, 1, "release/v1")
	require.NoError(t, err)
	require.Empty(t, protection.Group, "unrelated repository must not inherit the rule")
	_, err = db.GetEngine(ctx).ID(6).Cols("parent_id", "full_path", "lower_path").Update(&governance_model.Namespace{ParentID: 3, FullPath: "org3/org6", LowerPath: "org3/org6"})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(1).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 6})
	require.NoError(t, err)
	protection, err = git_model.EvaluateEffectiveBranchProtection(ctx, 1, "release/v1")
	require.NoError(t, err)
	require.Len(t, protection.Group, 1)
	require.Equal(t, saved.ID, protection.Group[0].ID)
	owner := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.EqualValues(t, 6, owner.OwnerID)
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: actor.ID})
	permission, err := access_model.GetDoerRepoPermission(ctx, protection.Repo, user)
	require.NoError(t, err)
	mergeAllowed, err := protection.CanUserMerge(ctx, user, permission)
	require.NoError(t, err)
	require.True(t, mergeAllowed, "push-and-merge role must allow MR merge despite an empty merge-only role")
	allowed, err := protection.CanUserPush(ctx, user)
	require.NoError(t, err)
	require.True(t, allowed)
	require.NoError(t, db.Insert(ctx, &governance_model.ReferenceReservation{Resource: governance_model.Resource("repository", 1), TransactionID: "synthetic-pending"}))
	_, err = SaveGroupBranchProtection(ctx, actor, GroupBranchProtectionInput{ID: saved.ID, GroupID: 3, Revision: root.Revision + 1, RuleName: "release/*", PushRole: governance_model.Developer})
	require.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = db.GetEngine(ctx).Where("resource = ?", governance_model.Resource("repository", 1)).Delete(new(governance_model.ReferenceReservation))
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &governance_model.Reservation{Resource: governance_model.Resource("repository", 1), AuthorizationID: "synthetic-merge"}))
	_, err = SaveGroupBranchProtection(ctx, actor, GroupBranchProtectionInput{ID: saved.ID, GroupID: 3, Revision: root.Revision + 1, RuleName: "release/*", PushRole: governance_model.Developer})
	require.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = db.GetEngine(ctx).Where("resource = ?", governance_model.Resource("repository", 1)).Delete(new(governance_model.Reservation))
	require.NoError(t, err)
	updated, err := SaveGroupBranchProtection(ctx, actor, GroupBranchProtectionInput{ID: saved.ID, GroupID: 3, Revision: root.Revision + 1, RuleName: "release/*", PushRole: governance_model.Developer})
	require.NoError(t, err)
	require.Equal(t, governance_model.Developer, updated.PushRole)
	denied, err := SaveGroupBranchProtection(ctx, actor, GroupBranchProtectionInput{ID: saved.ID, GroupID: 3, Revision: root.Revision + 2, RuleName: "release/*"})
	require.NoError(t, err)
	require.Zero(t, denied.PushRole)
	protection, err = git_model.EvaluateEffectiveBranchProtection(ctx, 1, "release/v1")
	require.NoError(t, err)
	allowed, err = protection.CanUserPush(ctx, user)
	require.NoError(t, err)
	require.False(t, allowed)
	require.NoError(t, db.Insert(ctx, &git_model.ProtectedBranch{RepoID: 1, RuleName: "release/v1", CanPush: true}))
	protection, err = git_model.EvaluateEffectiveBranchProtection(ctx, 1, "release/v1")
	require.NoError(t, err)
	allowed, err = protection.CanUserPush(ctx, user)
	require.NoError(t, err)
	require.True(t, allowed, "native project rule can permissively combine with the group rule")
	_, err = db.GetEngine(ctx).Where("repo_id = ? AND branch_name = ?", 1, "release/v1").Cols("can_push", "unprotected_file_patterns").Update(&git_model.ProtectedBranch{UnprotectedFilePatterns: "docs/*"})
	require.NoError(t, err)
	repository := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.ErrorIs(t, checkGroupBranchProtectionAtPrepared(ctx, repository, &governance_model.ReferenceTransaction{Actor: actor}, governance_model.ReferenceChange{Ref: "refs/heads/release/v1", New: "abcdef"}, false), governance_model.ErrForbidden, "a new group rule cannot trust an unverified native file exception")
	_, err = SaveGroupBranchProtection(ctx, actor, GroupBranchProtectionInput{ID: saved.ID, GroupID: 6, Revision: 1, Delete: true})
	require.ErrorIs(t, err, governance_model.ErrConflict)
}

func TestGroupProtectionAddedAfterActionsPreReceive(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := db.GetEngine(ctx).ID(6).Cols("parent_id", "full_path", "lower_path").Update(&governance_model.Namespace{ParentID: 3, FullPath: "org3/org6", LowerPath: "org3/org6"})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(2).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 6})
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 2, Type: unit.TypeActions, Config: &repo_model.ActionsConfig{}}))
	runner := &actions_model.ActionRunner{Name: "group-protection-test-runner"}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, runner))
	_, err = db.GetEngine(ctx).ID(53).Cols("owner_id", "runner_id", "status").Update(&actions_model.ActionTask{OwnerID: 6, RunnerID: runner.ID, Status: actions_model.StatusRunning})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(198).Cols("owner_id").Update(&actions_model.ActionRunJob{OwnerID: 6})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(795).Cols("owner_id", "trigger_user_id").Update(&actions_model.ActionRun{OwnerID: 6, TriggerUserID: 2})
	require.NoError(t, err)
	repository := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	actionsUser := user_model.NewActionsUserWithTaskID(53)
	permission, err := access_model.GetActionsUserRepoPermission(ctx, repository, actionsUser, 53)
	require.NoError(t, err)
	require.True(t, permission.CanWrite(unit.TypeCode), "the task token could write before the new rule")
	before, err := git_model.EvaluateEffectiveBranchProtection(ctx, 2, "release/staging")
	require.NoError(t, err)
	require.False(t, before.IsProtected(), "the Actions pre-receive decision saw no group rule")
	root, err := CheckGroupAccess(ctx, 2, 3, governance_model.ManageGroup)
	require.NoError(t, err)
	_, err = SaveGroupBranchProtection(ctx, governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}, GroupBranchProtectionInput{GroupID: 3, Revision: root.Revision, RuleName: "release/*"})
	require.NoError(t, err)
	operation := &governance_model.ReferenceTransaction{Actor: governance_model.Actor{ID: user_model.ActionsUserID, Kind: "service_account", CredentialID: 53}}
	require.ErrorIs(t, checkGroupBranchProtectionAtPrepared(ctx, repository, operation, governance_model.ReferenceChange{Ref: "refs/heads/release/staging", New: "abcdef"}, false), governance_model.ErrForbidden)
	require.NoError(t, db.Insert(ctx, &git_model.ProtectedBranch{RepoID: 2, RuleName: "release/staging", CanPush: true, EnableWhitelist: true, WhitelistUserIDs: []int64{user_model.ActionsUserID}}))
	require.NoError(t, checkGroupBranchProtectionAtPrepared(ctx, repository, operation, governance_model.ReferenceChange{Ref: "refs/heads/release/staging", New: "abcdef"}, false), "an explicitly allowed current task token may still push")
	_, err = db.GetEngine(ctx).ID(53).Cols("status").Update(&actions_model.ActionTask{Status: actions_model.StatusCancelled})
	require.NoError(t, err)
	require.ErrorIs(t, checkGroupBranchProtectionAtPrepared(ctx, repository, operation, governance_model.ReferenceChange{Ref: "refs/heads/release/staging", New: "abcdef"}, false), governance_model.ErrForbidden, "revoking the task must revoke the prepared write")
}
