# 一期 Runner 领取与凭据边界验证

验证日期：2026-09-23。全部在本机开发树及隔离测试服务完成；未连接生产。PostgreSQL 使用独立的 `gitea_phase1` Colima 配置、17.11 服务与 `gitea_phase1_test` 测试库。测试口令只在本地环境变量中提供，本文不记录。

## 修复前失败回归

先固定旧归属候选，再把仓库从用户 2 转给用户 5，调用原 `claimJobForRunner`。命令：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^TestClaimJobRejectsOwnerChangedAfterScan$' ./models/actions/
```

修复前退出码 1；断言得到 `ok=true` 和运行中任务，期望 `ok=false`、无任务。该回归是可控顺序的 SQLite 单测，证明原领取终检缺口；不能据此宣称验证了 PostgreSQL 锁竞争。修复后同命令退出码 0。

## 修复后数据库与权限验证

下列集成命令使用隔离 PostgreSQL 与 MinIO；实际运行时还提供本地 `TEST_PGSQL_PASSWORD`。包含三名 Runner 并发领取，以及 XORM 候选 `SELECT` 完成 hook／通道屏障控制的“旧候选扫描→治理写事务修改 owner 并提交→领取”用例，不依靠延时。后者直接修改归属字段以隔离领取竞争，不代表完整仓库转移服务验收：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH \
GITEA_TEST_DATABASE=pgsql TEST_PGSQL_HOST=127.0.0.1:15433 \
TEST_PGSQL_DBNAME=gitea_phase1_test TEST_PGSQL_USERNAME=phase1_test \
TEST_PGSQL_SCHEMA=public TEST_MINIO_ENDPOINT=127.0.0.1:19000 \
go test -count=1 -run '^TestCreateTaskForRunner(ConcurrentClaim|RejectsOwnerChangeAfterCandidateScan)$' ./tests/integration
```

结果：退出码 0，最终重跑为 `ok gitea.dev/tests/integration 2.854s`。这验证了隔离 PostgreSQL 上并发领取与旧候选归属变化时序；不是完整转移服务、生产数据库压力或容量验收。

以下定向回归均退出码 0：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^TestEphemeralRunnerCannotClaimSecondCandidate$' ./models/actions/
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^TestTaskCredentialRejectsRevokedPrivateSourceRead$' ./services/actions/
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^TestCreatedGroupWithInactiveUserFlagCanClaimActions$' ./services/actions/
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^TestPrepareActionsForTransferCancelsQueueAndPreservesHistory$' ./services/repository/
```

转移用例同时断言：活动任务、专属 Runner、部署密钥阻止转移；后续事务失败时排队任务和注册令牌状态回滚；成功准备时旧队列取消、旧注册令牌失效、历史完成任务保留；旧注册令牌不能再注册，目的方新令牌可以注册。私有来源用例断言触发者撤销读取权前凭据有效、撤销后无效。群组用例通过真实 `CreateGroup` 创建 `IsActive=false` 的正常组织，并证明任务仍能领取。

相关包宽测：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -p 1 ./models/actions ./models/secret ./models/perm/access ./models/governance ./services/actions ./services/auth ./services/repository ./routers/api/actions ./routers/api/v1/repo ./routers/web/repo ./routers/web/repo/setting ./routers/api/v1/shared ./routers/web/shared/actions
```

结果：退出码 0；13 个目标包均通过或无测试文件。`git diff --check` 退出码 0。全仓 `make lint-go` 的原有问题与本次增量检查由阶段验收另行记录，不把此处包测试视作全仓通过。

## 组织标签转移的数据保全补充

使用真实 `AcceptTransferOwnership`，给 `repo3/org3` 的 `issue6` 增加来源组织 `orglabel3` 的当前关联及标签操作历史，再转给 `user1`。修复前运行：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^TestAcceptTransferOwnershipRejectsOldOrganizationLabelLoss$' ./services/repository/
```

结果退出码 1：转移返回 `nil`，断言同时报告“标签历史不可在转移时删除”和“标签关联不可在转移时删除”；仅保留历史、不保留当前关联的子用例也报告历史被删。这是完整服务调用的确定性 SQLite 复现，未把它记作 PostgreSQL 并发证据。

修复在转移的同一治理事务、改 owner 之前检查旧删除语句会影响的两类数据。命中后返回带说明的冲突，让用户保持当前归属；移除了原有的破坏性删除。修复后运行：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^Test(AcceptTransferOwnership(RejectsOldOrganizationLabelLoss|KeepsRepositoryLabels)|TransferOwnership)$' ./services/repository/
```

