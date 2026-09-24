# 第十五批：父任务整合验证与审查

日期：2026-09-24。源码基线仍为 `main @ 9a3f93813847f53b4762e28863b09b9266f75943`，保留工作树原有改动，未提交或推送源码。GPT-6 Sol high 子代理分别完成原生应用、屏蔽授权及 CI 修复；父任务检查实际调用链、补组织应用审计作用域和 HTTP 集成，再独立构建、验证。

## 实现与反证

- 原生 OAuth 的创建、编辑、轮换、删除在最终治理事务核对当前主体、原 sudo 管理员及目标范围；组织归档／待删除拒绝新增配置，保留删除撤权。个人 API 不能通过 sudo 组织身份绕过群组边界。测试先保留旧接口 201／200 红例，再验证拒绝；没有将每个归档行为都强行定为 P1。
- 屏蔽、解除、备注在最终事务重检当前主体；归档允许收回互动权限，禁止解除后重新开放。清理协作者／关注等列表改为每批从第一页处理并防无进展，修复边删边翻页遗漏。
- 原生组织应用审计原来写 user 范围，PG 红例明确 actual=user、expected=group；改为组织 group 与当前祖先投影，个人与实例保留原有范围，详情不含密钥哈希。普通 Owner 父子页面均实际读到事件。
- push Run 按事件 after 绑定提交；定时计划另读当前默认分支。计划替换、创建 Run、领取及凭据核验使用持久默认分支校验，PG 行锁固定交错通过；分支同步共用逐仓锁并读取现时 Git 引用。Git 和网络操作不在这段短授权事务中。
- 当前系统 Actions 用户的零 cron 路径也进入清理，不再将旧审查的“零 cron 必然残留”作为当前缺陷。重复等价通知取消旧 schedule、未装引用 Hook 的非默认分支删除／重建列表状态仍作为非阻塞边界登记。

## 父任务实际命令与结果

Go 1.26.4 darwin/arm64。隔离源码副本 `/Users/archer/.cache/gitea-phase1-integration-v3-v6imiqb5`；原始日志位于 `/Users/archer/.cache/gitea-phase1-v15-runtime`，不复制测试认证响应到文档。

```text
go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -json ./models/auth ./services/org ./services/user ./routers/web/org ./routers/web/admin ./routers/web/user/setting ./routers/api/v1/user ./routers/web/shared/user ./routers/api/v1/shared
go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -json ./models/actions ./services/actions ./modules/repository ./services/repository ./routers/private
```

| 执行 | 实际结果 |
| --- | --- |
| 原生九包 SQLite | 117 个顶层测试、含子项 289，通过，零 fail |
| CI 五包 SQLite | 207 个顶层测试、含子项 430，通过，零 fail；隔离副本使用实际 macOS 构建，原有 Linux hook 二进制问题不再影响测试 |
| 原生真实 PG 五项 | 全通过，含原生 HTTP、审计范围和 `postgres / gitea_phase1_test` 引擎断言 |
| CI／原生整合 PG 十二项首轮 | 11 通过；`TestAPICreateBranchWithSyncBranches` 在 pre-receive 因治理命名空间 fixture 不存在失败，不能把该轮写成退出 0 |
| 修正 fixture 后 PG 定向 | 分支同步 HTTP 与数据库引擎两项通过；十二个不同顶层目标最终各有通过记录，不是一次十二项全绿 |
| `make fmt` | 在隔离副本执行成功；本批文件无新增格式差异。副本被格式化的无关文件恢复为用户当前源码，没有全仓回写 |
| `make lint-go` | 全仓运行报告 46 个问题、退出 2；不能称全仓 lint 通过。已检查落在本批文件的 branch.go 提示属于此前默认分支代码，子代理增量检查另有零问题证据 |
| `go build -tags='bindata sqlite sqlite_unlock_notify' -o gitea .` | 重新生成选项／模板 bindata 后成功。最终仅测试 fixture 随后改动；业务源码与 v15 构建一致 |

PG 实际命令由 `run_pg.py` 设置隔离 `GITEA_TEST_DATABASE=pgsql`、PostgreSQL 17 与 MinIO 连接，运行：

```text
go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -json -run '^(TestScheduleBranchAuthorizationLock|TestScheduleUpdate|TestScheduleConcurrency|TestGroupMoveOrdersConcurrentScheduleRunCreation|TestCreateTaskForRunnerConcurrentClaim|TestCreateTaskForRunnerRejectsOwnerChangeAfterCandidateScan|TestAPICreateBranchWithSyncBranches|TestNativeOrgSettingsAncestorLifecycle|TestBlockUser|TestUserSettingsApplications|TestOAuth2Application|TestGovernanceOwnerIdentityRealDatabase)$' ./tests/integration
go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -json -run '^(TestAPICreateBranchWithSyncBranches|TestGovernanceOwnerIdentityRealDatabase)$' ./tests/integration
```

分支测试首因：`onGiteaRun` 内部重载 fixture，旧测试把“清空分支表”操作放在外部而被悄悄重置。最终将初始化、清表和断言都放入其回调，确认空表同步的真实路径；不是跳过失败或放宽生产 pre-receive。中间一次测试编译缺 import 已纠正，原始失败记录保留。

## 真实执行与结论

v15 开发构建 SHA-256 `cbd72c5fd98cddc82e48c14091b58d8149d24945ada72b33660479051425ac19`，隔离站点切换前备份 SQLite 与配置；原仓库 Linux gitea 的 SHA-256 仍为 `10f50310cfe1cf1d878c6a78fae4601d79f264e6dfe478edbc16b34fb0c75d5a`。

[普通 Owner 原生设置与无关用户](parent-v15-native-ui.md)验证同对象归档拒绝、恢复成功、完整路径、父级审计与三个 404；[真实 Runner](parent-v15-ci-ui.md)验证两次事件提交、实际 schedule 及 skip-ci 清理。子代理独立 CI 审查未发现成立 P0／P1；父任务撤回没有当前源码证据的零 cron、数据库表前缀猜测。

后续复审发现屏蔽操作缺少审计；该功能缺口由下一构建补齐，不能用 v15 页面宣称其存在。完整一期仍未完成：未验角色／入口／生命周期组合、数据库兼容、目标容量、人工效率和候选发布均按矩阵保留。已合法披露的数据无法追回；Git ref 到持久分支提交之间的窗口、单节点锁假设及 schedule 当前 HEAD 的保守失效行为明确保留。

后续第十六版已补原生屏蔽三类审计并完成实际页面与 PG 复验，见[v16 整合](parent-v16-integrated-validation.md)。本记录保留 v15 当时的边界。
