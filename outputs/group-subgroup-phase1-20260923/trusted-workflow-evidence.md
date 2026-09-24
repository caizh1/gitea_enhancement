# 统一工作流可信门禁阶段证据

## 已实现并验证的边界

- 最终合并授权从 `services/pull/merge.go` 的 `AuthorizePullMerge` 回调读取当前策略与固定 PR head。普通同名提交状态不能替代 scoped Run；无保护分支及禁用仓库 Actions 也不会移除必选来源配置。
- 有效来源查询包含当前拥有者的真实祖先链及实例来源，不包含共享群组。来源注册失效时保留必选配置；设置页将管理来源与只读继承来源分开展示，对无代码读取权的操作者仅显示来源仓库编号。
- 每次必选配置写入递增来源配置修订。运行插入时保存有效注册编号与配置修订；最终门禁校验来源身份、范围修订、消费仓库、head 提交、来源提交记录、最新有效 Run、最新 attempt 与全部必选任务模式。删除重建注册、旧配置运行和较早成功运行均不能替代当前证明。
- 启用必选项前安装并回读来源仓库的引用事务入口；旧的必选配置若缺入口，合并失败并要求重新保存配置。最终授权前从真实 Git 默认分支读取来源 SHA，数据库短事务内核对必选来源全集、注册/配置修订、引用修订、最新可信 Run 的来源 SHA，并显式检查来源仓库已有引用事务占用；通过后来源仓库加入持久合并占用。来源 YAML 删除或任意默认分支提交变化会使旧 Run 失效，其他引用变化只使并发快照重试。状态预检查也读真实来源 SHA 和待决引用占用。
- 祖先必选工作流领取时仅允许配置作用域及其祖先/实例管理的 Runner；实例必选仅允许实例 Runner。仓库 Runner 与更下级 Runner 仍可执行普通工作流，但不能为上级必选项提供成功证明。任务凭据使用时重检来源；最终门禁核对最新 Attempt 的实际 Task、Runner 来源以及复用任务的 `SourceTaskID`，避免旧成功记录绕过。
- 原生分支保护状态检查仍走原生 status；必选 scoped 工作流由 Run 证明。配置页中英文说明及本地/继承来源缺少引用事务入口时的诊断已对应更新。
- 新 scoped 提交状态上下文使用 `scoped:<来源编号>`，不持久化私有来源仓库名；合并门禁仍兼容旧 `owner/repo` 模式。状态预览将已保存的旧来源前缀转换为编号，Actions 列表按代码读权匿名化来源。旧状态在 PR、提交、分支、议题、搜索与状态 API 输出前，对仍能由当前注册或 Run 关联确认的来源按读权脱敏。
- 启用必选配置前取得来源仓库工作锁，锁覆盖读取工作流、安装引用事务入口及配置落盘；Git 默认分支名和数据库不一致时固定诊断并拒绝启用，与默认分支变更共享同一锁。

## 已执行检查

- `go test ./models/actions/ ./services/pull/ ./services/governance/ ./routers/web/shared/actions/ ./routers/web/repo/actions/ ./services/actions/ ./models/migrations/v1_27/`：七个包均退出 0。
- `go test -run '^TestRequiredScopedSourceReferenceSnapshot$' ./services/pull/`：退出 0；覆盖真实 Git SHA、缺失来源 hook、旧来源提交、Git 写入前的 prepared 占用、引用 ABA、配置变化及快照后新增必选来源。
- `go test -run '^TestScopedWorkflowSetRequiredInstallsSourceReferenceHook$' ./routers/web/shared/actions/`：退出 0；覆盖设置页启用必选时安装并回读来源 Git 引用事务入口。
- `TestRequiredScopedRunnerTrustBoundary` 初次红例：子组 Runner、仓库 Runner 和群组 Runner 违反实例必选交集共三项失败；修复后 `go test -run '^TestRequiredScopedRunner(TrustBoundary|ClaimAndCredential)$' ./models/actions/` 退出 0。`TestScopedRunRunnerProofFollowsReusedTask` 退出 0，核对复用任务来源。
- `go test -c -o /tmp/group-phase1-integration.test ./tests/integration/`：仅编译通过，未运行集成测试，也未使用共享 PG 测试库。
- 中英文 locale JSON 解析、`git diff --check`：退出 0。
- `go test ./routers/web/repo/ ./routers/api/v1/repo/ ./services/actions/ ./services/pull/ ./routers/web/shared/actions/ ./routers/web/repo/actions/`：六个包退出 0；其中新负例覆盖旧来源名配置匹配、opaque 新状态，以及无来源代码读权时旧状态脱敏。
- 定向 `golangci-lint`：新建门禁文件无报错；全命令仍因 `models/actions/governance_audit.go`、`models/actions/runner.go`、`services/governance` 的已有/并行文件（含本次接入的 `merge_approval.go` 内原有未使用函数）以及 `services/pull/protected_branch.go` 共十三条问题退出 1，不能记作 lint 通过。

## 尚未执行的真实验收

源码单测与集成编译不能替代真实 PR、Runner 和页面操作。Git 引用事务入口的正常推送、失败恢复与多仓库并发仍需在共享 PG 验收库空闲后运行集成用例；生产环境未验证。来源默认分支即使只有无关提交也要求该 PR head 生成新 Run，这是当前保守策略，配置页已说明。默认分支名称切换的授权后窗口尚待仓库服务统一拒绝必选来源切换；完成前不能宣称最终并发边界闭合。项目按从零部署设计；若导入更早版本留下的状态，来源仓库已删除且状态又没有 Run 链接时无法可靠还原其身份，该类未知历史记录未做迁移脱敏。

## 未执行验收

新增真实 PR/Runner 集成用例覆盖无保护分支、fork `pull_request_target` 的 head 绑定，以及路径过滤只产生 skipped 状态时仍阻断合并；当前仅完成编译。真实内网模型与生产环境均未验收。

## 任务凭据撤权复查（追加）

- 本机 SQLite 红例 `GITEA_TEST_DATABASE=sqlite3 go test -count=1 -run '^TestScopedWorkflowRunDoesNotReviveAfterSourceReRegistration$' ./models/actions` 退出 1，失败点是来源移除后以同仓库重新注册，旧运行仍被判为有效。修复后同命令退出 0；原注册配置普通编辑仍有效，新注册生成的运行仍有效。
- `GITEA_TEST_DATABASE=sqlite3 go test -count=1 ./models/actions` 退出 0，包含原 Runner 可信边界用例；其手造运行夹具补齐了生产插入时保存的来源注册编号。
- `golangci-lint run ./models/actions` 在配置正确的 Go PATH 下退出 1，报 `models/actions/governance_audit.go` 的未使用函数及 `models/actions/runner.go` 的 `perfsprint`，均不在本次追加修改文件；不能记为 lint 通过。`git diff --check` 对三个追加 Go 文件退出 0。
- 已顺着 Basic、OAuth2、V3/V4 Artifact 与仓库权限最终查询复查：任务 token 入口与后续仓库权限都会重新校验任务状态、来源注册身份和来源读取权限；V4 签名上传/下载链接也在请求时重校验。对象存储直出 URL 一旦签发，不再经过 Gitea 的撤权检查，其短期有效期是独立边界。
