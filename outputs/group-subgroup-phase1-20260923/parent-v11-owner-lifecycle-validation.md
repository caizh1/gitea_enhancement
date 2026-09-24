# 第十一版：Owner 边界与生命周期复验

日期：2026-09-24。父任务负责调用链反证、构建、真实浏览器和 API；GPT-6 Sol high 子代理完成有界修复及自动化。仅操作本机隔离实例及合成数据。

## 已成立缺陷与修复

### 受限共享被当作完整 Owner

`services/governance/group_archive.go` 原先先检查有效 `ManageGroup`，再接受任意授权的原始 `Role == Owner`。自定义 Reporter 仅加 `ManageGroup`，加上另一来源“受邀群组 Owner、共享上限 Reporter”，满足旧检查，却不存在任何完整 Owner 授权。空群组没有仓库删除权的附加检查，因此实际可以安排删除，并能触达相同检查保护的删除执行链。它是当前支持的公开角色／成员／共享服务可构造的组合，不是手工伪造数据库权限。

父任务在旧 v10 上通过正式 API 建立空群组 18、受邀群组 17、用户 3 及上述两条来源。API 归档预览返回 200；同一用户在浏览器设置页看到删除表单，输入当前完整路径并安排删除，实际变成 `phase1-owner-boundary-deletion-18-1790180947`，显示等待删除。该测试只安排可恢复删除，没有执行物理删除。

修复复用 `HasOwnerGrant`：必须有一条原始 Owner 来源，在共享上限处理后仍完整具备 Owner 能力。它共同保护预览、归档、安排删除、恢复和最终删除执行。真正 Owner、祖先 Owner、完整 Owner 共享及实例管理员原有边界保留。

### 审计的共享上限失效

`services/governance/audit.go` 原先检查单条授权有 `ReadAudit` 后，直接信任原始 Owner 角色。Maintainer 上限包含该能力，但普通群组 Maintainer 并非审计服务允许的群组 Owner 或显式审计自定义角色。受邀群组 Owner 因原始角色保留而越过该条件，可以读取来源及子树审计。

父任务以正式 API 将群组 15 按 Maintainer 上限共享给群组 17，用户 3 在 17 为 Owner。返回授权明确为 Role 50、CeilingRole 40、ReadAudit true。v10 浏览器群组首页显示审计入口，点击后可见审计记录；同身份 API 返回 200 和 15 条事件。记录全部为本机合成数据。

修复要求完整 Owner 来源，并保留明确授予 `ReadAudit` 的自定义角色分支。普通能力并集没有被全局替换。

### 原生 Owner 派生的一致性

相邻 `models/organization/governance.go` 原先对多条能力求并集，再据全部 Owner 能力认定原生 Owner。两条各自非 Owner 的真实可赋来源可拼齐该并集。现在公共授权读取仍返回能力并集，唯独 Owner 身份使用同一 `HasOwnerGrant`。原生 Owners Team 后备路径及有效身份过滤保留；父任务核对了原生组织、审计及生命周期消费者，没有引入第二套成员系统。

前两类具备实际越权链和严重影响，修复前成立 P1；原生派生点一并修复。最终判定针对修复后的当前代码，不把旧缺陷永久列为当前阻塞。

## 部署后同身份正反例

v11 macOS arm64 开发构建 SHA-256：`f89cd8fcf1a1128c8cc03c1549b22528f1466f5c2f903411a38ca18bdf2180a7`。确认本机唯一服务、没有活动／排队任务及两个 Runner 启用后，正常终止 v10、备份 SQLite 与配置、原子更新固定 `gitea-current` 入口并启动。首次预检误用了 Runner 字段 `disabled`，在停止服务前报错；核对实际字段 `is_disabled` 后才执行切换，没有强杀或中断中的半切换。

同一受限用户重新登录后：群组 15 审计页面显示原生 HTML 404；API 审计读取、导出创建均 404。群组 18 设置页不再提供恢复或删除控件；正确当前修订及路径的归档预览、恢复、永久删除 API 均 404，待删除记录不变。恢复后再次安排删除仍 404，不是仅隐藏按钮。

普通 Owner 在重启后的 UI 完成两个恢复正例：

- 群组 14 是原有空叶子，删除预览为 1 组、0 项目，保留期 30 天。v10 由真正 Owner 安排删除后创建入口消失；用实际 API 尝试创建子组返回 409，未新增记录。v11 重启后待删除路径、到期时间及恢复表单仍在；输入当时完整路径恢复后，原 `phase1-parent/child/deep/leaf` 与创建入口恢复。
- 群组 18 是上述越权红例的空样本。真正 Owner 可在 v11 恢复，原路径及未归档状态还原；受限身份无法恢复或重新安排删除。

只读数据库确认两个群组 `archived = false`、`delete_after = 0`，待删除任务数为 0。随后经正式 API 撤销本轮群组 15／18 对群组 17 的共享、用户 3 在 18／17 的成员关系，全部 204；没有残留越权样本授权。群组 17／18 本身作为测试记录保留，Owner 仍是测试用户 2。

## 自动化证据和纠正

子代理最初把带 `GITEA_TEST_DATABASE=pgsql` 的服务单测标为 PG。父任务核对 `models/unittest/testdb.go`，发现 `MainTest` 固定创建 SQLite，因此 `owner-identity-pg.jsonl`、`group-deletion-owner-pg.jsonl` **不是 PG 证据**。原日志保留且标明更正，详见[数据库证据](owner-identity-db-evidence.md)。

真正 PG 集成随后使用 `tests/integration`，新用例断言数据库类型并执行 `SELECT current_database()`，确认 `postgres / gitea_phase1_test`。父任务解析最终 JSONL，4 个顶层测试通过，包耗时 8.405 秒：

- `TestGovernanceAuditHTTP`；
- `TestGovernanceGroupDeletionRealGit`；
- `TestGovernanceOwnerIdentityRealDatabase`；
- `TestRepositoryInvitationLinkFromCollaborators`。

原始记录：`/Users/archer/.cache/gitea-phase1-v5-validation/v11-owner-identity-real-pg-3.jsonl`。既有删除测试的 SQLite 专属故障触发器在 PG 报语法错误；新增 PG 函数／触发器分支后验证同一审计失败回滚语义，SQLite 分支另行复测通过。没有放宽业务检查或跳过失败断言。其他单元红绿、格式与增量 lint 详见上述证据及原始日志。

由于原生 Owner helper 改动影响公共调用者，冻结后另执行一次必要的扩大回归：`go test -tags='bindata sqlite sqlite_unlock_notify' -p 1 -count=1 -json ./models/organization ./models/perm/access ./services/governance ./services/org ./services/actions ./routers/api/v1/shared ./routers/web/shared/actions`。7 个包全部通过，263 个顶层测试（含子项共 573 个 pass 事件），没有 fail。父任务重新解析原始 `v11-owner-helper-expanded-sqlite.jsonl` 确认；SHA-256 为 `5f1f1256f5f16a8a7a0ccd2a5f5431378acb2d3af185b750e1555db6448cb21d`。该批明确为 SQLite 单元与 handler 回归，不标成 PG 或真实浏览器验收。

## 验收边界

真实 UI 只覆盖空群组延迟删除、重启保留、恢复与受限身份反例；包含仓库、包及运行中任务的完整生命周期矩阵仍未完成。PG 的真实 Git 删除／回滚是集成证据，不冒充浏览器永久删除演练。

未提供的 Linux amd64／8 vCPU／16 GiB 容量环境、完整数据库兼容及人工节省 80% 继续未验证。一期不能标记完成。