结果退出码 0，`ok gitea.dev/services/repository 1.409s`。正例证明 repo3 自己的标签关联与标签历史保留且转移成功；负例证明来源组织标签的当前关联或仅历史均会阻止转移，仓库归属及转移申请未改变。

## 群组移动后的 Actions 信任失效

通过真实 `MoveGroup` 复现：根群组 Owner 原先继承了子群组管理权，取得子群组 Runner 注册令牌；把子群组移到另一根后，该 Owner 失去管理权，但旧令牌仍可用。固定单测在修复前执行：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^TestMoveGroupInvalidatesInheritedOwnerRunnerToken$' ./services/governance/
```

结果退出码 1，`services/governance/group_test.go:147` 的旧令牌失效断言失败，实际 `IsActive=true`。另一个修复前固定回归 `TestRerunRejectsWorkflowCreatedBeforeGroupMove` 返回 `nil` 而非预期冲突，说明完成的旧工作流可在移动后重跑。这两项都是 SQLite 服务测试，不声称模拟数据库并发。

修复后，同一治理写事务中处理受影响群组子树：有运行中任务、群组／仓库专属 Runner 或部署密钥时明确拒绝；否则取消等待队列、停用群组及仓库旧注册令牌、使旧 Run 永久失效，并给受影响仓库的 Actions 范围修订号递增。历史已完成任务保留，可通过移动后的新触发建立新 Run。同根改父级也执行这一边界，移出再移回不会恢复旧 Run 的重跑资格。以下服务及迁移回归退出码 0：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test ./services/actions ./services/governance ./services/repository ./models/actions ./models/migrations/v1_27
```

该次结果分别为 `services/actions 1.993s`、`services/governance 10.614s`、`services/repository 2.854s`、`models/actions 1.287s`、`models/migrations/v1_27 0.462s`，均 `ok`。之后新增的同根改父级定向用例也单独通过；完整包测试是在该新增用例之前运行。迁移 `AddActionsScopeSafety` 的测试从不含新列的旧表结构升级，验证旧 Run 默认未失效、仓库修订号默认 0；不能把该测试等同生产升级演练。

PostgreSQL 使用独立测试库与 MinIO，运行下列定向集成测试（本地密码仍仅由环境变量提供）：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH \
GITEA_TEST_DATABASE=pgsql TEST_PGSQL_HOST=127.0.0.1:15433 \
TEST_PGSQL_DBNAME=gitea_phase1_test TEST_PGSQL_USERNAME=phase1_test \
TEST_PGSQL_SCHEMA=public TEST_MINIO_ENDPOINT=127.0.0.1:19000 \
go test -v -run '^TestGroupMove(InvalidatesActionsTrust|OrdersConcurrentRunCreation)$' ./tests/integration/
```

结果两项 `PASS`。第一项实际经过 `MoveGroup`，验证旧令牌不能再注册、旧队列取消、旧完成 Run 失效，以及新归属管理者取得新令牌并注册、新触发的任务可领取。第二项在 `action_run_job INSERT` 后通过 XORM hook 和通道固定住尚未提交的旧 Run，待真实 `MoveGroup` 提交后放行，`PrepareRunAndInsert` 返回冲突且不留下 Run；没有依赖睡眠碰时序。它仅覆盖这一指定交错顺序；反向顺序由末端治理写锁与同一数据库事务的提交顺序约束，尚无独立运行时屏障用例。测试使用隔离 PostgreSQL，不是实际 Runner 网络交换或生产容量验收。

`InsertRun` 先捕获仓库归属、命名空间与 Actions 范围修订号，Git／工作流处理结束后，在外层数据库事务末端取得短治理写锁并重读比较；锁持有到外层提交。移动和转移都在同一写边界提高修订号、使旧 Run 失效，因此 A→B→A 的旧快照也不能误通过。`ScopeInvalidated` 写入只修改该列，旧 Run 对象的普通更新不会清掉标记；最终领取、任务 token 和重跑均重检该标记。这里的修订号只覆盖本批改变 Actions 信任来源的群组移动及仓库转移，不是完整祖先权限继承版本平台。

本批增量 lint 命令退出码 0、`0 issues`，`git diff --check` 退出码 0：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH /Users/archer/go/bin/golangci-lint run --new-from-rev=HEAD ./services/governance ./services/actions ./services/repository ./models/actions ./models/repo ./models/migrations/... ./tests/integration
git diff --check
```

