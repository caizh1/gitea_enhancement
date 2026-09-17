// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"testing"

	"gitea.dev/models/perm"
	"gitea.dev/modules/git"

	"github.com/stretchr/testify/assert"
)

func TestGetAccessMode(t *testing.T) {
	cases := []struct {
		verb, lfsVerb string
		expected      perm.AccessMode
	}{
		{git.CmdVerbUploadPack, "", perm.AccessModeRead},
		{git.CmdVerbUploadArchive, "", perm.AccessModeRead},
		{git.CmdVerbReceivePack, "", perm.AccessModeWrite},
		{git.CmdVerbLfsAuthenticate, git.CmdSubVerbLfsUpload, perm.AccessModeWrite},
		{git.CmdVerbLfsAuthenticate, git.CmdSubVerbLfsDownload, perm.AccessModeRead},
		{git.CmdVerbLfsTransfer, git.CmdSubVerbLfsUpload, perm.AccessModeWrite},
		{git.CmdVerbLfsTransfer, git.CmdSubVerbLfsDownload, perm.AccessModeRead},
	}
	for _, tc := range cases {
		t.Run(tc.verb+"/"+tc.lfsVerb, func(t *testing.T) {
			mode, ok := getAccessMode(tc.verb, tc.lfsVerb)
			assert.True(t, ok)
			assert.Equal(t, tc.expected, mode)
		})
	}
}

func TestSSHRemoteAddress(t *testing.T) {
	for _, tc := range []struct{ connection, address string }{
		{"192.0.2.10 42500 192.0.2.20 22", "192.0.2.10:42500"},
		{"2001:db8::1 42500 2001:db8::2 22", "[2001:db8::1]:42500"},
		{"", ""},
		{"192.0.2.10 42500", ""},
		{"客户端自报主机 42500 192.0.2.20 22", ""},
		{"192.0.2.10 0 192.0.2.20 22", ""},
		{"192.0.2.10 65536 192.0.2.20 22", ""},
		{"192.0.2.10 invalid 192.0.2.20 22", ""},
	} {
		t.Run(tc.connection, func(t *testing.T) {
			assert.Equal(t, tc.address, sshRemoteAddress(tc.connection))
		})
	}
}

// TestGetAccessModeUnknownVerb locks in the invariant that getAccessMode reports
// ok=false for unrecognised verbs and LFS sub-verbs, so runServ rejects them. An
// unknown verb has no valid access mode; if it were treated as AccessModeNone (0)
// it would pass the `userMode < mode` permission check in routers/private/serv.go
// and hand out valid LFS JWTs for any private repository.
func TestGetAccessModeUnknownVerb(t *testing.T) {
	cases := []struct{ verb, lfsVerb string }{
		{git.CmdVerbLfsAuthenticate, ""},
		{git.CmdVerbLfsAuthenticate, "badverb"},
		{git.CmdVerbLfsTransfer, "badverb"},
		{"git-unknown-verb", ""},
	}
	for _, tc := range cases {
		t.Run(tc.verb+"/"+tc.lfsVerb, func(t *testing.T) {
			mode, ok := getAccessMode(tc.verb, tc.lfsVerb)
			assert.False(t, ok)
			assert.Equal(t, perm.AccessModeNone, mode)
		})
	}
}
