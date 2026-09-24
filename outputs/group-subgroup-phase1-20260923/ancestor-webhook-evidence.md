# 一期祖先 Webhook 实现证据

记录日期：2026-09-23。工作树为同一目录内的未提交变更；本记录仅覆盖 I-05 Webhook 工作包，不表示一期整体完成。

## 人工基线与边界

原生 Webhook 事件准备仅查询仓库、本级拥有者及系统 Hook。顶级／中间群组要接收孙级项目事件，需要对每个项目重复建 Hook；转移或移动期间的迟到事件可能按准备时的新归属查询。目标用户是管理群组 Hook 的原生组织 Owner。输入是已有仓库 push、Issue、PR、评论、审查及 workflow_run／workflow_job 事件。本期不扩展 fork、release、package、wiki 等事件的祖先范围，也不新增群组管理事件产品。

原生 Hook 的新增、修改、删除、测试、历史和人工重投入口继续承担来源管理；子群组 Owner 可触发父 Hook 所订阅的项目事件，但不获得父 Hook 配置或历史的管理权。未进行真实 UI 与真实外部接收端验收，尚不能量化人工节省比例或宣称达到 80%。

## 实际调用链与最小实现

- `models/webhook/webhook.go` 的原生创建／修改／删除原已进入 `governance.WithWrite`。新增配置修订；停用在同一写事务将尚未授权的任务标为终态；删除保留任务 ID、事件类型、投递状态等事实，清空载荷、密钥快照及请求细节，审计保留脱敏配置差异。`models/webhook/governance_audit.go` 的组织／用户批量删除也调用同一脱敏取消逻辑。
- `services/webhook/webhook.go` 的 `PrepareWebhooks` 在短写事务中重新读取仓库，比较事件对象的 OwnerID 与 ActionsScopeRevision，再由 `governance.Ancestors` 取本级至根的真实父链，不查询共享关系。仓库、本级组织、祖先组织、系统 Hook 按 Hook ID 去重；不同 ID 即使 URL 相同仍各自产生任务。超出本期事件目录只保留原仓库／直属组织／系统投递。
- 入队任务保存 RepoID、事件范围修订、来源 ID 链、Hook 配置修订、原始载荷以及配置加密快照。仓库的迟到旧事件被明确拒绝并由通知调用方记录错误；已入队旧事件继续绑定原 Hook，不会改投新祖先。已有 `secret.EncryptSecret` 是 AES-CFB，不能验证完整性；新增 HMAC 覆盖密文、Hook ID、来源、修订及载荷，篡改后发送授权会取消任务。
- `services/webhook/deliver.go` 的发送授权与停用／删除共用治理写事务。授权时回读任务、当前 Hook 活跃状态及快照完整性，提交一次性 `is_delivered` 决策后才构造请求并进行网络投递，不在数据库写锁内发网络请求。授权之后发生的停用／删除不撤销已授权的网络尝试，符合明确的线性化边界；不承诺恰好一次。
- 对事件时祖先 Hook，发送授权还会核对 RepoID 仍存在；仓库已删除则终态取消，不向已删除项目的祖先端点发送积压内容。直属组织／系统 Hook 保留原生 repository-deleted 等事件行为。
- 人工重投重新读取当前 Hook 与仓库祖先范围，用当前 Hook 配置创建新签名快照；若来源 Hook 已不适用则拒绝。旧版 v2 历史任务可从原始 `repository.id` 重建并重新校验来源；无法证明来源的旧 v1／空载荷组织任务拒绝重投。历史测试投递仍由原生仓库 Hook UI/API 进入新快照入队路径。
- 子群组原生组织 Hook 设置页增加祖先来源只读提示。先查有已启用 Hook 的真实祖先，再按当前操作者的 `ReadGroup` 过滤路径与数量；无权时仅显示受限群组 ID。跳往父来源设置／历史页必须同时有治理 `ManageGroup` 和原生组织 Owner 资格。此投影不携带 Hook URL、密钥或投递载荷。
- 迁移 373 为新列建表结构并将升级时仍待发、缺少事件时范围及快照的旧任务标为已取消；保留其历史载荷供符合条件的人工重投。新部署由注册模型直接创建列。

## 自动化与源码反证

- `go test ./models/webhook ./services/webhook ./models/migrations/v1_27 ./routers/web/org -count=1`：四个包全部通过。`go test ./services/repository -run '^TestDeleteRepositoryRetainsRedactedWebhookFacts$' -count=1`：通过。运行时移除了本机 HTTP 代理环境变量；未移除时仅原有 `TestWebhookProxy` 的外部代理断言失败，实际读到代理 `127.0.0.1:1082`，与功能修改无关。
- `golangci-lint run --timeout 5m ./models/webhook/... ./services/webhook/... ./models/migrations/v1_27/... ./routers/web/org/...`：0 issue。
- 仓库 Web/API Hook 消费者和 `services/repository`、`services/governance` 六个包定向编译通过；`git diff --check` 通过。未运行共享 PostgreSQL、浏览器、真实 Runner 或外部 Webhook 接收端。
- 正例：三级祖先加仓库 Hook 各有独立任务；两个相同 URL、不同 ID 的父／子 Hook 均保留；配置修改后旧任务仍向旧地址投递；新任务和可证明来源的旧版任务人工重投成功。
- 反例：兄弟群组和实际分享关系中的链外群组不继承；Release 不向祖先扩散；陈旧 ActionsScopeRevision 拒绝入队；转出祖先范围后拒绝重投；停用取消待发且恢复活跃后不复活，已提交的发送授权不被追溯取消；删除清空敏感历史字段；改写来源链或载荷的任务在发送前取消；无来源的旧任务拒绝重投；事件仓库已删除的祖先任务拒绝发送。
- UI 权限正反例：能读取但不能管理父组的子组 Owner 仅看到来源路径与启用数量，不获得设置链接；同时有治理管理与原生 Owner 资格者获得来源链接；父组转为私有且操作者无读取权时，路径、数量与链接均被清空。
- 源码调用者检索确认生产中的任务创建仅在 `services/webhook/webhook.go` 的准备和重投两处；旧模型级无校验重投已移除。原生 Web/API 设置均通过 `UpdateWebhook` 与 `DeleteWebhookBy...`；组织／用户批量删除走 `DeleteOwnerWebhooks`。

## 对抗审查与剩余集成

VERDICT: PASS（本工作包及已接入的项目删除路径；真实 UI 和外部接收端仍未验证）。

P0/P1 BLOCKERS: 无。项目删除原有直接删除任务的问题已由父任务在 `services/repository/delete.go` 改为同治理锁下逐 Hook 取消与脱敏；外层数据库事务持锁至提交。定向删除测试保留了任务事实与取消原因，反证了原阻塞路径。

UNVERIFIED RISKS:

- 真实浏览器中的来源权限、原生历史与人工重投按钮、外部接收端的网络签名/SSRF/TLS 行为未运行；静态代码和本地 HTTP 测试不能替代真实验收。
- 模板工具 `uv` 在当前 shell 不存在，`djlint` 未运行；模板 Go 语法与页面布局仍待真实 UI 验证。
- 已发送授权后服务进程崩溃仍沿用原生“一次尝试”语义，任务不会自动重发；需要人工确认失败历史后重投。

NON-BLOCKING FINDINGS:

- 仓库设置页仍只显示仓库直属 Hook；子群组组织设置页已显示受权限过滤的祖先来源。父组织原生 Owner 可在来源组织 Hook 页管理、查看历史及重投。
- Hook 删除后原生详情 URL 不再可用；脱敏任务事实留在数据库并由治理审计关联，仍受原生历史清理策略影响。
