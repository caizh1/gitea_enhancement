# 原生团队用户写入最终授权证据

日期：2026-09-23。本包仅修复用户发起的原生 Team 写入；没有操作运行实例、浏览器或真实成员。测试只用仓库固定样本和合成数据。

## 已复现的首错

Web `TeamsAction/add` 在路由阶段缓存 `ctx.Org.IsOwner`；若治理 Owner 在此后撤权，旧代码直接调用没有操作者参数的 `AddTeamMember`，其写锁内不复核操作者。固定用例 `TestTeamActionCannotAddAfterOwnerRevocation` 先给请求设置已通过的 Owner 快照，再撤销其原生 Owner，旧代码仍返回 303 并把目标加入 Owners Team；修复后返回 404 且未加入。API `/teams/{id}/members` 与新建、编辑、删除 Team，以及团队/仓库两侧的仓库分配入口存在相同预查到写入窗口。

## 最小修复与消费者

- `services/org/team_actor.go` 让当前操作者、目标 Team、目标账号在同一个 `governance.WithWrite` 内重读。新增成员/团队配置还检查组织与祖先的归档、待删状态；撤销成员、删除 Team 保持可用。`services/org/team.go` 的更新逻辑补当前 Team 重读和 Owners Team 改名阻断，避免旧对象写错或改变 Owners 身份。
- `services/repository/repo_team_actor.go` 对当前 Team、组织、操作者和仓库归属做最终复核。组织团队页要求当前 Owner；组织 API、仓库设置页和仓库 API 保留 `RepoAdminChangeTeamAccess` 委派，但还要求当前仓库整体 Admin。新增关系拒绝归档/待删，撤销关系允许。无 Code 单元的仓库用整体仓库 Admin 判定，避免合法管理员被错误拒绝。
- `routers/web/org/teams.go` 接入成员增删、加入团队、团队新建/编辑/删除、单仓及全仓分配；`routers/api/v1/org/team.go` 接入对应 API；`routers/web/repo/setting/collaboration.go` 与 `routers/api/v1/repo/teams.go` 接入独立仓库设置/API 分配入口。最终撤权、对象消失、生命周期冲突分别映射安全的 403/404/409。
- 原有无操作者的服务函数保留给初始化、LDAP、内部清理等调用；本次没有改变其内部语义。用户发起的上述四组入口已全部改用带操作者包装。

## 执行结果

- 首错红例：`go test -run '^TestTeamActionCannotAddAfterOwnerRevocation$' -count=1 ./routers/web/org`，旧代码实际 303 且加入 Owners Team；修复后同命令通过。
- `go test -run '^(TestNativeTeamActorWrite|TestTeamInvite)' -count=1 ./services/org` 通过，涵盖当前 Owner 正向、撤权后五类写入拒绝、已删除 Team 拒绝、邀请原子化与归档边界。
- `go test -run '^TestTeamRepository(ActorWrite|Archive)' -count=1 ./services/repository` 通过，涵盖委派管理员正向、委派开关撤销、仓库 Admin 降权、Owner 撤权、Team 删除、归档拒增允删。
- `go test -run '^(TestDeleteTeam|TestAddTeamPost.*)$' -count=1 ./routers/web/repo/setting` 通过。历史测试中没有操作者的模拟请求已补真实站点管理员夹具；测试也覆盖没有 Code 单元的仓库。
- `GITEA_TEST_DATABASE=sqlite go test -tags 'sqlite sqlite_unlock_notify' -run '^TestOrgTeam(EmailInvite.*|InviteRevokedInviterCannotGrantOwner|InviteRevokedGovernanceOwnerCannotGrantNativeOwner|InviteArchivedGroupRejectsNewGrantButAllowsRevocation)$' -count=1 ./tests/integration` 通过。
- `PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH /Users/archer/go/bin/golangci-lint run --new-from-rev=HEAD ./services/org/... ./services/repository/... ./routers/web/org/... ./routers/web/repo/setting/... ./routers/api/v1/org/... ./routers/api/v1/repo/...` 报 `0 issues`；`git diff --check` 通过。
- `go test ./services/org ./services/repository ./routers/web/org ./routers/web/repo/setting ./routers/api/v1/org ./routers/api/v1/repo` 中五包通过；`services/repository` 的既有 Git hook 用例因仓库根 `gitea` 为 Linux 二进制，在本机报 `Exec format error`。没有覆盖该文件；父任务将在隔离副本构建 macOS 可执行文件后复验。

## 对抗式自审

VERDICT: PASS

P0/P1 BLOCKERS: 未发现。已反证路由缓存权限、团队删除后的旧指针、组织错配、委派开关撤销、仓库 Admin 撤销、归档期间增权和站点管理员路径；最终校验与写入共享治理短事务。

UNVERIFIED RISKS: 此增量未在 PostgreSQL 或运行实例重测；父任务已接手整合复验。用户发起入口已接通，但后台 LDAP/初始化仍走无操作者函数，其安全性依赖各自上游身份来源，不在本包目标内。

NON-BLOCKING FINDINGS: 无本包新增问题。
