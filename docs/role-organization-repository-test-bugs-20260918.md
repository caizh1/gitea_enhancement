# 角色、组织、权限与仓库联动验收问题登记

基线：`1d07c4e`。本轮仅在本机隔离实例测试，未修复业务代码。相同根因的多个失败记录只算一个问题；测试脚本错误、未验证项和目标契约差异分别说明。最终执行范围与统计见[测试报告](role-organization-repository-test-report-20260918.md)。

| 编号 | 等级 | 本轮结论 | 场景 |
|---|---|---|---|
| PERM-RACE-001 | P1，针对严格在途撤权验收目标 | 已知问题再次实测复现；上游既有在途语义，增强最终门禁仍未补齐，不是已证增强回归 | C04 |
| CAP-PR-001 | P2 | Planner 及相关共享交集有 PR 元数据能力，API 仍被代码门禁误拒绝；网页可用，代码负向有效 | R04、S04、X07、E04 |
| PERF-LIST-001 | P2 | 固定规模权限分页正确，但 P95 9.623 秒，超过执行前冻结的 2 秒目标 | C08 |
| MERGE-EVENT-001 | P2 | 新的审批发布链路再次确认合并后缺少 main 的通用 push 动态，引用/PR/标签完整 | E05 |
| LIFE-RECREATE-001 | P2，恢复/重建目标差异 | 子组仓库清理后兼容别名仍占用旧身份，同名创建返回 409；保留别名有安全设计意图，但当前恢复目标未满足 | C07、P12/E08相关变体 |

## P0/P1 BLOCKERS

### PERM-RACE-001

- **位置**：[普通代码最终引用入口](../routers/private/hook_reference.go:155)、[最终检查](../services/governance/reference_approvals.go:137)。
- **触发条件**：普通用户唯一组织 Developer 授权；功能分支推送已通过原生前置鉴权，尚未更新引用。
- **因果链**：原生鉴权成功 → 专用扩展暂停 → 正常 API 撤销唯一授权并返回成功 → 同凭据新 Git 读取拒绝 → 释放旧推送 → 原生引用 Hook 未变化，最终门禁没有重算当前代码写权限 → 推送成功且远端引用新增。
- **实际影响**：撤权已提交后，旧在途请求仍能修改仓库，违反本矩阵 C04 已明确的严格要求。
- **P1 理由**：当前、可达、有确定时序及真实引用变化的写入完整性缺口；不是离线提交、旧克隆内容或按钮残留。此评级针对本产品明确验收目标，不是上游官方漏洞评级。
- **反证**：本轮 HTTP、SSH 均独立复现；撤权后新请求拒绝，排除了未真正撤权；记录原生 Hook 内容不变，排除替换原生检查产生假象。其他并发管理请求正确拒绝，并不能证明普通 Git 推送具有相同原子边界。
- **证据**：[本轮 C04 原始记录](evidence/governance-linkage-20260918/concurrency-results.jsonl)、[确定性执行器](../tools/governance-concurrency-acceptance.py)、[既有上游源码归属核查](evidence/user-permission-20260918/perm-race-attribution.md)。没有重新运行纯上游二进制对照。

## NON-BLOCKING FINDINGS

### CAP-PR-001：P2

[API 路由](../routers/api/v1/api.go:1577) 对整个 PR API 施加代码读取门禁，与 [Planner 的独立 PR 能力](../models/governance/permissions.go:63) 不一致。本轮单独 Planner 及 Planner/Reporter、Reporter/Planner、Planner/Developer 共享交集均复现元数据 API 403；同身份事项操作和 PR 网页可用，代码及补丁拒绝正确。

已反证账号串用、没有实际 PR 和共享能力配置错误。子代理曾建议 P1，主执行者按严重影响规则维持 P2：网页替代路径可用，没有代码泄露或全面交付阻断的证据。不能通过取消整个代码门禁修复而引入泄露。

证据：[角色成员记录](evidence/governance-linkage-20260918/rm-results.jsonl)、[共享入口记录](evidence/governance-linkage-20260918/sx-results.jsonl)。

### PERF-LIST-001：P2

