# 协作者统一成员与 Owner 权限验收记录

日期：2026-09-22。环境：macOS arm64、Go 1.26.4、SQLite、真实本地 HTTP/SSH Git 服务。源码基线为当前工作区，保留本次开始前已有修改。此记录不是 Ubuntu 离线包或生产环境发布验收。

## 实现结果

项目 `/owner/repo/collaborators` 成为统一入口；项目页、设置页和旧治理成员入口指向同一成员视图。上方每人一行，按用户 ID 去重，实际角色只读；展开来源显示直接、继承、共享、原生团队、原生协作者和个人所有者。下方保留邀请群组。支持搜索、来源筛选、角色筛选、仅看 Owner，并先筛选再分页。

新增授权写入既有治理 Membership；已有原生协作者可独立调整、移除，原生 Owners 团队保留。添加默认 Developer，普通角色为 Guest、Planner、Reporter、Developer、Maintainer、Owner，自定义角色在高级选项中。没有新增另一套 Owner 数据表或权限引擎。

Owner 必须由显式 Owner 来源产生，并通过全部共享上限的能力交集。普通管理员团队映射 Maintainer；普通角色或自定义能力相加不能升级为 Owner。站点管理员保留管理能力，只在实际成员行标示站点管理员身份，不自动加入所有项目的成员名单。

直接 Owner、共享 Owner 可管理项目；不会获得所属组织和兄弟项目的权限。Maintainer 对已有 Owner 的角色、有效期、删除以及原生协作者入口均受到保护。归档入口在治理写锁内重新核对 Owner；删除、转移沿用同一实际权限结果，转移还检查目标命名空间权限和永久 Owner。

永久 Owner 保障覆盖直接授权变化、用户停用/禁登/删除、原生 Owners 变更和转移。临时、共享、待接受邀请和站点管理员兜底身份不计为永久保障。失败说明包含需安排接替所有者的项目。

## 预览、隐私和审计

- 预览执行真实变更校验后回滚，授权、修订号和审计均不落库。
- 预览版本覆盖全部参与计算的来源、账号状态、角色配置、项目归属/可见性及修订号；提交在写锁中重新校验。来源变化或到期导致冲突，要求重新预览。
- 页面修改输入后清除旧预览；错误保留输入。Owner 相关变更和自身权限变化明确确认；列表内 Owner 撤权也有确认弹窗。
- 共享撤销预览显示实际角色变化、剩余来源、共享上限和公开/内部可见性的基础访问提示。
- 成功记录来源前后值及逐成员实际权限影响，避免大群组超过单条审计 64 KiB 上限；失败记录分类原因。审计与授权在同一事务提交。
- 私有项目无权用户返回 404。安全成员视图不返回邮箱、完整用户记录或私有来源内部标识。
- 公开访客仅看到公开成员来源；私有邀请群组独有的成员不能通过搜索或分页枚举。有限视图明确提示角色按可见来源计算。项目成员可以查看成员名单，但私有来源名称、链接仍按独立权限裁剪；项目 Owner 可以识别本项目邀请的群组，不会因此得到来源群组管理链接。

## 验收证据

| 项目 | 结果与边界 |
|---|---|
| Go 回归 | 9 个包全部通过：治理服务、治理模型、访问模型、仓库服务、用户服务、仓库模块、上下文服务、仓库 API、仓库设置路由。使用 `-count=1`，非缓存结果。 |
| 新增固定样本 | 5 个统一成员测试通过：直接 Owner、共享上限、普通管理员团队、自定义能力防扩权、隐私、到期、原生来源独立移除、并发降权、过滤分页。 |
| 预览与审计 | 预览不写 Membership、修订号和审计；实际提交有权限影响审计。变更来源后即使使用新修订号，旧预览仍被拒绝。 |
| 永久 Owner 并发 | 两位直接 Owner 同时降权，只允许一笔成功；最后一位永久 Owner 的降权被拒绝。临时 Owner 不能满足接替保障。 |
| 分页规模 | 205 条固定授权中有 103 位 Owner，筛选后正确返回 100 + 3，不漏人、不重复。一次独立样本读取约 81 毫秒；不作为生产容量承诺。 |
| 真实 HTTP/SSH | 直接 Owner 两种协议均可克隆、推送；撤销后两种协议均拒绝克隆、推送。共享 Owner 两种协议可推送；共享降为 Developer 后保留代码写入但归档被拒绝；共享到期后两种协议的读取和推送立即被拒绝，无需清理任务。 |
| API 与页面 | Maintainer 可以打开协作者页，不能修改 Owner、撤销 Owner 或管理共享；原生协作者 API 不能绕过。直接 Owner 归档/恢复成功，Maintainer 归档被拒绝。无代码读取能力的 Guest 仍能读取允许的成员元数据。 |
| CSRF | 跨站来源的页面预览 POST 被拒绝，返回 403。 |
| 综合集成 | `TestUnifiedMembersHTTPAndSSH`、`TestGovernanceGroupHTTP`、`TestAPIRepoCollaboratorPermission` 合并运行通过，最后一次总耗时 10.850 秒。其中真实协议新增 15 个子场景全部通过。 |
| 前端自动测试 | 2 个 Vitest 样本通过：编辑输入清除旧预览、失败保留输入；取消列表内 Owner 撤权确认后不提交。 |
| 浏览器 | 桌面及 390 × 844 窄屏检查通过；窄屏 `clientWidth=scrollWidth=390`，成员容器宽 374。验证默认角色、弹窗、键盘展开来源、仅看 Owner、共享 Owner 预览和取消。浏览器演练不提交权限变更，真实提交由隔离集成样本覆盖。 |
| 构建 | 原生 Go 可执行文件及 Vite 前端构建成功。预览实例使用独立数据库和仓库目录。 |
| 格式与 lint | 执行 `make fmt`；本次新增 Go 文件专项 lint 无报告，相关 TS 的 ESLint 通过，`git diff --check` 通过。全量 `make lint-go` 仍报告 61 项既有检查问题；全量 JS lint 也存在其他文件的既有问题，不能宣称全仓 lint 通过。 |

