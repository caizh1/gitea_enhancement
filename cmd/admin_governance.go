// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	mirror_service "gitea.dev/services/mirror"
	pull_service "gitea.dev/services/pull"
	repo_service "gitea.dev/services/repository"

	"github.com/urfave/cli/v3"
)

func newGovernanceAdminCommand() *cli.Command {
	return &cli.Command{Name: "governance", Usage: "查询和核对待恢复的原生治理操作", Commands: []*cli.Command{
		{Name: "install-reference-hook", Usage: "为指定代码仓库接入原生审批引用事务入口，不覆盖第三方入口", Flags: []cli.Flag{&cli.Int64Flag{Name: "repository-id", Required: true}}, Action: runGovernanceInstallHook},
		{Name: "repository-storage", Usage: "按稳定项目 ID 返回内部物理相对路径", Flags: []cli.Flag{&cli.Int64Flag{Name: "repository-id", Required: true}}, Action: runGovernanceRepositoryStorage},
		{Name: "pending", Usage: "列出待核对的合并及引用事务", Action: runGovernancePending},
		{Name: "recover", Usage: "核对持久化结果，不重新执行 Git 写入", Flags: []cli.Flag{&cli.StringFlag{Name: "kind", Required: true, Usage: "操作类别：merge、reference、repository_creation 或 mirror"}, &cli.StringFlag{Name: "id", Required: true, Usage: "待恢复的操作 ID"}}, Action: runGovernanceRecover},
	}}
}

func runGovernanceRepositoryStorage(ctx context.Context, c *cli.Command) error {
	if err := initDB(ctx); err != nil {
		return err
	}
	repo, err := repo_model.GetRepositoryByID(ctx, c.Int64("repository-id"))
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"repository_id":              repo.ID,
		"full_path":                  repo.FullPath(),
		"storage_path":               repo.RepoPath(),
		"wiki_storage_path":          filepath.Join(setting.RepoRootPath, filepath.FromSlash(repo.WikiStorageRepo().RelativePath())),
		"storage_relative_path":      repo.RelativePath(),
		"wiki_storage_relative_path": repo_model.RelativeWikiPath(repo.StorageOwnerName(), repo.StorageName()),
	})
}

func runGovernancePending(ctx context.Context, _ *cli.Command) error {
	if err := initDB(ctx); err != nil {
		return err
	}
	var merges []struct {
		ID     string
		RepoID int64
		PullID int64
		State  string
	}
	var references []struct {
		ID     string
		RepoID int64
		State  string
	}
	var creations []struct {
		ID             string `json:"id"`
		RepoID         int64  `json:"repository_id"`
		State          string `json:"state"`
		Phase          string `json:"phase"`
		Origin         string `json:"origin"`
		LastReasonCode string `json:"reason_code"`
	}
	var mirrorOperations []struct {
		ID             string `json:"id"`
		RepoID         int64  `json:"repository_id"`
		MirrorID       int64  `json:"mirror_id"`
		Direction      string `json:"direction"`
		Action         string `json:"action"`
		State          string `json:"state"`
		LastReasonCode string `json:"reason_code"`
	}
	if err := db.GetEngine(ctx).Table(new(governance_model.MergeAuthorization)).Select("id, repo_id, pull_id, state").Where("state IN (?, ?) OR (state = ? AND EXISTS (SELECT 1 FROM pull_request WHERE pull_request.id = governance_merge_authorization.pull_id AND pull_request.has_merged = ?))", "authorized", "unknown", "succeeded", false).Asc("created_at").Limit(1000).Find(&merges); err != nil {
		return err
	}
	if err := db.GetEngine(ctx).Table(new(governance_model.ReferenceTransaction)).Select("id, repo_id, state").In("state", []string{"prepared", "unknown"}).Asc("created_at").Limit(1000).Find(&references); err != nil {
		return err
	}
	if err := db.GetEngine(ctx).Table(new(governance_model.RepositoryCreation)).Select("id, repo_id, state, phase, origin, last_reason_code").In("state", []string{"prepared", "unknown"}).Asc("created_unix").Limit(1000).Find(&creations); err != nil {
		return err
	}
	if err := db.GetEngine(ctx).Table(new(governance_model.MirrorOperation)).Select("id, repo_id, mirror_id, direction, action, state, last_reason_code").In("state", []string{"prepared", "unknown"}).Asc("created_unix").Limit(1000).Find(&mirrorOperations); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"合并操作": merges, "引用事务": references, "项目创建": creations, "镜像配置": mirrorOperations, "说明": "每类最多显示 1000 条；只读查询不释放占用。"})
}

func runGovernanceRecover(ctx context.Context, c *cli.Command) error {
	if err := initDB(ctx); err != nil {
		return err
	}
	actor := governance_model.Actor{Kind: "system", Name: fmt.Sprintf("主机治理恢复命令（UID %d）", os.Getuid()), Transport: "cli"}
	state := ""
	var err error
	if c.String("kind") == "repository_creation" {
		state, err = repo_service.RecoverRepositoryCreation(ctx, c.String("id"))
	} else if c.String("kind") == "mirror" {
		state, err = mirror_service.RecoverMirrorOperation(ctx, c.String("id"))
	} else {
		state, err = pull_service.RecoverGovernanceOperation(ctx, c.String("kind"), c.String("id"), actor)
	}
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"操作ID": c.String("id"), "状态": state, "说明": "已在真实引用锁保护下核对，未执行新的合并或推送。"}); err != nil {
		return err
	}
	if state == "unknown" {
		return fmt.Errorf("%w：引用与已记录前后值不一致，保留占用，需要人工调查", governance_model.ErrConflict)
	}
	return nil
}

func runGovernanceInstallHook(ctx context.Context, c *cli.Command) error {
	if err := initDB(ctx); err != nil {
		return err
	}
	repo, err := repo_model.GetRepositoryByID(ctx, c.Int64("repository-id"))
	if err != nil {
		return err
	}
	if err := gitrepo.InstallReferenceTransactionHook(ctx, repo); err != nil {
		return err
	}
	installed, err := gitrepo.ReferenceTransactionHookInstalled(repo)
	if err != nil {
		return err
	}
	if !installed {
		return errors.New("引用事务入口安装后核对失败")
	}
	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
		if err != nil {
			return err
		}
		var ancestors []int64
		for _, n := range chain {
			if n.Kind == "group" {
				ancestors = append(ancestors, n.ID)
			}
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "repository.approval_hook_installed", Actor: governance_model.Actor{Kind: "system", Name: fmt.Sprintf("主机治理接入命令（UID %d）", os.Getuid()), Transport: "cli"}, ScopeType: "repository", ScopeID: repo.ID, AncestorIDs: ancestors, ObjectType: "repository", ObjectID: repo.ID, ObjectPath: repo.FullPath(), Result: "success"})
	}); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"仓库ID": repo.ID, "引用事务入口已核对": true, "说明": "保留已有原生审批记录；历史批准不会自动获得新的差异版本依据。"})
}
