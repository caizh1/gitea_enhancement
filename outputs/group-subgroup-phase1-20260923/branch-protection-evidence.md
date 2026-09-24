# 一期群组基础分支保护实施证据

记录时间：2026-09-23 20:23 CST。目标用户是顶级群组 Owner；人工基线是给当前每个仓库逐一配置并在新仓、移动和规则变化时重复核对。目标是一次配置后对当前子树实时生效，减少至少 80% 的重复配置与判断。输入条件是已完成治理 Namespace 初始化、仓库当前归属明确，Git 引用事务 Hook 已启用。支持边界仅含基础分支模式、推送与合并角色、强推；不含群组级代码所有者、完整自定义角色或环境权限。实际节省比例与生产误判率尚未测量，不能声称达到目标。

## 当前实现

- 顶级群组设置保存基础规则，校验当前根群组 `ManageGroup`、Namespace 修订及有效状态；使用当前仓库 Owner 的 `Ancestors` 解析根源，不沿共享边继承，也不复制到仓库。规则写入在治理短事务内检查受影响子树的活跃引用与合并占用，避免改变在途写入的许可。
- 对每个分支取全部匹配群组规则与原生仓库首匹配规则。推送、合并基础允许集合取并集；群组 `PushRole` 是“允许推送并合并”，合并还须当前仓库原生合并能力或有效 `MergeCode`，自定义 Push-only 能力不能借此合并。强推还须有效推送权及任一规则允许。原生签名、状态、审批、保护文件与强制治理门禁仍独立执行。任一群组规则命中即禁止 Git 删除。群组匹配区分大小写，`*` 可跨斜杠匹配。
- Git HTTP/SSH 的 pre-receive 检查和引用事务 prepared 阶段读取当前规则；prepared 重新检查群组保护的删除、推送、强推和部署密钥。受保护 PR 合并在最终授权的治理写事务内核对当前规则。Web/API 文件写入、分支改名、分支列表和分支 API 回显使用有效保护。
- 根群组设置页提供 `main`、`master`、`develop`、`release/*` 输入提示及保存、修改、删除；非管理者只读。仓库设置页显示继承规则和来源；来源路径须当前用户有 `ReadGroup`，管理链接须当前用户有 `ManageGroup`。仓库页无法编辑群组来源。
- 治理 API：`GET /api/v1/governance/groups/3/branch-protections` 返回当前修订和规则；`POST` 创建示例为 `{"revision":7,"rule_name":"release/*","push_role":30,"merge_role":40,"allow_force_push":false}`；`PUT /api/v1/governance/groups/3/branch-protections/12` 使用同样正文；`DELETE /api/v1/governance/groups/3/branch-protections/12?revision=8`。实际 ID、修订须先读取，不可照抄示例。仓库管理员可 `GET /api/v1/repos/{owner}/{repo}/inherited_branch_protections` 查看只读来源；无来源读取权时仅显示受限来源 ID，管理链接仅来源 Owner 可得。管理入口是 `/governance/groups/3?tab=settings`，仓库只读来源在 `/{owner}/{repo}/settings/branches`。

## 已执行验证

