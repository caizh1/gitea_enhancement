# 群组／子群组一期阶段构建说明

## 必要能力一期最终收尾（2026-09-24）

本节为最新结论；下方 F6、F2、v16 等内容保留历史证据，不再代表当前候选或当前交付门槛。用户最终范围是日常开发、普通管理员管理及群组化 CI/CD，不要求旧69项、复杂迁移或专项指标全部清零。

当前分支 `codex/group-subgroup-phase1-alignment`，HEAD `7dbdfb23570c4e164061e48707837f5e719d0985` 加保留的未提交工作树；最终[源码清单](../outputs/group-subgroup-phase1-20260923/candidate-phase1-release-20260924/source-manifest.json)指纹 `56a9eb949588adf001580c6e60f7ab44c4b8fd0acf6350e99d363ecc9d49f728`。前端及三类嵌入资源已重建，构建期间源码未变；没有提交、推送或操作生产实例。

**必要功能一期完成。上线准备：待目标环境确认。** 最终候选完成已授权隔离部署及日常主链，未操作生产实例。当前没有成立的未解决P0/P1或明确日常功能阻断。

| 最终构建 | SHA-256 | 真实验证边界 |
| --- | --- | --- |
| [macOS arm64包](../outputs/group-subgroup-phase1-20260923/candidate-phase1-release-20260924/phase1-darwin-arm64.tar.gz) | 二进制 `ead882375f0f1da90e3b9d9d9c42d5c1b5631e0c5044fec4f881c02afe4ee475` | SQLite正常迁移/重启；纯父授权Git/PR、Run880父Runner、审批/合并、v4上下行/UI删除 |
| [Linux arm64包](../outputs/group-subgroup-phase1-20260923/candidate-phase1-release-20260924/phase1-linux-arm64.tar.gz) | 二进制 `12d7064ad5928991c2a06e2d26d1afdd146281e8e68573048789538c047c1a1c` | PostgreSQL专用实例升级；真实登录/HTTP Git/Generic/旧数据完整性 |
| [Linux amd64包](../outputs/group-subgroup-phase1-20260923/candidate-phase1-release-20260924/phase1-linux-amd64.tar.gz) | 二进制 `83504407befdf05bdfdedb3edde338741e0962cad659dbd3f59fbb8864f15a28` | 已构建；附固定Runner；目标主机未提供，未运行 |

[包摘要清单](../outputs/group-subgroup-phase1-20260923/candidate-phase1-release-20260924/delivery-manifest.json)、[完整源码快照](../outputs/group-subgroup-phase1-20260923/candidate-phase1-release-20260924/phase1-source.tar.gz)、[待提交文件清单](../outputs/group-subgroup-phase1-20260923/candidate-phase1-release-20260924/pending-files.txt)、[中文安装/启动/初始配置/冒烟/恢复说明](../outputs/group-subgroup-phase1-20260923/candidate-phase1-release-20260924/INSTALL.md)。快照包含未跟踪源码；单独Git diff不包含这些新文件，不能用历史HEAD或差异文件代替完整候选。构建摘要及前端嵌入SHA见source-manifest。大型包保存在outputs，未加入暂存区。

支持Runner `v1.0.8 @ c749e52bb712bf8029bc8d9193297e32740305c6`；mac实跑SHA `77f81455bc76ac39fd5f29c1e7aae3b72b7bec83d7bad003bace710a88bfbf3c`，amd64附包SHA `c0eec0f69e6a3c4bbd12639aa166557d12f4dea2b25120cd2a82ef1a506248fa`。artifact上传固定 `ChristopherHX/gitea-upload-artifact@81f940d004763f986ba3582c007fd842dd5cb0d7`，下载固定 `ChristopherHX/gitea-download-artifact@75635f32b4c1c41c4b3d64e8f85210112ed4c9c7`。可重复的兼容方案已完成上下行、摘要核对和必要管理；不承诺所有官方v4客户端及`ref_protected`表达式。服务端Secrets保护、fork限制、Run48预期失败和跨仓Actions拒绝保持。

