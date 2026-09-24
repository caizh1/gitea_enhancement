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

## 2026-09-24 M11-a 补验与关闭

本节是后续独立验收，不回写上文 v11 的运行事实。新增 `tests/integration/governance_last_permanent_owner_concurrency_test.go` 的期限断言补齐版 SHA-256 为 `70d6d675edae4d5615d07dccacdeb07e63346f8ee4c0d19bc549cb43806c3d94`，后续纯测试 lint 修订版为 `5758f8e79447ffa0c0d4b38cfa5b9701997b4b564b8011570fba3ea97695a356`。两版均在 macOS arm64 开发构建 `71d19b0d1687ac1e71ccbd5aaec8f79e7c3e5481d8ea749d15377afd360c5ed0` **之后**补充，不能称为该二进制源码清单的一部分；真实页面使用的是已部署的 71d 构建。原始结果、文件哈希及 UI 全文见 [Owner 关闭证据汇总](owner-closure-results.json)。

隔离副本 `/Users/archer/.cache/gitea-phase1-integration-v3-v6imiqb5` 的真实 PostgreSQL 集成测试过滤 `TestLastPermanentGroupOwnerConcurrentLeave` 与 `TestExpiredTemporaryOwnerCannotReplaceLastPermanentOwner`。两项在期限断言补齐版测试日志 `/Users/archer/.cache/gitea-phase1-closure-20260924/m11-owner-temp-future-expired-pg.jsonl` 均通过，日志 SHA-256 `ddeea04e79cdcdb7c626fa7e813c557790529831deaafdc3b9ffd3bf5690ca8c`；引擎检查和 `SELECT current_database()` 指向 `gitea_phase1_test`。更早同两项通过日志 `m11-owner-new-pg-final.jsonl` 保留，SHA-256 `c46f6e4541bdebbb29e00017897e6cc6987c0d4c8b5e5071cc9595054f0c36f7`。`m11-owner-temp-future-expired-pg.jsonl` 适用期限断言补齐版 `70d6d675…`。更早的 `m11-owner-new-pg-final.jsonl` 尚不含临时Owner未到期的新增断言，未保存独立源码摘要，只保留为历史通过日志；不得绑定到70d6版或下述最终5758版，当前关闭依据是最终修订版复跑。

并发用例让两名永久人类 Owner 从同一起始通道发起退出：一人成功、一人收到最后 Owner 冲突，最终仅保留一名永久 Owner；该人再退出仍被拒。起始通道只同步**请求发起**，不强制数据库锁内部的交错。成立的保证来自 `WithWrite` 对治理写入的持久行锁，以及 `LeaveGroup` 在同一事务中删除直接成员关系后检查有效永久 Owner；失败时整笔回滚。第二项分别验证临时 Owner 尚未到期时虽有 Owner 能力，仍不能充当永久兜底；将其期限置为过去后能力立即失效，唯一永久 Owner 的退出继续拒绝。这是固定业务结果和事务路径的证据，不声称枚举了全部并发调度顺序。

原有七个服务定向用例均通过，日志 `/Users/archer/.cache/gitea-phase1-closure-20260924/m11-sqlite-owner-services.jsonl`，SHA-256 `1495a473329e1181d3fcda7ef049c45136dc43235c85ccfaec976e2969c056cc`；它们的 TestMain 固定创建 SQLite，不计入上述 PG 两项。Bot 不能兜底和原生 Owners Team 的历史 SQLite/UI 基线继续保留。先前本轮另跑的 `TestGovernanceOwnerIdentityRealDatabase` 一项真实 PG 通过，仅验证 Owner 来源身份与越权，不能替代新并发测试。

已部署 71d 上，普通非实例管理员 Owner21 从成员来源进入原生 Owners Team，在 `/org/phase1-native-v15/teams/owners` 点击 Leave→Yes，页面明确显示“必须保留至少一个有效且不过期的 Owner”；刷新后仍为一名成员且 Owner21 保留。原始页面全文与哈希分别收在汇总的 `ui-last-owner-denied.txt`、`ui-last-owner-retained.txt`。这与 PG 并发／到期用例共同关闭唯一矩阵 `M11-a` 的冻结样本；不扩大为所有账号、Team 写入口都已穷举。

