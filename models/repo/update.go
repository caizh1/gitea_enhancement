// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"context"
	"fmt"
	"slices"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/util"
)

// UpdateRepositoryOwnerNames updates repository owner_names (this should only be used when the ownerName has changed case)
func UpdateRepositoryOwnerNames(ctx context.Context, ownerID int64, ownerName string) error {
	if ownerID == 0 {
		return nil
	}

	if _, err := db.GetEngine(ctx).Where("owner_id = ?", ownerID).Cols("owner_name").NoAutoTime().Update(&Repository{
		OwnerName: ownerName,
	}); err != nil {
		return err
	}

	return nil
}

// UpdateRepositoryUpdatedTime updates a repository's updated time
func UpdateRepositoryUpdatedTime(ctx context.Context, repoID int64, updateTime time.Time) error {
	_, err := db.GetEngine(ctx).Exec("UPDATE repository SET updated_unix = ? WHERE id = ?", updateTime.Unix(), repoID)
	return err
}

// UpdateRepositoryColsWithAutoTime updates repository's columns and the timestamp fields automatically
func UpdateRepositoryColsWithAutoTime(ctx context.Context, repo *Repository, colName string, moreColNames ...string) error {
	return updateRepositoryCols(ctx, repo, append([]string{colName}, moreColNames...), false)
}

// UpdateRepositoryColsNoAutoTime updates repository's columns, doesn't change timestamp field automatically
func UpdateRepositoryColsNoAutoTime(ctx context.Context, repo *Repository, colName string, moreColNames ...string) error {
	return updateRepositoryCols(ctx, repo, append([]string{colName}, moreColNames...), true)
}

func updateRepositoryCols(ctx context.Context, repo *Repository, cols []string, noAutoTime bool) error {
	update := func(ctx context.Context) error {
		session := db.GetEngine(ctx).ID(repo.ID).Cols(cols...)
		if noAutoTime {
			session = session.NoAutoTime()
		}
		_, err := session.Update(repo)
		return err
	}
	pathChanged := slices.Contains(cols, "name") || slices.Contains(cols, "owner_id")
	governed := pathChanged
	for _, col := range []string{"is_private", "is_archived", "status", "is_mirror", "default_branch", "trust_model", "is_fork"} {
		governed = governed || slices.Contains(cols, col)
	}
	if !governed {
		return update(ctx)
	}
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		if !pathChanged {
			return update(ctx)
		}
		previous, err := GetRepositoryByID(ctx, repo.ID)
		if err != nil {
			return err
		}
		ownerID, name := previous.OwnerID, previous.Name
		if slices.Contains(cols, "name") {
			name = repo.Name
		}
		if slices.Contains(cols, "owner_id") {
			ownerID = repo.OwnerID
		}
		repo.OwnerNamespace, err = governance_model.ChangeNativeRepositoryPath(ctx, repo.ID, ownerID, name)
		if err != nil {
			return err
		}
		cols = append(cols, "owner_namespace")
		return update(ctx)
	})
}

// ErrReachLimitOfRepo represents a "ReachLimitOfRepo" kind of error.
type ErrReachLimitOfRepo struct {
	Limit int
}

// IsErrReachLimitOfRepo checks if an error is a ErrReachLimitOfRepo.
func IsErrReachLimitOfRepo(err error) bool {
	_, ok := err.(ErrReachLimitOfRepo)
	return ok
}

func (err ErrReachLimitOfRepo) Error() string {
	return fmt.Sprintf("user has reached maximum limit of repositories [limit: %d]", err.Limit)
}

func (err ErrReachLimitOfRepo) Unwrap() error {
	return util.ErrPermissionDenied
}

// ErrRepoAlreadyExist represents a "RepoAlreadyExist" kind of error.
type ErrRepoAlreadyExist struct {
	Uname string
	Name  string
}

// IsErrRepoAlreadyExist checks if an error is a ErrRepoAlreadyExist.
func IsErrRepoAlreadyExist(err error) bool {
	_, ok := err.(ErrRepoAlreadyExist)
	return ok
}

func (err ErrRepoAlreadyExist) Error() string {
	return fmt.Sprintf("repository already exists [uname: %s, name: %s]", err.Uname, err.Name)
}

func (err ErrRepoAlreadyExist) Unwrap() error {
	return util.ErrAlreadyExist
}

// ErrRepoFilesAlreadyExist represents a "RepoFilesAlreadyExist" kind of error.
type ErrRepoFilesAlreadyExist struct {
	Uname string
	Name  string
}

// IsErrRepoFilesAlreadyExist checks if an error is a ErrRepoAlreadyExist.
func IsErrRepoFilesAlreadyExist(err error) bool {
	_, ok := err.(ErrRepoFilesAlreadyExist)
	return ok
}

func (err ErrRepoFilesAlreadyExist) Error() string {
	return fmt.Sprintf("repository files already exist [uname: %s, name: %s]", err.Uname, err.Name)
}

func (err ErrRepoFilesAlreadyExist) Unwrap() error {
	return util.ErrAlreadyExist
}

// UpdateRepoSize updates the repository size, calculating it using getDirectorySize
func UpdateRepoSize(ctx context.Context, repoID, gitSize, lfsSize int64) error {
	_, err := db.GetEngine(ctx).ID(repoID).Cols("size", "git_size", "lfs_size").NoAutoTime().Update(&Repository{
		Size:    gitSize + lfsSize,
		GitSize: gitSize,
		LFSSize: lfsSize,
	})
	return err
}
