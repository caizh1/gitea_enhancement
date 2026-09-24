// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	actions_model "gitea.dev/models/actions"
	activities_model "gitea.dev/models/activities"
	asymkey_model "gitea.dev/models/asymkey"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	"gitea.dev/modules/util"
	"gitea.dev/services/feed"
	governance_service "gitea.dev/services/governance"
	notify_service "gitea.dev/services/notify"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var notifySync sync.Once

func registerNotifier() {
	notifySync.Do(func() {
		notify_service.RegisterNotifier(feed.NewNotifier())
	})
}

func TestTransferOwnership(t *testing.T) {
	registerNotifier()

	assert.NoError(t, unittest.PrepareTestDatabase())

	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	assert.NoError(t, repo.LoadOwner(t.Context()))
	repoTransfer := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoTransfer{ID: 1})
	assert.NoError(t, repoTransfer.LoadAttributes(t.Context()))
	assert.NoError(t, AcceptTransferOwnership(t.Context(), repo, doer))

	transferredRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	assert.EqualValues(t, 1, transferredRepo.OwnerID) // repo_transfer.yml id=1
	unittest.AssertNotExistsBean(t, &repo_model.RepoTransfer{ID: 1})

	exist, err := util.IsExist(repo_model.RepoPath("org3", "repo3"))
	assert.NoError(t, err)
	assert.True(t, exist)
	exist, err = util.IsExist(repo_model.RepoPath("user1", "repo3"))
	assert.NoError(t, err)
	assert.False(t, exist)
	assert.Equal(t, "org3", transferredRepo.GovernanceStorageOwner)
	assert.Equal(t, "repo3", transferredRepo.GovernanceStorageName)
	assert.Equal(t, repo_model.RepoPath("org3", "repo3"), transferredRepo.RepoPath())
	unadopted, _, err := ListUnadoptedRepositories(t.Context(), "org3/repo3", &db.ListOptions{Page: 1, PageSize: 20})
	require.NoError(t, err)
	assert.NotContains(t, unadopted, "org3/repo3")
	oldOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})
	assert.ErrorIs(t, DeleteUnadoptedRepository(t.Context(), doer, oldOwner, "repo3"), repo_model.ErrRepoAlreadyExist{Uname: "org3", Name: "repo3"})
	_, err = AdoptRepository(t.Context(), doer, oldOwner, CreateRepoOptions{Name: "repo3"})
	assert.ErrorIs(t, err, repo_model.ErrRepoAlreadyExist{Uname: "org3", Name: "repo3"})
	unittest.AssertExistsAndLoadBean(t, &activities_model.Action{
		OpType:    activities_model.ActionTransferRepo,
		ActUserID: 1,
		RepoID:    3,
		Content:   "org3/repo3",
	})

	unittest.CheckConsistencyFor(t, &repo_model.Repository{}, &user_model.User{}, &organization.Team{})
}

func TestRepositoryReturnsToItsOwnStoragePath(t *testing.T) {
	registerNotifier()
	t.Run("transfer back", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
		originalOwner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})
		require.NoError(t, repo.LoadOwner(t.Context()))
		originalStorage := repo.RelativePath()
		require.NoError(t, AcceptTransferOwnership(t.Context(), repo, doer))
		require.NoError(t, StartRepositoryTransfer(t.Context(), doer, originalOwner, repo, nil))
		fresh := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
		require.Equal(t, originalOwner.ID, fresh.OwnerID)
		require.Equal(t, originalStorage, fresh.RelativePath())
		require.EqualValues(t, 2, fresh.ActionsScopeRevision)
	})
	t.Run("rename back", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		require.NoError(t, repo.LoadOwner(t.Context()))
		originalName, originalStorage := repo.Name, repo.RelativePath()
		require.NoError(t, ChangeRepositoryName(t.Context(), doer, repo, "phase1-renamed"))
		require.NoError(t, ChangeRepositoryName(t.Context(), doer, repo, originalName))
		fresh := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
		require.Equal(t, originalName, fresh.Name)
		require.Equal(t, originalStorage, fresh.RelativePath())
	})
}

