# 群组与子群组企业能力对齐计划审核

审核日期：2026-09-23。

审核对象：用户提供的《群组与子群组完整管理对齐方案》及当前源码。开始检查时工作树存在未提交改动；审核期间这些改动被外部提交，结束核查基线为 `9a3f93813847f53b4762e28863b09b9266f75943`。本轮未修改业务代码、未部署服务。

证据边界：完成了源码调用链检查和 GitLab 官方在线文档核对；未运行真实浏览器、Runner、Webhook 接收端、制品客户端及并发复现。下面的源码缺陷不表示已在用户环境观察到事故。官方在线文档是本次对照材料，不等于已经冻结了某个 GitLab 发行版本的验收基线。

## 一、结论

**VERDICT: FAIL**

判定对象是“当前方案及代码是否足以支撑所述完整交付目标”。FAIL 的阻塞依据只有第二节的一项当前代码 P1；其余范围遗漏、语义补充和未验证事项不作为 P0/P1 凑数。方案已经提出任务领取和凭据下发时重新检查范围，方向正确，但需要把这一要求落实为能够抵抗并发转移的授权一致性约束。

对计划完整性的独立判断：**方向正确、已有缺口判断基本成立，但尚不足以作为 GitLab 企业版群组核心能力的完整实施清单。**

主要问题是“覆盖现有 Gitea 组织设置”与“覆盖 GitLab 企业群组核心工作”不是同一个集合。即使原计划中的七个功能域全部完成，仍可能没有群组机器凭据、群组仓库保护策略、受保护凭据与部署边界，以及跨仓库协作管理。

应保留的设计：复用组织身份与祖先链；权限来源可解释；共享授权与配置继承分开；资源使用权和来源管理权分开；逐功能定义继承；每批同时交付 UI 和执行验证；旧入口通过验收后再收口。

## 二、P0/P1 BLOCKERS

### B-01：P1，仓库转移与 Runner 领取之间存在失效授权窗口

**具体位置：**

- [候选任务筛选](/Users/archer/Work/gitea-enhancement/models/actions/task.go:254)：组织 Runner 按当时的 `repository.owner_id` 选择任务；候选查询在领取事务之前执行。
- [领取事务](/Users/archer/Work/gitea-enhancement/models/actions/task.go:319)：加载任务对应的当前仓库后，没有重新检查 Runner 是否仍覆盖该仓库。
- [仓库转移](/Users/archer/Work/gitea-enhancement/services/repository/transfer.go:244)：更新仓库所有者；该路径没有使上述已选候选任务失效。
- [读取组织 Secrets](/Users/archer/Work/gitea-enhancement/models/secret/secret.go:195)：使用任务所加载仓库的当前所有者读取 Secrets。
- [组装 Runner 响应](/Users/archer/Work/gitea-enhancement/services/actions/task.go:139)：把 Secrets 放入返回给领取者的任务数据。

**触发条件：**仓库 R 在组织 A 中有等待执行的普通 push 任务；A 的组织 Runner 正在轮询；有权限的操作者把 R 正常转移到组织 B；B 配有组织级 Secret，且该名称没有被仓库同名 Secret 覆盖。Runner 不需要具有 B 的组织权限。

**完整因果链：**

1. A 的 Runner 查询候选任务，R 此时仍属于 A，查询合法命中。
2. 在领取事务加载仓库前，转移事务提交，R 的所有者变为 B。
3. 领取事务从已选候选进入，加载到属于 B 的 R，但没有重新校验 Runner 的覆盖范围。
4. 领取的乐观更新只约束任务尚未被领取、仍处于等待状态，转移没有改变这两个条件，领取可以成功。
5. Secrets 解析使用已加载仓库的 B 所有者，读取并解密 B 的组织级 Secrets。
6. 任务响应把这些 Secrets 发送给 A 的 Runner。工作流即使没有显式引用某个 Secret，服务端仍会组装该 Secret 集合。

**实际影响及严重程度：**旧组织 Runner 能取得新组织凭据；泄露跨越组织授权边界，撤销 Runner 或转移回仓库不能收回已经下发的凭据。满足阻止版本发布的 P1 安全影响。

