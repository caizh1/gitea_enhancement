# 五项联动问题的上游与增强归属复核

对照范围：原始 Gitea 源码提交 `146cc3eec57174711eac0e0a0c7b38670c6e3922` 与增强提交 `1d07c4e3edaae9a69ff2d875adb4d88ecb3c1fda`。两者在本仓库中为前后相邻提交。本次只核对源码差异、调用链及已有真实运行证据，没有另起纯上游二进制实例，也没有修复业务代码。

## 结论

**1 项属于上游既有在途行为，4 项应归入我们的增强责任范围。** 后四项中，三项是实现或集成问题，一项是增强的身份保留策略与同名恢复/重建目标冲突。不能仅看最终报错代码是否原生，就把问题归给原生 Gitea。

| 问题 | 归属结论 | 增强责任 | 证据边界 |
|---|---|---|---|
| PERM-RACE-001 | 上游既有在途鉴权窗口，不是已证新增回归 | 我们新增的最终引用检查没有补齐严格撤权承诺 | 上游源码路径与本版双协议时序证据；未运行纯上游二进制对照 |
| CAP-PR-001 | 增强角色与原生 PR API 门禁的适配遗漏 | 新增 Planner/read_pulls 与 read_code 分离后，API 未同步适配 | 原生门禁确实存在，但不能因此推卸新增角色契约责任 |
| PERF-LIST-001 | 增强新增的全库授权扫描带来的性能问题 | 搜索前逐仓库计算治理授权，再分页 | 新增慢路径归属确定；9.623 秒中各因素占比没有同规模上游 A/B 或 SQL 剖析 |
| MERGE-EVENT-001 | 增强合并授权、资源占用与原生 post-receive 的集成问题 | 内部合并状态更新被自己的资源占用拒绝，恢复未补通用 push 事件 | 500/动态缺失实测；内部冲突首因由代码链确定性推导，原始日志未保留具体错误堆栈 |
| LIFE-RECREATE-001 | 增强新增的历史身份保留策略造成恢复/重建限制 | 清理保留别名，新仓库登记兼容路径冲突 | 实测两个独立样本；属于目标差异，并非越权或数据泄露 |

## 逐项依据

### 1. 在途撤权：原始 Gitea 已存在的窗口

原始 `routers/private/hook_pre_receive.go` 在 `loadPusherAndPermission` 中读取权限，`canWriteCodeUnit` 缓存结果。通过前置检查之后，上游没有本项目新增的最终引用事务鉴权入口。因此，检查通过后撤权并不等于原生实现会取消已经接受的在途推送。

增强增加了 [hook_reference.go](../routers/private/hook_reference.go:155) 和最终事务机制，但普通代码路径没有重算当前操作者代码写权限。当前 HTTP/SSH 的受控时序复现与上述机制一致。

归属应写为：**上游既有行为；增强未满足新增的严格撤权目标**。P1 是针对本轮明确验收目标的等级，不能写成“我们新增了一个 P1 回归”，也不能当作上游官方安全评级。[详细对照](evidence/user-permission-20260918/perm-race-attribution.md)。

### 2. Planner PR API 拒绝：我们的集成遗漏

原始 `routers/api/v1/api.go:1465` 已对 `/pulls` 整组要求 `reqRepoReader(unit.TypeCode)`；当前对应 [api.go:1577](../routers/api/v1/api.go:1577) 仍保持这一约束。

但 [Planner 能力及独立 ReadPulls](../models/governance/permissions.go:63) 是我们新增的：允许 PR 元数据读取，禁止代码读取。共享的能力交集也是新增语义。原生检查行未改，并不表示新增角色在该入口被误拒是上游造成的。

**应归为我们的能力模型扩展未适配全部入口，P2。** 此处不声称上游原生 Team 的所有单元组合都没有相似限制，只确认本轮 Planner/共享契约由我们引入并应由我们负责。

### 3. 分页变慢：新增治理搜索路径