func TestTransferredPrivateRepositoryKeepsAccessRequestPath(t *testing.T) {
	registerNotifier()
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	applicant := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 8})
	repo, err := CreateRepositoryDirectly(ctx, owner, owner, CreateRepoOptions{Name: "request-transfer-source"}, true)
	require.NoError(t, err)
	require.NoError(t, repo.LoadOwner(ctx))
	oldPath := repo.FullPath()
	request, err := governance_service.RequestAccess(ctx, governance_model.Actor{ID: applicant.ID, Name: applicant.Name, Kind: "user", Transport: "api"}, "repository", repo.ID)
	require.NoError(t, err)
	require.Equal(t, oldPath, request.ScopePath)

	require.NoError(t, MakeRepoPrivate(ctx, repo, true))
	ownerActor := governance_model.Actor{ID: owner.ID, Name: owner.Name, Kind: "user", Transport: "api"}
	invitation := &governance_model.Invitation{ScopeType: "repository", ScopeID: repo.ID, Email: "pending-review@example.test", LowerEmail: "pending-review@example.test", ScopePath: oldPath, Inviter: ownerActor, Role: governance_model.Developer, ExpiresUnix: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, db.Insert(ctx, invitation))
	targetRoot, err := governance_service.CreateGroup(ctx, ownerActor, governance_service.GroupOption{Path: "request-transfer-private", Visibility: 2})
	require.NoError(t, err)
	target, err := governance_service.CreateGroup(ctx, ownerActor, governance_service.GroupOption{Path: repo.Name, ParentID: targetRoot.ID, Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, StartRepositoryTransfer(ctx, owner, unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: target.ID}), repo, nil))
	transferred := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	require.Equal(t, target.ID, transferred.OwnerID)
	require.True(t, transferred.IsPrivate)
	expectedPath := target.FullPath + "/" + repo.Name
	assert.Equal(t, target.FullPath, transferred.OwnerNamespace)
	assert.Equal(t, expectedPath, transferred.FullPath())
	invitation = unittest.AssertExistsAndLoadBean(t, &governance_model.Invitation{ID: invitation.ID})
	assert.Equal(t, repo.ID, invitation.ScopeID)
	assert.Equal(t, expectedPath, invitation.ScopePath)

	requests, err := governance_service.OwnAccessRequests(ctx, applicant.ID, 0)
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.Equal(t, request.ID, requests[0].ID)
	require.Equal(t, oldPath, requests[0].ScopePath)
	_, err = governance_service.GetAccessRequestState(ctx, applicant.ID, "repository", repo.ID, 0)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, transferred, applicant)
	require.NoError(t, err)
	require.False(t, permission.CanRead(unit.TypeCode))
	state, err := governance_service.GetAccessRequestState(ctx, owner.ID, "repository", repo.ID, 0)
	require.NoError(t, err)
	require.Equal(t, expectedPath, state.FullPath)
	page, err := governance_service.ListInvitations(ctx, owner.ID, "repository", repo.ID, 0)
	require.NoError(t, err)
	require.Len(t, page.Invitations, 1)
	require.Equal(t, invitation.ID, page.Invitations[0].ID)
	require.NoError(t, governance_service.RevokeInvitation(ctx, ownerActor, "repository", repo.ID, invitation.ID))
	require.NoError(t, governance_service.DecideAccessRequest(ctx, ownerActor, "repository", repo.ID, request.ID, governance_service.GroupMemberOption{}, false))
	unittest.AssertCount(t, &governance_model.AccessRequest{ID: request.ID}, 0)
}