## 原生 schedule 与重跑身份补充

原生定时任务的 `TriggerUserID` 是系统身份 `ActionsUserID(-2)`，而普通任务的凭据终检会把它交给 `GetUserByID`；正常 schedule 因找不到数据库用户而失效。修复前先添加带持久 `ActionSchedule`、Run、Job、Task 的固定回归：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^TestScheduledTaskCredentialValid$' ./models/actions/
```

修复前退出码 1，`models/actions/task_test.go:67 Should be true`；修复后退出码 0。此红例固定的是正常计划任务的凭据有效性及领取中调用的终检，不是独立的真实 Runner RPC 领取红例。

系统身份现在只在 Run 关联的持久计划仍属于当前仓库、当前 owner、当前 `ActionsScopeRevision` 时有效。迁移 371 为已有 `ActionSchedule` 增加默认 0 的 `ScopeRevision`；计划检测先在锁外读 Git／解析工作流，再于短治理写事务中复核仓库快照并原子清理、重建计划。移动与仓库转移在同一事务清除旧计划/spec，并递增仓库修订号；已装载在内存中的旧 spec 会在 `InsertRun` 的末端治理边界重读计划并拒绝，旧计划移回原父级仍不能恢复。`startTasks` 跳过已失效快照或该边界的冲突，避免一条旧 spec 阻塞后续仓库。群组移动服务测试覆盖旧计划删除、移回 ABA 拒绝、新计划可建立 Run，以及旧异步检测不能删除移动后新计划。

重跑沿用 Run，但新任务属于新的 `ActionRunAttempt`。修复前凭据有效性及私有来源读取都沿用旧 `ActionRun.TriggerUserID`，会误拒绝原触发者撤权后由当前合法操作者发起的重跑；现在按任务 Job 的 `RunAttemptID` 选择当前 Attempt 触发者，旧式无 Attempt 任务才退回 Run 触发者。定向正反例验证合法新操作者通过、当前操作者停用或无私有源读取权时拒绝。

修复后以下六个包完整单测均退出码 0：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test ./models/actions ./models/migrations/v1_27 ./models/perm/access ./services/actions ./services/governance ./services/repository
```

结果依次为 `ok` 1.583s、0.859s、1.404s、2.242s、10.713s、3.038s。迁移回归验证旧 schedule 行升级后 `scope_revision=0`，并保留原 Run/仓库默认值断言；没有在真实历史生产库上执行升级。

