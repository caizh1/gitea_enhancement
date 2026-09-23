# 当前版本真实用户开发与权限实测报告

测试日期：2026-09-18。源码：`1d07c4e3edaae9a69ff2d875adb4d88ecb3c1fda`。

**VERDICT: FAIL**

已确认 **1 个 P1、2 个 P2**。P1 为普通 Git 推送在撤权完成后仍使用之前取得的授权写入。其底层窗口是原生 Gitea 1.27.3 的既有行为，不是已证增强提交新引入的回归；本项目新增的最终引用事务门禁没有为普通代码路径补齐当前写权限复核，因而仍未满足本轮 F01 的严格验收要求。主执行者与 Sol 子代理分工执行，缺陷评级由主执行者复核；未修改业务代码或公共 API，尚未修复这些问题。

**本轮仍不代表全量协议、平台和入口交叉验收完成。** 已补真实 macOS VS Code 图形操作及 Docker Linux Remote-SSH；Windows/TortoiseGit 按用户后续指示跳过，不计通过。所有 70 个场景的具体范围和缺口见覆盖台账。GUI 补测通过独立真实 Electron 窗口操作菜单、编辑器和按钮，未用命令行冒充图形操作。

## 交付索引

- [缺陷清单与评级依据](user-development-permission-bugs-20260918.md)：每个 Bug 的位置、触发、因果链、影响、反证和复测要求。
- [PERM-RACE-001 归属核查](evidence/user-permission-20260918/perm-race-attribution.md)：上游源码基线、增强差异、评级边界与未执行范围。
- [70 行覆盖台账](evidence/user-permission-20260918/coverage.md)：逐场景证据、状态与未完成变体。
- [逐条判读台账](evidence/user-permission-20260918/execution-ledger.json)：每条原始记录的文件、行号、最终判定及排除理由。
- [统计数据](evidence/user-permission-20260918/summary.json)、[原始证据目录](evidence/user-permission-20260918/)、[原始设计矩阵](user-development-permission-matrix.md)。

## 实际环境

| 项目 | 实际值与边界 |
|---|---|
| 服务端 | 本机 Docker 内 Linux amd64，独立 Gitea 与 PostgreSQL，全部测试仓库和账号为专用假数据 |
| 程序 | 从指定提交构建后挂载运行，版本 `1.27.3+acceptance.1d07c4e` |
| 程序 SHA-256 | `3e662e3e136d4e1c7b14545c3676773bd6348984551919702a1898eec620c73b` |
| 基础镜像 | `gitea-governance-combined:review-20260916-r5`；摘要 `sha256:1cea29101700826d6b617777828215972fdabea7979a52671be89089ec080f6b`。镜像内旧程序被当前构建程序覆盖，不能仅靠镜像名证明当前源码 |
| 数据库 | PostgreSQL 17.11；Gitea 数据库 schema 371 |
| 前端 | 复用已有产物；已逐文件核对 `web_src`、包清单、锁文件、Vite 配置与对应源码一致；模板及 options 来自当前提交 |
| 访问 | HTTP `127.0.0.1:3538`；SSH `127.0.0.1:4538`。未使用阿里云、未向生产仓库推送 |
| 客户端 | macOS Git、独立 VS Code 图形窗口；真实 Remote-SSH 到 Docker Ubuntu 24.04、远端 Git 2.43；内置浏览器网页操作。Windows 用户指定跳过 |
| 环境变更 | 初始 LFS 未启用，后启用并核验真实协议，保留前后配置与 404 日志旁证；邀请测试启用官方 dummy mailer，只写本地日志、不投递外网，变更及恢复时间见独立记录 |
| 数据隔离 | 独立工作区、账号 Token、SSH 密钥和凭据设置；敏感配置仅在临时目录，文件权限 600，不入证据目录 |

基线见 [baseline.json](evidence/user-permission-20260918/baseline.json)、[客户端版本](evidence/user-permission-20260918/client-baseline.json)，后续 dummy 配置见 [environment-overlay.json](evidence/user-permission-20260918/environment-overlay.json)。测试服务重启均只针对本轮 Gitea 容器，不重启数据库或其他项目容器。

## 统计口径与结果

<!-- statistics:start -->
本轮累计保存 **672 条原始记录**：其中 **600 条有效检查，586 条通过、14 条失败**；72 条明确排除或被后续证据替代。失败归并为 **3 个 Bug**，不是 14 个 Bug。

纯正向检查通过 **278/287（96.86%）**；另有 63 条复合及一致性检查。越权放行复现 4 条、合法操作误拒绝复现 9 条，分别归并到对应缺陷。

当前 **70/70 个场景均已有相应执行证据**，其中部分仍未穷尽协议/客户端/入口变体；这不是 70 个场景全部通过。Windows/TortoiseGit 用户指定跳过。

本次 GUI 补测新增 **45 条有效检查，均通过**；无新增确认的 Gitea 缺陷。此前结论中的 3 个缺陷仍未修复，PERM-RACE-001 的上游归属已更正。
<!-- statistics:end -->