func TestAcceptTransferOwnershipRejectsOldOrganizationLabelLoss(t *testing.T) {
	registerNotifier()
	for _, tc := range []struct {
		name        string
		association bool
	}{
		{name: "association and history", association: true},
		{name: "history without current association"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			ctx := t.Context()
			if tc.association {
				require.NoError(t, db.Insert(ctx, &issues_model.IssueLabel{IssueID: 6, LabelID: 3}))
			}
			history := &issues_model.Comment{Type: issues_model.CommentTypeLabel, PosterID: 1, IssueID: 6, LabelID: 3, Content: "1"}
			require.NoError(t, db.Insert(ctx, history))
			repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
			require.NoError(t, repo.LoadOwner(ctx))
			recipient := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})

			err := AcceptTransferOwnership(ctx, repo, recipient)
			hasHistory, readErr := db.GetEngine(ctx).ID(history.ID).Exist(new(issues_model.Comment))
			require.NoError(t, readErr)
			assert.True(t, hasHistory, "标签历史不可在转移时删除")
			if tc.association {
				hasAssociation, readErr := db.GetEngine(ctx).Where("issue_id = ? AND label_id = ?", 6, 3).Exist(new(issues_model.IssueLabel))
				require.NoError(t, readErr)
				assert.True(t, hasAssociation, "标签关联不可在转移时删除")
			}
			require.ErrorIs(t, err, governance_model.ErrConflict)
			assert.Contains(t, err.Error(), "标签")
			assert.EqualValues(t, 3, unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3}).OwnerID)
			unittest.AssertExistsAndLoadBean(t, &repo_model.RepoTransfer{ID: 1})
		})
	}
}

func TestAcceptTransferOwnershipKeepsRepositoryLabels(t *testing.T) {
	registerNotifier()
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	require.NoError(t, db.Insert(ctx, &issues_model.IssueLabel{IssueID: 6, LabelID: 10}))
	history := &issues_model.Comment{Type: issues_model.CommentTypeLabel, PosterID: 1, IssueID: 6, LabelID: 10, Content: "1"}
	require.NoError(t, db.Insert(ctx, history))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	require.NoError(t, repo.LoadOwner(ctx))
	recipient := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})

	require.NoError(t, AcceptTransferOwnership(ctx, repo, recipient))
	assert.Equal(t, recipient.ID, unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID}).OwnerID)
	unittest.AssertExistsAndLoadBean(t, &issues_model.IssueLabel{IssueID: 6, LabelID: 10})
	unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: history.ID})
}

func TestTransferKeepsLabelsAvailableFromTargetAncestors(t *testing.T) {
	for _, association := range []bool{true, false} {
		t.Run(util.Iif(association, "current", "history"), func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			ctx := t.Context()
			require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
			child := &governance_model.Namespace{ID: 1001, Kind: "group", ParentID: 3, Slug: "target-child", Visibility: 2}
			require.NoError(t, governance_model.InsertNamespace(ctx, child))
			if association {
				require.NoError(t, db.Insert(ctx, &issues_model.IssueLabel{IssueID: 6, LabelID: 3}))
			} else {
				require.NoError(t, db.Insert(ctx, &issues_model.Comment{Type: issues_model.CommentTypeLabel, PosterID: 1, IssueID: 6, LabelID: 3, Content: "1"}))
			}
			require.NoError(t, rejectTransferWithLostLabels(ctx, 3, child.ID), "目标祖先仍提供同一标签 ID 时可以保留")
			require.NoError(t, db.Insert(ctx, &governance_model.Share{ScopeType: "group", ScopeID: 17, GroupID: 3, MaxRole: governance_model.Owner}))
			require.ErrorIs(t, rejectTransferWithLostLabels(ctx, 3, 17), governance_model.ErrConflict, "共享不是标签继承")
		})
	}
}

