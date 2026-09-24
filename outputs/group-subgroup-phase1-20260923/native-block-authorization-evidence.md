# 原生组织屏蔽授权与生命周期证据

日期：2026-09-24。范围仅为原生组织/个人用户的屏蔽、解除屏蔽、屏蔽备注，以及屏蔽附带的关系清理；不含 OAuth、Actions 或真实 UI 验收。修改前 `services/user/block.go` 已保存到本机缓存 `native-block-before-20260924.go`，未覆盖工作树的既有改动。

## 调用与边界

- Web 原生组织设置及个人设置、API 组织及个人屏蔽入口最终调用 `services/user/block.go`；同页备注原先从 `routers/web/shared/user/block.go` 直接调用模型更新，现也通过服务层。
- 原有 `CanBlockUser`/`CanUnblockUser` 在写事务外执行；现在最终写入先取得治理写锁，在同一事务重载当前操作者、屏蔽方、被屏蔽方及 Owner 权限，并复核管理员代办身份。原生旧组织尚无治理 Namespace 时保留原有兼容。
- 屏蔽会撤销关注、收藏、订阅、指派、协作者和待转移关系，因此归档/待删时仍允许收窄访问；解除屏蔽会放开访问，在组织或任一祖先归档/待删时返回冲突。备注不扩大访问，归档时仍可修改，但需要当前 Owner/管理员资格。Web 返回明确本地化错误，API 返回 409。
- 协作者删除和转移取消继续复用现有服务及嵌套治理事务；未引入网络或 Git 操作。与治理归档、成员变更、管理员原生更新共用写锁。批量删除循环固定读取第一页，并以已处理 ID 防止未推进时无限循环。

## 红绿记录

- 修复前执行 `go test -run '^TestNativeBlockRechecksActorAndLifecycle$' -count=1 ./services/user`：旧管理员快照屏蔽返回 nil（预期拒绝）；归档组织解除屏蔽返回 nil（预期冲突）。首次 Owner 样本误取非 Owner 用户导致断言失败，改为实际 Owner 后原逻辑在顺序撤权时已拒绝，未把该样本冒称为确定性竞态复现。
- 增加真实 26 条以上的组织仓库协作者关系后，修复前执行 `go test -run '^TestNativeBlockRechecksActorAndLifecycle/more_than_one_page_of_collaborations$' -count=1 ./services/user`：屏蔽返回成功，但仍剩 1 条协作者关系；修复后同命令通过，剩余为 0。该分页问题是原有缺陷，未预设 P1 等级。
- 最终执行 `/Users/archer/.cache/gitea-governance/go/bin/go test -count=1 ./services/user ./routers/web/shared/user ./routers/api/v1/shared`：三个包通过。测试包含旧原生无 Namespace 正例、个人用户兼容、父 Owner 管子组、子 Owner 不管父组、撤销 Owner/管理员、管理员代办撤权、归档及待删拒绝解除屏蔽、归档允许屏蔽和备注。
- 定向 lint 首次因 PATH 不含 Go 而未启动；加 `PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH` 后执行 `/Users/archer/go/bin/golangci-lint run --timeout=5m ./services/user ./routers/web/shared/user ./routers/api/v1/shared`，结果 `0 issues.`。双语 JSON 解析及目标文件 `git diff --check` 均通过。

## 未验证与反证

- 未使用共享 PG、运行中 UI 或实例数据；父任务仍需真实 UI/API 归档解除屏蔽同对象复验及 PG 原生 `TestBlockUser`。本轮没有把目标包编译冒称为真实端点验收。
- 原生管理员组合入口 `UpdateAdminUser` 已使用治理写锁；LDAP/OAuth 同步可直接调用 `UpdateUser`，与屏蔽在途事务的严格交错尚未验证，已交父任务协调独立范围。屏蔽清理大量关系时的治理锁持有时间亦未做容量量化。
- 旧有双向副作用存在待固定场景：个人屏蔽方若是被屏蔽方仓库的协作者，`DeleteCollaboration` 会按仓库管理者复核；屏蔽方作为待转移接收方时，现有 `CancelRepositoryTransfer` 只允许发送方/原仓库管理者。这两种真实组合尚未跑固定反例，不能宣称已通过，也不能仅凭源码推断列为 P1。

审查结论：本轮修复范围内未发现已成立的 P0/P1 阻塞；上述交错和容量为未验证风险。