这些数字是**检查记录**，不是整行场景或全部协议/平台变体的通过率。一个 Bug 可以产生多条重复复现记录，同一成功检查的复验也逐次计数；复合检查单列，不混入纯正向成功率。原始失败不会被删除后重新计作通过，台账指向追加更正。

执行前没有冻结跨角色、协议、入口、GUI 客户端的完整拆分分母，因此**不能提供可信的全量实例执行覆盖率**。本报告提供 70 行的逐项覆盖，未执行项不计通过。没有人工流程对比数据，不宣称节省人工 80%。

## 已实际验证的主要链路

- HTTP+Token 与 SSH 用户密钥的克隆、本地新提交、普通分支推送、独立远端 SHA 和重新克隆内容核对；并行开发非快进拒绝、解决冲突、旧克隆同步和继续交付。
- 基础角色、两级组织继承、父/子不同角色叠加、直接项目授权、原生协作者与 Team、共享能力交集、成员/共享到期、撤权和恢复。
- 项目转移、子组织换父级、仓库/父组织归档与恢复；归档期间撤销的权限没有被恢复操作复活；审批齐全也不能向归档仓库合并。
- Token 范围和撤销、SSH 密钥撤销、账号禁用、伪造提交作者、部署密钥范围、保护分支/标签、多引用及原子推送、文件 API 和网页编辑。
- 两人审批、同一人跨审批组去重、子组直接审批资格、作者自批、检查失败、追加提交、目标分支变化、审批人/合并人撤权、Fork 与上游权限边界。
- F02 在保持原生引用 Hook 字节不变的前提下，分别对审批人成员资格、合并人成员资格、审批撤销制造受控并发；撤销操作返回 409，合并按已取得的授权完成，验证了明确排序。
- F03 的 HTTP/SSH 服务中断恢复、客户端断开后远端对账均完成。合并中断后，重启等待 12 秒仍停留在授权状态；人工执行官方 `gitea admin governance recover --kind merge --id <事务ID>` 后释放保留项，重新审批并合并成功，PR、主分支与原 Head 祖先关系一致。见 [中断证据](evidence/user-permission-20260918/interruption-results.jsonl)。这不是纯自动恢复通过。
- 同一引用的双推送及同一目标分支的双 PR 同时合并；失败一方更新审批后交付，最终保留双方内容与提交祖先关系。这是本次真实调度证据，不代表穷尽所有并发时序。
- 私有子模块分别授权与撤权，并在子模块自身对象库核对撤权后的新对象不可得；LFS 真实 batch/object 上传下载、Git 指针推送，以及撤权后的新请求和旧签发请求拒绝。
- API 实际创建邀请后验证未接受邀请不授予权限；专属邀请行构造过期状态后仍拒绝；正式授权的同账号可正常读写。过期为明确标注的 fixture，不是等待 90 天。

## 图形化界面与 TortoiseGit

| 客户端 | 实际执行 | 未完成部分 |
|---|---|---|
| macOS VS Code | HTTP+Token 与 SSH 正常克隆、建分支、编辑、提交、发布；父组织继承及越界拒绝；获取和拉取区分；冲突解决、储藏恢复；角色/保护分支/凭据切换/撤权；同步部分成功；网络中断恢复 | 以逐条证据为准，未穷尽每个角色×协议×所有客户端组合；GUI不提供删除远程默认分支选项，该子项不适用 |
| 内置浏览器 | Developer 网页编辑受保护 main 时改走新功能分支，创建 PR；审批不足不能更新 main；降为 Reporter 后无法直接编辑；撤权后旧会话 404 | 不替代桌面客户端测试 |
| Windows VS Code / TortoiseGit | **用户指定跳过** | 无通过结论，也未以 Wine 或其他平台替代 |
| VS Code Remote-SSH Linux | 本机真实 SSH 连接独立 Docker Linux；远端 Git 完成 GUI 克隆、分支、编辑、暂存、提交、推送、拉取、撤权拒绝、恢复交付及保护 main 拒绝 | HTTP+Token，远端独立凭据；不代表 Windows 或阿里云网络；Remote-SSH 扩展为本轮记录的预发布版本 |

此前系统屏幕捕捉持续返回 `ScreenCaptureKit SCStreamErrorDomain -3811`。本轮改用独立 VS Code 真实 Electron 窗口的 CDP 渲染界面，截图和输入恢复可用；操作仍经过界面菜单与按钮，没有调用内部 Git 命令 API。此问题属于工具环境，不证明 Astra 网络安全审核拒绝或 Gitea 缺陷。

新增图形证据：[macOS正常与冲突同步](evidence/user-permission-20260918/gui-flow-results.jsonl)、[图形权限与凭据](evidence/user-permission-20260918/gui-permissions-results.jsonl)、[Remote-SSH执行](evidence/user-permission-20260918/gui-remote-results.jsonl)、[Remote-SSH日志与分支反证](evidence/user-permission-20260918/gui-remote-self-review.md)、[网络中断时序](evidence/user-permission-20260918/g12-network-timing.json)。截图按每条记录的英文文件名保存在同一证据目录。

