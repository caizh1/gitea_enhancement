# 真实用户开发与权限验收缺陷登记

源码基线：`1d07c4e3edaae9a69ff2d875adb4d88ecb3c1fda`。本轮只测试与记录，没有修复业务代码，也没有部署到阿里云或生产环境。

最终评级由主执行者结合真实结果及反证裁定，不按失败记录条数重复计算缺陷。以下为当前已确认的三个问题；环境不支持、脚本断言错误不列为产品 Bug。

| 编号 | 等级 | 问题 | 状态 | 对应场景 |
|---|---|---|---|---|
| PERM-RACE-001 | P1 | 上游既有在途鉴权窗口下，已完成撤权后先前通过鉴权的 Git 推送仍可写入 | 待修复 | F01 |
| MERGE-EVENT-001 | P2 | 治理合并内部 post-receive 返回 500，恢复 PR 状态后未补齐通用 push 事件 | 待修复 | E01、E03、E04 |
| CAP-PR-001 | P2 | 具有 PR 读取能力但没有代码读取能力的用户，被 PR API 错误拒绝 | 待修复 | A04、C04 |

## PERM-RACE-001：P1

**位置**：[前置鉴权](../routers/private/hook_pre_receive.go:123)、[普通代码引用入口](../routers/private/hook_reference.go:155)、[最终引用事务检查](../services/governance/reference_approvals.go:137)、[事务内检查回调](../models/governance/reference_transaction.go:263)。

**归属**：底层在途请求窗口继承自原生 Gitea 1.27.3，不是已证实由增强提交新引入的上游兼容性回归。原生版本只在 `pre-receive` 阶段取得并缓存写权限，之后没有最终权限复核；本项目新增了 `reference-transaction` 最终门禁，但普通代码路径没有像 Wiki 路径一样重新核验当前用户、部署密钥及代码写权限。因此准确表述是“上游既有行为，加上增强最终门禁未覆盖代码权限”。详细源码对比见[归属核查](evidence/user-permission-20260918/perm-race-attribution.md)。

**触发条件**：普通开发者仅有一条有效的直接或继承写授权；其功能分支推送已经通过原生 pre-receive 权限检查，但引用尚未写入；管理员此时成功撤销其最后一条授权。

**复现步骤**：

1. 普通账号克隆私有仓库，在本地生成新提交并发起推送。
2. 在专用隔离实例的 `pre-receive.d` 中加入测试暂停扩展，排序在原生权限检查之后。原生 `reference-transaction` 文件保持逐字节不变。
3. 到达暂停点后，调用正常成员管理 API 撤销唯一写权限，等待返回成功。
4. 同一账号新发起仓库详情请求得到 404；新 Git 读取请求失败，证明撤权已生效。
5. 释放在途推送。客户端仍返回成功，管理员独立读取远端，发现测试分支已经指向该账号的新提交。

**因果链**：前置 Hook 读取旧权限 → 撤权事务成功提交 → 普通代码引用进入最终事务 → 回调检查归档、审批相关快照和部分保护规则，但没有重新核验操作者当前代码写权限 → 引用提交成功。

**实际影响**：已被移除的成员仍可凭撤权前开始的请求向私有仓库写入代码，违反本轮“撤权先完成时不得再以旧授权写入”的明确要求。

**为什么是 P1**：本轮 F01 已明确要求“撤权先完成时不得再以旧授权写入”，当前可达结果违反这一已确认验收要求并影响仓库写入完整性。不是仍能阅读旧克隆、离线提交或前端按钮未刷新，也不能通过要求用户重新登录修复。在正常负载下窗口可能较短，但受控暂停只放大已有时序，不替代鉴权。这个评级针对当前产品验收要求，不代表已经确定上游 Gitea 官方会把其既有语义评为 P1 安全漏洞。

**反证**：HTTP、SSH 均复现；再用仅直接项目授权的新普通账号复现，排除父组、共享、Team、协作者和管理员残留授权。最后用原生引用 Hook 字节不变的版本再次复现，排除替换原生入口造成的问题。F02 的合并授权路径能以 409 阻止冲突撤权，不代表普通推送路径具有同等保护。

**证据**：[HTTP/SSH 原始时序](evidence/user-permission-20260918/races-results.jsonl)、[唯一直接授权反证](evidence/user-permission-20260918/race_confirmation-results.jsonl)、[原生入口不变反证](evidence/user-permission-20260918/native_hook_confirmation-results.jsonl)、[上游与增强归属核查](evidence/user-permission-20260918/perm-race-attribution.md)。四条失败记录归并为一个缺陷。本轮没有另外启动纯上游二进制实例；上游归属结论依据同仓库上游源码基线、提交差异和当前版本确定性复现，若向上游提交安全问题仍需补官方构建的独立运行证据。

