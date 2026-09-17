// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitea.dev/modules/git/gitcmd"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/util"
)

// InstallReferenceTransactionHook 仅显式接入的仓库安装；已有第三方入口必须保留并另行迁移。
func InstallReferenceTransactionHook(ctx context.Context, repo Repository) error {
	hookDir := filepath.Join(repoPath(repo), "hooks")
	target := filepath.Join(hookDir, "reference-transaction")
	content := referenceTransactionHookContent()
	if info, err := os.Lstat(target); err == nil && !info.Mode().IsRegular() {
		return errors.New("引用事务入口不是普通文件")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	previous, err := os.ReadFile(target)
	if err == nil {
		if !bytes.Equal(previous, content) {
			return errors.New("已存在其他引用事务入口，必须先完成兼容迁移")
		}
		if err := disableAutomaticReferenceMaintenance(ctx, repo); err != nil {
			return err
		}
		return ensureExecutable(target)
	}
	if !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(hookDir, ".governance-reference-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(content); err == nil {
		err = file.Chmod(0o755)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := disableAutomaticReferenceMaintenance(ctx, repo); err != nil {
		return err
	}
	// 同目录硬链接原子发布，不能覆盖检查后由其他操作创建的入口。
	if err := os.Link(file.Name(), target); err != nil {
		return err
	}
	dir, err := os.Open(hookDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// disableAutomaticReferenceMaintenance 避免自动 pack-refs 把物理松散引用迁移误报为逻辑分支删除。
// 维护统一由带可信 maintenance 身份的后台 GC 执行。
func disableAutomaticReferenceMaintenance(ctx context.Context, repo Repository) error {
	for _, pair := range [][2]string{{"gc.auto", "0"}, {"maintenance.auto", "false"}} {
		if _, _, err := RunCmdString(ctx, repo, gitcmd.NewCommand("config").AddDynamicArguments(pair[0], pair[1])); err != nil {
			return err
		}
	}
	return nil
}

func referenceTransactionHookContent() []byte {
	return fmt.Appendf(nil, "#!/usr/bin/env %s\n# Gitea 原生治理引用事务入口\nexport GITEA_REFERENCE_WRITER_ID=$PPID\nexec %s hook --config=%s reference-transaction \"$1\"\n", setting.ScriptType, util.ShellEscape(setting.AppPath), util.ShellEscape(setting.CustomConf))
}

// ReferenceTransactionHookInstalled 核对实际入口内容，不能把配置开关当成已安装证明。
func ReferenceTransactionHookInstalled(repo Repository) (bool, error) {
	path := filepath.Join(repoPath(repo), "hooks", "reference-transaction")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return false, nil
	}
	content, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(content, referenceTransactionHookContent()) {
		return false, err
	}
	for _, expected := range [][2]string{{"gc.auto", "0"}, {"maintenance.auto", "false"}} {
		value, _, err := RunCmdString(context.Background(), repo, gitcmd.NewCommand("config", "--get").AddDynamicArguments(expected[0]))
		if gitcmd.IsErrorExitCode(err, 1) {
			return false, nil
		}
		if err != nil || strings.TrimSpace(value) != expected[1] {
			return false, err
		}
	}
	return true, nil
}
