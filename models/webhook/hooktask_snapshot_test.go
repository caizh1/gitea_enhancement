// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhook

import (
	"encoding/hex"
	"strings"
	"testing"

	webhook_module "gitea.dev/modules/webhook"

	"github.com/stretchr/testify/require"
)

func TestHookTaskSnapshotIntegrity(t *testing.T) {
	hook := &Webhook{ID: 42, OwnerID: 7, URL: "https://example.com/hook", Secret: "old-secret", ConfigRevision: 3}
	task := &HookTask{HookID: hook.ID, PayloadContent: `{"ref":"main"}`, PayloadVersion: 2, EventType: webhook_module.HookEventPush}
	require.NoError(t, task.CaptureHook(hook, 5, 9, []int64{7, 6, 3}))
	require.NotContains(t, task.HookSnapshotEncrypted, hook.URL)
	snapshot, err := task.HookSnapshot()
	require.NoError(t, err)
	require.Equal(t, hook.URL, snapshot.URL)
	originalEvent := task.EventUUID
	require.NotEmpty(t, originalEvent)
	task.EventUUID = "替换事件标识"
	_, err = task.HookSnapshot()
	require.Error(t, err)
	task.EventUUID = originalEvent
	task.ScopeOwnerIDs = `[19]`
	_, err = task.HookSnapshot()
	require.Error(t, err)
	task.ScopeOwnerIDs = `[7,6,3]`
	task.PayloadContent = `{"ref":"changed"}`
	_, err = task.HookSnapshot()
	require.Error(t, err)
}

func TestHookTaskLegacySignedSnapshot(t *testing.T) {
	hook := &Webhook{ID: 42, OwnerID: 7, URL: "https://example.com/hook", ConfigRevision: 3}
	task := &HookTask{HookID: hook.ID, PayloadContent: `{}`, PayloadVersion: 2}
	require.NoError(t, task.CaptureHook(hook, 5, 9, []int64{7}))
	ciphertext := strings.Split(task.HookSnapshotEncrypted, ":")[1]
	signature, err := task.snapshotSignature(ciphertext, "v1")
	require.NoError(t, err)
	task.HookSnapshotEncrypted = "v1:" + ciphertext + ":" + hex.EncodeToString(signature)
	task.EventUUID = "旧投递编号"
	snapshot, err := task.HookSnapshot()
	require.NoError(t, err)
	require.Equal(t, hook.URL, snapshot.URL)
}