原有 [gui-results](evidence/user-permission-20260918/gui-results.jsonl)、[Git扩展日志](evidence/user-permission-20260918/gui-log-excerpts.json)、[网页结果](evidence/user-permission-20260918/web-results.jsonl) 仍保留，补测不删除早期边界。GUI执行器前置条件不满足的记录明确排除，不能拿空推送或界面尚未完成的动作充当成功。

## P0/P1 BLOCKERS

**PERM-RACE-001，P1，经过反证仍成立。** 撤权成功后，先前通过前置鉴权的普通 Git 请求仍可写入。HTTP、SSH、唯一直接项目授权、原生引用 Hook 未改变四类证据相互支撑。源码对比表明底层窗口继承自原生 Gitea 1.27.3，不是已证增强回归；增强新增的最终引用事务门禁在普通代码路径漏掉了当前写权限复核。P1 是按 F01 已确认的严格撤权要求和实际写入影响评级，不代表上游官方安全评级。代码位置、完整因果链和影响见[缺陷登记](user-development-permission-bugs-20260918.md#perm-race-001p1)及[归属核查](evidence/user-permission-20260918/perm-race-attribution.md)。因此当前结论为 FAIL，不能据正常开发链路通过就批准发布。

未确认 P0。不把环境缺口、后台计算中的 405、完整性保护的 409 或测试脚本错误升级为阻塞缺陷。

## NON-BLOCKING FINDINGS

- **MERGE-EVENT-001，P2**：治理合并内部 post-receive 500；外层恢复保证引用、PR、审批与合并通知一致，但缺少通用 push 动态。
- **CAP-PR-001，P2**：Planner 及相关共享能力交集可通过网页读取 PR，却被 PR 列表 API 的代码读取门禁返回 403。主执行者已复核；子代理初步 P1 已降为最终 P2。

两者均待修复，P2 不单独导致 FAIL；当前 FAIL 来自 PERM-RACE-001。

## UNVERIFIED RISKS 与证据限制

1. Windows/TortoiseGit 用户指定跳过。macOS 和 Docker Linux Remote-SSH 已新增真实 GUI 证据，但没有穷尽每一角色、协议与客户端的笛卡尔积，不能声称完整跨平台兼容。
2. 本机和服务容器均未安装 Git LFS 客户端。本轮验证真实 LFS 协议和指针内容，不涵盖 `clean/smudge`、客户端自动大文件上传下载完整流程。
3. 邀请使用 dummy mailer，不证明真实邮件投递、验证邮箱或邀请接受闭环。C07 的“尚未接受/已过期不获得权限”有独立证据。
4. Webhook、Actions 等下游是否受 MERGE-EVENT-001 影响，缺少配置实际集成的样本，不凭源码猜测扩大影响范围。
5. 70 行中的搜索/页面入口、全部撤权来源和平台交叉组合尚未全覆盖；故障与并发结论只适用于记录的暂停点、协议和本次运行，不替代长期稳定性测试。
6. D10 早期脚本曾覆盖初次 LFS 未启用的 404 和客户端错误标头造成的 422 原始 JSONL；这些文件不可恢复。本轮最终有效证据和后续独立见证已保留，容器日志尚有 404 旁证，详见 [assets-followup](evidence/user-permission-20260918/assets-followup-results.jsonl)。不把丢失的原始记录计入统计，也不伪造回填。
7. F03 使用了官方人工恢复命令；没有证明纯自动恢复、自动恢复时限或长期故障恢复能力。最终未决事务及引用/合并保留项为 0，审计事件已回读。
8. 原始断言中的列表可见性、公开仓库预置、事项 reader/poster 语义、保护标签不存在、归档状态码及 PR 异步计算均已独立归因。原始文件保留，统计按明确排除清单计算。
9. PERM-RACE-001 的上游归属依据同仓库 Gitea 1.27.3 源码基线和提交差异，没有另外启动纯上游二进制实例。现有证据足以区分“上游既有窗口”和“增强未补齐最终复核”，但若向上游报告安全问题，仍需使用官方构建独立复现，不能把本报告的 P1 直接当作上游评级。

## 重现与后续验收

`tools/user-*-acceptance.py` 及补测脚本是本次隔离实例的操作记录，部分阶段依赖已创建的固定数据，不是可直接对任意地址运行的通用测试套件。不要将其中临时凭据提交到仓库，也不要把故障阶段指向生产实例。

修复前保留失败证据。优先修复 P1，再以同样暂停点和独立远端见证复测；P2 分别验证合法 PR API 与完整 push 事件链。后续若用户恢复 Windows/TortoiseGit 验收，需使用真实 Windows；Git LFS 客户端及覆盖台账的其他缺口仍需单独验收，不把本轮结果重新命名为全量通过。

本轮结束时隔离 Gitea 健康正常并保留运行，测试暂停 Hook 已清理，原生 Hook 哈希保持不变。未修改业务代码，测试数据与凭据仍仅用于本机隔离环境。