新增 PostgreSQL 固定屏障 `TestGroupMoveOrdersConcurrentScheduleRunCreation` 退出码 0：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH \
GITEA_TEST_DATABASE=pgsql TEST_PGSQL_HOST=127.0.0.1:15433 \
TEST_PGSQL_DBNAME=gitea_phase1_test TEST_PGSQL_USERNAME=phase1_test \
TEST_PGSQL_SCHEMA=public TEST_MINIO_ENDPOINT=127.0.0.1:19000 \
go test -v -run '^TestGroupMoveOrdersConcurrentScheduleRunCreation$' ./tests/integration/
```

XORM hook 在旧计划的 `action_run_job INSERT` 后固定未提交 Run；真实 `MoveGroup` 提交清理计划和范围变更后放行，旧 Run 返回 `ErrConflict`、没有持久落库，旧计划已删除。测试输出为 `--- PASS: TestGroupMoveOrdersConcurrentScheduleRunCreation (0.84s)`、`PASS`、`ok gitea.dev/tests/integration 2.556s`。这是单一指定时序的真实隔离 PostgreSQL 验证，不代表全部并发排列或实际 Runner 网络执行。

最终增量 lint 在 `models/actions`、迁移、权限、Actions、群组、仓库和集成测试目录运行，退出码 0、`0 issues.`；`git diff --check` 退出码 0。父代理另行负责独立宽测、真实 UI/Runner 与总体审查。

## 扩展集成失败定位（当前代码未因本节改动）

在隔离源码副本 `/Users/archer/.cache/gitea-phase1-integration-lco96594` 和同一隔离 PostgreSQL／MinIO 中重跑两项失败子例；下面只保留准确断言及首因，不把二次失败写成全部宽测结论。

`go test -count=1 -v -run '^TestScheduleUpdate/PullMerge/merge$' ./tests/integration/` 退出码 1；正则同时匹配 `merge` 与 `rebase-merge` 两个子例。两个子例的内部 `/api/internal/hook/post-receive/...` 均返回 HTTP 500，首错是 `failed to set pr to merged`，底层 `governance.ErrConflict`（“数据已变化或相关合并尚未完成”）。之后在 `actions_schedule_test.go:274` 找不到期望合并提交的 `ActionSchedule`。`HookPostReceive` 在 `PushUpdates` 和 Actions 通知前，先调用 `SetMerged`；该调用的 `WithWrite(repo,pull,issue)` 命中仍有效的合并 reservation，提前返回，所以 schedule 的缺失是下游结果。本批未修改 `routers/private/hook_post_receive.go`、`services/pull/merge.go` 或治理 reservation 逻辑，且 HEAD 中相同调用和资源检查已存在；这项不是本批 schedule 修复引入的失败。尚未在独立 HEAD 二进制上执行对照，不把静态 HEAD 等同性说成运行基线通过。

`go test -count=1 -v -run '^TestActionsScopedWorkflows$/^Deletion_cleans_up_source_registration$' ./tests/integration/` 退出码 1。测试中注册来源成功，`DELETE /api/v1/repos/user2/sw-delete-source` 返回 204，但 `actions_scoped_workflow_test.go:545` 立即断言来源注册不存在失败。当前且 HEAD 相同的 API 路径只调用 `ScheduleRepositoryDeletion`：把来源仓库归档并加入可恢复删除计划；实体到期由 `DeleteRepositoryDirectly` 清理，才删除 `ActionScopedWorkflowSource`。因此该断言与已有延迟删除语义不符，不能据此认定本批新回归。

独立发现的相邻风险：归档／待删除来源的注册应保留供恢复，但 `processScopedWorkflows` 只跳过空来源，没有检查 `IsArchived`；`LoadParsedScopedWorkflows` 仍可读其默认分支，消费者仍可能新建 scoped Run。`InsertRun` 和任务终检目前只核对消费者仓库生命周期。此路径已反馈父代理，需补负向运行证据和相应终检后才能把生命周期保护列为已验收。本节不把该项标记为已修复。

## Scoped 工作流来源生命周期补证

前节记录的是修复前状态。后续固定的服务红例在来源归档后仍得到凭据有效 true；隔离 PostgreSQL 的删除语义红例在 API 返回 204、来源进入可恢复归档后仍创建一个消费者 Scoped Run。旧断言“API 删除立即清除注册”与延迟删除语义冲突，现改为验证归档期间注册保留、运行暂停、到期实体清除后注册才移除。

来源可执行性现在单独核对仓库 Ready、非空、未归档、来源拥有者及其祖先未归档／待删除、注册所属范围和来源修订号。必选状态检查配置仍读取原注册；来源不可用不会因过滤注册而移除必选检查。检测阶段跳过不可执行来源；InsertRun 在末端短治理写事务重读来源并比较持久化的 WorkflowSourceScopeRevision，任务领取、Token 终检和重跑也核对该修订号。来源 A→B→A 后旧注册不会重新激活；管理员需在当前归属下移除旧注册并重新添加。迁移 343 给旧注册和旧 Run 补默认 0 修订号；旧注册对应来源当前修订号已大于 0 时暂停执行，但配置仍保留。

设置页在旧注册失效时只显示来源 ID、移除入口和重新确认提示，不再按旧归属展示来源名称及工作流清单；旧注册的添加和必选配置 POST 返回明确 400 提示，配置写事务末端重读修订号。暂时不可用的来源单独显示暂停提示，恢复后在范围修订号未变化时可继续使用原注册。这里验证了页面/API 返回与模型状态，未以真实浏览器鼠标完成现场验收。

修复后定向单元与迁移命令均退出码 0：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^(TestGetEffectiveScopedWorkflowSources|TestScopedWorkflowSourceValidLifecycle|TestScopedWorkflowSourceCRUD|TestPrepareRunAndInsertRejectsChangedScopedSource|TestScopedTaskCredentialRejectsArchivedWorkflowSource)$' ./models/actions/ ./services/actions/
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^TestAddActionsScopeSafety$' ./models/migrations/v1_27/
```

