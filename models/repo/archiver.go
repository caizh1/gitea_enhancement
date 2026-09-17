// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// ArchiverStatus represents repo archive status
type ArchiverStatus int

// enumerate all repo archive statuses
const (
	ArchiverGenerating = iota // the archiver is generating
	ArchiverReady             // it's ready
)

// ArchiveType archive types
type ArchiveType int

const (
	ArchiveUnknown ArchiveType = iota
	ArchiveZip                 // 1
	ArchiveTarGz               // 2
	ArchiveBundle              // 3
)

// String converts an ArchiveType to string: the extension of the archive file without prefix dot
func (a ArchiveType) String() string {
	switch a {
	case ArchiveZip:
		return "zip"
	case ArchiveTarGz:
		return "tar.gz"
	case ArchiveBundle:
		return "bundle"
	}
	return "unknown"
}

func SplitArchiveNameType(s string) (string, ArchiveType) {
	switch {
	case strings.HasSuffix(s, ".zip"):
		return strings.TrimSuffix(s, ".zip"), ArchiveZip
	case strings.HasSuffix(s, ".tar.gz"):
		return strings.TrimSuffix(s, ".tar.gz"), ArchiveTarGz
	case strings.HasSuffix(s, ".bundle"):
		return strings.TrimSuffix(s, ".bundle"), ArchiveBundle
	}
	return s, ArchiveUnknown
}

// RepoArchiver represents all archivers
type RepoArchiver struct { //revive:disable-line:exported
	ID          int64       `xorm:"pk autoincr"`
	RepoID      int64       `xorm:"index unique(s)"`
	Type        ArchiveType `xorm:"unique(s)"`
	Status      ArchiverStatus
	CommitID    string             `xorm:"VARCHAR(64) unique(s)"`
	CreatedUnix timeutil.TimeStamp `xorm:"INDEX NOT NULL created"`
}

func init() {
	db.RegisterModel(new(RepoArchiver))
}

// RelativePath returns the archive path relative to the archive storage root.
func (archiver *RepoArchiver) RelativePath() string {
	return fmt.Sprintf("%d/%s/%s.%s", archiver.RepoID, archiver.CommitID[:2], archiver.CommitID, archiver.Type.String())
}

// repoArchiverForRelativePath takes a relativePath created from (archiver *RepoArchiver) RelativePath() and creates a shell repoArchiver struct representing it
func repoArchiverForRelativePath(relativePath string) (*RepoArchiver, error) {
	parts := strings.SplitN(relativePath, "/", 3)
	if len(parts) != 3 {
		return nil, util.NewInvalidArgumentErrorf("invalid storage path: must have 3 parts")
	}
	repoID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, util.NewInvalidArgumentErrorf("invalid storage path: invalid repo id")
	}
	commitID, archiveType := SplitArchiveNameType(parts[2])
	if archiveType == ArchiveUnknown {
		return nil, util.NewInvalidArgumentErrorf("invalid storage path: invalid archive type")
	}
	return &RepoArchiver{RepoID: repoID, CommitID: commitID, Type: archiveType}, nil
}

// GetRepoArchiver get an archiver
func GetRepoArchiver(ctx context.Context, repoID int64, tp ArchiveType, commitID string) (*RepoArchiver, error) {
	var archiver RepoArchiver
	has, err := db.GetEngine(ctx).Where("repo_id=?", repoID).And("`type`=?", tp).And("commit_id=?", commitID).Get(&archiver)
	if err != nil {
		return nil, err
	}
	if has {
		return &archiver, nil
	}
	return nil, nil //nolint:nilnil // return nil to indicate that the object does not exist
}

// ExistsRepoArchiverWithStoragePath checks if there is a RepoArchiver for a given storage path
func ExistsRepoArchiverWithStoragePath(ctx context.Context, storagePath string) (bool, error) {
	// We need to invert the path provided func (archiver *RepoArchiver) RelativePath() above
	archiver, err := repoArchiverForRelativePath(storagePath)
	if err != nil {
		return false, err
	}

	return db.GetEngine(ctx).Exist(archiver)
}

// UpdateRepoArchiverStatus updates archiver's status
func UpdateRepoArchiverStatus(ctx context.Context, archiver *RepoArchiver) error {
	_, err := db.GetEngine(ctx).ID(archiver.ID).Cols("status").Update(archiver)
	return err
}

// DeleteAllRepoArchives deletes all repo archives records
func DeleteAllRepoArchives(ctx context.Context) error {
	// 1=1 to enforce delete all data, otherwise it will delete nothing
	_, err := db.GetEngine(ctx).Where("1=1").Delete(new(RepoArchiver))
	return err
}

// FindRepoArchiversOption represents an archiver options
type FindRepoArchiversOption struct {
	db.ListOptions
	OlderThan time.Duration
}

func (opts FindRepoArchiversOption) ToConds() builder.Cond {
	cond := builder.NewCond()
	if opts.OlderThan > 0 {
		cond = cond.And(builder.Lt{"created_unix": time.Now().Add(-opts.OlderThan).Unix()})
	}
	return cond
}

func (opts FindRepoArchiversOption) ToOrders() string {
	return "created_unix ASC"
}

// SetArchiveRepoState sets if a repo is archived
func SetArchiveRepoState(ctx context.Context, repo *Repository, isArchived bool) error {
	var next *Repository
	err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		var err error
		next, err = GetRepositoryByID(ctx, repo.ID)
		if err != nil {
			return err
		}
		if !isArchived {
			pending, err := db.ExistByID[governance_model.RepositoryDeletion](ctx, repo.ID)
			if err != nil {
				return err
			}
			if pending {
				return governance_model.ErrConflict
			}
			chain, err := governance_model.Ancestors(ctx, next.OwnerID)
			if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
				return err
			}
			for _, ancestor := range chain {
				if ancestor.Archived || ancestor.DeleteAfter != 0 {
					return governance_model.ErrConflict
				}
			}
		}
		if next.IsArchived == isArchived {
			return nil
		}
		before := next.IsArchived
		next.IsArchived = isArchived
		if isArchived {
			next.ArchivedUnix = timeutil.TimeStampNow()
		} else {
			next.ArchivedUnix = 0
		}
		if err := UpdateRepositoryColsNoAutoTime(ctx, next, "is_archived", "archived_unix"); err != nil {
			return err
		}
		chain, err := governance_model.Ancestors(ctx, next.OwnerID)
		var ancestors []int64
		if errors.Is(err, governance_model.ErrNotFound) {
			if err = next.LoadOwner(ctx); err != nil {
				return err
			}
			if next.Owner.IsOrganization() {
				ancestors = append(ancestors, next.OwnerID)
			}
		} else if err != nil {
			return err
		} else {
			for _, group := range chain {
				if group.Kind == "group" {
					ancestors = append(ancestors, group.ID)
				}
			}
		}
		details, err := json.Marshal(map[string]any{"before": before, "after": isArchived})
		if err != nil {
			return err
		}
		kind := "repository.archived"
		if !isArchived {
			kind = "repository.unarchived"
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: kind, Actor: governance_model.AuditActor(ctx), ScopeType: "repository", ScopeID: next.ID, AncestorIDs: ancestors, ObjectType: "repository", ObjectID: next.ID, ObjectPath: next.FullPath(), Result: "success", Details: details})
	})
	if err != nil {
		return err
	}
	repo.IsArchived, repo.ArchivedUnix = next.IsArchived, next.ArchivedUnix
	return nil
}
