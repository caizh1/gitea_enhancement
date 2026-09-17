// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package mirror

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/require"
)

func runGit(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
}

func TestPullMirrorFetchPassesTrustedReferenceIdentity(t *testing.T) {
	root := t.TempDir()
	remote, work, local := filepath.Join(root, "remote.git"), filepath.Join(root, "work"), filepath.Join(root, "local.git")
	require.NoError(t, os.MkdirAll(work, 0o755))
	runGit(t, root, nil, "init", "--bare", remote)
	runGit(t, work, nil, "init")
	runGit(t, work, nil, "config", "user.name", "镜像测试")
	runGit(t, work, nil, "config", "user.email", "mirror@example.invalid")
	require.NoError(t, os.WriteFile(filepath.Join(work, "README"), []byte("first"), 0o644))
	runGit(t, work, nil, "add", "README")
	runGit(t, work, nil, "commit", "-m", "first")
	runGit(t, work, nil, "remote", "add", "origin", remote)
	runGit(t, work, nil, "push", "origin", "HEAD:main")
	runGit(t, root, nil, "init", "--bare", local)
	runGit(t, local, nil, "remote", "add", "--mirror=fetch", "origin", remote)
	runGit(t, local, mirrorReferenceEnvironment(&repo_model.Repository{ID: 42}, false), "fetch", "origin")

	capture := filepath.Join(root, "hook-env")
	hook := "#!/bin/sh\nprintf '%s|%s|%s|%s' \"$GITEA_REPO_ID\" \"$GITEA_INTERNAL_PUSH\" \"$GITEA_REFERENCE_ACTOR\" \"$GITEA_PUSHER_TRANSPORT\" > " + capture + "\ncat >/dev/null\n"
	require.NoError(t, os.WriteFile(filepath.Join(local, "hooks", "reference-transaction"), []byte(hook), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(work, "README"), []byte("second"), 0o644))
	runGit(t, work, nil, "add", "README")
	runGit(t, work, nil, "commit", "-m", "second")
	runGit(t, work, nil, "push", "origin", "HEAD:main")
	runGit(t, local, mirrorReferenceEnvironment(&repo_model.Repository{ID: 42}, false), "fetch", "origin")
	value, err := os.ReadFile(capture)
	require.NoError(t, err)
	require.Equal(t, "42|true|mirror|mirror", strings.TrimSpace(string(value)))
}

func TestPullMirrorRecoveryRejectsPartialWikiUpdate(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test.MockVariableValue(&setting.SecretKey, "镜像恢复测试密钥")()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.NoError(t, repo.LoadOwner(t.Context()))
	remoteName := "origin"
	oldMain, newMain := "https://old.example/repo.git", "https://new.example/repo.git"
	oldWiki, newWiki := "https://old.example/repo.wiki.git", "https://new.example/repo.wiki.git"
	_ = gitrepo.GitRemoteRemove(t.Context(), repo, remoteName)
	require.NoError(t, gitrepo.GitRemoteAdd(t.Context(), repo, remoteName, oldMain, gitrepo.RemoteOptionMirrorFetch))
	_ = gitrepo.DeleteRepository(t.Context(), repo.WikiStorageRepo())
	require.NoError(t, gitrepo.InitRepository(t.Context(), repo.WikiStorageRepo(), repo.ObjectFormatName))
	require.NoError(t, gitrepo.GitRemoteAdd(t.Context(), repo.WikiStorageRepo(), remoteName, oldWiki, gitrepo.RemoteOptionMirrorFetch))
	mirror := &repo_model.Mirror{ID: 999, RepoID: repo.ID, Repo: repo, RemoteAddress: oldMain}
	require.NoError(t, db.Insert(t.Context(), mirror))
	operation, err := beginPullMirrorOperation(t.Context(), mirror, oldMain, newMain, oldWiki, newWiki, repo.OriginalURL, "https://new.example/repo.git", true)
	require.NoError(t, err)
	require.NoError(t, gitrepo.GitRemoteRemove(t.Context(), repo, remoteName))
	require.NoError(t, gitrepo.GitRemoteAdd(t.Context(), repo, remoteName, newMain, gitrepo.RemoteOptionMirrorFetch))
	_, err = db.GetEngine(t.Context()).ID(operation.ID).Cols("next_attempt_unix").Update(&governance_model.MirrorOperation{NextAttemptUnix: time.Now().Add(-time.Minute).Unix()})
	require.NoError(t, err)
	require.NoError(t, RunMirrorOperationRecovery(t.Context()))
	fresh := unittest.AssertExistsAndLoadBean(t, &governance_model.MirrorOperation{ID: operation.ID})
	require.Equal(t, "unknown", fresh.State)
	require.Equal(t, "remote_value_mismatch", fresh.LastReasonCode)
}

