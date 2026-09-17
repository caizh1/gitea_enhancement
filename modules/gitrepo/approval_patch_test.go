// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitea.dev/modules/git/gitcmd"

	"github.com/stretchr/testify/require"
)

func TestApprovalPatchID(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = dir
		output, err := command.CombinedOutput()
		require.NoError(t, err)
		return strings.TrimSpace(string(output))
	}
	run("init", "--bare")
	run("config", "user.name", "审批测试")
	run("config", "user.email", "approval@example.invalid")
	blob := func(value string) string {
		t.Helper()
		output, _, err := gitcmd.NewCommand("hash-object", "-w", "--stdin").WithDir(dir).WithStdinCopy(strings.NewReader(value)).RunStdString(ctx)
		require.NoError(t, err)
		return strings.TrimSpace(output)
	}
	tree := func(value, mode, name string) string {
		t.Helper()
		output, _, err := gitcmd.NewCommand("mktree").WithDir(dir).WithStdinCopy(strings.NewReader(mode + " blob " + blob(value) + "\t" + name + "\n")).RunStdString(ctx)
		require.NoError(t, err)
		return strings.TrimSpace(output)
	}
	commit := func(treeID, parent, message string) string {
		args := []string{"commit-tree", treeID, "-m", message}
		if parent != "" {
			args = append(args, "-p", parent)
		}
		return run(args...)
	}
	base := commit(tree("原内容\n", "100644", "file"), "", "基点")
	changedTree := tree("if enabled:\n    run()\n", "100644", "file")
	head := commit(changedTree, base, "修改")
	repo := &mockRepository{path: dir}
	patch := func(baseID, headID string) string {
		t.Helper()
		value, err := ApprovalPatchID(ctx, repo, repo, baseID, headID, nil)
		require.NoError(t, err)
		return value
	}
	initial := patch(base, head)
	rebasedParent := commit(tree("原内容\n", "100644", "file"), base, "仅历史变化")
	rebased := commit(changedTree, rebasedParent, "重排后修改")
	require.Equal(t, initial, patch(rebasedParent, rebased), "相同差异保留标识")
	whitespace := commit(tree("if enabled:\n  run()\n", "100644", "file"), base, "缩进变化")
	require.NotEqual(t, initial, patch(base, whitespace))
	mode := commit(tree("if enabled:\n    run()\n", "100755", "file"), base, "执行位变化")
	require.NotEqual(t, initial, patch(base, mode))
	renamed := commit(tree("if enabled:\n    run()\n", "100644", "renamed"), base, "路径变化")
	require.NotEqual(t, initial, patch(base, renamed))
	binaryA := commit(tree("\x00内容甲", "100644", "file"), base, "二进制甲")
	binaryB := commit(tree("\x00内容乙", "100644", "file"), base, "二进制乙")
	require.NotEqual(t, patch(base, binaryA), patch(base, binaryB))
	require.Equal(t, patch(base, base), patch(rebasedParent, rebasedParent))
	forkDir := filepath.Join(t.TempDir(), "分叉")
	_, _, err := gitcmd.NewCommand("clone", "--bare", "--no-local").AddDynamicArguments(dir, forkDir).RunStdString(ctx)
	require.NoError(t, err)
	// 分叉没有任何引用，因此基点和源提交只能通过显式对象目录读取。
	require.NoError(t, os.MkdirAll(filepath.Join(forkDir, "objects"), 0o755))
	forkPatch, patchErr := ApprovalPatchID(ctx, repo, &mockRepository{path: forkDir}, base, head, nil)
	require.NoError(t, patchErr)
	require.Equal(t, initial, forkPatch)
	committers, committerErr := ApprovalCommitterEmails(ctx, repo, repo, base, head)
	require.NoError(t, committerErr)
	require.Equal(t, []string{"approval@example.invalid"}, committers)
	run("config", "user.email", "rebased@example.invalid")
	rewritten := commit(changedTree, base, "由另一人重排")
	committers, committerErr = ApprovalCommitterEmails(ctx, repo, repo, base, rewritten)
	require.NoError(t, committerErr)
	require.Equal(t, []string{"rebased@example.invalid"}, committers, "重新提交后按当前 committer 判定")
	require.Equal(t, initial, patch(base, rewritten), "提交者变化不等于代码差异变化")
}
