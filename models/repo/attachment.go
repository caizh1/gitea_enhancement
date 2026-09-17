// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// Attachment represent a attachment of issue/comment/release.
type Attachment struct {
	ID                int64  `xorm:"pk autoincr"`
	UUID              string `xorm:"uuid UNIQUE"`
	RepoID            int64  `xorm:"INDEX"`           // this should not be zero
	IssueID           int64  `xorm:"INDEX"`           // maybe zero when creating
	ReleaseID         int64  `xorm:"INDEX"`           // maybe zero when creating
	UploaderID        int64  `xorm:"INDEX DEFAULT 0"` // Notice: will be zero before this column added
	CommentID         int64  `xorm:"INDEX"`
	Name              string
	DownloadCount     int64              `xorm:"DEFAULT 0"`
	Size              int64              `xorm:"DEFAULT 0"`
	CreatedUnix       timeutil.TimeStamp `xorm:"created"`
	CustomDownloadURL string             `xorm:"-"`
}

func init() {
	db.RegisterModel(new(Attachment))
}

// IncreaseDownloadCount is update download count + 1
func (a *Attachment) IncreaseDownloadCount(ctx context.Context) error {
	// Update download count.
	if _, err := db.GetEngine(ctx).Exec("UPDATE `attachment` SET download_count=download_count+1 WHERE id=?", a.ID); err != nil {
		return fmt.Errorf("increase attachment count: %w", err)
	}

	return nil
}

// AttachmentRelativePath returns the relative path
func AttachmentRelativePath(uuid string) string {
	return path.Join(uuid[0:1], uuid[1:2], uuid)
}

// RelativePath returns the relative path of the attachment
func (a *Attachment) RelativePath() string {
	return AttachmentRelativePath(a.UUID)
}

// DownloadURL returns the download url of the attached file
func (a *Attachment) DownloadURL() string {
	if a.CustomDownloadURL != "" {
		return a.CustomDownloadURL
	}

	return setting.AppURL + "attachments/" + url.PathEscape(a.UUID)
}

// ErrAttachmentNotExist represents a "AttachmentNotExist" kind of error.
type ErrAttachmentNotExist struct {
	ID   int64
	UUID string
}

// IsErrAttachmentNotExist checks if an error is a ErrAttachmentNotExist.
func IsErrAttachmentNotExist(err error) bool {
	_, ok := err.(ErrAttachmentNotExist)
	return ok
}

func (err ErrAttachmentNotExist) Error() string {
	return fmt.Sprintf("attachment does not exist [id: %d, uuid: %s]", err.ID, err.UUID)
}

func (err ErrAttachmentNotExist) Unwrap() error {
	return util.ErrNotExist
}

// GetAttachmentByID returns attachment by given id
func GetAttachmentByID(ctx context.Context, id int64) (*Attachment, error) {
	attach := &Attachment{}
	if has, err := db.GetEngine(ctx).ID(id).Get(attach); err != nil {
		return nil, err
	} else if !has {
		return nil, ErrAttachmentNotExist{ID: id, UUID: ""}
	}
	return attach, nil
}

// GetAttachmentByUUID returns attachment by given UUID.
func GetAttachmentByUUID(ctx context.Context, uuid string) (*Attachment, error) {
	attach := &Attachment{}
	has, err := db.GetEngine(ctx).Where("uuid=?", uuid).Get(attach)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrAttachmentNotExist{0, uuid}
	}
	return attach, nil
}

// GetAttachmentsByUUIDs returns attachment by given UUID list.
func GetAttachmentsByUUIDs(ctx context.Context, uuids []string) ([]*Attachment, error) {
	if len(uuids) == 0 {
		return []*Attachment{}, nil
	}

	// Silently drop invalid uuids.
	attachments := make([]*Attachment, 0, len(uuids))
	return attachments, db.GetEngine(ctx).In("uuid", uuids).Find(&attachments)
}

// ExistAttachmentsByUUID returns true if attachment exists with the given UUID
func ExistAttachmentsByUUID(ctx context.Context, uuid string) (bool, error) {
	return db.GetEngine(ctx).Where("`uuid`=?", uuid).Exist(new(Attachment))
}

// GetAttachmentsByIssueID returns all attachments of an issue.
func GetAttachmentsByIssueID(ctx context.Context, issueID int64) ([]*Attachment, error) {
	attachments := make([]*Attachment, 0, 10)
	return attachments, db.GetEngine(ctx).Where("issue_id = ? AND comment_id = 0", issueID).Find(&attachments)
}

