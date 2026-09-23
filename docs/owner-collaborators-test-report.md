# Owner 与协作者分层测试报告

## 当前结论

整套方案尚未达到全量验收完成。已落地 72 项可执行用例目录、独立权限预期、Go/前端/真实部署自动化及需求证据映射。本轮发现并修复了 Minimal Access、预览绑定和管理员禁登入口的问题；仍保留真实失败和未验证项，不把测试通过数量当作全部需求通过。

- Go：九个相关包，377 条叶子测试通过，零失败；父测试不重复计数。
- 前端：8 条 Vitest 交互测试通过；定向 ESLint 通过。
- 部署：Ubuntu 24.04.5 amd64、PostgreSQL 17.11、当前权限修复构建；API、HTTP/SSH Git 和 Chromium 实际运行。最终运行 `members-1790055531881` 共 51 个子场景：49 通过、2 失败；子场景计时合计约 199.8 秒，不含构建与准备。
- Firefox：本机 Playwright Firefox 启动超时，日志包含 `sandbox_extension_issue_file_to_process` 和 `RenderCompositorSWGL failed mapping default framebuffer`。无页面执行结果，不等于产品 Firefox 测试通过或失败。
- P0/P1 对抗审查单列；审查 PASS 不代表功能验收 PASS。

## 交付文件

| 文件 | 内容 |
|---|---|
| `docs/owner-collaborators-test-plan.md` | 72 项前置、操作者、步骤及预期，六角色矩阵，执行方式和人工验收标准 |
| `services/governance/repository_members_acceptance_test.go` | 独立能力矩阵、36 共享组合、Minimal Access、已有 Owner 保护、预览上下文与内容、审计回滚、到期边界、永久 Owner、自定义角色及分页规模 |
| `services/user/owner_acceptance_test.go` | 管理员组合入口禁登唯一永久 Owner 的拒绝及接替后成功 |
| `web_src/js/features/repo-members.test.ts` | 预览失效、确认取消、重复提交、错误保留输入、转义和 UTC 截止时间 |
| `tools/test-owner-collaborators.mjs` | 使用正式 API、普通用户、真实浏览器及 Git 的隔离部署验收；凭据经标准输入，报告脱敏 |
| `tools/summarize-owner-collaborators.py` | 汇总 Go JSON 和部署报告，生成完整需求映射和每条实际结果 |
| `docs/owner-collaborators-adversarial-review.md` | P0/P1 结论、已修复因果链、反证及未验证风险 |
| `outputs/owner-collaborators-acceptance/summary/traceability.md` | 本轮编号到实际执行证据的映射 |
| `outputs/owner-collaborators-acceptance/summary/case-results.json` | 可执行用例表及逐子场景实际结果，含状态、耗时、证据 |

## 修复与首次失败证据

| 问题 | 首次证据 | 修复方式及回归 |
|---|---|---|
| 群组 Minimal Access 被算入私有项目成员；可直接设置项目 Minimal Access | `service-first-run.jsonl` | 项目来源排除 Minimal Access，统一写入守卫拒绝该角色；独立 Guest 授权与撤销三阶段通过 |
| 相同命名空间下不同项目可复用预览摘要 | `service-expanded.jsonl` | 摘要加入项目与操作者身份；不同项目/操作者/重放/降权回归 |
| 预览 Developer 后可附带同一令牌提交其他用户、Owner 或不同期限 | `preview-payload-first.jsonl` | 摘要绑定实际请求内容；治理、共享和原生成员统一校验；API 三种替换均应 409 |
| 管理员组合账号修改可禁登唯一永久 Owner | `members-1790055167921/report.json`、`admin-owner-first.jsonl` | 在管理员写事务中补永久 Owner 检查；唯一 Owner 拒绝、添加接替者后允许 |

证据均在 `outputs/owner-collaborators-acceptance/` 下。首次失败保留，没有以成功重跑覆盖。

## 保留的功能失败

1. **AUTH-10，P2：Planner PR 列表读取返回 403。** 模型允许 PR 读取且禁止代码读取，但原生 `/pulls` API 路由组额外要求代码读取。测试保持 200 的独立预期，不通过放开整个代码相关路由组掩盖差异。
2. **NAV-05，P2：只有代码单元的 Guest 项目不能从首页发现。** 使用真实首页仓库链接定位失败；没有手输协作者 URL 把正常导航失败改判成功。

这两项不构成已证明的权限扩大或失主，因此不升格为 P0/P1；但仍阻止声明全部功能验收通过。