隔离源码副本中的 macOS gitea 二进制重建退出码 0。以下 PostgreSQL 命令省略本地测试密码，实际运行时通过环境变量提供。来源状态修复后的完整 TestActionsScopedWorkflows 退出码 0，耗时 102.347 秒；该轮完整测试发生在最后的注册修订与设置页补丁之前，不能视为最终源码的全量结果。新增固定屏障 TestScopedSourceArchiveOrdersConcurrentRunCreation 退出码 0。屏障在消费者 Job 插入后挂住未提交 Scoped Run，真实来源归档提交后放行，Run 返回冲突并回滚，注册仍存在；没有使用睡眠碰时序。

```sh
GITEA_TEST_DATABASE=pgsql TEST_PGSQL_HOST=127.0.0.1:15433 TEST_PGSQL_DBNAME=gitea_phase1_test TEST_PGSQL_USERNAME=phase1_test TEST_PGSQL_SCHEMA=public TEST_MINIO_ENDPOINT=127.0.0.1:19000 go test -count=1 -run '^TestActionsScopedWorkflows$' ./tests/integration/
GITEA_TEST_DATABASE=pgsql TEST_PGSQL_HOST=127.0.0.1:15433 TEST_PGSQL_DBNAME=gitea_phase1_test TEST_PGSQL_USERNAME=phase1_test TEST_PGSQL_SCHEMA=public TEST_MINIO_ENDPOINT=127.0.0.1:19000 go test -count=1 -run '^TestScopedSourceArchiveOrdersConcurrentRunCreation$' ./tests/integration/
GITEA_TEST_DATABASE=pgsql TEST_PGSQL_HOST=127.0.0.1:15433 TEST_PGSQL_DBNAME=gitea_phase1_test TEST_PGSQL_USERNAME=phase1_test TEST_PGSQL_SCHEMA=public TEST_MINIO_ENDPOINT=127.0.0.1:19000 go test -count=1 -run '^TestActionsScopedWorkflows/Stale_source_registration_requires_reconfirmation$' ./tests/integration/
```

最后一项设置页/API 子例在修正预期 HTTP 状态（JSONError 正常返回 400）后退出码 0，耗时 6.829 秒。最终源码又单独执行归档固定屏障、延迟删除子例，分别退出码 0，耗时 2.651 秒和 7.618 秒。`make fmt GO=/Users/archer/.cache/gitea-governance/go/bin/go` 退出码 0；增量 Go lint 覆盖模型、迁移、Actions、设置路由及集成测试，退出码 0、0 issues；`git diff --check` 退出码 0。本节不涵盖 I-04 全局必选检查在无分支保护规则时的现有放行缺口，也不能替代真实 Runner 与生产升级验收。

## Scoped 设置写入边界补证

设置路由在请求入口拿到的管理员或群组 Owner 权限可能在 Git 读取或表单解析期间失效。针对 Add、Required、Remove 三个入口，新增路由定向测试先保存旧的操作者快照，再撤销真实 Owner 团队成员关系；修复前 `go test -run '^TestScopedWorkflowSettingsWriteRejectsStalePermission$' ./routers/web/shared/actions` 退出码 1，三个撤权子例均错误地执行写入回调。修复后同一包的 `go test -count=1 ./routers/web/shared/actions` 退出码 0，另有三个处理器子例验证缓存管理员身份在数据库降权后均返回 403、注册状态未被改写；当前合法 Owner 正例仍允许写入回调。

三个写入口现均在短治理事务内重载当前用户并核管理员、个人所有者或群组 Owner 权限，同时把全局 `instance:0`、组织／个人范围 `group:<id>` 与来源仓库资源纳入占用检查。对应四种资源的固定 reservation 测试均阻止写入回调。Add 在事务内重读来源归属和修订号；Required 保留事务内来源修订终检；Remove 可清理失效来源注册。资源占用返回 409，来源修订变化返回重新确认提示。Git 读取仍在事务外。局部 `gofmt`、`git diff --check` 退出码 0，`go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./routers/web/shared/actions` 退出码 0、0 issues。本节仅是定向 SQLite 验证；最终合并代码的 PostgreSQL Scoped 整组由父任务另行复核。
