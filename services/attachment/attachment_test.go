// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package attachment

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/storage"

	_ "gitea.dev/models/actions"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type attachmentReadFunc func([]byte) (int, error)

func (f attachmentReadFunc) Read(p []byte) (int, error) { return f(p) }

func TestReleaseAttachmentFinalAuthorizationAfterArchive(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	payload := bytes.NewReader([]byte("release file"))
	var archiveErr error
	archived := false
	reader := attachmentReadFunc(func(p []byte) (int, error) {
		if !archived {
			archived = true
			archiveErr = repo_model.SetArchiveRepoState(t.Context(), repo, true)
		}
		if archiveErr != nil {
			return 0, archiveErr
		}
		return payload.Read(p)
	})
	attach, err := newAttachment(t.Context(), &repo_model.Attachment{RepoID: repo.ID, UploaderID: doer.ID, Name: "artifact.txt"}, reader, -1, doer)
	require.NoError(t, archiveErr)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	require.NotNil(t, attach)
	_, err = repo_model.GetAttachmentByUUID(t.Context(), attach.UUID)
	require.True(t, repo_model.IsErrAttachmentNotExist(err))
	_, err = storage.Attachments.Stat(attach.RelativePath())
	require.ErrorIs(t, err, os.ErrNotExist, "已保存但未获最终授权的附件对象应清理")
}

func TestReleaseAttachmentFinalAuthorizationAfterDeactivation(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	payload := bytes.NewReader([]byte("release file"))
	var deactivateErr error
	deactivated := false
	reader := attachmentReadFunc(func(p []byte) (int, error) {
		if !deactivated {
			deactivated = true
			_, deactivateErr = db.GetEngine(t.Context()).ID(doer.ID).Cols("is_active").Update(&user_model.User{IsActive: false})
		}
		if deactivateErr != nil {
			return 0, deactivateErr
		}
		return payload.Read(p)
	})
	attach, err := newAttachment(t.Context(), &repo_model.Attachment{RepoID: 1, UploaderID: doer.ID, Name: "artifact.txt"}, reader, -1, doer)
	require.NoError(t, deactivateErr)
	require.ErrorIs(t, err, governance_model.ErrForbidden)
	require.NotNil(t, attach)
	_, err = repo_model.GetAttachmentByUUID(t.Context(), attach.UUID)
	require.True(t, repo_model.IsErrAttachmentNotExist(err))
}

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

func TestUploadAttachment(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	assert.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})

	fPath := "./attachment_test.go"
	f, err := os.Open(fPath)
	assert.NoError(t, err)
	defer f.Close()

	attach, err := NewAttachment(t.Context(), &repo_model.Attachment{
		RepoID:     1,
		UploaderID: user.ID,
		Name:       filepath.Base(fPath),
	}, f, -1)
	assert.NoError(t, err)

	attachment, err := repo_model.GetAttachmentByUUID(t.Context(), attach.UUID)
	assert.NoError(t, err)
	assert.Equal(t, user.ID, attachment.UploaderID)
	assert.Equal(t, int64(0), attachment.DownloadCount)
	attachment.Name = "重命名后的公开附件名.txt"
	require.NoError(t, UpdateAttachment(t.Context(), "*/*", attachment))
	require.NoError(t, repo_model.DeleteAttachment(t.Context(), attachment, true))
	var events []governance_model.AuditEvent
	require.NoError(t, db.GetEngine(t.Context()).In("type", []string{"attachment.created", "attachment.updated", "attachment.deleted"}).Where("object_id = ?", attachment.ID).OrderBy("id").Find(&events))
	require.Len(t, events, 3)
	require.Contains(t, string(events[0].Details), filepath.Base(fPath))
	require.Contains(t, string(events[1].Details), "重命名后的公开附件名.txt")
}