func TestPrepareActionsForTransferCancelsQueueAndPreservesHistory(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api", RequestID: "actions-transfer"})
	repo := &repo_model.Repository{OwnerID: 2, OwnerName: "user2", Name: "phase1-transfer", LowerName: "phase1-transfer", Status: repo_model.RepositoryReady}
	require.NoError(t, db.Insert(ctx, repo))
	run := &actions_model.ActionRun{RepoID: repo.ID, OwnerID: 2, WorkflowID: "test.yml", Index: 1, TriggerUserID: 2, Status: actions_model.StatusWaiting}
	require.NoError(t, db.Insert(ctx, run))
	job := &actions_model.ActionRunJob{RepoID: repo.ID, OwnerID: 2, RunID: run.ID, JobID: "old-queue", Status: actions_model.StatusWaiting}
	require.NoError(t, db.Insert(ctx, job))
	history := &actions_model.ActionTask{RepoID: repo.ID, OwnerID: 2, Status: actions_model.StatusSuccess}
	history.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, history))

	active := &actions_model.ActionTask{RepoID: repo.ID, OwnerID: 2, Status: actions_model.StatusRunning}
	active.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, active))
	require.ErrorIs(t, prepareActionsForTransfer(ctx, repo.ID), governance_model.ErrConflict)
	assert.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID}).Status)
	assert.False(t, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: run.ID}).ScopeInvalidated)
	_, err := db.DeleteByID[actions_model.ActionTask](ctx, active.ID)
	require.NoError(t, err)

	runner := &actions_model.ActionRunner{RepoID: repo.ID, Name: "old-runner"}
	runner.GenerateAndFillToken()
	require.NoError(t, db.Insert(ctx, runner))
	require.ErrorIs(t, prepareActionsForTransfer(ctx, repo.ID), governance_model.ErrConflict)
	_, err = db.DeleteByID[actions_model.ActionRunner](ctx, runner.ID)
	require.NoError(t, err)

	key := &asymkey_model.DeployKey{RepoID: repo.ID, KeyID: 9914, Name: "old-key"}
	require.NoError(t, db.Insert(ctx, key))
	require.ErrorIs(t, prepareActionsForTransfer(ctx, repo.ID), governance_model.ErrConflict)
	_, err = db.DeleteByID[asymkey_model.DeployKey](ctx, key.ID)
	require.NoError(t, err)
	oldToken := &actions_model.ActionRunnerToken{RepoID: repo.ID, Token: "phase1-old-repo-registration-token", IsActive: true}
	require.NoError(t, db.Insert(ctx, oldToken))
	laterFailure := errors.New("later transfer step failed")
	err = db.WithTx(ctx, func(ctx context.Context) error {
		require.NoError(t, prepareActionsForTransfer(ctx, repo.ID))
		return laterFailure
	})
	require.ErrorIs(t, err, laterFailure)
	assert.Equal(t, actions_model.StatusWaiting, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID}).Status)
	assert.True(t, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunnerToken{ID: oldToken.ID}).IsActive)
	assert.False(t, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: run.ID}).ScopeInvalidated)

	require.NoError(t, prepareActionsForTransfer(ctx, repo.ID))
	assert.Equal(t, actions_model.StatusCancelled, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunJob{ID: job.ID}).Status)
	assert.Equal(t, actions_model.StatusSuccess, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: history.ID}).Status)
	assert.False(t, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRunnerToken{ID: oldToken.ID}).IsActive)
	assert.True(t, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: run.ID}).ScopeInvalidated)
	_, err = db.GetEngine(ctx).ID(repo.ID).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 5})
	require.NoError(t, err)
	staleRunner := &actions_model.ActionRunner{RepoID: repo.ID, Name: "stale-owner-runner", UUID: "phase1-stale-runner"}
	staleRunner.GenerateAndFillToken()
	require.Error(t, actions_model.RegisterRunnerWithToken(ctx, staleRunner, oldToken.ID))
	newToken, err := actions_model.NewRunnerToken(ctx, 0, repo.ID)
	require.NoError(t, err)
	newRunner := &actions_model.ActionRunner{RepoID: repo.ID, Name: "new-owner-runner", UUID: "phase1-new-runner"}
	newRunner.GenerateAndFillToken()
	require.NoError(t, actions_model.RegisterRunnerWithToken(ctx, newRunner, newToken.ID))
}

func TestPrepareActionsForTransferScopeRevisionSurvivesReturn(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"})
	repo := &repo_model.Repository{OwnerID: 2, OwnerName: "user2", Name: "scope-return", LowerName: "scope-return", Status: repo_model.RepositoryReady}
	require.NoError(t, db.Insert(ctx, repo))
	run := &actions_model.ActionRun{RepoID: repo.ID, OwnerID: 2, WorkflowID: "old.yml", Index: 1, TriggerUserID: 2, Status: actions_model.StatusSuccess}
	require.NoError(t, db.Insert(ctx, run))
	require.NoError(t, prepareActionsForTransfer(ctx, repo.ID))
	_, err := db.GetEngine(ctx).ID(repo.ID).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 5})
	require.NoError(t, err)
	require.NoError(t, prepareActionsForTransfer(ctx, repo.ID))
	_, err = db.GetEngine(ctx).ID(repo.ID).Cols("owner_id").Update(&repo_model.Repository{OwnerID: 2})
	require.NoError(t, err)
	current := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	assert.EqualValues(t, 2, current.OwnerID)
	assert.EqualValues(t, 2, current.ActionsScopeRevision)
	assert.True(t, unittest.AssertExistsAndLoadBean(t, &actions_model.ActionRun{ID: run.ID}).ScopeInvalidated)
}