本轮修复AUTH-CFG-01事务内当前配置管理权及AUTH-CFG-02工作流启停旧快照覆盖权限上限；见[最终审查](group-subgroup-phase1-review.md)。既有必要入口、schedule、Webhook、标签、成员/Owner/共享及生命周期安全修复继续交付，适用证据不重置。

目标环境尚需地址/架构、授权的部署路径及数据库配置。就绪后按INSTALL在该环境完成备份/正常迁移、普通账号主链冒烟；当前Linux arm64两核四GiB功能结果不替代目标容量或amd64结果。非必要后置事项仍未验证且不标通过：复杂迁移/改名、全生命周期、群组事件Webhook、跨子群组制品聚合、高级产品能力、全组合兼容、千仓容量、80%真人效率和专项窄屏。本次必要一期在此收尾。

## 2026-09-24 当前日常范围候选 F6

本轮接续现有工作树完成必要功能开发，未切分支、提交或推送。日常范围与非阻塞项按[最新计划](group-subgroup-phase1-plan.md)，不再以旧69项及专项全清零作为当前门槛。新增内容：纯祖先授权的原生Code/Packages入口、Fork目的地/按钮及完整路径、祖先工作流失效反馈；同时包含F4已经真实修复的归档恢复旧任务授权失效。具体结果见[验收](group-subgroup-phase1-acceptance.md)及[审查PASS](group-subgroup-phase1-review.md)。

HEAD `7dbdfb23570c4e164061e48707837f5e719d0985` 加未提交工作树；[F6源码清单](../outputs/group-subgroup-phase1-20260923/candidate-daily-paths-20260924/source-manifest.json)指纹 `b88651b76ff502c9445f6d51b34914b0bdf3640034b1d3440204b39c75ffde79`。构建输入期间未变化，前端与三类嵌入资源均重建。

| 当前产物 | SHA-256 | 真实验证范围 |
| --- | --- | --- |
| [macOS arm64](../outputs/group-subgroup-phase1-20260923/candidate-daily-paths-20260924/gitea-darwin-arm64) | `fca562abd1909954f84ebc29c87a551ca8617055ffc73a4bbfc33d3d93601c96` | 正常停服/备份/迁移/重启，schema379；普通Owner/无关用户UI、HTTP Git/Generic、Run667真实执行 |
| [Linux amd64](../outputs/group-subgroup-phase1-20260923/candidate-daily-paths-20260924/gitea-linux-amd64) | `a4115da1f899e24585a594ac4eb34dbb03477a62a8d4b3d4de3494a5be8bbc1d` | 交叉构建通过；没有指定主机运行证明 |
| [Linux arm64](../outputs/group-subgroup-phase1-20260923/candidate-daily-paths-20260924/gitea-linux-arm64) | `4555b58469938cd63a6dd8aec6877a63607c7780dfd9a9f7f906b1faee1e940a` | 交叉构建通过；历史F2容器升级证据保留，不冒称F6实测 |

复跑构建：`python3 contrib/phase1-acceptance/build_candidate.py --output <新目录> --go <Go1.26.4路径> --targets darwin/arm64 linux/amd64 linux/arm64 --reuse-installed`。按正常流程备份数据库/配置/仓库，运行候选`migrate`再启动；不要将旧程序直接用于已迁移数据降级。源码清单、原始构建命令和返回码位于候选目录；没有敏感测试数据库加入包或Git。

支持组合仍为Runner v1.0.8固定提交、Gitea兼容artifact v4客户端，详见下方原精确提交与限制。`ref_protected`表达式未支持，服务端Secrets保护仍有效；官方artifact v4 GHES检查失败未被冒充修复。当前没有生产发布操作。目标Linux、MSSQL及真人80%专项未验证，按新范围列独立后续验证，不能声称达标。

## F2阶段历史候选（保留二进制、命令与原结果）