// GetAttachmentsByIssueIDImagesLatest returns the latest image attachments of an issue.
func GetAttachmentsByIssueIDImagesLatest(ctx context.Context, issueID int64) ([]*Attachment, error) {
	attachments := make([]*Attachment, 0, 5)
	return attachments, db.GetEngine(ctx).Where(`issue_id = ? AND (name like '%.apng'
		OR name like '%.avif'
		OR name like '%.bmp'
		OR name like '%.gif'
		OR name like '%.jpg'
		OR name like '%.jpeg'
		OR name like '%.jxl'
		OR name like '%.png'
		OR name like '%.svg'
		OR name like '%.webp')`, issueID).Desc("comment_id").Limit(5).Find(&attachments)
}

// GetAttachmentsByCommentID returns all attachments if comment by given ID.
func GetAttachmentsByCommentID(ctx context.Context, commentID int64) ([]*Attachment, error) {
	attachments := make([]*Attachment, 0, 10)
	return attachments, db.GetEngine(ctx).Where("comment_id=?", commentID).Find(&attachments)
}

// GetAttachmentByReleaseIDFileName returns attachment by given releaseId and fileName.
func GetAttachmentByReleaseIDFileName(ctx context.Context, releaseID int64, fileName string) (*Attachment, error) {
	attach, has, err := db.Get[Attachment](ctx, builder.Eq{"release_id": releaseID, "`name`": fileName})
	if err != nil {
		return nil, err
	} else if !has {
		return nil, err
	}
	return attach, nil
}

func GetUnlinkedAttachmentsByUserID(ctx context.Context, userID int64) ([]*Attachment, error) {
	attachments := make([]*Attachment, 0, 10)
	return attachments, db.GetEngine(ctx).Where("uploader_id = ? AND issue_id = 0 AND release_id = 0 AND comment_id = 0", userID).Find(&attachments)
}

// DeleteAttachment deletes the given attachment and optionally the associated file.
func DeleteAttachment(ctx context.Context, a *Attachment, remove bool) error {
	_, err := DeleteAttachments(ctx, []*Attachment{a}, remove)
	return err
}

// DeleteAttachments deletes the given attachments and optionally the associated files.
func DeleteAttachments(ctx context.Context, attachments []*Attachment, remove bool) (int, error) {
	if len(attachments) == 0 {
		return 0, nil
	}

	ids := make([]int64, 0, len(attachments))
	resources := make([]string, 0, len(attachments))
	for _, a := range attachments {
		ids = append(ids, a.ID)
		if a.RepoID > 0 {
			resources = append(resources, governance_model.Resource("repository", a.RepoID))
		}
	}
	slices.Sort(resources)
	resources = slices.Compact(resources)

	var deleted []*Attachment
	var cnt int64
	err := governance_model.WithWrite(ctx, resources, func(ctx context.Context) error {
		return db.WithTx(ctx, func(ctx context.Context) error {
			if err := db.GetEngine(ctx).In("id", ids).Find(&deleted); err != nil {
				return err
			}
			var err error
			cnt, err = db.GetEngine(ctx).In("id", ids).NoAutoCondition().Delete(new(Attachment))
			if err != nil {
				return err
			}
			for _, a := range deleted {
				var cleanupScope *Repository
				var cleanupAncestors []int64
				if a.RepoID > 0 {
					cleanupScope, cleanupAncestors, err = ContentAuditRepositoryScope(ctx, a.RepoID)
					if err != nil {
						return err
					}
					if err := AppendContentAudit(ctx, "attachment.deleted", a.RepoID, "attachment", a.ID, "/attachments/"+a.Name, map[string]any{"before": map[string]any{"filename": a.Name, "size": a.Size}}); err != nil {
						return err
					}
				}
				if remove {
					cleanup := &governance_model.ResourceCleanup{Kind: "attachment", ResourceID: a.ID, Actor: governance_model.AuditActor(ctx), ObjectPath: a.Name, Objects: []governance_model.CleanupObject{{Kind: "attachment", Path: a.RelativePath()}}}
					if cleanupScope != nil {
						cleanup.ScopeType, cleanup.ScopeID = "repository", cleanupScope.ID
						cleanup.ObjectPath, cleanup.AncestorIDs = cleanupScope.FullPath()+"/attachments/"+a.Name, cleanupAncestors
					}
					if err := db.Insert(ctx, cleanup); err != nil {
						return err
					}
				}
			}
			return nil
		})
	})
	if err != nil {
		return 0, err
	}

	return int(cnt), nil
}