## 测试工程修正记录

下列是测试准备或测量错误，不能算产品缺陷：

- 服务尚在初始化就开始准备数据；后续先确认版本接口就绪。
- 组路径与测试用户重名，以及把 `compatibility_name` 误读为内部 Go 字段名称；改用独立前缀和实际接口字段。
- 对预期 404 的分支查询仍读取 `commit.id`；改为仅成功响应检查提交值，拒绝请求仍核对状态和不存在的分支。
- 浏览器点击筛选后立即读取 DOM；改用可等待的行数断言。
- 缩放后直接测量原生节流菜单的过渡帧；等待布局稳定后，三种 Chromium 视口通过，原始溢出截图仍保留。
- 用缺少预览令牌的请求测 CSRF，实际触发的是参数检查 400；改用有效预览和明确跨站 Origin/Sec-Fetch-Site 验证 403。当前 Gitea 使用来源保护，不能把“没有旧式 CSRF token”直接判成漏洞。
- 审计故障触发器在测试清理阶段使用已经取消的测试上下文；改为独立清理上下文，避免污染其他测试。
- 测试准备新增其他项目授权会更新个人命名空间修订号；随后重新加载页面再开始用户提交，避免把人为制造的旧页面当正常流程。

## 环境与证据边界

部署使用独立 QEMU 差分盘，没有使用生产账号、生产数据或本机日常运行实例。二进制复制到测试容器内进行功能验证，基础镜像并非本轮新发布镜像；不宣称离线安装包已经包含本轮修复。每次构建和运行保留日志、源码摘要和运行编号。

Go、Vitest、实际部署是三个不同层次，不能相互替代。真实 Git 拒绝要求实际权限错误，连接错误和超时不计通过；允许的普通分支推送校验远端引用。原 PAT、SSH key、本地克隆复用验证的是后续服务端请求，不能撤销用户此前已下载到本地的代码。

## 未完成的验收组合

完整范围以 72 项映射为准。目前 64 项具有部分通过的自动化覆盖，2 项包含真实失败；NAV-08、VIEW-08、OWN-05、OWN-07、UX-04、FLOW-05 六项没有本轮执行记录。部分覆盖不代表全部组合通过。主要缺口包括：Firefox、受限及审计账号全部组合、每种 Owner 的完整生命周期闭环、全部继承和共享链、所有旧入口和批准路径、账号删除、转移与撤权的双向屏障并发、真实 Git 到期和保护分支/必需审批、1001 人多来源的真实部署性能、浏览器 200% 缩放及完整键盘焦点验收。

本轮已有模型和服务层覆盖其中部分规则，但没有将其记为 PostgreSQL 或浏览器端完整通过。维护人员五问盲测、人工基线和 80% 降本指标均未验证。

## 静态检查

已执行项目 `make fmt`；仅格式化造成的原本干净无关文件已恢复，保留本轮相关格式整理。`make lint-go` 仍有仓库既有告警，完整输出保留；本轮修改文件的新增问题定向处理。`make lint-js` 被现有依赖链接的 pnpm 自动安装确认阻塞，未重装用户依赖；直接使用现有 ESLint/Vitest 完成修改测试文件检查，不能称全仓前端 lint 通过。

## 最终证据索引

- `outputs/owner-collaborators-acceptance/regression-owner-fixed.jsonl`：九包最终 Go 回归。
- `outputs/owner-collaborators-acceptance/frontend-results.json`：8 条前端断言及耗时。
- `outputs/owner-collaborators-acceptance/members-1790055531881/report.json`：最终实际部署结果。
- 同运行目录 `requests.jsonl`：脱敏请求路径及预期/实际状态；`chromium-*.png`：三视口截图。
- `outputs/owner-collaborators-acceptance/git-refs-final.txt`：实际远端引用；允许的 12 个测试分支均指向 `6279e6527d480a33048c3db947c3a7cc0da73f1d`，没有被拒绝的 Guest/Planner/Reporter/撤权推送分支。
- `outputs/owner-collaborators-acceptance/baseline-final.json`：源码摘要、Git 基线、环境和二进制身份。部署及本地二进制 SHA-256 均为 `619af417b579b7f61ab335e959bf07224d63ccb83e66280234c4dafb85d59dc9`。
- `outputs/owner-collaborators-acceptance/source-snapshot.tar.gz`：本轮源码与测试快照。

源码修复未制作成新的发布镜像或一键安装包；本报告只对应所述隔离验收构建。