主要测试命令：

```bash
go test -count=1 -tags 'sqlite sqlite_unlock_notify' \
  ./services/governance ./models/governance ./models/perm/access \
  ./services/repository ./services/user ./modules/repository \
  ./services/context ./routers/api/v1/repo ./routers/web/repo/setting

GITEA_TEST_DATABASE=sqlite go test -count=1 -v \
  -tags 'sqlite sqlite_unlock_notify' ./tests/integration \
  -run '^(TestUnifiedMembersHTTPAndSSH|TestGovernanceGroupHTTP|TestAPIRepoCollaboratorPermission)$'

pnpm exec vitest run web_src/js/features/repo-members.test.ts
pnpm exec eslint web_src/js/features/repo-members.ts \
  web_src/js/features/repo-members.test.ts web_src/js/features/repo-settings.ts
```

集成测试的 Git Hook 必须调用与执行主机架构一致的 Gitea 二进制。验收期间临时使用本机构建，结束后恢复仓库根目录原二进制，不能把 Linux ELF 直接用于 macOS Hook。

## P0/P1 对抗审查

VERDICT: PASS

P0/P1 BLOCKERS：无已成立且未修复的问题。

审查范围包含授权能力合并、共享上限、角色/能力分离、原生团队、直接授权、原生 API、邀请接受与申请批准调用链、项目生命周期、永久 Owner、账号停用、并发写锁、预览回滚/过期、只读视图、字段裁剪、分页过滤、CSRF、前端输入及取消流程。

审查中定位并修复的严重路径：归档 API 原先允许仓库管理员进入，不能仅依赖页面上的 Owner 按钮。现在归档服务在治理写事务中读取当前 Owner 权限；直接 Owner 成功、Maintainer 被拒绝的接口测试已通过。普通团队管理员不再由底层 AccessMode 自动变成 Owner。已反证站点管理员管理能力、合法组织 Owner、项目 Owner 及目标命名空间权限路径，不阻断这些正常能力。

UNVERIFIED RISKS：

- 未在本轮重跑 Ubuntu amd64、PostgreSQL/MySQL、多节点部署或真实移动设备；本轮真实协议验收环境是本机 SQLite。
- 本轮并发固定样本覆盖同时降权、旧预览冲突；并非对所有数据库和全部转移/到期时序组合的形式化证明。既有转移撤权测试随仓库服务回归通过。
- 没有在真实管理员样本上测量原流程与新流程的操作量差异，不能宣称达到减少 80%。

NON-BLOCKING FINDINGS：

- 仓库全量 lint 存在本功能之外的既有问题，需独立治理；本次未批量修改无关文件。
- 新成员接口在配套接口说明中记录；未扩展自动生成的 Swagger 模型说明。

## 人工流程基线与效率边界

目标用户是项目维护者、Owner 和只读成员。输入条件为本机固定样本：直接成员、两个共享来源、原生协作者、个人及组织归属、权限到期、多页数据。

人工基线任务为：分别打开原生协作者、组织团队、治理成员、共享页面，按用户人工汇总来源，判断最高有效能力，再到来源页面修改，最后复查剩余来源。新流程将查找、汇总、影响计算及冲突校验集中在协作者页，人工主要完成搜索、选择变更、核对预览、确认。

目标减少至少 80% 的人工操作和判断。当前只完成固定功能样本和浏览器交互验收，尚无经记录的对照人员、起止时间及同任务操作次数；该量化指标明确为未验证，不以推算百分比替代实测。

## GitLab 对齐项与明确差异

核对依据：[项目成员 API](https://docs.gitlab.com/api/project_members/)、[群组共享规则](https://docs.gitlab.com/user/project/members/sharing_projects_groups/)。

| 对齐项 | 当前实现 |
|---|---|
| 项目直接 Owner | 支持，限定本项目，不提升组织权限。 |
| 邀请群组 Owner 上限 | 支持，成员来源能力与每层共享上限取交集。 |
| 有效成员与独立来源 | 每人一行，展开来源；删除一条来源保留其余来源。 |
| 私有来源保护 | 公开访客不枚举私有群组独有成员；群组身份和管理链接单独授权。 |
| Maintainer 与 Owner 分界 | Maintainer 管理普通直接成员；Owner 管理 Owner、共享和生命周期。 |

明确差异：保留 Gitea Owners 团队及原生按单元授权；无法准确归为标准角色时展示组合权限。每人一行是已确认的交互优化。永久、非共享且有效的 Owner 保障按本方案执行。旧管理 API 保持兼容，预览令牌可选；新页面提交强制带预览令牌。仅面向全新部署，不提供历史数据迁移；不实现 GitLab 专属安全产品角色或全部 GitLab API 兼容层。
