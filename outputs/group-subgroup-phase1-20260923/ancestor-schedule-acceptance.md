# 祖先统一工作流定时收口验收

本记录对应唯一台账 `CI05-SCH-01`～`CI05-SCH-09`，不是另一份计划。开始时间：2026-09-24；分支 `codex/group-subgroup-phase1-alignment`，基线 `a488feb7a4ac2df8a7c17c01c536374b10f33098`。开工时已跟踪文件无修改，未跟踪历史产物保留；本轮不提交或推送源码远端。

## 修复前真实首错

应用是 v16，二进制 SHA-256 `85167c3625a6550b2351b6656d3e22f05f7d1885642d994f6b609b62528e04b2`；该标签仅为索引，源码以本节基线及后续文件清单为准。

普通非实例管理员 `phase1-v15-owner`（21）通过实际页面创建来源仓库 22 `phase1-native-v15/ancestor-schedule-source`、第三层群组 25 `phase1-native-v15/native-child/schedule-deep` 和消费仓库 23 `ancestor-schedule-consumer`。消费仓库仅初始化 README，提交 `16b864570a7379c10ad8b24a05a578d28c732ba2`，没有本地 YAML。

在父群组 CI/CD → Scoped Workflows 搜索并添加来源，页面保存成功，注册 ID 为 2。真实 HTTP Git 提交 `64d14446ec584ebe2efd91a0fc34eed57595ad38`：`.gitea/scoped_workflows/ancestor-schedule.yaml` 为每分钟 cron，`.gitea/workflows/native-schedule.yaml` 为每两分钟原生对照。刷新来源页实际看到祖先文件；消费仓 Actions 页面显示来源但运行数为 0。

跨过真实调度周期后，只读数据库确认来源仓有原生计划 5、Run93，消费者无计划、无 Run。没有手工插入计划／运行或伪造调度时间。首错原始记录：私有缓存 `/Users/archer/.cache/gitea-phase1-closure-20260924/runtime-red.json`。

HTTP Git 第一次 clone 因系统已有凭据助手选用其他隔离账号返回 404；以正式 API 验证本次账号是非管理员 21 且能读仓库后，单次 Git 命令显式禁用凭据助手并指定账号，clone 成功。此为验收客户端身份选择问题，不列为产品缺陷；没有修改全局 Git 配置。

合成凭据经正式 API 配置：父群组 `PHASE1_SCHEDULE_PARENT`、仅来源仓 `PHASE1_SCHEDULE_SOURCE_ONLY`。值为随机测试值，不落入本报告。工作流仅检验存在／不存在和请求状态，不打印凭据；真实 Runner 使用现有根群组 Runner 3 的 `phase1-v15` 标签。

## 实现与可追溯构建

业务实现沿用现有计划、Run、短授权事务及 Runner 协议：周期扫描补建祖先计划，分别保存消费者提交与来源提交、注册修订和范围修订；最终插入 Run 时原子推进到期规格，重复扫描不重复消费。来源异常不能阻断本仓 push 或原生 cron。没有在消费者复制 YAML，没有新增 `workflow_run`。

最终候选基于上述 HEAD 加当前工作树改动，完整源码指纹为 `1b485408ab940a5693a89dfd29581f6e5e73d355344ccf11a83a0f6702f9c547`；二进制 SHA-256 为 `71d19b0d1687ac1e71ccbd5aaec8f79e7c3e5481d8ea749d15377afd360c5ed0`。逐文件 SHA-256 与实际测试结果见[结果清单](ancestor-schedule-results.json)。不可变文件为 `gitea-phase1-closure-schedule-71d19b0d1687`，未加入 Git；2026-09-24 09:42:34 启动于原隔离实例，PID15965。

首轮真实闭环使用 `d1819685…`，旧排队取消修复使用源码指纹 `31dc3fa5…`／二进制 `9f77cd831306…`。最终版进一步限制替换及取消范围；中间 `a35d1cb5…`／`e08c83b35ffb…` 因孤儿运行回归失败，未部署 macOS 运行实例。各轮首错和通过证据均保留，不把首轮结果改写成最终重测。最终修改不改变身份、凭据或 Runner 协议；受影响取消、原生定时及实际祖先运行重新验证。正常迁移使隔离 SQLite 版本从 377 升至 378，替换前各保留私有数据库备份。

## 已运行的自动化

隔离源码副本：`/Users/archer/.cache/gitea-phase1-integration-v3-v6imiqb5`。Go 为 `/Users/archer/.cache/gitea-governance/go/bin/go`，版本 1.26.4；构建标签 `bindata sqlite sqlite_unlock_notify`。PostgreSQL 17.11 及 MinIO 来自已有隔离容器，不是指定容量环境。

