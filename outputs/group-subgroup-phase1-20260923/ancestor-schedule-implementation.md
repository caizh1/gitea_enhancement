# 祖先定时工作流实现核对（2026-09-24）

## 人工基线与目标

目标用户是群组负责人和后代仓库维护者。输入是来源仓库默认分支下注册的统一工作流、消费者仓库当前默认分支、有效群组来源、Actions 配置及到期 cron。原行为只为消费者本地工作流建立计划；没有本地 YAML 的后代仓库不能自动运行祖先 cron。人工逐仓复制 YAML 或插入 Run 不属于可接受流程。目标是在复用原生计划表和每分钟调度器的前提下，省去全部逐仓复制及手动触发，并保持误授权、跨仓来源 Secret 泄露为零。真实 Runner、十仓人工效率和容量指标留给总体验收，不在此记录中宣称达标。

## 本次实现

- `ActionSchedule` 保存来源仓库、来源提交、来源范围修订、注册配置修订与统一工作流标记；迁移编号为 377。计划仍写入原有 `action_schedule`/`action_schedule_spec`。
- 消费者默认分支检测与每分钟调度均可生成祖先计划；同一计划内容重复扫描保持原计划与 `Next`，来源、注册、消费范围或 opt-out 改变时重新检测。来源解析失败按来源隔离，消费者本级与其他健康来源的计划仍可更新；旧损坏来源计划会被移除。
- 到期规格按 ID 游标扫描，避免更新 `Next` 后分页跳过。`Next` 取当前时刻之后最近的一次 cron。`InsertRun` 现有事务的最终授权段对同一规格执行条件更新，且与 Run/Job 入库同事务；并发落败者回滚，不发通知。
- `ScheduledRunValid` 在建 Run、领取任务和后续凭据授权链上核对消费者计划、当前分支、来源仓库及其当前默认分支、注册身份与配置修订、归档状态、opt-out。Run 的执行仓库仍为消费者，凭据按消费者 `RepoID` 读取；来源仓库仅提供工作流内容。没有增加 `workflow_run` 行为。

## 已验证证据

1. 改动前定向红测：`TestScheduledScopedRunSourceBound` 编译失败，明确指出 `ActionSchedule` 缺少来源仓库、来源提交、来源范围、注册修订字段。
2. 改动后 `go test -run '^TestScheduledScopedRunSourceBound$' ./models/actions/` 通过；覆盖来源注册修订、删除后重注册、归档、消费者 opt-out、来源默认分支 SHA 变化的拒绝，以及有效状态正例。
3. `go test -run '^(TestNextScheduleTimeKeepsNextMinute|TestWithScheduleInEventPayload)$' ./services/actions/` 通过；固定 08:00:30 扫描时下次应为 08:01:00。
4. `go test -run '^$' ./tests/integration/` 编译通过。集成测试已写无本地 YAML 消费者自动建计划和 Run、重复扫描不重复、PostgreSQL 两个旧规格副本同时消费只一个成功、损坏可选来源时本级 push 与 cron 保持可用。完整执行由隔离实例统一进行。
5. `make fmt`、`git diff --check` 通过。`make lint-go` 已运行，但本机缺少 `golangci-lint` 可执行文件，在启动检查阶段失败，尚不代表代码 lint 通过。

定向测试原始输出保存在私有缓存 `/Users/archer/.cache/gitea-phase1-schedule-20260924/` 下的 `model-targeted.log`、`service-targeted.log`、`integration-compile.log`；三条命令退出码均为 0。`lint-go.log` 保存了缺少可执行文件的首错，退出码为 2。原先直接运行 SQLite 集成测试时，最早可复核的环境错误是 pre-receive hook 尝试执行工作树内旧 `gitea` 文件并报 `cannot execute binary file`，随后 API 创建仓库/文件失败；该次测试没有形成有效业务判定。

## 待总体验收

- 在隔离 PostgreSQL、真实 UI 和 Runner 上完成祖先来源注册、无本地 YAML 消费者自动产生 Run、Secret/变量来源与隔离、token 权限、原生 schedule、重复扫描、来源变更及撤权验收。
- 运行 PostgreSQL 并发用例并保留原始日志；本机 SQLite 集成运行曾被工作树旧 Linux `gitea` 二进制的 `cannot execute binary file` 阻断，不能据此判断业务测试成败。
- 本记录仅说明代码和定向检查，未验证用户要求的 80% 人工节省与生产容量。
