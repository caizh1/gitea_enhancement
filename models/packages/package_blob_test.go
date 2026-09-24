// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package packages

import (
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestPrepareBlobUploadReservesCleanupWindow(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	input := &PackageBlob{Size: 1, HashMD5: strings.Repeat("d", 32), HashSHA1: strings.Repeat("d", 40), HashSHA256: strings.Repeat("d", 64), HashSHA512: strings.Repeat("d", 128)}
	blob, _, err := GetOrInsertBlob(ctx, input)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Exec("UPDATE package_blob SET created_unix = ? WHERE id = ?", 1, blob.ID)
	require.NoError(t, err)
	expired, err := FindExpiredUnreferencedBlobs(ctx, 0)
	require.NoError(t, err)
	require.Contains(t, idsOfBlobs(expired), blob.ID)
	reserved, existed, err := PrepareBlobUpload(ctx, input)
	require.NoError(t, err)
	require.True(t, existed)
	require.Equal(t, blob.ID, reserved.ID)
	expired, err = FindExpiredUnreferencedBlobs(ctx, 0)
	require.NoError(t, err)
	require.NotContains(t, idsOfBlobs(expired), blob.ID, "即时清理不能回收正在上传的正文")
	_, err = db.GetEngine(ctx).Exec("UPDATE package_blob SET created_unix = ? WHERE id = ?", 1, blob.ID)
	require.NoError(t, err)
	expired, err = FindExpiredUnreferencedBlobs(ctx, 24*time.Hour)
	require.NoError(t, err)
	require.Contains(t, idsOfBlobs(expired), blob.ID, "失败且过期的孤立正文仍由原清理任务回收")
}

func idsOfBlobs(blobs []*PackageBlob) []int64 {
	ids := make([]int64, 0, len(blobs))
	for _, blob := range blobs {
		ids = append(ids, blob.ID)
	}
	return ids
}

func TestGetOrInsertBlobConcurrent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	testBlob := PackageBlob{
		Size:       123,
		HashMD5:    "md5",
		HashSHA1:   "sha1",
		HashSHA256: strings.Repeat("a", 64),
		HashSHA512: "sha512",
	}

	const numGoroutines = 3
	var wg errgroup.Group
	results := make([]*PackageBlob, numGoroutines)
	existed := make([]bool, numGoroutines)
	for idx := range numGoroutines {
		wg.Go(func() error {
			blob := testBlob // Create a copy of the test blob for each goroutine
			var err error
			results[idx], existed[idx], err = GetOrInsertBlob(t.Context(), &blob)
			return err
		})
	}
	require.NoError(t, wg.Wait())

	// then: all GetOrInsertBlob succeeds with the same blob ID, and only one indicates it did not exist before
	existedCount := 0
	assert.NotNil(t, results[0])
	for i := range numGoroutines {
		assert.Equal(t, results[0].ID, results[i].ID)
		if existed[i] {
			existedCount++
		}
	}
	assert.Equal(t, numGoroutines-1, existedCount)
}

func TestIsBlobAccessibleForRestrictedUser(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 33})
	pkg, err := TryInsertPackage(t.Context(), &Package{OwnerID: owner.ID, Type: TypeContainer, Name: "limited", LowerName: "limited"})
	require.NoError(t, err)
	version, err := GetOrInsertVersion(t.Context(), &PackageVersion{PackageID: pkg.ID, Version: "1", LowerVersion: "1"})
	require.NoError(t, err)
	blob, _, err := GetOrInsertBlob(t.Context(), &PackageBlob{Size: 1, HashMD5: "md5", HashSHA1: "sha1", HashSHA256: strings.Repeat("a", 64), HashSHA512: "sha512"})
	require.NoError(t, err)
	_, err = TryInsertFile(t.Context(), &PackageFile{VersionID: version.ID, BlobID: blob.ID, Name: "blob", LowerName: "blob"})
	require.NoError(t, err)

	accessible, err := IsBlobAccessibleForUser(t.Context(), blob.ID, unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2}))
	require.NoError(t, err)
	assert.True(t, accessible)

	accessible, err = IsBlobAccessibleForUser(t.Context(), blob.ID, unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 29}))
	require.NoError(t, err)
	assert.False(t, accessible)
}
