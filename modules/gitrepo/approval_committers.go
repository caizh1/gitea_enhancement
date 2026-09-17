// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package gitrepo

import (
	"bufio"
	"context"
	"errors"
	"slices"
	"strings"

	"gitea.dev/modules/git"
	"gitea.dev/modules/git/gitcmd"
)

// ApprovalCommitterEmails 使用原始 committer 邮箱，不使用可被仓库改写的 mailmap。
func ApprovalCommitterEmails(ctx context.Context, baseRepo, headRepo Repository, baseID, headID string) ([]string, error) {
	for _, id := range []string{baseID, headID} {
		if _, err := git.NewIDFromString(id); err != nil {
			return nil, err
		}
	}
	command := gitcmd.NewCommand("log", "--format=%ce", "--no-use-mailmap", "--max-count=100001").AddDynamicArguments(headID).AddArguments("--not").AddDynamicArguments(baseID).AddArguments("--").WithDir(repoPath(headRepo)).WithEnv(approvalObjectEnv(baseRepo, headRepo, nil))
	reader, closeReader := command.MakeStdoutPipe()
	defer closeReader()
	emails := make(map[string]bool)
	err := command.WithPipelineFunc(func(gitcmd.Context) error {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 1024), 4096)
		count := 0
		for scanner.Scan() {
			count++
			if count > 100000 {
				return errors.New("审批提交者范围超过十万提交，需要拆分变更")
			}
			email := strings.ToLower(strings.TrimSpace(scanner.Text()))
			if email != "" {
				emails[email] = true
			}
		}
		return scanner.Err()
	}).Run(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(emails))
	for email := range emails {
		result = append(result, email)
	}
	slices.Sort(result)
	return result, nil
}