已生成可验证候选包，**尚未批准一期正式发布**。基线为 `codex/group-subgroup-phase1-alignment @ 7dbdfb23570c4e164061e48707837f5e719d0985` 加当前工作树；未提交或推送。当前隔离 macOS 实例已正常备份、迁移到 schema379、启动并健康检查 200。以下 SHA 与[构建清单](../outputs/group-subgroup-phase1-20260923/candidate-lifecycle-fixed-20260924/source-manifest.json)绑定，历史 v16／N／Q 证据不改写成当前候选实测。

| 产物 | SHA-256 | 实际状态 |
| --- | --- | --- |
| macOS arm64 Gitea | `2d64a75d8442565b31762279db142cd63fac50cbece5d762d1f558f074b511de` | 本机隔离部署，创建角色及归档恢复／真实 Runner 复验；Hook 的先前 F 候选证据按自身版本保留 |
| Linux arm64 Gitea | `ed2b2115f0acd98cdf25aeafb4d0a9c81729b88b6f2d49605e4b1c8be54a084a` | 原隔离容器原地备份升级378→379；健康、登录、静态资源及五个包文件路径/摘要不变，非目标容量通过 |
| Linux amd64 Gitea | `1403c5749b502642a32152cefe38f8c566498a13406ad97ca4b297e957ec6035` | 交叉构建，未冒称目标环境安装通过 |
| Linux amd64 Runner v1.0.8 源码构建 | `c0eec0f69e6a3c4bbd12639aa166557d12f4dea2b25120cd2a82ef1a506248fa` | 固定模块提交的配套候选，未在指定目标机器运行 |
| [Linux 候选压缩包](../outputs/group-subgroup-phase1-20260923/candidate-lifecycle-fixed-20260924/phase1-candidate-linux-amd64.tar.gz) | `3ac45e4347a28952b6a0ba27fc4202291aba72805e6556fe6716ea4ebd07ab5b` | 包含二进制、中文安装说明和源码／包清单，无测试数据库或凭据 |

候选源码指纹为 `2fd0a1e11ea46698f8ac6bb7d348dcff9cfddc71e20bb01e30ed547ee2b4e9cb`。构建执行 Vite 前端、三类 bindata 和 Go 编译；精确命令、锁文件复用检查及首次 pnpm 缓存路径失败见[可复跑入口](../contrib/phase1-acceptance/README.md)。没有删除用户 node_modules，也没有放宽应用安全配置以完成验收。

新增正常迁移 378 使数据库版本升至379，修复完整路径索引的数据库兼容；先在隔离副本备份并运行包内 `gitea migrate`，再创建专用测试管理员、启动和验证。迁移后不支持直接用旧二进制降级数据库；需要回退时使用经过验证的完整备份恢复流程。本包仅供隔离验收，不提供生产发布批准。

Linux arm64 升级的[完整命令及备份/结果](../outputs/group-subgroup-phase1-20260923/linux-arm64-f2-upgrade-acceptance.md)已保存。它采用与 F2 一致的业务源码及 bindata；构建后测试/验收脚本变化单列，不将当前整个工作树冒称冻结指纹。

Runner 兼容基线固定为 `gitea.com/gitea/runner v1.0.8 @ c749e52bb712bf8029bc8d9193297e32740305c6`；服务端受保护 Secret 生效，但此 Runner 不支持 `github.ref_protected`／`gitea.ref_protected` 表达式。artifact v4 使用固定的 Gitea 兼容客户端：上传 `ChristopherHX/gitea-upload-artifact@81f940d004763f986ba3582c007fd842dd5cb0d7`，下载 `ChristopherHX/gitea-download-artifact@75635f32b4c1c41c4b3d64e8f85210112ed4c9c7`。没有宣称官方 `actions/upload-artifact@v4.6.2` 的 GHES 检查兼容，也没有放宽服务端授权。[真实组合及失败保留](../outputs/group-subgroup-phase1-20260923/artifact-v4-closure-acceptance.md)。

目标 Linux amd64 8 vCPU／16 GiB／SSD／PostgreSQL 未提供；容量脚本、指标采集和 amd64 包已准备，目标运行仍未验证。MSSQL 原生 x86-64 主机亦未提供；已运行的四库兼容和 Linux arm64 功能环境分别保留。真人十仓效率、最终候选整体回归及所有必选剩余编号须满足[唯一台账](group-subgroup-phase1-matrix.md)后才可正式交付。

