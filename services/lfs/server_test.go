// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package lfs

import (
	"strings"
	"testing"

	asymkey_model "gitea.dev/models/asymkey"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	perm_model "gitea.dev/models/perm"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	lfs_module "gitea.dev/modules/lfs"
	"gitea.dev/services/contexttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestDeployKeyLFSTokenCurrentScope(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx, _ := contexttest.MockContext(t, "/")
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	require.EqualValues(t, 3, repo.OwnerID)
	publicKey := &asymkey_model.PublicKey{Name: "lfs-current-key", Fingerprint: "lfs-current-key", Type: asymkey_model.KeyTypeDeploy, Mode: perm_model.AccessModeWrite}
	require.NoError(t, db.Insert(t.Context(), publicKey))
	key := &asymkey_model.DeployKey{RepoID: repo.ID, KeyID: publicKey.ID, Name: "lfs-current-key", Mode: perm_model.AccessModeWrite}
	require.NoError(t, db.Insert(t.Context(), key))
	token, err := GetLFSAuthTokenWithBearer(AuthTokenOptions{Op: "upload", UserID: repo.OwnerID, RepoID: repo.ID, DeployKeyID: key.ID})
	require.NoError(t, err)
	_, signed, _ := strings.Cut(token, " ")
	owner, keyID, err := handleLFSToken(ctx, signed, repo, perm_model.AccessModeWrite)
	require.NoError(t, err)
	require.Equal(t, repo.OwnerID, owner.ID)
	require.Equal(t, key.ID, keyID)

	other := *repo
	other.ID = 1
	_, _, err = handleLFSToken(ctx, signed, &other, perm_model.AccessModeWrite)
	require.Error(t, err)
	other = *repo
	other.OwnerID = 2
	_, _, err = handleLFSToken(ctx, signed, &other, perm_model.AccessModeWrite)
	require.Error(t, err)

	key.Mode = perm_model.AccessModeRead
	_, err = db.GetEngine(t.Context()).ID(key.ID).Cols("mode").Update(key)
	require.NoError(t, err)
	_, _, err = handleLFSToken(ctx, signed, repo, perm_model.AccessModeWrite)
	require.Error(t, err)
	_, _, err = handleLFSToken(ctx, signed, repo, perm_model.AccessModeRead)
	require.NoError(t, err)
	publicKey.Type = asymkey_model.KeyTypeUser
	_, err = db.GetEngine(t.Context()).ID(publicKey.ID).Cols("type").Update(publicKey)
	require.NoError(t, err)
	_, _, err = handleLFSToken(ctx, signed, repo, perm_model.AccessModeRead)
	require.Error(t, err)
	publicKey.Type = asymkey_model.KeyTypeDeploy
	_, err = db.GetEngine(t.Context()).ID(publicKey.ID).Cols("type").Update(publicKey)
	require.NoError(t, err)
	_, err = db.DeleteByID[asymkey_model.DeployKey](t.Context(), key.ID)
	require.NoError(t, err)
	_, _, err = handleLFSToken(ctx, signed, repo, perm_model.AccessModeRead)
	require.Error(t, err)
}

func TestRejectedLFSUploadCleanupQueueTracksEachRevision(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	pointer, err := lfs_module.GeneratePointer(strings.NewReader("同一 OID 的延迟清理只保留最新暂存版本"))
	require.NoError(t, err)
	require.NoError(t, queueRejectedLFSUploadCleanup(t.Context(), pointer, 5))
	require.NoError(t, queueRejectedLFSUploadCleanup(t.Context(), pointer, 6))
	var tasks []*governance_model.ResourceCleanup
	require.NoError(t, db.GetEngine(t.Context()).Where("kind = ?", "lfs_upload").Find(&tasks))
	require.Len(t, tasks, 2)
	revisions := []int64{tasks[0].Objects[0].Revision, tasks[1].Objects[0].Revision}
	require.ElementsMatch(t, []int64{5, 6}, revisions)
}

func TestAuthenticate(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx, _ := contexttest.MockContext(t, "/")

	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

	getUserToken := func(op string, userID int64, repo *repo_model.Repository) string {
		s, _ := GetLFSAuthTokenWithBearer(AuthTokenOptions{Op: op, UserID: userID, RepoID: repo.ID})
		_, token, _ := strings.Cut(s, " ")
		return token
	}

	t.Run("handleLFSToken", func(t *testing.T) {
		u, _, err := handleLFSToken(ctx, "", repo1, perm_model.AccessModeRead)
		require.Error(t, err)
		assert.Nil(t, u)

		u, _, err = handleLFSToken(ctx, "invalid", repo1, perm_model.AccessModeRead)
		require.Error(t, err)
		assert.Nil(t, u)

		u, _, err = handleLFSToken(ctx, getUserToken("download", 2, repo1), repo1, perm_model.AccessModeRead)
		require.NoError(t, err)
		assert.EqualValues(t, 2, u.ID)
	})

	t.Run("authenticate", func(t *testing.T) {
		const prefixBearer = "Bearer "
		token := getUserToken("download", 2, repo1)
		assert.False(t, authenticate(ctx, repo1, "", true, false))
		assert.False(t, authenticate(ctx, repo1, prefixBearer+"invalid", true, false))
		assert.True(t, authenticate(ctx, repo1, prefixBearer+token, true, false))
	})

	handleLFSTokenTestPerm := func(op string, userID int64, repo *repo_model.Repository, accessMode perm_model.AccessMode) error {
		token := getUserToken(op, userID, repo)
		u, _, err := handleLFSToken(ctx, token, repo, accessMode)
		if err == nil {
			assert.Equal(t, userID, u.ID)
		}
		return err
	}

	t.Run("handleLFSToken blocks prohibited users", func(t *testing.T) {
		user37 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 37})

		// prohibited user
		assert.True(t, user37.ProhibitLogin)
		err := handleLFSTokenTestPerm("download", 37, repo1, perm_model.AccessModeRead)
		assert.ErrorContains(t, err, "not allowed to access any repository")

		// normal user
		_, _ = db.GetEngine(t.Context()).ID(37).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: false})
		err = handleLFSTokenTestPerm("download", 37, repo1, perm_model.AccessModeRead)
		assert.NoError(t, err)

		// inactive user
		_, _ = db.GetEngine(t.Context()).ID(37).Cols("is_active").Update(&user_model.User{IsActive: false})
		err = handleLFSTokenTestPerm("download", 37, repo1, perm_model.AccessModeRead)
		assert.ErrorContains(t, err, "not allowed to access any repository")
	})

	t.Run("handleLFSToken blocks users without repo access", func(t *testing.T) {
		repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
		err := handleLFSTokenTestPerm("download", 10, repo2, perm_model.AccessModeRead)
		assert.ErrorContains(t, err, "no permission to access the repository")
	})

	t.Run("handleLFSToken requires write access for uploads", func(t *testing.T) {
		err := handleLFSTokenTestPerm("download", 10, repo1, perm_model.AccessModeRead)
		assert.NoError(t, err)
		err = handleLFSTokenTestPerm("upload", 10, repo1, perm_model.AccessModeWrite)
		assert.ErrorContains(t, err, "no permission to access the repository")
	})

	t.Run("handleLFSToken allows writes for authorized users", func(t *testing.T) {
		err := handleLFSTokenTestPerm("upload", 2, repo1, perm_model.AccessModeWrite)
		assert.NoError(t, err)
	})
}