最终仅对两份集成测试文件修复 9 条 lint 风格问题，断言语义不变，业务源码与已部署 71d 二进制均未变。`tests/integration/actions_scoped_schedule_test.go` 最终 SHA-256 为 `f96a1676ffc8ac1917bd53b7a3fdf8e37df9c55c0b42b10a2f4be2a658d6caf7`，Owner 并发测试文件最终 SHA-256 为 `5758f8e79447ffa0c0d4b38cfa5b9701997b4b564b8011570fba3ea97695a356`。最终真实 PostgreSQL 日志 `/Users/archer/.cache/gitea-phase1-closure-20260924/owner-schedule-pg-lintfix-final-20260924.jsonl`（SHA-256 `ffc0e65d17d4b0128b4c5573600a2b89bf068f3f064dc53e3017545c88603525`）包含 `TestScopedScheduleWithoutConsumerWorkflow`、`TestScopedScheduleSourceVersionCancelsOldRun`、`TestScopedScheduleReplacementPreservesOtherPlans`、`TestScopedScheduleSourceFailureDoesNotBlockPush` 及前述两项 M11 测试，共六项顶层测试，41.368 秒全部通过；Owner 测试日志明确输出数据库 `gitea_phase1_test`。实际复跑命令如下；`run-pg.py` 从私有配置向进程环境传入数据库口令，本证据不记录口令。

```sh
python3 /Users/archer/.cache/gitea-phase1-closure-20260924/run-pg.py owner-schedule-pg-lintfix-final-20260924 '^(TestScopedScheduleWithoutConsumerWorkflow|TestScopedScheduleSourceVersionCancelsOldRun|TestScopedScheduleReplacementPreservesOtherPlans|TestScopedScheduleSourceFailureDoesNotBlockPush|TestLastPermanentGroupOwnerConcurrentLeave|TestExpiredTemporaryOwnerCannotReplaceLastPermanentOwner)$'
```

原增量 lint 日志 `/Users/archer/.cache/gitea-phase1-closure-20260924/lint-owner-closure.log` 记录 9 条问题，SHA-256 `0b8ce09ee7b725000456aadcf00c47e5d04158d90b4eea80a0196b94bbf0b7a1`；最终以 `GOOS=linux TAGS=bindata golangci-lint run --build-tags=linux,bindata ./tests/integration/` 检查，日志 `lint-owner-closure-final-20260924.log` 为 `0 issues.`，SHA-256 `e92606b0bf483111dff0a120c315ea165821348f31365020e2468a0059095c47`。两个 lint 命令口径不同，不将最终全包 Linux tag 结果冒称为旧增量命令的重跑。

## 2026-09-24 M02-a 纯继承创建与越界边界关闭

本节复用同一证据汇总 [Owner 关闭证据汇总](owner-closure-results.json)，不改动上文 M11-a 的判断。已部署 macOS arm64 构建 SHA-256 为 `71d19b0d1687ac1e71ccbd5aaec8f79e7c3e5481d8ea749d15377afd360c5ed0`。普通非实例管理员 Owner26 与 Maintainer27 在深层组25的角色都只继承自组24，未直接加入组25；原始授权来源与账号身份见私有 API 记录 `m02-runtime-api.json`，其 SHA-256 为 `56aca1123649b96dc256475c77ac4d719cf2606885324d663a224c3595e6e3ae`。

Owner26 在组25的原生页面点击创建子群组，创建组28；刷新后完整路径 `phase1-native-v15/native-child/schedule-deep/inherited-owner-leaf` 仍可见。Maintainer27 在同一组的原生创建表单提交组31，刷新后路径 `phase1-native-v15/native-child/schedule-deep/maintainer-ui-leaf` 仍可见。该 Maintainer 此前经 API 创建组30得到201，组30的持久对象亦由原始 API 再次读取。Owner26 与 Maintainer27 对父组23和兄弟组29的创建 API 均返回404，Owner26 直接访问二者原生创建页也返回404。Maintainer27 在组25成员页仅见“继承 · phase1-native-v15/native-child”来源和“在授权来源处管理”，没有成员编辑表单；原生组织设置页为404，成员写 API 返回404。页面全文、路径、哈希及原始 API 响应均收于同一 JSON。

