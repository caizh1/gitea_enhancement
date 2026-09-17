// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/git"
	"gitea.dev/modules/git/gitcmd"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/lfs"
	"gitea.dev/modules/log"
	"gitea.dev/modules/process"
	"gitea.dev/modules/proxy"
	"gitea.dev/modules/repository"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"
	"gitea.dev/services/migrations"
	repo_service "gitea.dev/services/repository"
)

var stripExitStatus = regexp.MustCompile(`exit status \d+ - `)

// CreatePushMirror 以持久操作记录串联数据库、Git remote 与成功审计。
func CreatePushMirror(ctx context.Context, mirror *repo_model.PushMirror, addr string) (retErr error) {
	repo := mirror.GetRepository(ctx)
	hasWiki := repo_service.HasWiki(ctx, repo)
	wikiAddr := ""
	if hasWiki {
		wikiAddr = repository.WikiRemoteURL(ctx, addr)
	}
	operation, err := beginPushMirrorOperation(ctx, mirror, "create", "", addr, "", wikiAddr, hasWiki)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			compensateMirrorOperation(ctx, operation, func(tx context.Context) bool {
				removeErr := RemovePushMirrorRemote(tx, mirror)
				_, deleteErr := db.GetEngine(tx).ID(mirror.ID).Delete(new(repo_model.PushMirror))
				return (removeErr == nil || git.IsRemoteNotExistError(removeErr)) && deleteErr == nil
			})
		}
	}()
	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", mirror.RepoID)}, func(ctx context.Context) error {
		if err := db.Insert(ctx, mirror); err != nil {
			return err
		}
		operation.MirrorID = mirror.ID
		_, err := db.GetEngine(ctx).ID(operation.ID).Cols("mirror_id").Update(operation)
		return err
	}); err != nil {
		return err
	}
	if err := AddPushMirrorRemote(ctx, mirror, addr); err != nil {
		return err
	}
	if err := completePushMirrorOperation(ctx, operation, mirror, "repository.mirror_configured", map[string]any{"action": "created", "remote_name": mirror.RemoteName, "remote": safeMirrorURL(mirror.RemoteAddress), "interval_seconds": mirror.Interval.Seconds(), "sync_on_commit": mirror.SyncOnCommit}); err != nil {
		return err
	}
	return nil
}

// DeletePushMirror 删除Git remote失败或数据库事务失败时保留恢复记录。
func DeletePushMirror(ctx context.Context, mirror *repo_model.PushMirror) (retErr error) {
	remote, err := gitrepo.GitRemoteGetURL(ctx, mirror.GetRepository(ctx), mirror.RemoteName)
	if err != nil {
		return err
	}
	hasWiki := repo_service.HasWiki(ctx, mirror.Repo)
	wikiRemote := ""
	if hasWiki {
		value, wikiErr := gitrepo.GitRemoteGetURL(ctx, mirror.Repo.WikiStorageRepo(), mirror.RemoteName)
		if wikiErr != nil {
			return wikiErr
		}
		wikiRemote = value.String()
	}
	operation, err := beginPushMirrorOperation(ctx, mirror, "delete", remote.String(), "", wikiRemote, "", hasWiki)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			compensateMirrorOperation(ctx, operation, func(tx context.Context) bool {
				return restorePushMirrorRemotes(tx, mirror, remote.String(), wikiRemote, hasWiki) == nil
			})
		}
	}()
	if err := RemovePushMirrorRemote(ctx, mirror); err != nil {
		return err
	}
	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", mirror.RepoID)}, func(ctx context.Context) error {
		fresh, has, err := repo_model.GetPushMirrorByIDAndRepoID(ctx, mirror.ID, mirror.RepoID)
		if err != nil || !has || fresh.RemoteName != mirror.RemoteName {
			return governance_model.ErrConflict
		}
		if err := repo_model.DeletePushMirrors(ctx, repo_model.PushMirrorOptions{ID: mirror.ID, RepoID: mirror.RepoID}); err != nil {
			return err
		}
		return completePushMirrorOperation(ctx, operation, mirror, "repository.mirror_updated", map[string]any{"action": "deleted", "remote_name": mirror.RemoteName})
	}); err != nil {
		return err
	}
	return nil
}