**支持证据：**以上现有调用链及其可达执行顺序，属于源码确定性推导；尚未执行带同步屏障的并发复现。

**反证检查：已完成。**

- 原始 SQL 的所有者过滤发生在转移之前，不保护之后的领取。
- [接口重新读取 Runner](/Users/archer/Work/gitea-enhancement/routers/api/actions/runner/runner.go:183) 检查的是 Runner 当前状态，不重新验证任务仓库归属。
- 任务领取的普通数据库事务不使用转移路径的 [治理写锁](/Users/archer/Work/gitea-enhancement/models/governance/transaction.go:36)；而且该执行顺序允许转移先提交，再开始领取事务，不依赖数据库脏读。
- 防止两个 Runner 重复领取的条件，不包含仓库归属或授权版本。
- fork Secrets 限制不适用这里的普通 push 任务。
- 转移过程中清理旧组织的跨仓库白名单，不能阻止这次 Secrets 响应。

**对计划的修订要求：**不能只增加两次互相独立的范围查询。领取、仓库/群组移动和凭据选择必须使用一致的授权版本或事务约束；授权过期时拒绝下发并安全释放领取。网络响应不应占用长期数据库锁。还要分别定义已排队、已领取未下发、执行中、重跑及任务令牌后续访问的处理。

**补充验收：**用同步屏障固定“候选已选中 → 转移提交 → 领取”的顺序，确认旧 Runner 收不到任务和新组织 Secret，新范围的 Runner 可以正常领取。再对“领取完成 → 凭据下发”边界做同类验证。只使用测试凭据。

## 三、原计划列出的缺口是否真实存在

| 能力 | 当前源码结论 | 证据与计划处理 |
| --- | --- | --- |
| 群组树、成员继承、共享 | 已有相当多的底层能力；不能当作从零开发 | [成员授权链](/Users/archer/Work/gitea-enhancement/models/governance/membership.go:88) 已处理直接、继承和共享；继续验收和补齐入口 |
| 父群组 Owner 管理下级 | 原生组织 Owner 判定已有治理桥接；不能笼统断言所有旧入口都拒绝父级 Owner | [Owner 判定](/Users/archer/Work/gitea-enhancement/models/organization/org_user.go:61)；逐入口检查剩余独立权限路径 |
| Variables | 仅实例、直接所属组织/用户、仓库三级合并 | [实际解析](/Users/archer/Work/gitea-enhancement/models/actions/variable.go:183)；缺祖先组织链 |
| Secrets | 仅直接所属组织和仓库；已有 fork 限制及复用工作流的凭据传递限制 | [实际解析](/Users/archer/Work/gitea-enhancement/models/secret/secret.go:182)；接入祖先时保留已有约束 |
| Runner | 组织 Runner 只匹配直接所属仓库；需要同时修改查询、调度通知和安全领取 | [领取查询](/Users/archer/Work/gitea-enhancement/models/actions/task.go:260)、[任务版本通知](/Users/archer/Work/gitea-enhancement/models/actions/tasks_version.go:75) |
| 统一工作流 | 有现成实现，但有效来源仅实例及直接 Owner | [有效来源](/Users/archer/Work/gitea-enhancement/models/actions/scoped_workflow.go:69)；不能只补 UI |
| 任务令牌 | 当前仅加载直接 Owner；仓库选择覆盖配置后只应用仓库上限 | [权限计算](/Users/archer/Work/gitea-enhancement/models/actions/token_permissions.go:29)；按计划将默认值覆盖与不可放宽上限分开 |
| Webhook | 仓库 Hook、直接 Owner Hook、系统 Hook；没有祖先收集 | [事件入队](/Users/archer/Work/gitea-enhancement/services/webhook/webhook.go:207)；[事件枚举](/Users/archer/Work/gitea-enhancement/modules/webhook/type.go:31) 也需补群组/成员事件 |
| 标签 | UI 和 API 仍按直接组织读取/校验 | [标签查询](/Users/archer/Work/gitea-enhancement/models/issues/label.go:560)、[API 接受标签](/Users/archer/Work/gitea-enhancement/routers/api/v1/repo/pull.go:469)；显示和写入两端都需接入祖先 |
| 软件包 | 已有治理写入保护，不是完全没有适配；列表仍按单个 Owner 查询 | [写入保护](/Users/archer/Work/gitea-enhancement/models/packages/governance.go:17)、[列表](/Users/archer/Work/gitea-enhancement/routers/web/user/package.go:60) |
| 移动影响预览 | 已列成员、共享、审批、审计外送等；尚未列 Actions、Secrets、Webhook、标签等来源 | [预览数据结构](/Users/archer/Work/gitea-enhancement/services/governance/group.go:42) |
| 统一导航 | 新群组导航缺多项原生管理入口；原生设置仍保有这些功能 | [群组导航](/Users/archer/Work/gitea-enhancement/templates/governance/navigation_header.tmpl:12)、[原生设置导航](/Users/archer/Work/gitea-enhancement/templates/org/settings/navbar.tmpl:10) |

