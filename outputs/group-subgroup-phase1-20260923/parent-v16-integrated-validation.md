# 第十六批：原生屏蔽审计与推送冲突整合

日期：2026-09-24。本轮使用 GPT-6 Sol high 子代理实现，父任务独立核对调用链、添加真实 PostgreSQL HTTP 断言并完成真实 UI／Runner 验收。仅操作本机隔离环境；源码工作树未提交或推送，用户原有改动保留。

## 当前构建

macOS arm64 开发构建：`gitea-phase1-ancestors-v16`，SHA-256 `85167c3625a6550b2351b6656d3e22f05f7d1885642d994f6b609b62528e04b2`。编译命令为 `go build -tags='bindata sqlite sqlite_unlock_notify' -o gitea .`，退出 0；复用 v15 已重生成的模板／选项资源，本次无资源和迁移新增。原有仓库 Linux gitea 未替换。

切换前确认没有进行中的 Task，正常停止本次隔离应用，备份数据库和配置后更换稳定入口 `gitea-current`。新进程 70663 正常提供服务。凭据与原始认证日志仅留私有缓存，不进交付文档。

## 修复与固定证据

### 原生屏蔽审计

实际 v15 普通 Owner 屏蔽／解除及备注操作未写审计，是 I-07 覆盖缺口。新增 HTTP 断言在真实 PostgreSQL 失败：成功 PUT 屏蔽后不存在 `user.blocked` 的 group 事件，不是根据缺少测试文件推断。

修复复用三个最终治理事务，写入 `user.blocked`、`user.block_note_updated`、`user.unblocked`。组织事件属于本组并投影发生时祖先；个人事件属于本人。详情只有 `blocked_before`、`blocked_after`、`note_changed`，不记录私有备注。重复屏蔽保持原有 400；保存相同备注仍成功但不重复记账。拒绝的解除不伪造成功事件。SQLite 故障注入证明审计失败连同屏蔽清理关系或备注一起回滚。

父任务 HTTP 集成验证三类事件的操作者、子群组与父级投影；重复屏蔽／备注不增事件，祖先归档拒绝解除且无成功事件，恢复后正常解除。个人审计由子代理服务测试覆盖，未冒称浏览器个人页面已验。

### 定时冲突不能吞普通推送

父审查定位到 `notify → handleSchedules → replaceSchedulesForRepo`：当 Git HEAD 与持久分支表尚未一致，计划检查返回 ErrConflict，原 `notify` 直接返回，调用者只记录错误，合法 push Run 消失；无 cron 的工作流也会经过此路径。该可复现问题为 P2 研发可靠性缺陷。

仅对该计划 ErrConflict 记录诊断后继续普通事件；其他错误仍返回，旧定时计划不获授权。普通 Run 的 `InsertRun` 最终治理事务仍比较当前 Owner、命名空间、ActionsScopeRevision 和归档状态；范围改变则回滚 Run／Job，不会取得目的范围凭据。固定红绿同时覆盖旧 H1、无 cron 和范围修订失效不生成 Run，见 [专项证据](schedule-push-conflict-evidence.md)。没有新增轮询重试或平行任务系统。

## 实际运行命令与结果

隔离构建副本 `/Users/archer/.cache/gitea-phase1-integration-v3-v6imiqb5`；Go 1.26.4。PG 为本机隔离 PostgreSQL 17 与 MinIO，设置 `GITEA_TEST_DATABASE=pgsql`，引擎用例实际断言 `postgres / gitea_phase1_test`。

```text
go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -json ./services/user ./models/governance ./routers/web/shared/user ./routers/api/v1/shared
go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -json ./services/actions
go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -json -run '^(TestNativeOrgSettingsAncestorLifecycle|TestBlockUser|TestGovernanceOwnerIdentityRealDatabase)$' ./tests/integration
go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -json -run '^(TestScheduleBranchAuthorizationLock|TestScheduleUpdate|TestScheduleConcurrency|TestGroupMoveOrdersConcurrentScheduleRunCreation|TestAPICreateBranchWithSyncBranches|TestNativeOrgSettingsAncestorLifecycle|TestGovernanceOwnerIdentityRealDatabase)$' ./tests/integration
golangci-lint run --new-from-rev=HEAD ./services/actions/... ./services/user/... ./models/governance/...
```

