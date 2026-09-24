# 推送事件提交与定时计划边界修复证据

## 首因与修改前状态

真实三次推送的 `payload.after` 各不相同，异步消费生成的 Run 63～65 却均记录最终 HEAD，原始事实见 `push-run-head-coalescing-evidence.md`。生产者 `services/repository/push.go` 和 `services/actions/notifier.go` 已把每次推送的 `NewCommitID` 放入 `PushPayload.After`；首因在 `services/actions/notifier_helper.go` 重新按引用读取消费时 HEAD。修改前文件保存在私有目录 `/Users/archer/.cache/gitea-phase1-v5-validation/push-event-before-1790202515`。本轮未操作运行实例、浏览器或共享 PostgreSQL，也未提交、推送、重置或清理工作树。

## 实现边界

- `push` 工作流按校验过的事件 `after` 读取提交，绑定普通与镜像通知的 Run SHA、工作流内容、标题、过滤状态和祖先工作流的消费仓库提交。无效或不存在的 `after` 拒绝回退到现时 HEAD。手动、PR、`pull_request_target`、定时事件仍沿原路径；祖先工作流来源仓库仍独立按当前可信配置加载。
- 默认分支定时计划始终按消费时当前 HEAD 检测；`[skip ci]` 只跳过本次 push Run，不阻止清除或替换定时计划。Actions 系统身份检测到零个 cron 时也会清旧计划。
- 计划替换在治理事务内锁定持久默认分支行并核对 SHA、默认分支、仓库范围和生命周期。计划入队预检当前分支；Run 插入与 Runner 最终领取在各自写事务内复核并锁住同一行。已领取任务每次权限核验也比较计划 ID、Run 引用和 SHA、当前默认分支 SHA/删除状态。PostgreSQL/MySQL 使用当前值锁读，SQLite 写事务内用条件更新获取写锁，不依赖各数据库不同的受影响行数语义。
- post-receive 分支同步及全量分支同步共用独立的逐仓 `branch_sync` 锁，避免较早回调在较晚回调之后按旧 payload 倒写分支。锁内、数据库事务外读取现时 Git ref/commit，再用短事务同步；删除后重建同样以现时 ref 为准。此锁与 Web Git 操作可能持有的 `repo_working` 键不同，避免 Git push 等待 hook 时自锁。公开全量同步入口覆盖后台队列、UI/API 空分支回退、镜像和新仓初始化；遇到已有 DB 事务会拒绝等待该锁。

## 固定红绿与已运行命令

`TestPushEventUsesAfterCommitAndCurrentSchedules` 使用隔离 Git 仓库夹具先后推送 H1/H2，消费 H1 事件。修改前 Run SHA、工作流 SHA、标题指向 H2，固定红例成立。增补 `[skip ci]` 删除 cron 后，旧计划没有清理，固定红例成立。增补旧 H1 检测暂停、H2 计划已写后旧 H1 替换，原代码未拒绝，固定红例成立。现断言 H1 Run 与提交状态、H2 定时计划、旧检测拒绝、引用删除与非法 SHA。

`TestSyncBranchesToDBUsesCurrentRefForLateHooks` 用真实隔离 Git ref 验证顺序晚到回调不会使 H2 回退，并验证删除后重建不会被晚到删除误标。该用例是**顺序旧回调**证据，未单独证明两个同步调用的并发屏障。

`TestScheduleBranchAuthorizationLock` 在 `tests/integration` 中准备旧计划和 Run。SQLite 路径已通过当前分支、H2、删除与行不存在的正反检查；PostgreSQL 路径额外用独立事务的 `lock_timeout` 与明确释放屏障，证明领取阶段的分支行锁阻止同时更新。真正 PostgreSQL 尚待父任务执行，不能将 SQLite 结果算作 PostgreSQL。

- `/Users/archer/.cache/gitea-governance/go/bin/go test -tags='bindata sqlite sqlite_unlock_notify' -run '^TestScheduledTaskCredentialValid$|^TestPushEventUsesAfterCommitAndCurrentSchedules$|^TestPrepareRunAndInsertOrganizationTriggerCannotQueue$|^TestRerunRejectsWorkflowCreatedBeforeGroupMove$' -count=1 ./models/actions ./services/actions`：两包通过，分别 1.224 秒和 1.957 秒。
- `/Users/archer/.cache/gitea-governance/go/bin/go test -tags='bindata sqlite sqlite_unlock_notify' -run '^TestSyncBranchesToDBUsesCurrentRefForLateHooks$|^TestScheduledTaskCredentialValid$|^TestPushEventUsesAfterCommitAndCurrentSchedules$|^TestPrepareRunAndInsertOrganizationTriggerCannotQueue$|^TestRerunRejectsWorkflowCreatedBeforeGroupMove$' -count=1 ./models/actions ./services/actions ./services/repository`：三包通过，分别 1.224 秒、2.127 秒、1.732 秒。
- `/Users/archer/.cache/gitea-governance/go/bin/go test -tags='bindata sqlite sqlite_unlock_notify' -run '^TestScheduleBranchAuthorizationLock$' -count=1 ./tests/integration`：补完 PG 失败路径的超时/释放清理后重跑 SQLite 集成顶级用例通过，1.864 秒。
- `/Users/archer/.cache/gitea-governance/go/bin/go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 ./models/actions ./services/actions ./services/repository ./modules/repository ./routers/private`：除 `services/repository` 包两个既有默认分支测试外其余四包通过。两测试首因是仓库根 `gitea` 为 Linux 二进制，Git reference-transaction hook 报 `Exec format error`，不是本次同步断言。原始失败保存在私有 `/Users/archer/.cache/gitea-phase1-v5-validation/branch-package-test.log`；父任务将在 macOS 隔离构建副本复验。
- `PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH /Users/archer/go/bin/golangci-lint run --new-from-rev=HEAD ./models/actions/... ./services/actions/... ./modules/repository/... ./services/repository/... ./routers/private/...`：增量检查通过，`0 issues`。定向 `gofmt` 与 `git diff --check` 通过。

以上均为测试源码/SQLite 证据，尚未完成本补丁的真正 PostgreSQL、实际 Runner 和 UI/Git 客户端验收。

## 对抗审查与明确限制

当前 Git ref 改变到 post-receive 持久分支表提交之间仍有窗口；本批计划与凭据授权的线性化点是**持久分支更新**，不能宣称 Git ref 变化瞬间撤回，也不能追回此前已合法下发给 Runner 的 Secret。当前 `branch_sync` 的默认内存锁以本期单应用节点部署为前提；跨实例需要共享全局锁配置及真实验收。重复同一 HEAD 的通知仍可能替换等价计划并取消当时合法运行，属于既有重复通知限制，未在本批改成幂等。未安装 reference hook 时，手动删除分支的 Git 删除与直接数据库标删之间仍可能和重建同步交错，使活跃分支短时误标删除；当前计划最终校验在此情况下拒绝旧任务，但原生分支列表状态的恢复需后续单独修复/验收。同步时显示的推送者也可能属于晚到回调，当前 SHA 本身仍来自最新 Git ref。

当前源码与测试已按父任务要求冻结；trusted_gate 独立终审为 PASS，未发现成立 P0/P1。真正 PostgreSQL 屏障待父任务执行；未据此宣称一期产品完成。目标容量环境未提供，人工节省 80% 未实测。