这些结论支持原计划的主要判断，但不支持“已完成百分之多少”的数字。完整操作矩阵尚未冻结，既没有稳定分母，也没有本轮运行验收分子。

## 四、还应纳入的核心范围

| 补项 | 本期至少需要明确的工作 | 当前证据或官方对照 |
| --- | --- | --- |
| 群组访问令牌、部署令牌与机器身份 | UI 创建、用途/作用域、到期、轮换、撤销、最后使用、子树访问与移动撤权；分别验证 API、Git 和包协议 | 当前 [访问令牌模型](/Users/archer/Work/gitea-enhancement/models/auth/access_token.go:27) 是个人令牌；本轮未发现完整群组令牌模型/入口。[群组访问令牌](https://docs.gitlab.com/user/group/settings/group_access_tokens/)、[部署令牌](https://docs.gitlab.com/user/project/deploy_tokens/) 是不同能力，OAuth 应用和 CI 任务令牌不能替代 |
| 群组仓库治理 | 默认分支及保护、保护分支/标签、Push Rules、签名要求、CODEOWNERS 与群组主体选择、合并检查；明确模板与强制规则 | 当前 [保护分支模型](/Users/archer/Work/gitea-enhancement/models/git/protected_branch.go:32) 以 RepoID 为作用域；审批规则已有群组实现，不代表所有仓库规则都已具备群组能力。[GitLab 保护分支](https://docs.gitlab.com/user/project/repository/branches/protected/) |
| 受保护凭据与部署边界 | Secret 何时可以交给作业：受保护分支/标签、环境范围、日志脱敏、隐藏、文件型凭据；生产环境部署授权与审批如纳入“完整 CI/CD”必须明确实现 | [CI/CD 变量](https://docs.gitlab.com/ci/variables/) 和 [受保护环境](https://docs.gitlab.com/ci/environments/protected_environments/)；当前 Secret 结构和解析未提供这些完整策略。“页面不返回明文”不能替代运行时凭据授权 |
| 群组安全设置 | 2FA 要求及宽限期、成员域名/IP/协议限制、群组外 fork/共享限制、直接项目成员限制、访问申请开关；逐项标明支持与差异 | [群组访问策略](https://docs.gitlab.com/user/group/access_and_permissions/)、[2FA](https://docs.gitlab.com/security/two_factor_authentication/)；当前已有部分共享限制，不应把本行整体记为全无 |
| LDAP 与自定义角色的管理闭环 | LDAP 群组映射、同步状态、来源只读/可覆盖规则、离职撤权；自定义角色创建/编辑/分配/删除及跨根移动；Team 来源解释 | [现有 LDAP 路径](/Users/archer/Work/gitea-enhancement/services/auth/source/ldap/source_authenticate.go:124) 映射原生 Team；[治理角色](/Users/archer/Work/gitea-enhancement/models/governance/models.go:75) 已存在。原生非 Owners Team 不自动等于可继承群组成员，需明确产品规则和实测 |
| 跨仓库日常协作 | 群组范围的 Issue/PR 列表、筛选与搜索、待审查工作、活动、共享项目入口、群组里程碑和看板；支持的通知/群组提及行为 | [GitLab 群组](https://docs.gitlab.com/user/group/) 和 [里程碑](https://docs.gitlab.com/user/project/milestones/)；当前 [里程碑模型](/Users/archer/Work/gitea-enhancement/models/issues/milestone.go:46) 仅仓库作用域。Gitea 原生组织看板要纳入盘点，不能遗漏已有能力 |
| 制品的明确分类 | 分开定义软件包、OCI 镜像、CI 构建产物、Release 附件、LFS 的归属与授权；逐协议验证路径、上传、下载、删除、保留和清理 | [GitLab 包展示](https://docs.gitlab.com/user/packages/package_registry/) 按权限聚合；[容器镜像](https://docs.gitlab.com/user/packages/container_registry/) 有独立协议。UI 可见不证明包客户端能正确使用嵌套命名空间 |
| 模板与常用集成 | 项目/文件/Issue/PR 模板的来源与使用权；现有通知、外部 Issue Tracker 等集成怎样在群组管理；不要求复制每种第三方连接器 | [群组项目模板](https://docs.gitlab.com/user/group/custom_project_templates/)、[集成默认配置](https://docs.gitlab.com/administration/settings/project_integration_management/)；Webhook 与集成配置不能完全等同 |
| 命名空间及长任务体验 | 改名/转移后浏览器、Git HTTP/SSH、API、Webhook 地址、包路径和旧别名的行为；长操作进度、部分失败和恢复；按权限聚合后的分页/总数 | 已有 [命名空间路由](/Users/archer/Work/gitea-enhancement/services/governance/namespace_resolver.go:60) 和别名机制；该通用路由显式跳过部分协议路径，不能用仓库页面成功推定所有协议完成适配 |

“研发规划分析另列范围”应限定为 Epic、Roadmap、组合规划、价值流与高级分析等明确清单。普通群组 Issue/PR 汇总、里程碑和已有看板是日常管理能力，建议纳入核心；如果排除，也必须写成接受的差异，不能用“规划分析”一词整体带过。

SAML/SCIM、企业合规中心可继续按原计划另列范围。完整身份联邦、Duo、集群/Agent、依赖代理、群组导入导出、用量/配额与计费应逐项定级为核心、后续或不支持；未明确决策前，不暗中扩大成本，也不宣称已覆盖。

## 五、继承规则还需修正或补足

1. **默认启用完整继承不等于每种设置强制递归。** 每个功能必须记录“实时继承、创建时复制、聚合展示、仅本级、不可放宽限制”中的准确行为，不能共用一个覆盖算法。
2. **GitLab Push Rules 是模板行为。** 新项目选择最近来源并复制，父级后续修改不会自动改掉已有项目；如果本项目采用持续强制策略，这是主动增强，需要标明差异。[官方说明](https://docs.gitlab.com/user/project/repository/push_rules/)
3. **GitLab 也有层级限制和不级联设置。** 群组保护分支的配置入口限制于顶级群组；成员锁按所在群组配置，不自动传入子群组。扩展成任意层级强制策略可以做，但不能称为原样复刻。[保护分支](https://docs.gitlab.com/user/project/repository/branches/protected/)、[成员锁](https://docs.gitlab.com/user/group/access_and_permissions/#prevent-members-from-being-added-to-projects-in-a-group)
4. **不同授权入口的成员集合不同。** 群组共享主要使用被邀请组的直接成员，项目共享包含不同的成员来源；审批人、保护分支名单、CODEOWNERS、提及通知也不能统一套用全部有效成员。当前共享计算已经作了部分区分，应保留。[共享规则](https://docs.gitlab.com/user/project/members/sharing_projects_groups/)
5. **Owner 目标不应掩盖其他角色。** 管理目标以普通 Owner 为中心，但实际能力必须细分到 Maintainer、Developer、Reporter、Guest、Minimal Access、Planner、自定义角色、Team 分单元权限和机器身份；操作权、发现权、使用权、来源管理权分别判定。
6. **不能只改 Runner 的匹配条件。** [任务版本](/Users/archer/Work/gitea-enhancement/models/actions/tasks_version.go:75) 目前只更新实例、直接 Owner 和仓库；祖先 Runner 即使能匹配，也需要获知子级出现了新任务。暂停、删除、恢复、转移都要影响有效范围和通知。
7. **统一工作流的“强制”需独立的合并语义。** 当前 [必需检查](/Users/archer/Work/gitea-enhancement/services/pull/commit_status.go:166) 在没有保护分支规则时直接返回空；[统一工作流调度](/Users/archer/Work/gitea-enhancement/services/actions/notifier_helper.go:662) 对 schedule/workflow_run 尚有明确不支持路径。计划需要覆盖无保护规则、禁用 Actions、无 YAML、来源缺失、同名状态、旧成功记录和各合并入口。不要把“禁止退出统一工作流”直接当成“任何合并都被强制门禁”。
8. **机器权限默认值和上限必须分开。** 仓库可以选择默认配置，不应因此跳过已经约定不可放宽的祖先上限。当前覆盖开关会改变实际 clamp 路径，必须一起调整，不能仅把直接 Owner 换成祖先查询。
9. **移动不只是更新归属。** 当前 [仓库转移](/Users/archer/Work/gitea-enhancement/services/repository/transfer.go:351) 会清理旧组织标签关系及相应标签评论；增加祖先标签后，要明确同根移动、跨根移动和历史引用的保存/转换规则。审计历史、已排队 Webhook、长期下载链接、清理任务也需要事件发生时范围与当前管理范围的规则。
10. **Secret 无泄露应有可验证边界。** UI/API/审计/预览/错误日志不得泄露，未获授权的 Runner/作业不得得到凭据；不能承诺收回已经交给执行器的明文，也不能把日志掩码当成阻止获密脚本主动外传的安全隔离。

## 六、建议调整后的实施顺序

| 阶段 | 必需交付 |
| --- | --- |
| 0：冻结对照与范围 | 指定 GitLab Self-Managed 版本、Premium/Ultimate 功能档位；形成操作级矩阵和接受的差异；定义人工基线；先确定统一导航结构 |
| 1：修复安全边界并统一授权 | 先处理 B-01；复用既有祖先链/能力计算；区分人员、机器、资源来源与策略；明确定时任务和协议入口的授权位置 |
| 2：成员、生命周期、仓库策略 | 闭合已有群组和成员管理；补安全设置、LDAP/Team 管理边界、角色、转移预览、保护和合并规则；每批有可用 UI |
| 3：完整 CI/CD 运行链 | Runner 发现/通知/领取、Variables/Secrets、统一工作流、令牌、保护凭据和部署授权；含撤权、移动、失败和重跑 |
| 4：外围核心工作闭环 | Webhook 群组事件及可靠投递、标签/里程碑、软件包/OCI/构建产物、常用集成、跨仓库列表与搜索 |
| 5：整体验收和入口收口 | 完成多角色、协议、生命周期、并发、规模和人工工作量对比；通过能力对应验收后隐藏或重定向旧入口 |

不建议另建一套通用组织系统或把所有功能复制进一个新后台。统一权限基础，保留各资源自身数据模型与专业执行器；导航重用现有组件和页面，补齐来源、有效状态与权限反馈。

## 七、功能矩阵和验收标准的补强

每个矩阵条目至少包含：操作编号、目标用户/机器身份、GitLab 版本/档位/来源、支持决定、UI 入口、资源所有者、允许能力、作用域、继承/覆盖/锁定语义、Web/API/协议/后台实际调用点、生命周期影响、正反例、证据编号、当前状态、差异。

状态应区分“源码存在、部分接入、缺失、未验证、已通过真实验收、明确不在范围”，不要用一个“已支持”复选框替代。

保留两个独立顶级群组、三层子群组、兄弟群组和十个仓库，并补充以下验收：

- 普通群组 Owner 账号不带实例管理员身份；兄弟隔离用私有资源，排除另一路直接/共享授权后再判定。
- 扩充角色覆盖；共享成员须拆出被邀请组直接成员、祖先继承成员、再次共享成员和子组独有成员。
- 每个功能做“UI 配置 → 刷新回显 → 协议/执行生效 → 撤权后拒绝”，包括直接调用旧 API 和旧 URL；数据库仅佐证。
- Runner 覆盖空闲后新任务唤醒、祖先注册、暂停/删除、标签匹配；加入 B-01 固定时序的并发检查。
- CI 覆盖排队、领取、凭据下发、执行中、重跑、定时触发、复用工作流、fork 与 pull_request_target；不要为对齐而默默破坏 Gitea 既有事件语义。
- 强制检查覆盖新旧提交、新旧策略、相同名字的不同来源、无工作流、来源不可用、取消/跳过/失败，以及 Web/API/自动合并和直接 Git 更新的预期边界。
- Webhook 验证真实接收端；Hook ID 去重之外，定义事件 ID、重复投递、超时、重投、历史权限以及移动前后在途事件的处理，避免事件丢失和错误接收方。
- 用实际包客户端和 OCI 客户端验证支持协议；软件包、CI artifact、Release 附件、LFS 分别测试归属和撤权，不用一次普通文件上传代表全部制品协议。
- 移动/改名后验证 HTTP/SSH clone/push、API、Wiki/LFS、嵌套包路径、历史别名、Webhook 载荷链接；待删除、恢复和重复请求必须幂等。
- 审计检查拒绝操作、失败、最终角色来源、权限调整前后、历史范围和异步任务关联；敏感值不进入证据包。
- 十仓库样本用于功能和人工效率，不代表规模验收；再按声明支持的成员数量、树深度、共享数和并发量测试列表延迟、权限解析和锁等待，给出明确支持上限。
- 大型移动/删除/批量变更应有任务状态、当前阶段、错误原因与安全恢复路径；UI 不得先显示成功而后台静默失败。

人工节省指标建议写为：在预先冻结的“集中配置与核对”任务集上，统计逐仓库基线与群组方案的人工活跃时间和重复操作次数；包含检查结果及处理异常的时间。分别报告 `(基线 - 新方案) / 基线`，目标至少 80%。不得仅用“十处配置变成一处”推算整个日常管理效率，也不得把脚本后台预配置的时间排除后宣称 UI 达标。

验收分母必须先固定：核心操作中“未实现”和“未验证”都计入未完成；排除项需留明示决定。核心覆盖率、正向成功率、越权/泄露反例结果与人工节省率分别报告。

## 八、UNVERIFIED RISKS

- B-01 尚未在真实 Runner 与实际数据库上做并发复现；源码结论不能冒充运行验证记录。
- 受保护检查是否可以通过伪造同名状态、旧成功记录或其他合并入口绕过，需要独立验证；本轮不另列 P1。
- 已发放任务令牌、回调、OCI/包长会话在移动或撤权后的实际行为未验证。
- 群组之外的协议路由、旧别名及软件包名称更新没有完成逐类型验收。
- LDAP 同步、Team 权限与治理成员管理的真实联动、来源可编辑性、外部目录撤权延迟未验证。
- 当前治理写入使用共用写锁；规模和并发指标未实测，不据此推断一定存在 P1 性能问题。
- 群组嵌套归档/恢复时既有独立归档状态的产品语义需专项验收；不能只验证最终“能打开”。
- 官方文档随版本变化；尚未绑定发行版、feature flag 和实际可用功能档位。

## 九、NON-BLOCKING FINDINGS

- P2：计划范围需补第四节中的核心工作，尤其不能用“其他组织设置”替代机器凭据、安全策略和跨仓库管理的独立验收项。
- P2：继承矩阵需纳入模板、不级联设置、顶级组限制、成员集合差异；更强的策略要注明是本项目扩展。
- P2：将 UI 完整性、协议执行完整性和权限正确性分开统计；统一导航结构应早定，每阶段交付页面，最后只做入口清理。
- P2：十仓库和 80% 指标需要冻结任务集、记录方式与支持规模；目前都不能当作已达到的结果。
- P3：统一文档名称和产品术语，明确 Gitea Repository 对应 GitLab Project，原生 Gitea Project 看板是另一类资源，避免矩阵把二者误合并。

建议的交付名称是“GitLab 企业群组核心能力对齐”。只有冻结范围中的操作和跨功能联动全部通过真实验收，才可标记完成；这一名称仍不等于实现 GitLab Enterprise 的全部产品能力。
