# 一期 Owner 撤权独立审查证据

## 已证实并修复

- API 编辑团队会先把请求中的名称写入团队对象，再判断是否为 Owners Team。原生团队通过服务 `UpdateTeam` 保存改名后，`IsOwnerTeam` 因按名称判断变为假；接着移除唯一成员会跳过最后 Owner 保护。
- 本机 SQLite 红例：`GITEA_TEST_DATABASE=sqlite3 go test -count=1 -run '^TestOwnerTeamRenameCannotRemoveLastOwner$' ./services/org` 退出 1，失败信息为“改名不得绕过最后 Owner 保护”，实际撤权调用返回 nil。
- 最小修复在团队更新写事务内重新读取原团队身份，禁止将原生 Owners Team 改名。相同用例转绿；原团队名称和唯一成员均保留。
- API 仅把该改名专属冲突映射为 HTTP 409，保留其他内部错误的既有映射。新增真实 PATCH 回归 `TestAPIOwnerTeamRenameConflict`，断言 409、明确消息和持久状态；该用例尚未运行。

## 已运行回归

- `GITEA_TEST_DATABASE=sqlite3 go test -count=1 ./services/org`：退出 0。
- `GITEA_TEST_DATABASE=sqlite3 go test -count=1 ./services/governance`：退出 0。
- `GITEA_TEST_DATABASE=sqlite3 go test -count=1 ./services/user`：退出 0。
- 定向 `TestMemberAcceptanceLastPermanentOwner`、`TestMemberAcceptanceExpiryBoundary`、`TestUnifiedOwnerConcurrentDemotion`、`TestPermanentOwnerRequiresActiveDirectOrInheritedSource`、`TestExpiredGrantsAuditAndRenewal`、`TestAdminCannotProhibitLastRepositoryOwner` 均实际运行并通过。
- `golangci-lint run ./services/org`：退出 0，报告 0 项；本次两个 Go 文件的 `git diff --check` 退出 0。
- 追加后的 `go test -count=1 ./services/org ./routers/api/v1/org`（本机 SQLite）：退出 0；`golangci-lint run ./services/org ./routers/api/v1/org`：0 项，四个 Go 文件的 `git diff --check`：退出 0。
- 集成测试包编译尝试失败于并行编辑中的 `tests/integration/org_webhook_test.go:41-42`，那里将字符串字段 `server.URL` 当函数调用；本轮没有执行新 API 集成用例，待该文件修复后由主任务在独占 PostgreSQL 环境重跑。
- 这些本机 SQLite 结果不等于 PostgreSQL 并发或真实浏览器验收；后者由主任务独立执行。

## 反证边界

- 直接治理成员与原生 Team 的正常撤权均经全局短写事务与当前状态下的永久 Owner 校验；共享及有到期时间的 Owner 不充当永久接替者。
- 服务层账号主动停用、禁登和删除入口检查其所维系的群组与项目 Owner；管理员主邮箱停用入口的模型缺口见下方补充反证。外部 OAuth 凭据失效的后台同步允许立即禁用身份并记录需人工接替事件，这是身份安全优先的独立行为。
- 未把测试缺失、仅有潜在路径或产品体验愿望作为 P0/P1 结论。

## 主邮箱停用与 Bot 保底补充反证（2026-09-23）

- 管理员邮箱页对其他用户的主邮箱提供停用按钮；`POST /-/admin/emails/activate` 调用 `ActivateUserEmail`。该模型方法原来在同一事务中停用主邮箱和账号，却没有执行 `EnsureUserCanLoseOwnerAccess`。服务层账号停用入口的检查不能覆盖它。
- 本机 SQLite 固定红例 `GITEA_TEST_DATABASE=sqlite3 go test -run '^TestDeactivatePrimaryEmailPreservesPermanentOwner$' ./models/user/` 退出 1：个人项目唯一 Owner、群组唯一治理永久 Owner 两个子例均错误返回 nil。修复在该模型事务中、账号从活跃转为停用前执行现有保底检查；测试确认拒绝时邮箱、账号保持原状，审计与二者同在事务中。增加有效永久接替者后允许停用，停用副邮箱仍不改变 Owner 账号状态。
- API `PUT /teams/{id}/members/{username}` 允许当前组织 Owner 调用原生 `AddTeamMember`；服务没有排除活跃 Bot。原保底查询只看账号活跃与禁登状态，既有原生 Owners Team 候选和治理直授候选都未限定人类账号。加入 Bot 后移除最后人类 Owner 会错误成功；这违反一期的永久有效**人类** Owner 条件。
- 本机 SQLite 固定红例 `GITEA_TEST_DATABASE=sqlite3 go test -run '^Test(NativeBotCannotReplaceLastHumanOwner|DirectBotCannotSatisfyPermanentGroupOwner)$' ./services/org/` 退出 1，两个测试均收到错误的 nil。修复仅在 `EnsurePermanentGroupOwner` 的原生旧范围查询和祖先候选最终查询限定 `user.type=0`。转绿后，Bot 不能替代最后人类 Owner；添加另一位永久有效人类 Owner 后原生移除操作可成功。
- 反证了另一条候选：组织账号虽可被原生 API 按用户名找到，其账号按原生语义 `is_active=false`，已有活跃检查不会把它算作有效 Owner；未将此候选列为阻塞问题。外部 OAuth2 `invalid_grant` 会强制停用失效身份并记录 `group.owner_attention_required`，属于凭据已失效时的恢复语义，不能靠继续信任失效身份维持表面 Owner；本补丁没有改变该路径。

## 本次实际验证与范围

- 修复后本机 SQLite：`./models/user/` 的主邮箱、副邮箱和相邻邮箱 5 项定向测试通过；`./services/org/` 的 Bot、原生 Owner、团队改名及占用 6 项定向测试通过；`./services/governance/` 的永久 Owner、邀请、共享 5 项定向测试通过。
- `golangci-lint run ./models/user/...` 与 `./services/org/...` 均报告 0 项；`go vet ./models/governance/ ./services/org/` 通过。本次四个 Go 文件的 `gofmt -d` 无差异，已跟踪改动的 `git diff --check` 通过。`golangci-lint run ./models/governance/...` 报告 7 项，均位于本次未修改的 `audit.go`、`models.go`、`native_approval.go`、`reference_transaction.go`、`reference_transaction_test.go`、`repository_creation.go`、`unified_approval_test.go`；不把该包静态检查记作通过，也未为本补丁改动这些并行文件。
- 本次仅改 `models/user/email_address.go`、`models/user/email_address_test.go`、`models/governance/permanent_owner.go`，新增 `services/org/permanent_owner_bot_test.go`。未使用共享 PostgreSQL 测试库；真实浏览器与 PostgreSQL 并发验收仍由主任务执行。
