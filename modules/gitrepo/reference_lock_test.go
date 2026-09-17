// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package gitrepo

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoveryHoldsRealReferenceLocksWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(output))
		return strings.TrimSpace(string(output))
	}
	run("init", "-q", "--initial-branch=main")
	run("-c", "user.name=恢复验收", "-c", "user.email=recovery@example.invalid", "commit", "--allow-empty", "-m", "基点")
	first := run("rev-parse", "HEAD")
	run("-c", "user.name=恢复验收", "-c", "user.email=recovery@example.invalid", "commit", "--allow-empty", "-m", "后续提交")
	second := run("rev-parse", "HEAD")
	run("update-ref", "refs/heads/main", first)
	repo := &mockRepository{path: dir}
	visited := false
	require.NoError(t, WithVerifiedReferenceLocks(t.Context(), repo, map[string]string{"refs/heads/main": first}, func(context.Context) error {
		visited = true
		_, err := exec.Command("git", "-C", dir, "update-ref", "refs/heads/main", second).CombinedOutput()
		require.Error(t, err, "恢复核对期间真实并发写入必须被引用锁拒绝")
		return nil
	}))
	require.True(t, visited)
	require.Equal(t, first, run("rev-parse", "HEAD"), "恢复锁不修改引用")
	require.Error(t, WithVerifiedReferenceLocks(t.Context(), repo, map[string]string{"refs/heads/main": second}, func(context.Context) error { t.Error("过期引用不能进入恢复回调"); return nil }))
	sentinel := errors.New("模拟数据库核对失败")
	require.ErrorIs(t, WithVerifiedReferenceLocks(t.Context(), repo, map[string]string{"refs/heads/main": first}, func(context.Context) error { return sentinel }), sentinel)
	run("update-ref", "refs/heads/main", second)
	require.Equal(t, second, run("rev-parse", "HEAD"), "失败回调也必须释放验证引用锁")
}