func TestStartRepositoryTransferSetPermission(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	recipient := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	assert.NoError(t, repo.LoadOwner(t.Context()))

	hasAccess, err := access_model.HasAnyUnitAccess(t.Context(), recipient.ID, repo)
	assert.NoError(t, err)
	assert.False(t, hasAccess)

	assert.NoError(t, StartRepositoryTransfer(t.Context(), doer, recipient, repo, nil))

	hasAccess, err = access_model.HasAnyUnitAccess(t.Context(), recipient.ID, repo)
	assert.NoError(t, err)
	assert.True(t, hasAccess)

	unittest.CheckConsistencyFor(t, &repo_model.Repository{}, &user_model.User{}, &organization.Team{})
}

func TestRepositoryTransferPreviewRejectsChangedTarget(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	target := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	require.NoError(t, repo.LoadOwner(t.Context()))

	impact, err := PreviewRepositoryTransfer(t.Context(), doer.ID, repo.ID, target.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, impact.OldPath)
	assert.NotEmpty(t, impact.NewPath)

	_, err = db.GetEngine(t.Context()).ID(target.ID).Cols("name").Update(&user_model.User{Name: "target-renamed"})
	require.NoError(t, err)
	freshTarget := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: target.ID})
	err = StartRepositoryTransferAfterPreview(t.Context(), doer, freshTarget, repo, nil, impact)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	unchanged := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	assert.Equal(t, repo.OwnerID, unchanged.OwnerID)
}

func TestRepositoryTransferPreviewRejectsChangedRelevantGovernance(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	target := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	require.NoError(t, repo.LoadOwner(t.Context()))
	impact, err := PreviewRepositoryTransfer(t.Context(), doer.ID, repo.ID, target.ID)
	require.NoError(t, err)
	_, err = db.GetEngine(t.Context()).ID(repo.OwnerID).Incr("revision").Update(new(governance_model.Namespace))
	require.NoError(t, err)
	err = StartRepositoryTransferAfterPreview(t.Context(), doer, target, repo, nil, impact)
	require.ErrorIs(t, err, governance_model.ErrConflict)
}

func TestRepositoryTransferPreviewRejectsChangedInheritedVariable(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	target := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	require.NoError(t, repo.LoadOwner(t.Context()))
	impact, err := PreviewRepositoryTransfer(t.Context(), doer.ID, repo.ID, target.ID)
	require.NoError(t, err)
	require.NoError(t, db.Insert(t.Context(), &actions_model.ActionVariable{OwnerID: target.ID, Name: "SYNTHETIC_LIFECYCLE", Data: "synthetic"}))
	err = StartRepositoryTransferAfterPreview(t.Context(), doer, target, repo, nil, impact)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	unchanged := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	assert.Equal(t, repo.OwnerID, unchanged.OwnerID)
}

func TestAcceptRepositoryTransferUsesRecipientAuditActor(t *testing.T) {
	registerNotifier()
	require.NoError(t, unittest.PrepareTestDatabase())

	source := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	recipient := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	require.NoError(t, repo.LoadOwner(t.Context()))
	require.NoError(t, StartRepositoryTransfer(t.Context(), source, recipient, repo, nil))

	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: recipient.ID, Name: recipient.Name, Kind: "user", Transport: "api", RequestID: "accept-transfer"})
	require.NoError(t, AcceptTransferOwnership(ctx, repo, recipient))
	transferred := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	assert.EqualValues(t, recipient.ID, transferred.OwnerID)
}