**修复方向与复测**：在与最终引用更新一致的授权事务边界重新核验当前操作者和有效授权，并保证授权依赖与撤权有可解释的串行顺序。修复后重跑 HTTP、SSH、直接与继承来源，以及部署密钥、账号禁用等适用变体；不能仅增加前端或前置 Hook 查询后宣称完成原子性修复。

## MERGE-EVENT-001：P2

**位置**：[post-receive 提前返回](../routers/private/hook_post_receive.go:123)、[被跳过的 PushUpdates](../routers/private/hook_post_receive.go:134)、[合并结果恢复](../services/pull/merge.go:480)、[恢复原生 PR 状态](../services/pull/governance_recovery.go:19)。

**触发条件**：满足两人审批等门禁后，通过 API 完成治理合并。

**实际结果**：多次合并的外层 API 返回 200，远端 main 更新正确，PR 为 merged，治理事务为 committed、合并授权为 succeeded；但内部 `/hook/post-receive` 返回 500。专属 E03/E04 仓库均存在合并动态及通知，却没有此次 main 更新的 push 动态，同仓库正常功能分支 push 有动态作为对照。

**因果链**：Hook 中处理 PR 合并状态失败并提前返回 → 通用 `PushUpdates` 未执行 → 外层核对实际引用并恢复 PR 状态、发送合并通知 → 没有补执行通用 push 更新链。关于 Hook 内 `SetMerged` 与已有治理资源占用冲突的具体首因，依据代码和事务状态推导；日志未提供错误正文或堆栈，不把推导包装成直接日志结论。

**反证与评级**：已排除客户端失败、引用未更新和 PR 状态丢失。当前证实的是错误信号和 push 动态缺失，恢复路径保住了提交与合并状态，因此为 P2。没有配置 Webhook 或 Actions 工作流的真实样本，不能据此直接断言关键集成事件全部丢失或升为 P1。

**证据**：[合并与内部错误对账](evidence/user-permission-20260918/approval-followup.md)、[数据库事件对账](evidence/user-permission-20260918/post-receive-event-results.jsonl)。

**复测要求**：修复后同时验证引用、PR、事务、push 动态、合并通知；对有固定工作流及本地测试接收器的实例验证 Webhook/Actions 恰好一次，不能只看合并接口 200。

## CAP-PR-001：P2

**位置**：[PR API 路由组](../routers/api/v1/api.go:1540)、[组级代码读取门禁](../routers/api/v1/api.go:1577)、[Planner 能力定义](../models/governance/permissions.go:63)。

**触发条件**：用户是 Planner，或共享角色/上限分别为 Planner/Reporter、Reporter/Planner、Planner/Developer。有效能力含 `read_pulls`，不含 `read_code`。

**实际结果**：相同账号可以通过网页查看固定 PR 列表，但 `GET /api/v1/repos/{owner}/{repo}/pulls` 返回 403。内容 API 仍拒绝读取代码。

**因果链**：治理层赋予独立 PR 读取能力 → 整个 `/pulls` API 路由组要求 `TypeCode` 读取能力 → 合法 PR 元数据读取请求在进入处理器前被拒绝。

**反证与评级**：四种角色组合复现同一根因，主执行者再次直接复现。初步子代理报告曾列 P1；最终降为 P2，因为网页路径可用，尚无泄漏、丢失或关键交付全面阻断证据，不满足本轮 P1 严重影响要求。历史初步结论保留并附更正，不作为最终评级。

**证据**：[独立能力反证](evidence/user-permission-20260918/capability-review.md)、[原始 API 检查](evidence/user-permission-20260918/api-completion-results.jsonl)、[主执行者复核](evidence/user-permission-20260918/personal-verification-results.jsonl)。

**复测要求**：按 PR 元数据、代码提交、文件及补丁的数据边界分开授权。不能直接删除整组门禁使没有代码权限的用户取得代码；需同时复验网页/API一致性和代码负向路径。

## 不是产品 Bug 的现象

- Guest 及只读事项交集账号可以创建事项、编辑自己的事项，这是原生 reader/poster 语义；编辑他人事项及添加标签均被拒绝。`write_issues` 对应事项管理能力，不等于所有创建行为。
- 私有父组织下创建的“公开仓库”被强制为私有。已改用公开组织下的公开样本，原始预置错误不计产品失败。
- 原生合并可合并性计算尚未完成时返回 405；在确认 `mergeable=true` 后，相同 PR 正常合并。不把等待后台计算前的请求误判成权限缺陷。
- 归档拒绝返回 423；初版断言仅接纳 403/409，已保留原始记录并追加复核。
- 修改原生引用 Hook 后返回 409 属于完整性保护。后续保持原生 Hook 不变，通过受支持的自定义前置扩展完成 F02。
- macOS 屏幕捕捉错误、无 Windows/TortoiseGit 环境、未安装 Git LFS 客户端属于验收环境边界。
