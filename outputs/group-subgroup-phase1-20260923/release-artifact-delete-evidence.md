# Release 附件与 Actions 产物删除验收及归档缺口

日期：2026-09-24。所有现场操作仅针对新建隔离仓库 `phase1-parent/child/phase1-asset-delete-1790186106`（数据库仓库 ID **19**），没有改动冻结十仓、父级策略或其他代理仓库。完整脱敏逐项记录见 `release-artifact-delete-v13.json`；脚本 `release-artifact-delete-acceptance.py` 使用交互式密码输入，文件和记录均不含凭据。后续父任务已接管仓库 19 的 UI 验收，本代理不再写该活跃实例。

## v13 真实协议观察

- 原生 Release API 上传两份各 67 字节附件。Reporter 删除返回 403，无关用户返回 404，随后原对象仍可下载且摘要一致。Owner 删除 `remove.txt` 返回 204，旧下载地址返回 404，`retain.txt` 仍可下载。保留页：`http://127.0.0.1:13043/phase1-parent/child/phase1-asset-delete-1790186106/releases/tag/v1-delete-check`。
- 真 Runner 完成 Run 78 并上传三份 v3 产物。Reporter、无关用户的 Web 删除均返回 404；Owner 删除 `phase1-remove` 返回 200，旧下载返回 404，`phase1-retain` 仍可下载且 ZIP 内容一致。保留页：`http://127.0.0.1:13043/phase1-parent/child/phase1-asset-delete-1790186106/actions/runs/78`。原生公开 REST 按 ID 删除仅支持 v4；不能以 v3 的 REST 404 充当权限结论。
- **首次可达红例**：只归档仓库 19 后，Owner 对 Run 78 的 `phase1-archive-probe` 执行 Web 删除仍返回 200，数据库产物状态变为 5；对另一次已完成 Run 80 执行 REST 删除仍返回 204，运行记录消失。归档只读约束未覆盖这些入口。脚本随后恢复仓库 19，保留附件与产物再次下载均成功；Run 78 的 `phase1-retain` 未删除。

## 源码因果链与修复

Web `ArtifactsDeleteView`、API `DeleteArtifact` 原先直接调用模型状态更新；Web/API `DeleteRun` 原先在入口读完成状态，服务层先在锁外取任务与产物快照，最终写入没有重验当前操作者、归档或最新 Run 状态。路由的 Actions 写权限不会因归档自动降权，因此归档后仍可永久修改记录和排队删除对象。

现在用户删除走 `RequestArtifactDeletionByID`、`RequestArtifactDeletionByRunAttempt` 或带当前操作者的 `DeleteRun`，在同一治理短事务中复核当前授权、仓库及祖先生命周期、目标仓库与 Run/attempt 绑定；`DeleteRun` 同事务重取最新 Run 状态并构造任务、产物和延迟清理快照，网络存储删除仍由既有清理机制在事务外完成。Web 产物删除按钮在生命周期只读时隐藏。模型底层删除函数保持给到期清理使用，不受人类写入守卫改变。

## 固定测试及边界

- 修复前新增 SQLite 测试 `TestArchivedRepositoryRejectsUserActionsDeletion` 四个子例均红：Web 产物 200、REST v4 产物 204、Web Run 200、REST Run 204，预期均为 423，且原产物状态或 Run 已改变。该固定测试不访问活跃实例。
- 修复后运行：`go test -tags='bindata sqlite sqlite_unlock_notify' -p 1 -count=1 -run '^(TestArchivedRepositoryRejectsUserActionsDeletion|TestActionsDeleteRunRejectsStaleCompletedSnapshot|TestActionsDeletionRechecksCurrentActor|TestExpiredV4ArtifactUserDeletion|TestActionsDeletionRejectsForeignTargets|TestAPIActionsWorkflowRun/DeleteRun(CheckPermission|Running|General)|TestActionsArtifactV4DeletePublicApi)$' ./tests/integration`，**通过，2.362 秒**。但复查 `-v` 实际选中列表后确认：该表达式把 `/` 放在顶层括号内，Go 测试选择器没有选中 `TestAPIActionsWorkflowRun` 的删除子测试；这条结果仅证明归档四入口、陈旧快照、撤权、伪造 ID、过期 v4 和原生 v4 产物删除，**不能作为原生 Run 删除正例的通过证据**。v4 expired 原模型仅更新 confirmed，可能返回 204 却保持可访问；现仅在用户服务内将 confirmed/expired 都标记待删，内部清理路径不变。
- 父任务随后真正运行 PG `TestAPIActionsWorkflowRun` 全项，`DeleteRunGeneral` 首次由原期望 204 变为 403（`v14-actions-delete-pg.jsonl`）。反证确认固定仓库 2 缺 `TypeActions` 的 `RepoUnit`：路由旧 fixture 可进入，但最终 `CheckRepositoryContentWrite` 按当前单位权限拒绝；之前只补 owner 命名空间仍不足。已仅在该旧正例 fixture 插入 Actions 单元，生产授权不变。独立 SQLite 明确选中 `go test -tags='bindata sqlite sqlite_unlock_notify' -p 1 -count=1 -run '^TestAPIActionsWorkflowRun$/^DeleteRun(CheckPermission|Running|General)$' -v ./tests/integration`，**三项均通过，1.528 秒**；其中 `DeleteRunGeneral` 实际 DELETE 首次 204、二次 404。父任务需同步该 fixture 后重跑 PG，不能把本轮 SQLite 结果写成 PG 通过。
- `go test -tags='bindata sqlite sqlite_unlock_notify' -p 1 -count=1 -run '^$' ./services/actions ./routers/web/repo/actions ./routers/api/v1/repo` 通过。`golangci-lint run ./services/actions ./routers/web/repo/actions` 为 0 issues；加入 API repo 包时仅报本批以外工作树的 `routers/api/v1/repo/branch.go:9-10` 两项格式问题，本代理未修改该文件。`git diff --check` 对本轮目标文件通过。
- 扩大运行 `TestActionsDeleteRun` 时，测试 Git hook 调用仓库根目录预留的 Linux `gitea` 可执行文件，在 macOS 产生 `Exec format error`，仓库创建 500 后该测试连锁失败；这不是本轮删除断言的结果，未覆盖该二进制。定向 API 正例和直接服务测试均已通过。
- 父任务后续已完成 v14 同对象协议复验及真正 PostgreSQL 9 项顶层回归，见 [v14 父任务报告](parent-v14-actions-validation.md)。浏览器 UI 受测试确认框阻塞，Release 附件在归档或撤权的在途竞态仍未验证。另 Runner v4 `DeleteArtifact` 在 task 凭据中间件验权之后、底层状态更新之前没有最终治理写事务；归档恰在此窗口提交的交错尚未固定或实测，本批不改变 Runner 协议路径，也不据此声称 P1。PG 未占用。目标 Linux 性能环境及 80% 人工节省仍未验证。