- `/Users/archer/.cache/gitea-governance/go/bin/go test -run '^(TestGroupBranchProtectionFollowsCurrentAncestryAndReservations|TestAddGroupProtectedBranches|TestPreReceiveCanWriteCodePerBranch)$' ./services/governance/ ./models/migrations/v1_27/ ./routers/private/ ./services/repository/ ./services/repository/files/ ./services/convert/ ./routers/api/v1/governance/ ./routers/api/v1/`：退出码 0。覆盖固定根→子→仓库、无关仓不继承、大小写、修订冲突、非法模式、非 Owner、引用与合并占用、原生允许集合并集、迁移幂等；后续新增原生未保护文件例外的最终拒绝回归，单独运行 `go test -run '^TestGroupBranchProtectionFollowsCurrentAncestryAndReservations$' ./services/governance/` 退出码 0。
- `PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH /Users/archer/go/bin/golangci-lint run --new-from-rev=HEAD ./models/git/... ./services/governance/... ./services/repository/... ./services/convert/... ./services/pull/... ./routers/private/... ./routers/api/v1/... ./routers/web/governance/...`：退出码 0，0 issues。包含仓库设置包的首次全范围检查因并行 Webhook 源码临时 typecheck 错误而中断；父代理已修复，正在复查。
- `go run ../../build/generate-bindata.go ../../templates bindata.dat` 在 `modules/templates` 执行成功；父审后模板再次生成，当前 SHA-256 为 `41c978e458b5fe2916881d8131c3005de7240438a7a4f8f52e8712db6611dbc8`，生成文件被 `.gitignore` 排除。`go test -tags bindata -run '^$' ./modules/templates/` 退出码 0。整合复制目录须在同步最终模板后自行重生成。
- `git diff --check` 退出码 0。未运行共享 PostgreSQL 测试库或实际 Git/UI 验收。

## 父审后语义修正（20:29 CST）

- [GitLab 官方保护分支说明](https://docs.gitlab.com/user/project/repository/branches/protected/)给出 `*gitlab*` 同时匹配 `gitlab/staging`、`master/gitlab/production`，且 Allowed to push and merge 也允许 MR 合并。先固定红测：跨斜杠模式未命中；`PushRole=Maintainer、MergeRole=0` 的有仓库合并能力用户被拒；prepared 对 `ActionsUserID` 返回 nil。随后将群组 glob 改为无分隔符匹配，合并基础允许集合加入 `PushRole` 并保留当前仓库合并能力门槛。对应三项及原有回归定向测试已转绿。
- Actions 窗口的反证：旧 `cmd/hook_reference.go` 未传任务 ID，且旧 `governanceGitActor` 用 `GetUserByID(-2)` 处理 Actions 合成用户，因此旧生产路径会在 prepared 之前拒绝 Actions 引用写入；此前红测直接调用内部函数所见的 nil 不构成已证实生产越权。现在引用 Hook 传递任务 ID，真实操作者解析与 prepared 写事务均重新运行现有 `GetActionsUserRepoPermission`，包括无群组规则的写入，保留任务、来源/目标及 token 写范围限制。固定样本证明：原先无群组规则时一个运行中任务的 token 确有代码写权；新增群组规则后 group-only 写入被最终拒绝；为测试构造原生显式白名单时可写；取消任务后再被拒。该显式合成用户白名单只是测试夹具，不表示管理 UI 支持此配置。群组角色不赋予合成用户。镜像系统例外收窄到当前仓库确为镜像及内部镜像操作者。真实 Actions 写入仍待整合验证。

## 对抗式复核与差异

VERDICT: PASS（限当前静态实现和单元证据；不代表真实协议/UI 产品验收）。

P0/P1 BLOCKERS: 无。特别检查了子级 Owner 改父级规则、共享边误作配置继承、pre-receive 后新增规则、受保护删除、强推与合并授权竞态。原生“未保护文件”例外在命中群组规则且用户没有有效推送权时一律保守拒绝，避免最终检查误信任未验证的文件差异；这是一期语义差异。

UNVERIFIED RISKS: 真实 PostgreSQL 迁移、HTTP/SSH Git 推送和强推、网页/API 修改、PR 合并及自动合并、模板真实浏览器渲染、多进程并发、MySQL 大小写唯一索引行为均待父代理整合验收。`bindata.dat` 是被忽略的本地产物，必须在最终源码快照重生成。性能与人工节省比例未测量。

NON-BLOCKING FINDINGS: 仓库原生规则仍按首匹配及原有精确名称不区分大小写行为；GitLab 对全部匹配规则取字段级最宽松结果，故一期不宣称完全等价。仓库部署密钥在仅命中群组规则、无原生允许规则时保守拒绝。顶级群组“初始默认分支保护”与本次实时基础规则不同，未在此复制新仓默认规则。
