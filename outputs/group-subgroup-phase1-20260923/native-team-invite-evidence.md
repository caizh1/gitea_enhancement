# 原生团队邀请最终授权修复证据

日期：2026-09-23。本记录覆盖原生 TeamInvite 的创建、接受和撤销；没有操作运行中的服务、浏览器或真实用户。测试账号和邮件均为仓库固定样本或合成值。相邻的原生 Team 用户写入另见 `native-team-actor-evidence.md`。

## 首错与因果链

`services/context/org.go` 将当前治理 Owner 视为原生组织 Owner，因此高级团队页可向原生 Owners Team 发邀请。旧 `TeamInvitePost` 仅凭 token 和邀请时记录的 inviter，先执行 `AddTeamMember`，再另一次删除邀请；邀请者撤权后，旧 token 仍可让受邀人进入 Owners Team。`models/governance/membership.go` 随即把该原生团队身份计为群组及后代 Owner。

先增加固定 SQLite HTTP 用例 `TestOrgTeamInviteRevokedInviterCannotGrantOwner`，在修复前执行：

```text
GITEA_TEST_DATABASE=sqlite go test -tags 'sqlite sqlite_unlock_notify' -run '^TestOrgTeamInviteRevokedInviterCannotGrantOwner$' -count=1 ./tests/integration
```

实际结果：期望旧 token POST 返回 404、受邀人未入组；旧实现返回 303，受邀人已经进入 Owners Team。该用例在修复后通过。

## 修改与安全边界

- `services/org/team_invite.go`：创建、接受和撤销都在 `governance.WithWrite` 内重新读取团队、组织、邀请者和当前权限。接受时还重新读取受邀账号活动状态；邀请复读、加入成员、删除邀请在同一数据库事务内完成，删除必须恰好影响一条记录，否则回滚。撤销与接受受同一写锁排序。
- 创建和接受新增当前组织及真实祖先 `Archived/DeleteAfter` 检查，归档或待删时拒绝新增成员；撤销邀请仍可进行。无治理命名空间的旧原生组织维持原行为。
- `routers/web/org/teams.go`：原生创建、撤销、接受入口统一调用上述服务；撤权、团队归属不符、账号失活等返回 404。现任站点管理员仍有原先的组织管理权限。
- 保留 bearer 链接语义：受邀邮箱不绑定兑换者，任何当前有效且持有 token 的登录账号仍可接受合法邀请。未增加到期机制或新的邀请表。
- 此修复未修改治理成员模型和运行实例。

## 验证

- SQLite：`go test -run '^TestTeamInvite' -count=10 ./services/org` 的邀请原子化版通过；随后新增归档、待删、祖先归档用例，`go test -run '^(TestNativeTeamActorWrite|TestTeamInvite)' -count=1 ./services/org` 通过。
- SQLite：`go test -race -run '^TestTeamInviteConcurrentRedeemAndRevoke$' -count=3 ./services/org` 通过，无测试侧数据竞争。
- SQLite HTTP：`GITEA_TEST_DATABASE=sqlite go test -tags 'sqlite sqlite_unlock_notify' -run '^TestOrgTeam(EmailInvite.*|InviteRevokedInviterCannotGrantOwner|InviteRevokedGovernanceOwnerCannotGrantNativeOwner|InviteArchivedGroupRejectsNewGrantButAllowsRevocation)$' -count=1 ./tests/integration` 通过；现有注册、登录、激活邀请流程继续通过。治理 Owner 路由用例从团队页创建 Owners Team 邀请，撤权后旧 token 被拒且未入组；归档用例确认旧邀请不可接受、新邀请不可创建而撤销可用。
- PostgreSQL：在 `/Users/archer/.cache/gitea-phase1-integration-v3-v6imiqb5` 仅同步最初邀请原子化四文件，使用隔离数据库 `127.0.0.1:15433/gitea_phase1_test` 单进程运行当时 HTTP 用例集合，`ok 4.250s`。测试库口令由进程环境提供，证据不记录值。后续归档检查和 Team 最终授权尚未在 PostgreSQL 重跑，需父任务整合验收。
- 定向 `golangci-lint run --new-from-rev=HEAD ./services/org/... ./routers/web/org/...`：`0 issues`。加入 `./tests/integration/...` 时仅剩另一工作包 `governance_release_archive_test.go:324` 的 `modernize/rangeint` 提示，本包测试文件无报告。`git diff --check` 通过。

## 对抗式自审

VERDICT: PASS

P0/P1 BLOCKERS: 无。已逐项反证缓存的 `ctx.Org.IsOwner`、旧 inviter 对象、团队跨组织归属、停用账号、重复兑换与撤销/接受交错；最终授权只读当前数据库，接受的三项写入同一事务。站点管理员路径与已有 bearer、注册/激活流程已回归。

UNVERIFIED RISKS: 最终源码未在运行实例做真实 UI 验收，归档增量未跑 PostgreSQL。邮件发送仍在邀请持久化提交后执行，若发送失败会留下可撤销的邀请，这是原有行为，本次未更改。

NON-BLOCKING FINDINGS: 无本包新增问题。
