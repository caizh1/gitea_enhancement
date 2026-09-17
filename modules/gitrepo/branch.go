// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitea.dev/modules/git"
	"gitea.dev/modules/git/gitcmd"
)

// GetBranchesByPath returns a branch by its path
// if limit = 0 it will not limit
func GetBranchesByPath(ctx context.Context, repo Repository, skip, limit int) ([]string, int, error) {
	gitRepo, err := OpenRepository(ctx, repo)
	if err != nil {
		return nil, 0, err
	}
	defer gitRepo.Close()

	return gitRepo.GetBranchNames(skip, limit)
}

func GetBranchCommitID(ctx context.Context, repo Repository, branch string) (string, error) {
	gitRepo, err := OpenRepository(ctx, repo)
	if err != nil {
		return "", err
	}
	defer gitRepo.Close()

	return gitRepo.GetBranchCommitID(branch)
}

// SetDefaultBranch sets default branch of repository.
func SetDefaultBranch(ctx context.Context, repo Repository, name string) error {
	_, _, err := RunCmdString(ctx, repo, gitcmd.NewCommand("symbolic-ref", "HEAD").
		AddDynamicArguments(git.BranchPrefix+name))
	return err
}

// GetDefaultBranch gets default branch of repository.
func GetDefaultBranch(ctx context.Context, repo Repository) (string, error) {
	stdout, _, err := RunCmdString(ctx, repo, gitcmd.NewCommand("symbolic-ref", "HEAD"))
	if err != nil {
		return "", err
	}
	stdout = strings.TrimSpace(stdout)
	if !strings.HasPrefix(stdout, git.BranchPrefix) {
		return "", errors.New("the HEAD is not a branch: " + stdout)
	}
	return strings.TrimPrefix(stdout, git.BranchPrefix), nil
}

// IsReferenceExist returns true if given reference exists in the repository.
func IsReferenceExist(ctx context.Context, repo Repository, name string) bool {
	_, _, err := RunCmdString(ctx, repo, gitcmd.NewCommand("show-ref", "--verify").AddDashesAndList(name))
	return err == nil
}

// IsBranchExist returns true if given branch exists in the repository.
func IsBranchExist(ctx context.Context, repo Repository, name string) bool {
	return IsReferenceExist(ctx, repo, git.BranchPrefix+name)
}

// DeleteBranch delete a branch by name on repository.
func DeleteBranch(ctx context.Context, repo Repository, name string, force bool) error {
	return DeleteBranchWithEnv(ctx, repo, name, force, nil)
}

// DeleteBranchWithEnv 删除分支并向引用事务 Hook 传递可信写入身份。
func DeleteBranchWithEnv(ctx context.Context, repo Repository, name string, force bool, env []string) error {
	cmd := gitcmd.NewCommand("branch")

	if force {
		cmd.AddArguments("-D")
	} else {
		cmd.AddArguments("-d")
	}

	cmd.AddDashesAndList(name)
	_, _, err := RunCmdStringWithEnv(ctx, repo, cmd, env)
	return err
}

// DeleteBranchReferenceWithEnv 使用调用者已读取的旧提交原子删除引用，避免并发新推送被误删。
func DeleteBranchReferenceWithEnv(ctx context.Context, repo Repository, name, expectedOld string, env []string) error {
	_, _, err := RunCmdStringWithEnv(ctx, repo, gitcmd.NewCommand("update-ref", "-d").AddDynamicArguments(git.BranchPrefix+name, expectedOld), env)
	return err
}

// CreateBranch create a new branch
func CreateBranch(ctx context.Context, repo Repository, branch, oldbranchOrCommit string) error {
	return CreateBranchWithEnv(ctx, repo, branch, oldbranchOrCommit, nil)
}

// CreateBranchWithEnv 创建分支并向引用事务 Hook 传递可信写入身份。
func CreateBranchWithEnv(ctx context.Context, repo Repository, branch, oldbranchOrCommit string, env []string) error {
	cmd := gitcmd.NewCommand("branch")
	cmd.AddDashesAndList(branch, oldbranchOrCommit)

	_, _, err := RunCmdStringWithEnv(ctx, repo, cmd, env)
	return err
}

// RenameBranch rename a branch
func RenameBranch(ctx context.Context, repo Repository, from, to string) error {
	return RenameBranchWithEnv(ctx, repo, from, to, nil)

}

// RenameBranchWithEnv 改名分支并向引用事务 Hook 传递可信写入身份。
func RenameBranchWithEnv(ctx context.Context, repo Repository, from, to string, env []string) error {
	_, _, err := RunCmdStringWithEnv(ctx, repo, gitcmd.NewCommand("branch", "-m").AddDynamicArguments(from, to), env)
	return err
}

// RenameBranchReferencesWithEnv 以单个 update-ref 事务同时创建新引用并删除旧引用。
// HEAD 是符号引用，由上层在同一持久业务操作中单独核对和更新。
func RenameBranchReferencesWithEnv(ctx context.Context, repo Repository, from, to string, env []string) error {
	commitID, err := GetBranchCommitID(ctx, repo, from)
	if err != nil {
		return err
	}
	input := fmt.Sprintf("start\ncreate %s%s %s\ndelete %s%s %s\nprepare\ncommit\n", git.BranchPrefix, to, commitID, git.BranchPrefix, from, commitID)
	_, _, err = RunCmdStringWithEnv(ctx, repo, gitcmd.NewCommand("update-ref", "--stdin").WithStdinBytes([]byte(input)), env)
	return err
}