## 下方为历史构建记录（保留原证据）

## 祖先定时收口时的状态

**尚未形成一期候选发布。** 当前隔离实例运行祖先定时收口构建 `71d19b0d1687…`，基于 HEAD `a488feb7a4ac2df8a7c17c01c536374b10f33098` 加工作树改动。祖先 schedule 已完成真实调度、Runner 和 UI 闭环；具体版本及证据见[定时验收](../outputs/group-subgroup-phase1-20260923/ancestor-schedule-acceptance.md)。下述历史版本证据继续保留，不等于[一期操作矩阵](group-subgroup-phase1-matrix.md)全部通过。发布判定仍以冻结的操作、容量、安全和人工效率门槛为准。

## 祖先定时收口构建与运行边界

- 当前 macOS arm64 SHA-256：`71d19b0d1687ac1e71ccbd5aaec8f79e7c3e5481d8ea749d15377afd360c5ed0`，构建时源码指纹 `1b485408ab940a5693a89dfd29581f6e5e73d355344ccf11a83a0f6702f9c547`。固定执行入口为 `/Users/archer/.cache/gitea-phase1-ui-iegohg9v/gitea-current`；Git hooks 依赖该路径。旧构建和升级前备份保留，仓库根目录原有 Linux `gitea` 未替换。后补 M11 仅增加测试，不改变此业务二进制；测试文件摘要另记，不回写旧构建清单。
- 本轮新增正常数据库迁移 378，持久化祖先工作流来源和计划授权版本。安装或验证时使用对应源码构建及正常迁移入口；本轮未交付降级工具。未修改 Runner 协议或要求无理由升级 Runner。测试 cron 已经通过正式 Git/API 清除，历史 Run 留作验收证据。
- 同一业务源码另构建 Linux arm64 二进制 `61f8dfa74e91771f7d98ff9526c87367d750c9dbf0e73a5ddd44ed359881d2f7`，在专用容器完成替换启动、数据库版本 378、健康接口、登录页与静态资源检查。此前该隔离环境的首轮构建完成空库安装；当前版本没有重新冒称空库安装。它是 2 vCPU／4 GiB arm64 功能环境，不能替代指定的 Linux amd64 8 vCPU／16 GiB 容量环境，也没有完成 Linux Git／Runner 链路。详细参数见[性能记录](group-subgroup-phase1-performance.md)。
- 第七版重新编译 Vite 前端，并重新生成 `options`、`templates`、`public/assets` 的 bindata；第八版将邀请无权／已撤销响应改为原生 HTML 404，并修正取消提示；第九版把相同的原生 404 反馈用于治理 Web 的无权／不存在场景。第十版补可发现群组的访问申请入口，并固定申请时路径隐私；第十一版收紧 Owner 身份及归档、删除、审计授权，并补仓库邮箱邀请入口及转移后邀请目标路径。第十二版重生成 `options`、`templates` bindata，修正必选工作流失败诊断、审计员只读入口与审计导出序号语义，并明确邀请的当前目标路径。第十三版重新生成 `options` bindata，补父组归档阻止单仓恢复时的明确错误提示；授权与归档事务未改。第十四版补 Actions 用户删除的最终权限、生命周期与当前运行状态检查，并使产物删除标记受生命周期约束。以上历史构建的数据库版本为377；本轮收口新增378迁移，见上文。
- 实例仅监听本机，使用隔离测试身份、SQLite、真实 Runner 和本机 SMTP。第五／六版有各自定向 PostgreSQL／MinIO 记录；第九版审计及邀请四项、第十版访问申请三项、第十一版 Owner／删除／审计／邀请四项，第十二版六项整合测试、第十三版四项归档整合测试均在真实 PostgreSQL 集成入口通过。它们均不能充当全量回归或目标部署证明。没有生产数据、生产凭据或一期候选发布产物。

## 已有真实验收片段

