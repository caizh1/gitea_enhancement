// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package attachment

import (
	"os"
	"path/filepath"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	_ "gitea.dev/models/actions"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