// UpdatePushMirrorInterval 保存真实旧值并在同一事务记录配置变更。
func UpdatePushMirrorInterval(ctx context.Context, mirror *repo_model.PushMirror) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", mirror.RepoID)}, func(ctx context.Context) error {
		fresh, has, err := repo_model.GetPushMirrorByIDAndRepoID(ctx, mirror.ID, mirror.RepoID)
		if err != nil || !has {
			return governance_model.ErrNotFound
		}
		before := fresh.Interval
		fresh.Interval = mirror.Interval
		if err := repo_model.UpdatePushMirrorInterval(ctx, fresh); err != nil {
			return err
		}
		fresh.Repo = mirror.GetRepository(ctx)
		return repo_service.AppendRepositoryAudit(ctx, fresh.Repo, fresh.Repo, "repository.mirror_updated", map[string]any{"direction": "push", "mirror_id": fresh.ID, "remote_name": fresh.RemoteName, "changed_fields": []string{"interval_seconds"}, "before_interval_seconds": before.Seconds(), "after_interval_seconds": fresh.Interval.Seconds()})
	})
}

// AddPushMirrorRemote registers the push mirror remote.
func AddPushMirrorRemote(ctx context.Context, m *repo_model.PushMirror, addr string) error {
	if err := addPushMirrorRemoteToRepository(ctx, m.Repo, m.RemoteName, addr); err != nil {
		return err
	}

	if repo_service.HasWiki(ctx, m.Repo) {
		wikiRemoteURL := repository.WikiRemoteURL(ctx, addr)
		if len(wikiRemoteURL) > 0 {
			if err := addPushMirrorRemoteToRepository(ctx, m.Repo.WikiStorageRepo(), m.RemoteName, wikiRemoteURL); err != nil {
				return err
			}
		}
	}

	return nil
}

func addPushMirrorRemoteToRepository(ctx context.Context, storageRepo gitrepo.Repository, remoteName, addr string) error {
	if err := gitrepo.GitRemoteAdd(ctx, storageRepo, remoteName, addr, gitrepo.RemoteOptionMirrorPush); err != nil {
		return err
	}
	if err := gitrepo.GitConfigAdd(ctx, storageRepo, "remote."+remoteName+".push", "+refs/heads/*:refs/heads/*"); err != nil {
		return err
	}
	return gitrepo.GitConfigAdd(ctx, storageRepo, "remote."+remoteName+".push", "+refs/tags/*:refs/tags/*")
}

func pushMirrorRemoteConfigurationMatches(ctx context.Context, storageRepo gitrepo.Repository, remoteName string) (bool, error) {
	key := "remote." + remoteName
	mirror, _, err := gitrepo.RunCmdString(ctx, storageRepo, gitcmd.NewCommand("config", "--bool", "--get").AddDynamicArguments(key+".mirror"))
	if err != nil {
		if gitcmd.IsErrorExitCode(err, 1) {
			return false, nil
		}
		return false, err
	}
	values, _, err := gitrepo.RunCmdString(ctx, storageRepo, gitcmd.NewCommand("config", "--get-all").AddDynamicArguments(key+".push"))
	if err != nil {
		if gitcmd.IsErrorExitCode(err, 1) {
			return false, nil
		}
		return false, err
	}
	refspecs := strings.Fields(values)
	slices.Sort(refspecs)
	return strings.TrimSpace(mirror) == "true" && slices.Equal(refspecs, []string{"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"}), nil
}