首轮脚本把 Maintainer 创建预期写成拒绝，因此对真实201报错。错误预期和原始响应保留，不将其当作产品失败。既有 [R07 角色矩阵](../../docs/role-organization-repository-test-matrix.md)及 `models/governance/permissions.go` 定义 Maintainer 具备 `CreateGroup`，不具备 `ManageGroup`、`ManageGroupMembers`；D-02 没有冻结“Maintainer 不可建子组”。本轮真实 UI／API 同时验证允许创建与不能越界管理，按这条已冻结权限语义关闭唯一矩阵 `M02-a`。

隔离副本真实 PostgreSQL 的既有定向回归另保留两份原始日志：`m02-m03-existing-pg.jsonl` 通过 `TestGovernanceGroupHTTP`、`TestGovernanceGroupShareHTTP`、`TestGovernanceOwnerIdentityRealDatabase`，SHA-256 `37ac428b82f40db70c8487d23dcd7c0d2453582eceb12065e1edd53c7b0fbdc2`；`m02-m03-visibility-pg.jsonl` 通过 `TestRepositoryVisibilityGroupSettings`，SHA-256 `4bf756afca2e88773b95b86b7c10dae8259f1bf010bd3decd690dcfbfe57ec4c`。前者日志明确输出 PostgreSQL 引擎及 `gitea_phase1_test`；后者按同一隔离 PG 环境运行，但日志本身未独立输出引擎断言。这些测试只证明各自既有场景通过，不能替代上述现场角色边界；独立的 `M03-a` 依据下节现场证据判断。

## 2026-09-24 M03-a 纯继承资料与可见性关闭

已部署 71d 构建在隔离实例中新建根群组32、中间群组33、末级群组34和末级仓库25。夹具初次共享曾撤销，随后对 Owner26 的正式 API 授权读取及原生成员页核对：在资料与可见性正例发生时，Owner26 对末级34**仅继承**中间组33的 Owner，没有其他 Grant；不把后续新增的共享来源算入正例身份。夹具记录、撤销时间线、初始匿名 HTTP Git 握手、全部正式 API 响应、真实 Git 客户端输出及原生 UI 全文均收于 [同一原始结果汇总](owner-closure-results.json)，附路径和 SHA-256。

Owner26 从统一群组设置进入原生组织设置页，更新显示名为“继承 Owner 资料已更新”和描述“一期收口 M03：纯继承 Owner 通过统一导航编辑，下级仓库按当前可见性鉴权。”，保存并刷新后仍在；随后将末级群组从 public 改为 private，刷新后仍为私有。此时尚未共享：正式 API 对 Owner26 的群组／组织／仓库读返回200，对账号27及无关账号22均返回404。真实 Git 客户端以同一个仓库 URL 执行以下命令：匿名和账号22各退出128，Owner26退出0并读到两条引用。初始匿名 HTTP Git 握手曾返回200，只证明握手响应；不能代替变私有后的 `ls-remote` 权限判断。凭据由私有 helper 通过进程环境与 Askpass 提供，URL 和公开汇总均不含凭据。

```sh
git ls-remote http://127.0.0.1:13043/phase1-closure-profile-public/profile-admin/profile-leaf/profile-access.git
```

随后通过正式 API 将末级34共享给组24，`capReporter=20`。账号27在末级34只有一条 `shared` 来源：原角色40，但上限20；其真实 Git `ls-remote` 退出0并读到两条引用。账号27和无关账号22分别对描述与可见性发起原生组织 PATCH，四次均为403，Owner26每次读回显示名、描述及私有状态不变。账号27的原生成员页只显示 Reporter 共享来源，没有管理表单；27和22的原生组织设置页均为404。最后 Owner26 刷新群组概览，显示名、描述、私有状态仍在，仓库入口可达。该时序同时覆盖合法纯继承编辑、变私有后的真实 Git 权限变化，以及受限共享和无关身份的写入拒绝，关闭唯一矩阵 `M03-a`；其他角色组合与并发路径不据此自动关闭。
