// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package attachment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/storage"
	"gitea.dev/modules/util"
	"gitea.dev/services/context/upload"
	governance_service "gitea.dev/services/governance"

	"github.com/google/uuid"
)

// NewAttachment creates a new attachment object, but do not verify.
func NewAttachment(ctx context.Context, attach *repo_model.Attachment, file io.Reader, size int64) (*repo_model.Attachment, error) {
	return newAttachment(ctx, attach, file, size, nil)
}

func newAttachment(ctx context.Context, attach *repo_model.Attachment, file io.Reader, size int64, doer *user_model.User) (*repo_model.Attachment, error) {
	if attach.RepoID == 0 {
		return nil, fmt.Errorf("attachment %s should belong to a repository", attach.Name)
	}

	attach.UUID = uuid.New().String()
	savedSize, err := storage.Attachments.Save(attach.RelativePath(), file, size)
	if err != nil {
		return nil, fmt.Errorf("Attachments.Save: %w", err)
	}
	attach.Size = savedSize
	err = governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", attach.RepoID)}, func(ctx context.Context) error {
		return db.WithTx(ctx, func(ctx context.Context) error {
			if doer != nil {
				if err := governance_service.CheckRepositoryContentWrite(ctx, doer, attach.RepoID, unit.TypeReleases); err != nil {
					return err
				}
			}
			if err := db.Insert(ctx, attach); err != nil {
				return err
			}
			return repo_model.AppendContentAudit(ctx, "attachment.created", attach.RepoID, "attachment", attach.ID, "/attachments/"+attach.Name, map[string]any{"after": map[string]any{"filename": attach.Name, "size": attach.Size}})
		})
	})
	if err != nil {
		_ = storage.Attachments.Delete(attach.RelativePath())
	}

	return attach, err
}

type UploaderFile struct {
	rd         io.ReadCloser
	size       int64
	respWriter http.ResponseWriter
}

func NewLimitedUploaderKnownSize(r io.Reader, size int64) *UploaderFile {
	return &UploaderFile{rd: io.NopCloser(r), size: size}
}

func NewLimitedUploaderMaxBytesReader(r io.ReadCloser, w http.ResponseWriter) *UploaderFile {
	return &UploaderFile{rd: r, size: -1, respWriter: w}
}

type UploadAttachmentFunc func(ctx context.Context, file *UploaderFile, attach *repo_model.Attachment) (*repo_model.Attachment, error)

func UploadAttachmentForIssue(ctx context.Context, file *UploaderFile, attach *repo_model.Attachment) (*repo_model.Attachment, error) {
	return uploadAttachment(ctx, file, setting.Attachment.AllowedTypes, setting.Attachment.MaxSize<<20, attach, nil)
}

func UploadAttachmentForRelease(ctx context.Context, file *UploaderFile, attach *repo_model.Attachment, doer *user_model.User) (*repo_model.Attachment, error) {
	// FIXME: although the release attachment has different settings from the issue attachment,
	// it still uses the same attachment table, the same storage and the same upload logic
	// So if the "issue attachment [attachment]" is not enabled, it will also affect the release attachment, which is not expected.
	if doer == nil || doer.ID != attach.UploaderID {
		return nil, governance_model.ErrForbidden
	}
	return uploadAttachment(ctx, file, setting.Repository.Release.AllowedTypes, setting.Repository.Release.FileMaxSize<<20, attach, doer)
}

func uploadAttachment(ctx context.Context, file *UploaderFile, allowedTypes string, maxFileSize int64, attach *repo_model.Attachment, doer *user_model.User) (*repo_model.Attachment, error) {
	src := file.rd
	if file.size < 0 {
		src = http.MaxBytesReader(file.respWriter, src, maxFileSize)
	}
	buf := make([]byte, 1024)
	n, _ := util.ReadAtMost(src, buf)
	buf = buf[:n]

	if err := upload.Verify(buf, attach.Name, allowedTypes); err != nil {
		return nil, err
	}

	if maxFileSize >= 0 && file.size > maxFileSize {
		return nil, util.ErrorWrap(util.ErrContentTooLarge, "attachment exceeds limit %d", maxFileSize)
	}

	attach, err := newAttachment(ctx, attach, io.MultiReader(bytes.NewReader(buf), src), file.size, doer)
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return nil, util.ErrorWrap(util.ErrContentTooLarge, "attachment exceeds limit %d", maxFileSize)
	}
	return attach, err
}

// UpdateAttachment updates an attachment, verifying that its name is among the allowed types.
func UpdateAttachment(ctx context.Context, allowedTypes string, attach *repo_model.Attachment) error {
	if err := upload.Verify(nil, attach.Name, allowedTypes); err != nil {
		return err
	}

	return repo_model.UpdateAttachment(ctx, attach)
}

func UpdateReleaseAttachment(ctx context.Context, doer *user_model.User, repoID int64, allowedTypes string, attach *repo_model.Attachment) error {
	if err := upload.Verify(nil, attach.Name, allowedTypes); err != nil {
		return err
	}
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repoID)}, func(ctx context.Context) error {
		if err := governance_service.CheckRepositoryContentWrite(ctx, doer, repoID, unit.TypeReleases); err != nil {
			return err
		}
		current, err := repo_model.GetAttachmentByID(ctx, attach.ID)
		if err != nil {
			return err
		}
		if current.ReleaseID != attach.ReleaseID || current.RepoID != 0 && current.RepoID != repoID {
			return governance_model.ErrForbidden
		}
		return repo_model.UpdateAttachment(ctx, attach)
	})
}

func DeleteReleaseAttachment(ctx context.Context, doer *user_model.User, repoID int64, attach *repo_model.Attachment) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repoID)}, func(ctx context.Context) error {
		if err := governance_service.CheckRepositoryContentWrite(ctx, doer, repoID, unit.TypeReleases); err != nil {
			return err
		}
		current, err := repo_model.GetAttachmentByID(ctx, attach.ID)
		if err != nil {
			return err
		}
		if current.ReleaseID != attach.ReleaseID || current.RepoID != 0 && current.RepoID != repoID {
			return governance_model.ErrForbidden
		}
		return repo_model.DeleteAttachment(ctx, current, true)
	})
}
