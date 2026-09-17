// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package gitrepo

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gitea.dev/modules/git/gitcmd"
)

// WithVerifiedReferenceLocks 只执行 verify 和 abort，持有真实 Git 引用锁时核对恢复状态。
func WithVerifiedReferenceLocks(ctx context.Context, repo Repository, expected map[string]string, apply func(context.Context) error) error {
	if len(expected) == 0 || len(expected) > 10000 || apply == nil {
		return errors.New("引用恢复输入无效")
	}
	refs := make([]string, 0, len(expected))
	for ref, value := range expected {
		if !strings.HasPrefix(ref, "refs/") || strings.ContainsAny(ref, "\x00\r\n\t ") || len(value) != 40 && len(value) != 64 {
			return errors.New("引用恢复输入无效")
		}
		if _, err := hex.DecodeString(value); err != nil {
			return err
		}
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	emptyHooks, err := os.MkdirTemp("", "gitea-reference-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(emptyHooks)
	// 此进程绝不提交引用；避免 verify 自身触发治理写入入口形成循环。
	command := gitcmd.NewCommand("update-ref", "--stdin").AddConfig("core.hooksPath", emptyHooks).WithDir(repoPath(repo)).WithTimeout(10 * time.Second)
	stdin, closeInput := command.MakeStdinPipe()
	defer closeInput()
	stdout, closeOutput := command.MakeStdoutPipe()
	defer closeOutput()
	var input strings.Builder
	input.WriteString("start\n")
	for _, ref := range refs {
		_, _ = fmt.Fprintf(&input, "verify %s %s\n", ref, expected[ref])
	}
	input.WriteString("prepare\n")
	return command.WithPipelineFunc(func(pipeCtx gitcmd.Context) error {
		defer closeInput()
		if _, err := stdin.Write([]byte(input.String())); err != nil {
			return err
		}
		reader := bufio.NewReader(stdout)
		for _, expectedLine := range []string{"start: ok\n", "prepare: ok\n"} {
			line, err := reader.ReadString('\n')
			if err != nil {
				return err
			}
			if line != expectedLine {
				return errors.New("Git 未确认引用恢复锁")
			}
		}
		if err := apply(pipeCtx); err != nil {
			return err
		}
		_, err := stdin.Write([]byte("abort\n"))
		return err
	}).RunWithStderr(ctx)
}