// RemovePushMirrorRemote removes the push mirror remote.
func RemovePushMirrorRemote(ctx context.Context, m *repo_model.PushMirror) error {
	_ = m.GetRepository(ctx)
	if err := gitrepo.GitRemoteRemove(ctx, m.Repo, m.RemoteName); err != nil {
		return err
	}

	if repo_service.HasWiki(ctx, m.Repo) {
		if err := gitrepo.GitRemoteRemove(ctx, m.Repo.WikiStorageRepo(), m.RemoteName); err != nil {
			return fmt.Errorf("remove Wiki push mirror remote: %w", err)
		}
	}

	return nil
}

func restorePushMirrorRemotes(ctx context.Context, mirror *repo_model.PushMirror, mainURL, wikiURL string, hasWiki bool) error {
	_ = gitrepo.GitRemoteRemove(ctx, mirror.Repo, mirror.RemoteName)
	if err := addPushMirrorRemoteToRepository(ctx, mirror.Repo, mirror.RemoteName, mainURL); err != nil {
		return err
	}
	if !hasWiki {
		return nil
	}
	_ = gitrepo.GitRemoteRemove(ctx, mirror.Repo.WikiStorageRepo(), mirror.RemoteName)
	return addPushMirrorRemoteToRepository(ctx, mirror.Repo.WikiStorageRepo(), mirror.RemoteName, wikiURL)
}

// SyncPushMirror starts the sync of the push mirror and schedules the next run.
func SyncPushMirror(ctx context.Context, mirrorID int64) bool {
	log.Trace("SyncPushMirror [mirror: %d]", mirrorID)
	defer func() {
		err := recover()
		if err == nil {
			return
		}
		// There was a panic whilst syncPushMirror...
		log.Error("PANIC whilst syncPushMirror[%d] Panic: %v\nStacktrace: %s", mirrorID, err, log.Stack(2))
	}()

	// TODO: Handle "!exist" better
	m, exist, err := db.GetByID[repo_model.PushMirror](ctx, mirrorID)
	if err != nil || !exist {
		log.Error("GetPushMirrorByID [%d]: %v", mirrorID, err)
		return false
	}

	_ = m.GetRepository(ctx)

	m.LastError = ""

	ctx, _, finished := process.GetManager().AddContext(ctx, fmt.Sprintf("Syncing PushMirror %s/%s to %s", m.Repo.OwnerName, m.Repo.Name, m.RemoteName))
	defer finished()

	log.Trace("SyncPushMirror [mirror: %d][repo: %-v]: Running Sync", m.ID, m.Repo)
	err = runPushSync(ctx, m)
	if err != nil {
		log.Error("SyncPushMirror [mirror: %d][repo: %-v]: %v", m.ID, m.Repo, err)
		m.LastError = stripExitStatus.ReplaceAllLiteralString(err.Error(), "")
	}

	m.LastUpdateUnix = timeutil.TimeStampNow()

	if err := repo_model.UpdatePushMirror(ctx, m); err != nil {
		log.Error("UpdatePushMirror [%d]: %v", m.ID, err)

		return false
	}

	log.Trace("SyncPushMirror [mirror: %d][repo: %-v]: Finished", m.ID, m.Repo)

	return err == nil
}

