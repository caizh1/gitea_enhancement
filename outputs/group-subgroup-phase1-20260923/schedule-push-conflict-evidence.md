# 默认分支定时计划冲突与普通 Push 事件

日期：2026-09-24。范围仅为 `services/actions/notifier_helper.go` 和 `services/actions/push_event_test.go`；未操作共享 PostgreSQL、运行实例或 Runner。

## 首因与红例

`notify` 对默认分支 Push 先执行 `handleSchedules`，之后才检测事件工作流。`replaceSchedulesForRepo` 在短事务内要求 Git 当前 HEAD 与持久 Branch SHA 一致，且已准备的仓库 Owner、Namespace、ActionsScopeRevision、默认分支及归档状态仍有效。任一不符返回 `ErrConflict`；原实现立即返回，`Notify` 只记录错误，不会重试。连续 Push 中，Git HEAD 已到 H2、Branch 表仍为 H1 时，H1 的合法 Push Run 因计划冲突丢失。即使当前工作流没有 cron，默认分支 Push 仍执行该计划检查。

先增加固定场景，再运行：

`/Users/archer/.cache/gitea-governance/go/bin/go test -run '^TestPushEventUsesAfterCommitAndCurrentSchedules$' -count=1 ./services/actions`

原实现失败于 `push_event_test.go:83`：`require.NoError(t, notify(ctx, input))` 收到“数据已变化或相关合并尚未完成”。这固定了丢 Run 的首因。随后扩展的无 cron 样本在添加测试夹具时先后出现目录已被 `git rm` 移除、连续内容相同导致 Git 无提交，两处均为新测试夹具问题，已修正；并非产品故障。

## 最小修复与权限反证

仅当 `handleSchedules` 返回 `governance.ErrConflict` 时记录仓库和计划提交诊断，继续处理普通事件工作流；其他错误保持返回。冲突事务没有提交新计划，也不会把旧计划改成新 HEAD。测试覆盖历史 H1 Push 正确创建 H1 Run、无 cron 工作流同样创建 Run，以及计划未被冲突更新。

`ErrConflict` 同时可能代表 Owner、Namespace、ActionsScopeRevision、默认分支或归档变化。因此继续事件检测不等于授权旧 Run：`handleWorkflows` 经 `PrepareRunAndInsert` → `InsertRun` 的最终 `WithWrite` 重新读取仓库，比较 Owner、Namespace、ActionsScopeRevision 和归档状态，失败即回滚。本测试将 ActionsScopeRevision 更新后，用旧仓库快照通知，确认没有新增 Run。既有 `TestPrepareRunAndInsertRejectsChangedScopedSource` 也验证来源变化拒绝。已领取任务的凭据校验未在本补丁变更；本次没有给旧定时计划、旧 Owner 或已撤权工作流放宽授权。

修复后同一单测通过（`ok gitea.dev/services/actions 2.078s`）；联合定向命令通过（`ok gitea.dev/services/actions 1.833s`）：

`/Users/archer/.cache/gitea-governance/go/bin/go test -run '^(TestPushEventUsesAfterCommitAndCurrentSchedules|TestPrepareRunAndInsertRejectsChangedScopedSource)$' -count=1 ./services/actions`

两个目标文件 `gofmt -d` 无差异，`git diff --check` 通过。未执行共享 PG、整包扩大回归或真实 Push/Runner 验收；交父任务继续。

## 审查结论

VERDICT: PASS；无成立 P0/P1 阻塞。原缺陷为可达的普通 Push Run 丢失，属于 P2 功能可靠性问题；定时计划和最终 Run 授权均保持失败关闭。尚未实测持续并发 Push 与真实 Runner 的端到端时序。