| 范围 | 已观察结果 | 边界 |
| --- | --- | --- |
| 祖先 CI 与可信门禁 | 无本级 YAML 的深层仓库由父组工作流来源和祖先 Runner 执行 push／PR；父级变量、掩码 Secret 与同名覆盖经过真实 Runner 验证。受保护 Secret 的真实分支 push 得到凭据，未保护分支 push 与实际执行的私有 fork PR 均取不到。根规则实际拒绝 HTTP／SSH 保护分支推送；旧成功在 Required 配置改变后不能放行 PR，新运行成功后普通 Owner 完成合并。 | 已验 Runner v1.0.8 不识别 `github.ref_protected` 表达式，不能将该上下文字段用于工作流门禁；受保护 Secret 的其他触发身份及全部重跑／撤权组合未验。 |
| 成员、审批与协作 | 父组审批规则阻断无批准合并；新提交使旧批准失效，二次审查批准后 PR 4 自动合并。治理邮件邀请经本机 SMTP、收件人网页接受与权限检查；Reporter 自退后，旧产物链接和新的 HTTP Git 请求被拒。原生 Team 的创建、仓库授权、撤权、空 Team 删除有真实 UI／客户端结果。 | 邀请链接到期、全角色与并发组合，通知、提及及非空 Team 删除未验。 |
| 共享、Hook、标签与制品 | 共享项目有独立导航和过滤列表；深层事件投递根／子 Hook，测试投递失败后可授权重投；祖先标签可在深层 Issue 选择和筛选。OCI／Generic 客户端、LFS、Release 附件及 Actions artifact 的代表性上传／下载和部分撤权反例通过；Generic／OCI 的指定版本删除另有下述客户端实测。 | 不能从代表性协议正例推出全部格式、删除、签名地址及生命周期矩阵完成。 |
| 第七至九版错误反馈与身份 | 部署密钥实际推送触发的组织身份 Run 立即取消并给出人工处理路径；有权 Owner 从 UI 显式重跑后，真实根 Runner 成功并上传产物。最后一名有效人类 Owner 尝试退出 Owners Team，UI 显示明确拒绝且成员保留。第八版收件人打开已撤销邀请看到原生 404；第九版 Developer 打开无权审计页也看到原生 404，普通 Owner 可从导航进入审计、筛选 Secret 配置事件并发起 CSV 导出。 | 组织部署密钥推送仍需有权人类显式重跑；没有交付群组机器身份产品。审计仅固定筛选和导出子项通过，未覆盖全部范围、分页和撤权交错。 |
| 第九版 Runner 暂停与恢复 | 普通 Owner 在原生页面停用父 Runner 2 和子 Runner 1；深层 Job55 持续等待且 TaskID 为 0。仅恢复父 Runner 后约 2 秒由其领取并成功执行，日志只报告受保护凭据存在，不输出值；最后恢复子 Runner。 | 覆盖排队期间停用与恢复，未覆盖执行中停用、删除、注册令牌轮换、全部撤权组合或千仓性能。 |
| 第十版访问申请与隐私 | 可发现的内部群组首页提供“访问申请与状态”；真实 UI 完成申请、本人撤回、Owner 批准 Reporter、再次申请后拒绝及成员自退。批准后实际 HTTP Git 读取／克隆成功、推送被拒；自退后新旧路径读取均被拒。资源改为私有并改名后，无当前读取权的申请人只能看到申请时路径，旧地址返回 404。 | 只覆盖固定身份、内部群组和 HTTP Git 样本；申请到期、多来源、其他协议及全角色组合仍未验。 |
| 第十一版 Owner 与删除生命周期 | 修复受限共享原始 Owner 角色被误用于审计、归档和删除，以及多条不完整授权拼成原生 Owner 身份。v10 受限用户曾在真实 UI 将空组 18 安排为待删除；v11 同身份审计页及相关 API 返回 404，删除状态不再可由其改变。真正 Owner 在重启后从 UI 恢复空组 14 和 18，原路径、未归档状态及创建入口恢复。 | 没有执行浏览器永久删除；含仓库／包／运行中任务的延迟删除与清理组合仍未完成。 |
| 第十一版转移、邀请与多来源 | 仓库成员页邮箱邀请经本机 SMTP 到达。仓库 12 转移到同名子群组后，收件人预览显示完整新路径；一份邀请经 UI 拒绝，另一份在转移回新路径后经 UI 接受，直接 Developer 来源生效，HTTP Git 新旧地址读取与合成分支推送成功。再叠加群组 Reporter 后，管理者预览并移除直接来源，写入被拒、读取保留；移除最后的群组来源后，页面和 HTTP Git 新旧地址均被拒。正式 API 建立的 90 秒 Reporter 来源到期后，页面、Git 读取拒绝且有到期审计。 | 覆盖单仓、固定角色和 HTTP Git；秒级到期不是 UI 设置。邀请链接到期、并发接受／转移、SSH／LFS／包旧别名及完整多来源矩阵未验。 |
| 第十二版诊断、审计与邀请 | PR 5 的失败必选 Run 在真实页面显示运行失败与修复后重跑提示，合并仍阻止。审计员从旧入口跳转到当前仓库协作者页，见只读来源与无编辑控件；实际 API 读 200、写 403，撤销审计员后均 404。新邀请邮件预览明确当前完整仓库路径，Owner 撤销后同链接原生 404。审计导出 API 保留 `max_sequence`：有匹配群组事件返回 375，内部全局快照上界为 386；空筛选返回 0、内部上界为 387，两份完成后数值不变，下载分别有 1／0 条。 | 只复验失败诊断这一真实 PR 状态、一次性审计员与两份导出；未覆盖全部诊断分支、审计范围、分页与撤权交错。浏览器点击首份下载，但实际落盘位置未核对。 |
| 第十二版包删除客户端 | 新建 `phase1-delete-*` Generic 1.0.0／2.0.0 与 OCI v1／v2／v3，Developer 发布、Owner／Reporter 下载并核对字节；Reporter 与无关用户删除均被拒且 Owner 仍可下载。Owner 删除 Generic 1.0.0、OCI v1 tag 与 v3 digest 后，同一客户端读取相应引用失败；Generic 2.0.0、OCI v2 和原有验收包仍可读。纠正 ORAS `not found` 无数字状态码的脚本误判后，41 项断言通过、1 项仅观察 tag 删除后原 digest 不可见。 | 仅固定唯一摘要样本；不推定 tag 与 digest 删除普遍等价，也未验证多 tag 同摘要、物理 blob 回收或 OCI 原生 UI 删除。首轮一次性样本的剩余版本与最终轮次分开记录。 |
| 第十二／十三版归档、制品与分支补验 | v12 普通 Owner 新建并初始化仓库 13／14，先独立归档 14，再归档父组；预览显示 1 群组、2 项目、原已归档 1 个。归档后两仓仍可读，推送和 Issue 创建被拒。v12 单仓解档正确拒绝但提示不足；v13 同操作显示父组限制及恢复路径。父组统一恢复后两仓归档状态清除、创建入口恢复，两仓真实推送及仓库 13 Issue 创建成功；这一统一恢复与已核实的 GitLab 19.4 固定源码／测试语义一致。v13 Owner 从原生制品 UI 删除的是**首轮** `phase1-delete-generic-1790184068819120000` 的 2.0.0；刷新后旧详情 404，最终轮次 2.0.0 和原有包仍可通过 Generic API 读取。v12 仓库 6 的内容 API 与 HTTP Git 另验禁推、正常推送和强推边界；v13 稳定 HEAD 的 API Run 60／61 成功。 | 两仓混合归档样本不覆盖待删除项目及运行中任务；未执行 OCI UI 删除。v12 的旧 Run 53／55 因领取前 SHA 不再为受保护分支 HEAD 而缺凭据，不能误判为 API 身份差异；其他角色、协议与并发组合待验。 |