func runPushSync(ctx context.Context, m *repo_model.PushMirror) error {
	timeout := time.Duration(setting.Git.Timeout.Mirror) * time.Second

	performPush := func(repo *repo_model.Repository, isWiki bool) error {
		var storageRepo gitrepo.Repository = repo
		if isWiki {
			storageRepo = repo.WikiStorageRepo()
		}
		remoteURL, err := gitrepo.GitRemoteGetURL(ctx, storageRepo, m.RemoteName)
		if err != nil {
			log.Error("GetRemoteURL(%s) Error %v", storageRepo.RelativePath(), err)
			return errors.New("Unexpected error")
		}

		if setting.LFS.StartServer {
			log.Trace("SyncMirrors [repo: %-v]: syncing LFS objects...", m.Repo)

			gitRepo, err := gitrepo.OpenRepository(ctx, storageRepo)
			if err != nil {
				log.Error("OpenRepository: %v", err)
				return errors.New("Unexpected error")
			}
			defer gitRepo.Close()

			lfsClient, err := lfs.NewClientFromEndpoint(remoteURL.String(), "", migrations.NewMigrationHTTPTransport())
			if err != nil {
				return err
			}
			if err := pushAllLFSObjects(ctx, gitRepo, lfsClient); err != nil {
				return util.SanitizeErrorCredentialURLs(err)
			}
		}

		log.Trace("Pushing %s mirror[%d] remote %s", storageRepo.RelativePath(), m.ID, m.RemoteName)

		envs := proxy.EnvWithProxy(remoteURL.URL)
		if err := gitrepo.PushToExternal(ctx, storageRepo, git.PushOptions{
			Remote:  m.RemoteName,
			Force:   true,
			Mirror:  true,
			Timeout: timeout,
			Env:     envs,
		}); err != nil {
			log.Error("Error pushing %s mirror[%d] remote %s: %v", storageRepo.RelativePath(), m.ID, m.RemoteName, err)

			return util.SanitizeErrorCredentialURLs(err)
		}

		return nil
	}

	err := performPush(m.Repo, false)
	if err != nil {
		return err
	}

	if repo_service.HasWiki(ctx, m.Repo) {
		if _, err := gitrepo.GitRemoteGetURL(ctx, m.Repo.WikiStorageRepo(), m.RemoteName); err == nil {
			err := performPush(m.Repo, true)
			if err != nil {
				return err
			}
		} else if !errors.Is(err, util.ErrNotExist) {
			log.Error("GetRemote of wiki failed: %v", err)
		}
	}

	return nil
}

func pushAllLFSObjects(ctx context.Context, gitRepo *git.Repository, lfsClient lfs.Client) error {
	contentStore := lfs.NewContentStore()

	pointerChan := make(chan lfs.PointerBlob)
	errChan := make(chan error, 1)
	go func() {
		errChan <- lfs.SearchPointerBlobs(ctx, gitRepo, pointerChan)
	}()

	uploadObjects := func(pointers []lfs.Pointer) error {
		err := lfsClient.Upload(ctx, pointers, func(p lfs.Pointer, objectError error) (io.ReadCloser, error) {
			if objectError != nil {
				return nil, objectError
			}

			content, err := contentStore.Get(p)
			if err != nil {
				log.Error("Error reading LFS object %v: %v", p, err)
			}
			return content, err
		})
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
		}
		return err
	}

	var batch []lfs.Pointer
	for pointerBlob := range pointerChan {
		exists, err := contentStore.Exists(pointerBlob.Pointer)
		if err != nil {
			log.Error("Error checking if LFS object %v exists: %v", pointerBlob.Pointer, err)
			return err
		}
		if !exists {
			log.Trace("Skipping missing LFS object %v", pointerBlob.Pointer)
			continue
		}

		batch = append(batch, pointerBlob.Pointer)
		if len(batch) >= lfsClient.BatchSize() {
			if err := uploadObjects(batch); err != nil {
				return err
			}
			batch = nil
		}
	}
	if len(batch) > 0 {
		if err := uploadObjects(batch); err != nil {
			return err
		}
	}

	err := <-errChan
	if err != nil {
		log.Error("Error enumerating LFS objects for repository: %v", err)
	}

	return err
}

func syncPushMirrorWithSyncOnCommit(ctx context.Context, repoID int64) {
	pushMirrors, err := repo_model.GetPushMirrorsSyncedOnCommit(ctx, repoID)
	if err != nil {
		log.Error("repo_model.GetPushMirrorsSyncedOnCommit failed: %v", err)
		return
	}

	for _, mirror := range pushMirrors {
		AddPushMirrorToQueue(mirror.ID)
	}
}