- `go test -tags='bindata sqlite sqlite_unlock_notify' -count=1 -json ./models/actions ./services/actions ./models/secret ./models/migrations/v1_27`：最终候选 137 个顶层、含子项 311 项通过，2 项已有跳过，0 失败。这些单测使用 SQLite，不能计作 PostgreSQL 证据。
- `python3 /Users/archer/.cache/gitea-phase1-closure-20260924/run-pg.py pg-schedule-closure-final '^(TestScopedScheduleReplacementPreservesOtherPlans|TestScopedScheduleWithoutConsumerWorkflow|TestScopedScheduleSourceVersionCancelsOldRun|TestScopedScheduleSourceFailureDoesNotBlockPush|TestScheduleUpdate|TestScheduleConcurrency|TestScheduleBranchAuthorizationLock|TestGroupMoveOrdersConcurrentScheduleRunCreation|TestScopedSourceArchiveOrdersConcurrentRunCreation|TestGovernanceOwnerIdentityRealDatabase)$'`：真实 PG 10 个顶层、含子项 21 项全部通过，89.717 秒。固定同一到期快照的并发消费、来源注册移除／重加、无关原生／其他来源计划保留、孤儿任务取消、普通 push 不受影响和原生定时均包含在内。运行器将工作目录固定为上述隔离副本。
- `golangci-lint run --new-from-rev=HEAD ./models/actions/... ./services/actions/... ./models/migrations/v1_27/...`：0 issues；`git diff --check` 通过。

构建后检查新集成测试时，首次 `golangci-lint run --new-from-rev=HEAD ./tests/integration/` 报9条测试代码问题，原日志 `lint-owner-closure.log` 保留。仅改两份测试的断言写法、局部变量名及未使用字段，断言含义保持；业务代码未变。再次执行四个祖先定时加两个Owner的真实PG用例，6顶层全部通过，41.368秒；`GOOS=linux TAGS=bindata golangci-lint run --build-tags=linux,bindata ./tests/integration/` 为0 issues。测试新哈希和两轮日志另记在[结果清单的构建后补充](ancestor-schedule-results.json)，不把这些测试变更伪称为71d原构建清单。成员创建与可见性另跑既有PG四项并配合真实UI/API/Git，详见Owner证据。

首次 PG 中两个新用例重复创建自动初始化已有的 README，返回 422，未走到产品断言。保留首错，修正夹具后通过。运行现场随后发现旧来源的 Run119～123 持续 Waiting：`CleanRepoScheduleTasks` 用短默认分支名查询，而 Run 保存完整 ref。新增 PG 红例明确得到期望 Cancelled(3)、实际 Waiting(5)，再将清理范围改为本仓全部未完成 `schedule`，绿例同时证明旧默认分支也取消、普通 push 不被取消。`9f77…` 候选上再次变更来源后，旧任务实际取消，UI Run122 显示 Canceled。未增加新的取消原因模型；页面沿用既有状态，版本变化原因由本次来源／计划证据解释。

最终对抗式审查又发现：仅来源 A 变化时，全量替换会取消消费者未变的原生／来源 B 定时。新增 PG 固定红例得到原计划 ID 期望5、实际15；最终实现按计划内容与授权版本匹配，保留未变计划、规格和任务，只替换失效计划。中间绿侧仍有一个旧默认分支孤儿取消断言失败；其夹具改用与实际 Run119～123 一致的非零已删除计划 ID，原取消断言保留。清理事务删除失效计划后，只收敛本仓无现存计划关联的非终态 schedule Run，排除普通 push 与合法原生／来源 B。最终同一整组测试通过，没有删断言或改变成功标准。

第一轮保留计划红例曾因父任务误用根目录，调用仓库已有 Linux ELF 在 macOS 下失败；该次只计验收命令错误，不计产品红例。修正为隔离副本后才获得上述确定性产品失败；原始日志都在结果清单内。

## 真实时钟、Runner 和 UI

仅用普通 Owner21、根群组 Runner3（`phase1-v15:host`）以及本轮合成仓库。所有 Run 由真实 cron 或明确标注的 UI 重跑产生；数据库访问只读。消费者提交始终为 `16b864570a7379c10ad8b24a05a578d28c732ba2`，未放置本地 YAML。