`models/repo/governance_search.go` 在原始提交不存在。[新增实现](../models/repo/governance_search.go:23) 先读全库仓库，再逐个调用 RepositoryGrants，计算能力并加载单元。[SearchRepository](../models/repo/repo_list.go:577)、CountRepository、SearchRepositoryIDs 的前置调用均由增强新增。上游对应入口直接构造查询条件、计数和分页。

因此新增的全库扫描成本属于我们的改动；不能称为原生 Gitea 自带的同一性能问题。当前规模实测 P95=9.623 秒、目标=2秒，P2。没有对纯上游做相同数据/授权条件的 A/B，不宣称“上游必定少于2秒”或“9.623秒全部由此函数消耗”。

### 4. 合并后 push 动态缺失：报错入口没改，调用的行为改了

`routers/private/hook_post_receive.go` 在两个提交中逐字相同，blob 均为 `d6b5dfcf86bc66f99f4943a527ef932fb3237fe7`。但它调用的 `SetMerged` 已被我们修改。

具体新增执行链：

1. [合并推送前](../services/pull/merge.go:440) 创建治理合并授权，在 [AuthorizeMerge](../models/governance/transaction.go:87) 中占用 repository/pull 等资源。
2. Git 执行 post-receive 时，原生处理器 [调用 SetMerged](../routers/private/hook_post_receive.go:274)，没有把本次授权转换成可排除自身占用的数据库上下文。
3. 新版 [SetMerged](../services/pull/merge.go:842) 外包治理 `WithWrite`；[资源占用检查](../models/governance/transaction.go:50) 发现尚未释放的授权占用，返回冲突。原始 SetMerged 只有常规数据库事务，没有这套治理占用。
4. 原生处理器因错误提前返回，后面的 `PushUpdates` 不执行。
5. 我们在 Git 命令返回之后才 [ReconcileMerge](../services/pull/merge.go:490) 释放占用，并通过 [FinalizeAuthorizedMerge](../services/pull/governance_recovery.go:19) 恢复 PR 状态；恢复未补发通用 push 事件。

原生“遇到错误提前返回”的处理并不能解释为原生正常合并自带这个治理冲突。**本轮问题应归为增强集成缺陷，P2。** 实测 main 引用、PR、合并与标签动作正确，main push 动态缺失且同次 hook 返回500；没有证据支持扩大为全部 Webhook/Actions 丢失。

### 5. 清理后同名重建409：我们新增的别名策略

原始版本没有 governance_resource_path、子组织兼容别名或相关永久占用。原始建库会调用 `DeleteRedirect`，删除仓库会清理对应 Redirect 记录。

我们新增的 [清理](../services/repository/cleanup.go:126) 只删除 `alias=false` 路径，保留旧身份别名。新建时 [RegisterNativeRepository](../models/governance/native_namespace.go:29) 又需要登记兼容地址，[reservePath](../models/governance/namespace.go:81) 因旧别名指向另一个 ID 而拒绝。

该行为有防止地址身份被重新分配的设计意图，不能误判成安全漏洞；但确实是增强带来的恢复/重建限制，不能归给原生 Gitea。**P2 目标差异：需要明确保留策略，或实现不复活旧权限的受控恢复/重建机制。**

## 对原验收结论的影响

- 如果问题是“本轮发现了几个由增强新增的 P0/P1”：这五项里，没有已确认的增强新增 P0/P1；增强责任范围内四项目前均为 P2。
- 如果验收仍要求“撤权先完成后，在途写入不得提交”：原严格验收 `VERDICT: FAIL` 仍成立，因为目标并未取消；只是不能把该 FAIL 描述为增强新增 P1 回归。
- 这不是对所有增强代码的完整安全背书，也不消除原报告中尚未验证的入口和组合。
- 间歇断连、登录超时首因尚未确定，仍不能分配为“上游问题”或“增强问题”。

本轮证据与未测边界见[联动报告](role-organization-repository-test-report-20260918.md)及[问题登记](role-organization-repository-test-bugs-20260918.md)。