这些结果分别见[祖先 UI／Runner](../outputs/group-subgroup-phase1-20260923/parent-ancestor-ui.md)、[第三批 Git 与保护规则](../outputs/group-subgroup-phase1-20260923/parent-third-batch-validation.md)、[第四批门禁与共享](../outputs/group-subgroup-phase1-20260923/parent-fourth-batch-validation.md)、[第五批审批与产物](../outputs/group-subgroup-phase1-20260923/parent-fifth-batch-validation.md)、[v5 真实 UI／协议](../outputs/group-subgroup-phase1-20260923/parent-v5-ui-validation.md)、[v6 自退](../outputs/group-subgroup-phase1-20260923/parent-v6-ui-validation.md)、[v7／v8 验证](../outputs/group-subgroup-phase1-20260923/parent-v7-v8-validation.md)、[受保护 Secret 实测](../outputs/group-subgroup-phase1-20260923/parent-protected-secret-ui.md)、[v9 审计](../outputs/group-subgroup-phase1-20260923/parent-v9-audit-validation.md)、[v9 Runner 暂停](../outputs/group-subgroup-phase1-20260923/parent-v9-runner-pause.md)、[v10 访问申请](../outputs/group-subgroup-phase1-20260923/parent-v10-access-request-validation.md)、[v11 Owner 与生命周期](../outputs/group-subgroup-phase1-20260923/parent-v11-owner-lifecycle-validation.md)、[v11 转移邀请](../outputs/group-subgroup-phase1-20260923/parent-v11-transfer-invitation-validation.md)、[v11 多来源与到期](../outputs/group-subgroup-phase1-20260923/parent-v11-multisource-invitation-validation.md)、[v12 包删除](../outputs/group-subgroup-phase1-20260923/package-delete-acceptance-evidence.md)、[v12 整合与真实界面](../outputs/group-subgroup-phase1-20260923/parent-v12-integrated-validation.md)、[v13 归档与制品](../outputs/group-subgroup-phase1-20260923/parent-v13-lifecycle-validation.md)、[API 分支保护](../outputs/group-subgroup-phase1-20260923/api-branch-protection-evidence.md)及[GitLab 19.4 固定归档语义](../outputs/group-subgroup-phase1-20260923/gitlab-19-4-archive-state-evidence.md)。v5 的隔离 PostgreSQL／MinIO 整合和 v6 定向结果另见[v5 整合](../outputs/group-subgroup-phase1-20260923/parent-v5-integrated-validation.md)、[v6 整合](../outputs/group-subgroup-phase1-20260923/parent-v6-integrated-validation.md)。第九版审计／邀请 PostgreSQL 四个顶级测试全部通过，包耗时 4.708 秒；第十版访问申请真实 PostgreSQL 三个顶级测试通过，包耗时 5.73 秒；第十一版真实 PostgreSQL 四个顶级测试通过，包耗时 8.405 秒；第十二版真实 PostgreSQL 六个顶级测试、含子项 26 个 pass 事件及零 fail，包耗时 92.865 秒；第十三版真实 PostgreSQL 四个顶级测试通过、零 fail，包耗时 12.866 秒，测试查询确认 `postgres / gitea_phase1_test`。[数据库证据纠正](../outputs/group-subgroup-phase1-20260923/owner-identity-db-evidence.md)明确服务包先前标作 PostgreSQL 的两份日志实际是 SQLite。第七版 `services/actions` 全包 70 个顶级测试通过。以上均不等同于第十三版全矩阵通过。

