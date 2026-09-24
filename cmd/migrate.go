// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/log"
	"gitea.dev/modules/setting"
	"gitea.dev/services/versioned_migration"

	"github.com/urfave/cli/v3"
)

func newMigrateCommand() *cli.Command {
	return &cli.Command{
		Name:        "migrate",
		Usage:       "Migrate the database",
		Description: `This is a command for migrating the database, so that you can run "gitea admin create user" before starting the server.`,
		Action:      runMigrate,
	}
}

func runMigrate(ctx context.Context, c *cli.Command) error {
	if err := initDB(ctx); err != nil {
		return err
	}

	log.Info("AppPath: %s", setting.AppPath)
	log.Info("AppWorkPath: %s", setting.AppWorkPath)
	log.Info("Custom path: %s", setting.CustomPath)
	log.Info("Log path: %s", setting.Log.RootPath)
	log.Info("Configuration file: %s", setting.CustomConf)

	if err := db.InitEngineWithMigration(context.Background(), versioned_migration.Migrate); err != nil {
		log.Fatal("Failed to initialize ORM engine: %v", err)
		return err
	}

	// 全新数据库也必须允许在 Web 启动前使用原生 CLI 创建首个管理员。
	return governance_model.InitializeLock(ctx)
}
