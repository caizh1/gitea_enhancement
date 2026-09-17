// Copyright 2025 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package gitrepo

import (
	"context"

	"gitea.dev/modules/git/gitcmd"
)

func RunCmd(ctx context.Context, repo Repository, cmd *gitcmd.Command) error {
	return cmd.WithDir(repoPath(repo)).WithParentCallerInfo().Run(ctx)
}

func RunCmdString(ctx context.Context, repo Repository, cmd *gitcmd.Command) (string, string, gitcmd.RunStdError) {
	// 保留调用方的隔离对象目录等环境，避免受保护分支检查读不到接收中的提交。
	return cmd.WithDir(repoPath(repo)).WithParentCallerInfo().RunStdString(ctx)
}

// RunCmdStringWithEnv 在仓库中执行命令并向引用事务 Hook 传入受信任环境。
func RunCmdStringWithEnv(ctx context.Context, repo Repository, cmd *gitcmd.Command, env []string) (string, string, gitcmd.RunStdError) {
	return cmd.WithDir(repoPath(repo)).WithEnv(env).WithParentCallerInfo().RunStdString(ctx)
}

func RunCmdBytes(ctx context.Context, repo Repository, cmd *gitcmd.Command) ([]byte, []byte, gitcmd.RunStdError) {
	return cmd.WithDir(repoPath(repo)).WithParentCallerInfo().RunStdBytes(ctx)
}

func RunCmdWithStderr(ctx context.Context, repo Repository, cmd *gitcmd.Command) gitcmd.RunStdError {
	return cmd.WithDir(repoPath(repo)).WithParentCallerInfo().RunWithStderr(ctx)
}