第十三版另补 Owner／继承 Developer 的 Web 文件修改、Developer API／HTTP Git 及 Reporter 拒写；真实作业 token 已验证同 Owner 私有跨仓 Code 只读与逐级上限。第十四版修复在真实专用仓库复现的归档 Actions 删除 P1；PostgreSQL 9 个顶层、含子项 36 个通过，0 失败／跳过，10.052 秒；同对象三个实际 Web／REST 删除入口拒绝且产物保留。详见[v14 验收](../outputs/group-subgroup-phase1-20260923/parent-v14-actions-validation.md)。v14 时浏览器被原生测试确认框阻塞；v15 已解除，但没有重新执行该删除 UI，仍未计入完成。

第十五版补原生应用、屏蔽管理的最终授权及 OAuth 组织审计投影；普通 Owner 归档时编辑应用／解除屏蔽被拒，恢复后同对象操作成功，无关用户页面 404。push 绑定事件 after，定时计划按现时默认分支核验；真实 Runner 的两次 push、schedule 和 skip-ci 移除计划均通过。父任务十四包 SQLite 共 324 个顶层测试通过；PG 最终证据和首轮 fixture 失败分别见[v15 整合](../outputs/group-subgroup-phase1-20260923/parent-v15-integrated-validation.md)。全仓 lint 仍报告 46 个问题，不是全仓通过。

