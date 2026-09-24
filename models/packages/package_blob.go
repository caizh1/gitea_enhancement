// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package packages

import (
	"context"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/perm"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// ErrPackageBlobNotExist indicates a package blob not exist error
var ErrPackageBlobNotExist = util.NewNotExistErrorf("package blob does not exist")

func init() {
	db.RegisterModel(new(PackageBlob))
}

// PackageBlob represents a package blob
type PackageBlob struct {
	ID          int64              `xorm:"pk autoincr"`
	Size        int64              `xorm:"NOT NULL DEFAULT 0"`
	HashMD5     string             `xorm:"hash_md5 char(32) UNIQUE(md5) INDEX NOT NULL"`
	HashSHA1    string             `xorm:"hash_sha1 char(40) UNIQUE(sha1) INDEX NOT NULL"`
	HashSHA256  string             `xorm:"hash_sha256 char(64) UNIQUE(sha256) INDEX NOT NULL"`
	HashSHA512  string             `xorm:"hash_sha512 char(128) UNIQUE(sha512) INDEX NOT NULL"`
	CreatedUnix timeutil.TimeStamp `xorm:"created INDEX NOT NULL"`
}

// GetOrInsertBlob inserts a blob. If the blob exists already the existing blob is returned
func GetOrInsertBlob(ctx context.Context, pb *PackageBlob) (*PackageBlob, bool, error) {
	var result *PackageBlob
	var exists bool
	err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		return governance_model.WithPackageContentLocks(ctx, []string{pb.HashSHA256}, func(ctx context.Context) error {
			var err error
			result, exists, err = getOrInsertBlob(ctx, pb)
			return err
		})
	})
	return result, exists, err
}

// PrepareBlobUpload 在短事务内保留内容引用，避免上传期间被过期清理误删。
func PrepareBlobUpload(ctx context.Context, pb *PackageBlob) (*PackageBlob, bool, error) {
	var result *PackageBlob
	var exists bool
	err := db.WithTx(ctx, func(ctx context.Context) error {
		var err error
		result, exists, err = GetOrInsertBlob(ctx, pb)
		if err != nil {
			return err
		}
		_, err = db.GetEngine(ctx).Exec("UPDATE package_blob SET created_unix = ? WHERE id = ?", timeutil.TimeStampNow(), result.ID)
		return err
	})
	return result, exists, err
}

func getOrInsertBlob(ctx context.Context, pb *PackageBlob) (*PackageBlob, bool, error) {
	e := db.GetEngine(ctx)

	existing := &PackageBlob{}

	hashCond := builder.Eq{
		"size":        pb.Size,
		"hash_md5":    pb.HashMD5,
		"hash_sha1":   pb.HashSHA1,
		"hash_sha256": pb.HashSHA256,
		"hash_sha512": pb.HashSHA512,
	}

	has, err := e.Where(hashCond).Get(existing)
	if err != nil {
		return nil, false, err
	}
	if has {
		return existing, true, nil
	}
	if _, err = e.Insert(pb); err != nil {
		// Handle race condition: another request may have inserted the same blob
		// between our SELECT and INSERT. Retry the SELECT to get the existing blob.
		if has, _ = e.Where(hashCond).Get(existing); has {
			return existing, true, nil
		}
		return nil, false, err
	}
	return pb, false, nil
}

// GetBlobByID gets a blob by id
func GetBlobByID(ctx context.Context, blobID int64) (*PackageBlob, error) {
	pb := &PackageBlob{}

	has, err := db.GetEngine(ctx).ID(blobID).Get(pb)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrPackageBlobNotExist
	}
	return pb, nil
}

// ExistPackageBlobWithSHA returns if a package blob exists with the provided sha
func ExistPackageBlobWithSHA(ctx context.Context, blobSha256 string) (bool, error) {
	return db.GetEngine(ctx).Exist(&PackageBlob{
		HashSHA256: blobSha256,
	})
}

// FindExpiredUnreferencedBlobs gets all blobs without associated files older than the specific duration
func FindExpiredUnreferencedBlobs(ctx context.Context, olderThan time.Duration) ([]*PackageBlob, error) {
	// 上传正文先于引用提交；即时清理必须给仍在传输的内容保留宽限期。
	olderThan = max(olderThan, 24*time.Hour)
	pbs := make([]*PackageBlob, 0, 10)
	return pbs, db.GetEngine(ctx).
		Table("package_blob").
		Join("LEFT", "package_file", "package_file.blob_id = package_blob.id").
		Where("package_file.id IS NULL AND package_blob.created_unix < ?", time.Now().Add(-olderThan).Unix()).
		Find(&pbs)
}

// DeleteBlobByID deletes a blob by id
func DeleteBlobByID(ctx context.Context, blobID int64) error {
	_, err := db.GetEngine(ctx).ID(blobID).Delete(&PackageBlob{})
	return err
}

// GetTotalBlobSize returns the total blobs size in bytes
func GetTotalBlobSize(ctx context.Context) (int64, error) {
	return db.GetEngine(ctx).
		SumInt(&PackageBlob{}, "size")
}

// GetTotalUnreferencedBlobSize returns the total size of all unreferenced blobs in bytes
func GetTotalUnreferencedBlobSize(ctx context.Context) (int64, error) {
	return db.GetEngine(ctx).
		Table("package_blob").
		Join("LEFT", "package_file", "package_file.blob_id = package_blob.id").
		Where("package_file.id IS NULL").
		SumInt(&PackageBlob{}, "size")
}

// IsBlobAccessibleForUser tests if the user has access to the blob
func IsBlobAccessibleForUser(ctx context.Context, blobID int64, user *user_model.User) (bool, error) {
	if user == nil {
		return false, nil
	}
	var owners []struct{ OwnerID int64 }
	err := db.GetEngine(ctx).Table("package_blob").
		Select("DISTINCT package.owner_id AS owner_id").
		Join("INNER", "package_file", "package_file.blob_id = package_blob.id").
		Join("INNER", "package_version", "package_version.id = package_file.version_id").
		Join("INNER", "package", "package.id = package_version.package_id").
		Where("package_blob.id = ?", blobID).
		Find(&owners)
	if err != nil {
		return false, err
	}
	for _, owner := range owners {
		var mode perm.AccessMode
		if user.IsGiteaActions() {
			mode, err = TaskPackageAccessMode(ctx, owner.OwnerID, user)
		} else {
			mode, err = packageActorAccessMode(ctx, owner.OwnerID, user.ID)
		}
		if err != nil {
			return false, err
		}
		if mode >= perm.AccessModeRead {
			return true, nil
		}
	}
	return false, nil
}