func TestAcceptRepositoryTransferRejectsRevokedSourceOwner(t *testing.T) {
	registerNotifier()
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	source := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	recipient := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	require.NoError(t, repo.LoadOwner(t.Context()))
	require.NoError(t, repo_model.DeleteRepositoryTransfer(t.Context(), repo.ID))
	require.NoError(t, StartRepositoryTransfer(t.Context(), source, recipient, repo, nil))

	// 接受前撤销原申请人在原群组的 Owners 团队资格。
	_, err := db.GetEngine(t.Context()).Where("org_id = ? AND uid = ?", repo.OwnerID, source.ID).Delete(new(organization.TeamUser))
	require.NoError(t, err)
	require.NoError(t, access_model.RecalculateAccesses(t.Context(), repo))
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: recipient.ID, Name: recipient.Name, Kind: "user", Transport: "api", RequestID: "accept-after-revoke"})
	err = AcceptTransferOwnership(ctx, repo, recipient)
	require.Error(t, err)
	unittest.AssertExistsAndLoadBean(t, &repo_model.RepoTransfer{RepoID: repo.ID})
	unchanged := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	assert.EqualValues(t, 3, unchanged.OwnerID)
}

func TestRepositoryTransfer(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})

	transfer, err := repo_model.GetPendingRepositoryTransfer(t.Context(), repo)
	assert.NoError(t, err)
	assert.NotNil(t, transfer)

	// Cancel transfer
	assert.NoError(t, CancelRepositoryTransfer(t.Context(), transfer, doer))

	transfer, err = repo_model.GetPendingRepositoryTransfer(t.Context(), repo)
	assert.Error(t, err)
	assert.Nil(t, transfer)
	assert.True(t, repo_model.IsErrNoPendingTransfer(err))

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	assert.NoError(t, repo_model.CreatePendingRepositoryTransfer(t.Context(), doer, user2, repo.ID, nil))

	transfer, err = repo_model.GetPendingRepositoryTransfer(t.Context(), repo)
	assert.NoError(t, err)
	assert.NoError(t, transfer.LoadAttributes(t.Context()))
	assert.Equal(t, "user2", transfer.Recipient.Name)

	org6 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	// Only transfer can be started at any given time
	err = repo_model.CreatePendingRepositoryTransfer(t.Context(), doer, org6, repo.ID, nil)
	assert.Error(t, err)
	assert.True(t, repo_model.IsErrRepoTransferInProgress(err))

	repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	// Unknown user, transfer non-existent transfer repo id = 2
	err = repo_model.CreatePendingRepositoryTransfer(t.Context(), doer, &user_model.User{ID: 1000, LowerName: "user1000"}, repo2.ID, nil)
	assert.Error(t, err)

	// Reject transfer
	err = RejectRepositoryTransfer(t.Context(), repo2, doer)
	assert.True(t, repo_model.IsErrNoPendingTransfer(err))
}

// Test transfer rejections
func TestRepositoryTransferRejection(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	// Set limit to 0 repositories so no repositories can be transferred
	defer test.MockVariableValue(&setting.Repository.MaxCreationLimit, 0)()
	defer test.MockVariableValue(&setting.Repository.UserMaxCreationLimit, 0)()
	defer test.MockVariableValue(&setting.Repository.OrgMaxCreationLimit, 0)()

	// Admin case
	doerAdmin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 5})

	transfer, err := repo_model.GetPendingRepositoryTransfer(t.Context(), repo)
	require.NoError(t, err)
	require.NotNil(t, transfer)
	require.NoError(t, transfer.LoadRecipient(t.Context()))

	require.True(t, doerAdmin.CanCreateRepoIn(transfer.Recipient)) // admin is not subject to limits

	// Administrator should not be affected by the limits so transfer should be successful
	assert.NoError(t, AcceptTransferOwnership(t.Context(), repo, doerAdmin))

	// Non admin user case
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 10})
	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 21})

	transfer, err = repo_model.GetPendingRepositoryTransfer(t.Context(), repo)
	require.NoError(t, err)
	require.NotNil(t, transfer)
	require.NoError(t, transfer.LoadRecipient(t.Context()))

	require.False(t, doer.CanCreateRepoIn(transfer.Recipient)) // regular user is subject to limits

	// Cannot accept because of the limit
	err = AcceptTransferOwnership(t.Context(), repo, doer)
	assert.Error(t, err)
	assert.True(t, IsRepositoryLimitReached(err))
}