| 场景 | 实际结果及证据 |
| --- | --- |
| 祖先首个闭环 | 计划7 → Run104 → Job115/116 成功；真实 UI 显示 Scheduled、`gitea-actions` 和两个 Job。工作流检查父组普通 Secret、消费仓受保护引用 Secret 均存在，来源仓独有 Secret 不进入消费者；显式空权限无法读本仓。 |
| 重复扫描 | 同一计划7 连续自然周期 Run104、108、112 各一次；PG 同一到期快照并发仅创建一个有效 Run。 |
| 人工重跑身份 | Owner 在 Run104 UI 点重跑：attempt1 为系统 -2，attempt2 为人类21，两次均成功；页面显示本次 Owner 身份。 |
| 来源内容更新 | 来源从 `3e1e67b...` 更新至 `e7d7e0e...`，消费仓未提交，计划15及 Run116 使用新来源；日志显示第二版、事件 schedule、系统身份、消费者原生仓库名。 |
| 运行中作业令牌撤权 | Owner UI 给同归属目标24添加只读许可；Run126/Job171 读目标返回200，到达 HTTP 屏障后 Owner UI Remove，释放屏障后同一 Job 下一请求404，Job成功。不同 Owner 的私有来源仓请求404；`permissions: {}` 本仓请求拒绝。 |
| 消费者归档／恢复 | Owner UI 归档后80秒、跨真实周期，运行数7不变且计划0；UI恢复后新计划26、Run140成功。 |
| 消费者移出／移回 | 正式转移 API 将消费者移至 Owner21 个人范围，80秒内运行数13不变、无祖先计划，Actions页不再显示来源；移回群组25后范围修订2、计划27、Run182成功。旧计划没有复活。 |
| 最终候选与强制配置修订 | UI将来源设为必选，配置修订2；计划34、Run197使用来源 `94b67ca2fb213a898fefa951bf8e76ed6c6e75d3` 和修订2成功，实际页面两 Job 100%成功。之后恢复非必选；UI正确提示仅 schedule 的工作流不会发布合并检查，不应将其用于 PR 必选门禁。 |
| 读取权限 | 正式 `/user` 确认 Owner和无关账号均非实例管理员；Run104详情 Owner200、无关人404、匿名404。 |
| 来源归档 | 正式 API归档来源；74秒后消费者运行数16不变、计划0；Owner重跑Run197返回409，attempt数仍为1。 |
| 来源恢复、移出／移回及重新授权 | 恢复后 Run202 成功；来源移出77秒消费者仍17个 Run、计划0，移回91秒仍不自动恢复。页面明确旧登记已失效。Owner UI移除登记2、重新添加登记3后，新计划44→Run209成功，旧信任没有复活。 |
| 两类 cron 删除 | `9f77…` 上经正式 Git 删除本仓 cron，136秒后原生最新 Run218 不增长、原生计划0，祖先新 Run223成功；再删除祖先 cron，120秒后消费者22个 Run不增长、来源／消费仓计划0。来源页显示找不到可执行文件，历史保留。 |
| 最终候选祖先重测 | `71d19…` 重启后正式 Git 恢复测试源；真实 Run231、236、240 成功，来源依次为 `02084f6…`、`d13dfd2…`。Owner页面核对 Run231；消费者23仍只有 README，提交未改变，没有复制 YAML。 |
| 最终候选原生在途连续性 | 仓24原生计划52→Run234/Job371/Task351，由根Runner3运行并进入HTTP屏障。只修改来源22；真实扫描73秒后，仓23/24祖先计划已使用新来源SHA，而原生计划52的ID、Next、消费者SHA及同一运行状态均保持。释放后同一Run成功，UI日志显示完整2分8秒，无重跑。随后正式API删除临时原生探针。 |
| 最终探针清理 | 正式 Git/API 删除全部测试 cron 后观察266秒，消费者23运行总数26不变，仓22/23/24计划均为0且无非终态任务；两个一次性HTTP屏障已正常停止。历史Run、日志与源码提交保留。 |

白名单运行状态及完整 UI 原文已汇总到[真实运行证据](ancestor-schedule-runtime-records.json)；原始文本、截图仍保存在私有目录 `/Users/archer/.cache/gitea-phase1-closure-20260924`。主要文件为 `runtime-red.json`、`runtime-preserve-deployment.json`、`ui-run126-full.txt`、`ui-final-run197.txt`、`ui-old-run122-cancelled.txt`、`ui-source-returned-full.txt`、`native-preserve-after-source-change.json`、`ui-native-preserve-success.txt`。部分早期 UI 文件只保存了浏览器差异，未作为完整页面原文使用；完整重新读取另存带 full 或具体 Run 的文件。来源配置页窄屏截图可读，按钮及文件状态无重叠。记录不含 Secret 明文。

验收客户端曾把 `github.repository` 的群组展示路径用作条件，导致撤权 Job 被跳过；按实际原生兼容仓库标识纠正后才计入 Run126。Run131是许可已撤销后下一周期继续运行一次性探针，因前置200不再成立而失败；未改成允许401/404来制造成功，随后移除已经完成的一次性探针。其他脚本首错包括记录脚本漏导入、错拼无关用户名、未到观察时长的探针及读取不存在的统计列，均不作为产品通过或缺陷证据。

## 当前检查点

`CI05-SCH-01`～`CI05-SCH-09` 的约定场景已关闭，唯一状态仍在现有矩阵。最终代码审查 **VERDICT: PASS**，当前本次差异无成立 P0/P1；误取消缺陷及旧队列不收敛已修复并有红绿证据。未验证风险为目标容量下全树扫描开销、其他数据库兼容、跨节点及完整一期其余操作，不能由此报告推导通过。

后续 M11-a 已补同组双永久 Owner 并发退出及临时 Owner 到期前后真实 PG 两项，并与71d UI拒绝、刷新保留证据共同关闭，详见既有[Owner证据](owner-identity-db-evidence.md)。新增文件是构建后的纯测试补充，不修改本次候选业务代码或旧清单。独立验收状态按唯一矩阵更新，本记录不代表一期整体交付完成；目标 Linux amd64 容量、正式发布和真实人工80%效率仍未完成。