第十六版进一步补齐屏蔽、解除、备注修改的事务审计及祖先投影，普通 Owner 在父子审计页面真实看到三类事件，详情不含备注。定时计划版本冲突不再吞普通 push，真正范围失效仍由最终 Run 事务拒绝。最终 PG 七项全绿，Actions 全包及屏蔽／审计相关四包全绿，增量 lint 为零问题，真实 Run92 成功；详见[v16 整合](../outputs/group-subgroup-phase1-20260923/parent-v16-integrated-validation.md)。这是隔离开发构建，尚不是一期发布候选。

## 发布前仍缺的开发与证据

- `CI05-SCH-01`～`09` 已按冻结场景关闭：无本地 YAML 的深层仓库自然生成 Run，来源变更、失效、消费者移动／归档、身份、凭据与 token 撤权均有对应证据。最终构建重测祖先 Run231／236／240 及来源更新期间保持执行的原生 Run234。`workflow_run` 没有本期新增承诺，未顺带实现。

- [操作矩阵](group-subgroup-phase1-matrix.md)与[阶段验收记录](group-subgroup-phase1-acceptance.md)中尚未覆盖的角色、来源、撤权、移动、归档恢复、协议和并发组合仍逐项待验；上述固定正反样本不代表整个操作类别完成，代码和自动化通过的行也不能直接记为真实 UI／客户端通过。
- 受保护 Secret 当前只认可可信事件的当前受保护分支 HEAD；本轮祖先 schedule 已验证保护引用凭据及来源仓独有凭据隔离，同一 Job 的跨仓 Code 读取在 UI 撤权后由 200 变为 404。来源移出再移回后，Owner 必须重新登记，已实际验证。其他手动、复用、在途停用／删除与跨协议组合仍按各自编号待验。PR、PR-target、fork 与标签不下发此类 Secret，不能宣称 GitLab 凭据全语义等价；审计全部范围与长任务重启恢复也未全部验证。已验成员到期不替代邀请链接到期。群组／成员变更 Hook 与跨子群组制品聚合维持已批准后置。
- 计划中的 Linux amd64／8 vCPU／16 GiB、千成员／千仓／20 层／100 共享／50 并发用户／10 Runner 容量环境尚未提供，性能未验证。十仓人工基线和新流程的活跃时间、重复操作、错误与介入比例尚未实测，不能宣称达到减少人工 80%。
- 当前跨仓作业 token 不授予 Actions 读权；v3 runtime 限同 Run，官方 upload-artifact v4.6.2 在本 Runner 组合报兼容错误，跨仓产物正例未通过。连续 push 的事件提交问题已在 v15 修复，真实 Run88／89 分别使用自身提交。未解决的客户端兼容见[令牌证据](../outputs/group-subgroup-phase1-20260923/ci06-runner-token-acceptance.md)，本次修复见[v15 CI](../outputs/group-subgroup-phase1-20260923/parent-v15-ci-ui.md)。
- 审计导出保留 `max_sequence` 的字段名和类型，含义改为授权筛选范围的快照末序号。仓库内没有依赖旧全局含义的消费者，外部客户端的语义兼容仍未验证。既有事件 ID 的全局序号间隔仍可能透露粗粒度活动，列为 P2，详见[审查报告](group-subgroup-phase1-review.md)。

本说明记录开发构建和已交付片段，不代替最终候选包、完整安全审查或[逐操作验收](group-subgroup-phase1-acceptance.md)。
