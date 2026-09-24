# 全新数据库 CLI 初始化回归

时间：2026-09-23T16:43:55.363407

修复前真实执行 migrate 成功，紧接 admin user create 失败，错误为“治理写入锁尚未初始化”。未手工写数据库。修复后在另一全新 SQLite 目录顺序执行上述原生命令，结果如下。测试口令不记录。

## 迁移全新数据库

退出码：0

```text
2026/09/23 16:43:55 cmd/migrate.go:32:runMigrate() [I] AppPath: /Users/archer/Work/gitea-enhancement/outputs/group-subgroup-phase1-20260923/gitea-ui-next
2026/09/23 16:43:55 cmd/migrate.go:33:runMigrate() [I] AppWorkPath: /Users/archer/.cache/gitea-phase1-cli-j5fziqwr
2026/09/23 16:43:55 cmd/migrate.go:34:runMigrate() [I] Custom path: /Users/archer/.cache/gitea-phase1-cli-j5fziqwr/custom
2026/09/23 16:43:55 cmd/migrate.go:35:runMigrate() [I] Log path: /Users/archer/.cache/gitea-phase1-cli-j5fziqwr/log
2026/09/23 16:43:55 cmd/migrate.go:36:runMigrate() [I] Configuration file: /Users/archer/.cache/gitea-phase1-cli-j5fziqwr/app.ini
2026/09/23 16:43:55 .../v3@v3.10.0/command_run.go:95:(*Command).Run() [I] PING DATABASE sqlite3
```

## Web 启动前创建管理员

退出码：0

```text
New user 'phase1-cli-admin' has been successfully created!
```