| 验证 | 最终实际结果 |
| --- | --- |
| 屏蔽／审计等四包 SQLite | 55 个顶层、含子项 79，全部通过，零失败／跳过 |
| Actions 全包 SQLite | 71 个顶层、含子项 181，全部通过，零失败／跳过 |
| 原生审计真实 PG 三项 | 3 个顶层、含子项 9，全部通过，零失败／跳过 |
| 最终整合 PG 七项 | 7 个顶层、含子项 18，全部通过，零失败／跳过；包括持久分支行锁屏障、定时／移动与原生 HTTP |
| 最终增量 lint | 在真实 Git 工作树执行，`0 issues`；隔离副本无 .git 的初次检查无法做差异过滤，报告七项既有问题，不能将那次写成增量通过 |
| 格式与空白 | 本批 Go 文件 `gofmt -l` 无差异；`git diff --check` 无输出。此前 `make fmt` 已在副本执行，无关格式未回写 |

原始日志位于私有缓存 `/Users/archer/.cache/gitea-phase1-v15-runtime`：`native-block-audit-red.jsonl`、`native-block-audit-green.jsonl`、`native-block-final-sqlite.jsonl`、`ci-v16-sqlite.jsonl`、`ci-v16-pg.jsonl`、`lint-v16-git-incremental.log`、`build-v16.log`。本批复用该目录名，不把 v16 日志混写为 v15 运行结果。全仓 `make lint-go` 在 v15 仍有 46 项问题，本次增量零问题不替代全仓通过。

## 真实浏览器与 Runner

普通非管理员 Owner 21 在原生子群组 24 的 Blocked users 页面屏蔽测试用户 22，编辑备注，保存并刷新，再解除并刷新。子群组审计显示三类成功记录 638／639／640；父群组 23 的实际审计页也同时显示三条。展开备注事件，只见 `{"blocked_after":true,"blocked_before":true,"note_changed":true}`，无原文。当前屏蔽列表为空，测试结束后没有持续限制该无关用户。

同一普通 Owner 真实 HTTP Git 推送 `0dddbd96daa46ce382f0fb2368436517e591456a`，真实 Runner 3 完成 Run92／Job101。浏览器从 Actions 进入作业，日志为 push、相同提交与“第十六版”；工作流继续实际校验 event.after 等于运行 SHA。此正例不是固定并发冲突，冲突时序由上述确定性测试覆盖。仓库 21 的定时计划仍为 0，v15 移除 cron 后没有继续产生该定时任务。

## 最终对抗审查

VERDICT: PASS

P0/P1 BLOCKERS: 无。本轮固定复现的两个 P2 已修复并复验。审查反证了“吞计划冲突会放开转移后旧工作流”的候选：普通 Run 最终范围检查仍在同一事务回滚，固定修订变化反例通过；最终领取的既有当前范围及凭据检查未删。

UNVERIFIED RISKS: 完整角色／协议／生命周期矩阵、非 PG 数据库锁兼容、跨节点共享锁、目标容量和人工效率仍未验。Git ref 到持久分支表提交之间的窗口仍在；已合法披露的内容不能收回。普通 UI Owner 同时属于父子原生 Owners Team，不冒称仅祖先 Owner；个人审计与审计故障回滚没有本次浏览器证据。

NON-BLOCKING FINDINGS: 定时计划当前 HEAD 的保守失效、重复等价通知可能取消旧定时运行、未安装引用 Hook 的非默认分支删除／重建列表恢复，以及现有 Runner v4 artifact／ref_protected 兼容限制，按阶段审查保留。全仓 lint 的既有问题未顺带重构。

一期交付判定：未完成。此次完成的是原生授权、已知 CI 问题及相邻审计缺口这一实施批次。操作矩阵的未验核心项、目标环境性能、80% 人工效率和候选发布都不能由本次 PASS 代替。原始群组／成员事件与子树制品聚合仍保留独立待开发差异。