// DeleteAttachmentsByIssue deletes all attachments associated with the given issue.
func DeleteAttachmentsByIssue(ctx context.Context, issueID int64, remove bool) (int, error) {
	attachments, err := GetAttachmentsByIssueID(ctx, issueID)
	if err != nil {
		return 0, err
	}

	return DeleteAttachments(ctx, attachments, remove)
}

// DeleteAttachmentsByComment deletes all attachments associated with the given comment.
func DeleteAttachmentsByComment(ctx context.Context, commentID int64, remove bool) (int, error) {
	attachments, err := GetAttachmentsByCommentID(ctx, commentID)
	if err != nil {
		return 0, err
	}

	return DeleteAttachments(ctx, attachments, remove)
}

// UpdateAttachmentByUUID Updates attachment via uuid
func UpdateAttachmentByUUID(ctx context.Context, attach *Attachment, cols ...string) error {
	if attach.UUID == "" {
		return errors.New("attachment uuid should be not blank")
	}
	current, err := GetAttachmentByUUID(ctx, attach.UUID)
	if err != nil {
		return err
	}
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", current.RepoID)}, func(ctx context.Context) error {
		return db.WithTx(ctx, func(ctx context.Context) error {
			current, err = GetAttachmentByUUID(ctx, attach.UUID)
			if err != nil {
				return err
			}
			if _, err := db.GetEngine(ctx).Where("uuid=?", attach.UUID).Cols(cols...).Update(attach); err != nil {
				return err
			}
			if slices.Contains(cols, "name") && current.Name != attach.Name {
				return AppendContentAudit(ctx, "attachment.updated", current.RepoID, "attachment", current.ID, "/attachments/"+attach.Name, map[string]any{"before": map[string]any{"filename": current.Name, "size": current.Size}, "after": map[string]any{"filename": attach.Name, "size": current.Size}})
			}
			return nil
		})
	})
}

// UpdateAttachment updates the given attachment in database
func UpdateAttachment(ctx context.Context, atta *Attachment) error {
	var current *Attachment
	var err error
	if atta.ID != 0 {
		current, err = GetAttachmentByID(ctx, atta.ID)
	} else {
		current, err = GetAttachmentByUUID(ctx, atta.UUID)
	}
	if err != nil {
		return err
	}
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", current.RepoID)}, func(ctx context.Context) error {
		if atta.ID != 0 {
			current, err = GetAttachmentByID(ctx, atta.ID)
		} else {
			current, err = GetAttachmentByUUID(ctx, atta.UUID)
		}
		if err != nil {
			return err
		}
		sess := db.GetEngine(ctx).Cols("name", "issue_id", "release_id", "comment_id", "download_count")
		if atta.ID != 0 && atta.UUID == "" {
			sess = sess.ID(atta.ID)
		} else {
			// Use uuid only if id is not set and uuid is set
			sess = sess.Where("uuid = ?", atta.UUID)
		}
		if _, err := sess.Update(atta); err != nil {
			return err
		}
		if current.Name != atta.Name {
			return AppendContentAudit(ctx, "attachment.updated", current.RepoID, "attachment", current.ID, "/attachments/"+atta.Name, map[string]any{"before": map[string]any{"filename": current.Name, "size": current.Size}, "after": map[string]any{"filename": atta.Name, "size": current.Size}})
		}
		return nil
	})
}

// DeleteAttachmentsByRelease deletes all attachments associated with the given release.
func DeleteAttachmentsByRelease(ctx context.Context, releaseID int64) error {
	attachments := make([]*Attachment, 0)
	if err := db.GetEngine(ctx).Where("release_id = ?", releaseID).Find(&attachments); err != nil {
		return err
	}
	_, err := DeleteAttachments(ctx, attachments, true)
	return err
}

// CountOrphanedAttachments returns the number of bad attachments
func CountOrphanedAttachments(ctx context.Context) (int64, error) {
	return db.GetEngine(ctx).Where("(issue_id > 0 and issue_id not in (select id from issue)) or (release_id > 0 and release_id not in (select id from `release`))").
		Count(new(Attachment))
}

// DeleteOrphanedAttachments delete all bad attachments
func DeleteOrphanedAttachments(ctx context.Context) error {
	_, err := db.GetEngine(ctx).Where("(issue_id > 0 and issue_id not in (select id from issue)) or (release_id > 0 and release_id not in (select id from `release`))").
		Delete(new(Attachment))
	return err
}
