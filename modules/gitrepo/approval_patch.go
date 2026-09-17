// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package gitrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gitea.dev/modules/git"
	"gitea.dev/modules/git/gitcmd"
)

// ApprovalPatchID 跨 Fork 读取对象；只比较固定提交，不依赖后台同步的 PR 引用。
// verbatim 保留有语义的缩进变化，仍忽略补丁行号，允许相同差异的 rebase 保留批准。
func ApprovalPatchID(ctx context.Context, baseRepo, headRepo Repository, baseID, headID string, objectEnv []string) (string, error) {
	for _, id := range []string{baseID, headID} {
		if _, err := git.NewIDFromString(id); err != nil {
			return "", err
		}
	}
	env := approvalObjectEnv(baseRepo, headRepo, objectEnv)
	mergeBase, _, err := gitcmd.NewCommand("merge-base").AddDynamicArguments(baseID, headID).WithDir(repoPath(headRepo)).WithEnv(env).RunStdString(ctx)
	if err != nil {
		return "", fmt.Errorf("无法计算审批共同基点：%w", err)
	}
	mergeBase = strings.TrimSpace(mergeBase)
	if _, err := git.NewIDFromString(mergeBase); err != nil {
		return "", err
	}
	diff := gitcmd.NewCommand("diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--ignore-submodules=none", "--submodule=short", "--diff-algorithm=myers", "--no-indent-heuristic", "--unified=3", "--src-prefix=a/", "--dst-prefix=b/").AddDynamicArguments(mergeBase, headID).AddArguments("--").WithDir(repoPath(headRepo)).WithEnv(env)
	reader, closeReader := diff.MakeStdoutPipe()
	defer closeReader()
	var patch string
	patchErr := diff.WithPipelineFunc(func(pipelineCtx gitcmd.Context) error {
		var runErr error
		patch, _, runErr = gitcmd.NewCommand("patch-id", "--verbatim").WithDir(repoPath(headRepo)).WithEnv(env).WithStdinCopy(reader).RunStdString(pipelineCtx)
		return runErr
	}).Run(ctx)
	if patchErr != nil {
		return "", fmt.Errorf("无法计算审批差异：%w", patchErr)
	}
	fields := strings.Fields(patch)
	if len(fields) == 0 {
		empty := sha256.Sum256([]byte("审批空差异"))
		return hex.EncodeToString(empty[:]), nil
	}
	if len(fields) != 2 {
		return "", errors.New("审批差异标识输出不完整")
	}
	if _, err := git.NewIDFromString(fields[0]); err != nil {
		return "", err
	}
	return fields[0], nil
}

func approvalObjectEnv(baseRepo, headRepo Repository, objectEnv []string) []string {
	env := append(os.Environ(), objectEnv...)
	alternates := strconv.Quote(filepath.Join(repoPath(baseRepo), "objects")) + string(os.PathListSeparator) + strconv.Quote(filepath.Join(repoPath(headRepo), "objects"))
	for _, item := range slices.Backward(env) {
		if value, found := strings.CutPrefix(item, "GIT_ALTERNATE_OBJECT_DIRECTORIES="); found {
			if value != "" {
				alternates = strings.Join([]string{alternates, value}, string(os.PathListSeparator))
			}
			break
		}
	}
	env = append(env, "GIT_ALTERNATE_OBJECT_DIRECTORIES="+alternates)
	return env
}