- **实测**：200 个组织、2,000 个仓库、1,000 名成员，组织常规深度 10。用户应见 1,000 个仓库；21 次分页请求均 200，集合、去重及边界正确。分页请求约 3.46–9.62 秒，P95 为 9.623 秒；预先冻结阈值为 2 秒。
- **用户影响**：普通仓库发现、分页和依赖相同授权搜索的界面明显变慢。即使某用户只有一个可见仓库，同一大数据库下的三次列表读取仍为约 4.20–6.26 秒。
- **代码路径**：[SearchRepository](../models/repo/repo_list.go:577) 进入 [prepareGovernanceSearch](../models/repo/governance_search.go:23)，先装载所有仓库，再逐仓库计算 RepositoryGrants 和单元能力，然后分页。该全库逐对象计算与实测放大方向一致；没有逐条 SQL 剖析，不能给出未测的查询总数或唯一耗时占比。
- **反证**：不是分页过滤泄露或漏项；无容器 OOM/重启证据；API 单项读取和版本探针仍可用。共享环境还有其他验收操作，不能将 9.623 秒外推到任意生产配置，也不能把全部间歇断连归为同一根因。
- **级别**：明确未满足本轮响应目标，但尚无严重全站不可用、数据损坏或跨租户泄露证据，P2。
- **证据**：[执行前冻结配置](evidence/governance-linkage-20260918/scale-baseline.json)、[C08 原始记录](evidence/governance-linkage-20260918/concurrency-results.jsonl)、[资源抽样](evidence/governance-linkage-20260918/scale-resource-observation.txt)。资源为共享 Docker 18 CPU、约 8 GiB；抽样不是峰值，200 个仓库有初始化内容，其余为空仓，不代表大仓库代码搜索容量。

### LIFE-RECREATE-001：P2，目标差异

- **当前可达样本**：子组仓库 `sc-denied-349f9d/node-58/scale-repo-337-0` 创建期间连接中断。清理后旧仓库 ID 1684 的数据库实体和当前正式路径已移除，但 `g_54f9ba02f41e4d05a4f95c662ddb9b14/scale-repo-337-0` 仍以 `alias=true` 指向旧 ID。同名创建返回 409。
- **因果链**：[清理逻辑](../services/repository/cleanup.go:126) 仅移除 `alias=false` 路径 → [RegisterNativeRepository](../models/governance/native_namespace.go:29) 为新 ID 登记正式及原生兼容路径 → [reservePath](../models/governance/namespace.go:81) 发现兼容别名已属于旧 ID → 新建事务冲突回滚。
- **反证**：这不是瞬态 revision 冲突，也不是仓库实体仍存在。源码明确保留兼容地址身份以防错误重分配，因此不能称它为旧权限转嫁或 P1 漏洞；当前实际结果是安全拒绝。它与本矩阵“清理后恢复同一建库操作/同名重建”的目标存在差异，需要明确产品契约或提供受控恢复方式。
- **实际影响与替代**：用户不能直接重试原名称；本轮仅对该测试夹具采用不同名称继续达到固定规模，没有删除历史别名或修改实现。替代有额外操作成本，P2，不导致单独 FAIL。
- **证据**：[C07 记录](evidence/governance-linkage-20260918/concurrency-results.jsonl)、[组织仓库补测](evidence/governance-linkage-20260918/or-results.jsonl)。网络中断本身的首因尚未确定，与别名冲突的确定性根因分开。

### MERGE-EVENT-001：P2

本轮独立仓库 ID 2123 完成两人审批、最新检查、合并与标签发布，主分支 SHA 为 `408e1c` 开头。独立 `action` 表对账显示功能分支 push 有 6 条、合并动态 3 条、普通标签与 release 标签各 3 条，但对应 main push 为 0；同次内部 post-receive 返回 500。活动 API 空数组只是初始线索，最终定性依靠数据库对照及服务日志。

[post-receive 提前返回](../routers/private/hook_post_receive.go:123) 跳过通用 PushUpdates，外层恢复保持了 PR、引用及合并事件，但未补齐通用 push 动态。初始 `SX-E05` 合并到已有 `MERGE-EVENT-001`，不重复登记。未配置实际 Webhook/Actions，不声称所有下游事件丢失。证据为 [sx-results 第26条](evidence/governance-linkage-20260918/sx-results.jsonl) 及关联第25条。

## UNVERIFIED RISKS

- 登录后的已认证首页、组织页面在规模准备期间出现慢请求及客户端超时；08:20:19 UTC 多个请求连接断开、服务端记录 context canceled，容器无重启/OOM。暂未证明是产品故障、宿主网络还是连接取消所致。匿名首页和版本接口快速返回不能替代已认证业务页面验收。保留[中断窗口日志](evidence/governance-linkage-20260918/scale-interruption-log.txt)。
- `MERGE-EVENT-001` 已在本轮重新验证；实际 Webhook/Actions 恰好一次交付仍未验证，不能从 push 动态缺失推定所有集成结果。
- 逐行未测网页入口、并发变体和跨节点组合见报告覆盖表；未验证项不升级为 P0/P1，也不计通过。

## 归属复核补充

对照原始提交 `146cc3e` 后：PERM-RACE-001 为上游既有在途行为，增强尚未满足严格撤权目标；CAP-PR-001、PERF-LIST-001、MERGE-EVENT-001 归属增强实现或集成；LIFE-RECREATE-001 为增强的身份保留策略与恢复目标冲突。四项增强责任问题目前均为 P2。详见[逐项源码归属评估](governance-issue-attribution-20260918.md)。
