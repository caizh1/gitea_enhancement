// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"sync"
	"testing"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	"gitea.dev/modules/util"
	"gitea.dev/services/feed"
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
	assert.EqualValues(t, repo.OwnerID, unchanged.OwnerID)
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
