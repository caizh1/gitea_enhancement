// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package cmd

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gitea.dev/modules/git"
	"gitea.dev/modules/private"
	repo_module "gitea.dev/modules/repository"
	"gitea.dev/modules/setting"

	"github.com/urfave/cli/v3"
)

func newHookReferenceTransactionCommand() *cli.Command {
	return &cli.Command{Name: "reference-transaction", Usage: "记录 Git 引用事务", Action: runHookReferenceTransaction}
}

func runHookReferenceTransaction(ctx context.Context, c *cli.Command) error {
	state := c.Args().First()
	if state == "preparing" {
		return nil
	}
	if state != "prepared" && state != "committed" && state != "aborted" {
		return errors.New("未知 Git 引用事务阶段")
	}
	var oldCommitIDs, newCommitIDs []string
	var refFullNames []git.RefName
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 8192)
	for scanner.Scan() {
		oldID, newID, ref, ok := parseGitHookCommitRefLine(scanner.Text())
		if !ok {
			return errors.New("Git 引用事务输入不完整")
		}
		// Git 2.54 的 update-ref 可能发出 0→0 回调；HEAD 等符号引用也不属于逻辑分支或标签变化。
		if oldID == newID || !ref.IsBranch() && !ref.IsTag() {
			continue
		}
		if len(refFullNames) >= 10000 {
			return errors.New("单次引用事务超过 10000 条")
		}
		oldCommitIDs = append(oldCommitIDs, oldID)
		newCommitIDs = append(newCommitIDs, newID)
		refFullNames = append(refFullNames, ref)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(refFullNames) == 0 {
		return nil
	}
	setup(ctx, false)
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(setting.RepoRootPath, cwd)
	if err != nil {
		return err
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 2 || parts[0] == ".." || !strings.HasSuffix(parts[1], ".git") {
		return errors.New("引用事务仓库不在配置的存储目录")
	}
	name := strings.TrimSuffix(parts[1], ".git")
	isWiki := strings.HasSuffix(name, ".wiki")
	name = strings.TrimSuffix(name, ".wiki")
	userID, err := strconv.ParseInt(os.Getenv(repo_module.EnvPusherID), 10, 64)
	if err != nil && os.Getenv(repo_module.EnvPusherID) != "" {
		return err
	}
	deployKeyID, err := strconv.ParseInt(os.Getenv(repo_module.EnvDeployKeyID), 10, 64)
	if err != nil && os.Getenv(repo_module.EnvDeployKeyID) != "" {
		return err
	}
	internal, _ := strconv.ParseBool(os.Getenv(repo_module.EnvIsInternal))
	referenceActor := os.Getenv(repo_module.EnvReferenceActor)
	if internal && referenceActor == "maintenance" {
		return nil
	}
	prID, _ := strconv.ParseInt(os.Getenv(repo_module.EnvPRID), 10, 64)
	repoID, err := strconv.ParseInt(os.Getenv(repo_module.EnvRepoID), 10, 64)
	if err != nil || repoID <= 0 {
		return errors.New("引用事务缺少稳定项目 ID")
	}
	options := private.HookOptions{RepositoryID: repoID, UserID: userID, IsInternal: internal, IsWiki: isWiki, PullRequestID: prID, ReferenceState: state, ReferenceWriterID: os.Getenv("GITEA_REFERENCE_WRITER_ID"), ReferenceActor: referenceActor, PushTrigger: repo_module.PushTrigger(os.Getenv(repo_module.EnvPushTrigger))}
	options.ActionsTaskID, _ = strconv.ParseInt(os.Getenv(repo_module.EnvActionsTaskID), 10, 64)
	options.ReferenceOperationID = os.Getenv(repo_module.EnvReferenceOperationID)
	options.ReferenceOperation = os.Getenv(repo_module.EnvReferenceOperation)
	options.ReferenceOldBranch = os.Getenv(repo_module.EnvReferenceOldBranch)
	options.ReferenceNewBranch = os.Getenv(repo_module.EnvReferenceNewBranch)
	options.ReferenceUpdateHEAD, _ = strconv.ParseBool(os.Getenv(repo_module.EnvReferenceUpdateHEAD))
	options.ContentEvent = os.Getenv("GITEA_GOVERNANCE_CONTENT_EVENT")
	options.ContentObjectPath = os.Getenv("GITEA_GOVERNANCE_CONTENT_PATH")
	options.ContentDetails = []byte(os.Getenv("GITEA_GOVERNANCE_CONTENT_DETAILS"))
	options.DeployKeyID = deployKeyID
	options.MergeAuthorizationID = os.Getenv(repo_module.EnvMergeAuthorizationID)
	options.PusherRemoteAddr, options.PusherTransport = os.Getenv(repo_module.EnvPusherRemoteAddr), os.Getenv(repo_module.EnvPusherTransport)
	options.OldCommitIDs, options.NewCommitIDs, options.RefFullNames = oldCommitIDs, newCommitIDs, refFullNames
	extra := private.HookReferenceTransaction(ctx, parts[0], name, options)
	if extra.HasError() {
		return fail(ctx, extra.UserMsg, "引用事务记录失败：%v", extra.Error)
	}
	return nil
}