func TestPushMirrorRecoveryRequiresMainAndWikiRemote(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test.MockVariableValue(&setting.SecretKey, "推送镜像恢复测试密钥")()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.NoError(t, repo.LoadOwner(t.Context()))
	_ = gitrepo.DeleteRepository(t.Context(), repo.WikiStorageRepo())
	require.NoError(t, gitrepo.InitRepository(t.Context(), repo.WikiStorageRepo(), repo.ObjectFormatName))
	remoteName := "push-recovery"
	mainURL, wikiURL := "https://target.example/repo.git", "https://target.example/repo.wiki.git"
	mirror := &repo_model.PushMirror{RepoID: repo.ID, Repo: repo, RemoteName: remoteName, RemoteAddress: mainURL}
	operation, err := beginPushMirrorOperation(t.Context(), mirror, "create", "", mainURL, "", wikiURL, true)
	require.NoError(t, err)
	require.NoError(t, db.Insert(t.Context(), mirror))
	operation.MirrorID = mirror.ID
	_, err = db.GetEngine(t.Context()).ID(operation.ID).Cols("mirror_id", "next_attempt_unix").Update(&governance_model.MirrorOperation{MirrorID: mirror.ID, NextAttemptUnix: time.Now().Add(-time.Minute).Unix()})
	require.NoError(t, err)
	require.NoError(t, gitrepo.GitRemoteAdd(t.Context(), repo, remoteName, mainURL, gitrepo.RemoteOptionMirrorPush))
	require.NoError(t, RunMirrorOperationRecovery(t.Context()))
	fresh := unittest.AssertExistsAndLoadBean(t, &governance_model.MirrorOperation{ID: operation.ID})
	require.Equal(t, "unknown", fresh.State)

	// 只有 URL 的半成品配置不能被恢复任务误判成功；补齐主仓和 Wiki 的限定 refspec 后才可完成。
	require.NoError(t, gitrepo.GitRemoteRemove(t.Context(), repo, remoteName))
	require.NoError(t, addPushMirrorRemoteToRepository(t.Context(), repo, remoteName, mainURL))
	require.NoError(t, addPushMirrorRemoteToRepository(t.Context(), repo.WikiStorageRepo(), remoteName, wikiURL))
	mainConfigOK, err := pushMirrorRemoteConfigurationMatches(t.Context(), repo, remoteName)
	require.NoError(t, err)
	require.True(t, mainConfigOK)
	wikiConfigOK, err := pushMirrorRemoteConfigurationMatches(t.Context(), repo.WikiStorageRepo(), remoteName)
	require.NoError(t, err)
	require.True(t, wikiConfigOK)
	mainActual, mainExists, err := mirrorRemoteValue(t.Context(), repo, remoteName)
	require.NoError(t, err)
	wikiActual, wikiExists, err := mirrorRemoteValue(t.Context(), repo.WikiStorageRepo(), remoteName)
	require.NoError(t, err)
	require.True(t, mainExists)
	require.True(t, wikiExists)
	require.Equal(t, mainURL, mainActual)
	require.Equal(t, wikiURL, wikiActual)
	state, err := RecoverMirrorOperation(t.Context(), operation.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", state)
}

func TestFinishFailedMirrorOperationReleasesUnknownOperation(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test.MockVariableValue(&setting.SecretKey, "镜像失败恢复测试密钥")()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.NoError(t, repo.LoadOwner(t.Context()))
	mirror := &repo_model.PushMirror{RepoID: repo.ID, Repo: repo, RemoteName: "unknown-to-failed", RemoteAddress: "https://target.example/repo.git"}
	operation, err := beginPushMirrorOperation(t.Context(), mirror, "create", "", mirror.RemoteAddress, "", "", false)
	require.NoError(t, err)
	_, err = db.GetEngine(t.Context()).ID(operation.ID).Cols("state").Update(&governance_model.MirrorOperation{State: "unknown"})
	require.NoError(t, err)
	require.NoError(t, finishFailedMirrorOperation(t.Context(), operation, true))
	fresh := unittest.AssertExistsAndLoadBean(t, &governance_model.MirrorOperation{ID: operation.ID})
	require.Equal(t, "failed", fresh.State)
	require.Equal(t, "operation_rolled_back", fresh.LastReasonCode)
}

func TestRestorePushMirrorRemotesKeepsRestrictedRefspecs(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.NoError(t, repo.LoadOwner(t.Context()))
	_ = gitrepo.DeleteRepository(t.Context(), repo.WikiStorageRepo())
	require.NoError(t, gitrepo.InitRepository(t.Context(), repo.WikiStorageRepo(), repo.ObjectFormatName))
	mirror := &repo_model.PushMirror{RepoID: repo.ID, Repo: repo, RemoteName: "restore-restricted"}
	require.NoError(t, restorePushMirrorRemotes(t.Context(), mirror, "https://target.example/repo.git", "https://target.example/repo.wiki.git", true))
	mainOK, err := pushMirrorRemoteConfigurationMatches(t.Context(), repo, mirror.RemoteName)
	require.NoError(t, err)
	require.True(t, mainOK)
	wikiOK, err := pushMirrorRemoteConfigurationMatches(t.Context(), repo.WikiStorageRepo(), mirror.RemoteName)
	require.NoError(t, err)
	require.True(t, wikiOK)
}
