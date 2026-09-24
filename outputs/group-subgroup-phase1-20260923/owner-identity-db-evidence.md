# 所有者授权与群组删除数据库回归证据

## 证据口径纠正

此前将 `group-deletion-owner-pg.jsonl` 与 `owner-identity-pg.jsonl` 记录为 PostgreSQL 结果，是错误的。`services/governance` 使用 `models/unittest/testdb.go` 中的 `MainTest`，该入口建立 SQLite 测试库并固定 `setting.Database.Type = "sqlite3"`。即使命令设置了 `GITEA_TEST_DATABASE=pgsql`，上述两个服务包日志仍只证明 SQLite 通过。原始日志已保留，没有覆盖；不能用于 PostgreSQL 验收。

## 真实数据库回归

新建 `tests/integration/governance_owner_identity_test.go`，由 `tests/integration/integration_test.go` 的 `TestMain` 调用 `tests.InitIntegrationTest`。测试在 `GITEA_TEST_DATABASE=pgsql` 时断言 `setting.Database.Type.IsPostgreSQL()`，并执行 `SELECT current_database()`，与 `TEST_PGSQL_DBNAME` 比对。实际日志显示引擎为 `postgres`、数据库为 `gitea_phase1_test`。测试通过公开服务创建可赋的自定义角色和群组共享，验证能力并集不能拼成 Owner 身份、审计与归档/删除拒绝，以及完整 Owner 正例。

实际命令在隔离副本 `/Users/archer/.cache/gitea-phase1-integration-v3-v6imiqb5` 执行，清除了大小写代理变量，并设置 `GITEA_TEST_DATABASE=pgsql`、`TEST_PGSQL_HOST=127.0.0.1:15433`、`TEST_PGSQL_DBNAME=gitea_phase1_test`、`TEST_PGSQL_USERNAME=phase1_test`、`TEST_PGSQL_SCHEMA=public`、`TEST_MINIO_ENDPOINT=127.0.0.1:19000`；测试口令仅通过进程环境传递，此处不记录。Go 命令为：

```sh
/Users/archer/.cache/gitea-governance/go/bin/go test -tags='bindata sqlite sqlite_unlock_notify' -p 1 -count=1 -json -run '^(TestGovernanceOwnerIdentityRealDatabase|TestGovernanceAuditHTTP|TestGovernanceGroupDeletionRealGit|TestRepositoryInvitationLinkFromCollaborators)$' ./tests/integration
```

最终原始日志：`/Users/archer/.cache/gitea-phase1-v5-validation/v11-owner-identity-real-pg-3.jsonl`。四项顶级测试分别通过：审计 HTTP 1.09 秒、群组删除真实 Git 3.77 秒、所有者身份真实数据库 0.93 秒、仓库邀请导航 0.61 秒；整个集成包 8.405 秒，退出码 0。

首轮新测试因误用不存在的 `db.Engine.QueryString` 而编译失败，原始日志为 `v11-owner-identity-real-pg.jsonl`。修正后第二轮三项通过，旧 `TestGovernanceGroupDeletionRealGit` 在 SQLite 专用 `CREATE TRIGGER ... WHEN NEW ...` 处收到 PostgreSQL `syntax error at or near "NEW"`；原始日志为 `v11-owner-identity-real-pg-2.jsonl`。该失败发生在测试故障注入语法，业务删除路径此前已运行；现已按方言使用 PostgreSQL 函数与触发器执行同一审计回滚检查。修正后的该测试在 SQLite 也通过，原始日志 `v11-group-deletion-sqlite.jsonl`，顶级 2.94 秒、包 4.493 秒。

`golangci-lint run --new-from-rev=HEAD ./tests/integration/` 为 0 条问题，相关文件 `git diff --check` 通过。本轮没有操作运行中的 UI 实例；真实 UI 结果另由父任务记录。
