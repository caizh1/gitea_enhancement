# 群组／子群组一期：阶段代码与真实执行复查

## 必要能力一期最终收尾（2026-09-24）

本节为最新结论；下方 F6、F2、v16 等内容保留历史证据，不再代表当前候选或当前交付门槛。用户最终范围是日常开发、普通管理员管理及群组化 CI/CD，不要求旧69项、复杂迁移或专项指标全部清零。

当前分支 `codex/group-subgroup-phase1-alignment`，HEAD `7dbdfb23570c4e164061e48707837f5e719d0985` 加保留的未提交工作树；最终[源码清单](../outputs/group-subgroup-phase1-20260923/candidate-phase1-release-20260924/source-manifest.json)指纹 `56a9eb949588adf001580c6e60f7ab44c4b8fd0acf6350e99d363ecc9d49f728`。前端及三类嵌入资源已重建，构建期间源码未变；没有提交、推送或操作生产实例。

**VERDICT: PASS**

**P0/P1 BLOCKERS：无未解决项。** 主任务复核当前授权事务及工作流配置调用链，子代理交叉审查，最终定向PG/单测与真实日常链证据适用。结论限定本次必要范围和已验证组合。

本轮成立并关闭：

1. **AUTH-CFG-01，P1。** 位置：`routers/web/shared/actions/general.go`、仓库Actions三配置保存入口、Variables/Secrets写服务。旧前置管理检查后可撤权提交，后续治理写事务未重新检查当前管理权，旧请求仍可改变公共配置/凭据。反证确认“已有治理事务”只串行化写入，不自动等于操作权限复核。修复将当前有效操作者、资源、生命周期及管理能力检查与实际写入放到同一治理事务；正式撤权后的旧请求403/拒绝且值不变，合法操作保留。[固定红绿与实际对照](../outputs/group-subgroup-phase1-20260923/final-configuration-authorization-acceptance.md)。
2. **AUTH-CFG-02，P1。** 位置：`services/actions/workflow.go`及原生Web启停。原Actions Writer API缓存整份ActionConfig；管理员收紧令牌上限后，Writer启停覆盖旧配置，导致权限上限回退。旧缺陷由当前调用链和写字段静态确定，不声称已运行旧版红例。反证检查确认Writer可合法到达，而权限上限本应只能管理员修改。修复在同一事务重载最新配置，仅更改工作流状态；保持Writer合法启停能力、撤权拒绝、Required限制。实际PG旧上下文/新上限对照通过。

Required opt-out被当作额外绕过的推测经反证排除：运行时Required已有优先规则，不能仅因旧配置字段存在就新增P1。历史归档恢复旧授权问题已修并回归，未无理由重开。

**UNVERIFIED RISKS：** 指定Linux amd64目标安装、生产拓扑、全部平台数据库客户端与专项容量未验证；这些不等于发现功能缺陷。目标上线仍需部署环境确认，不能以交叉编译代替。已下载副本或合法披露明文无法追溯撤回。

**NON-BLOCKING FINDINGS：** 原全仓lint存量问题保留，本轮新问题已修且最终增量lint为`0 issues`；未写成全仓lint通过。此前Fork快捷列表/大群组查询P2、少数原生图标可访问名P3保留。改名、复杂迁移、全生命周期、群组管理事件Hook、制品聚合和专项指标按用户批准后置，不扩大当前交付。

**判定：必要功能一期完成；在本次核查和验证范围内，无已发现的上线阻塞。上线准备待目标环境确认。**

## 最新开发收尾审查：F6，2026-09-24

**VERDICT: PASS**

**P0/P1 BLOCKERS：无。** 本结论针对当前工作树本轮必要日常修复、此前生命周期修复及其受影响调用链；由gpt-6-sol high子代理实施、另一子代理独立审查，主任务复查代码并操作真实UI。不把全量GitLab能力、没有提供的平台或全部未知攻击写成通过。HEAD `7dbdfb23570c4e164061e48707837f5e719d0985`，F6冻结源码指纹 `b88651b76ff502c9445f6d51b34914b0bdf3640034b1d3440204b39c75ffde79`，macOS二进制 `fca562abd1909954f84ebc29c87a551ca8617055ffc73a4bbfc33d3d93601c96`。

反证与结论：

- `Organization.UnitPermission`只把既有治理Code/Packages能力与Team取并集。有原生Team时立即返回合并权限，不再落入公开可见性读取后备；Guest/Minimal没有被扩成Reporter，未扩Projects。实际包授权仍在原消费者核对，无权用户UI404/下载401。
- Fork GET与创建入口复用合法目的组织，详情按钮补纯继承权限；POST与服务端仍复核创建能力、数量和来源。F5真实个人源仓79→子组Fork82可读Git，F6显示层只改FullPath/title，不改隐藏ID或API名称。模板自动转义、范围检查及禁同Owner/重复Fork语义保持。
- 消费者失效来源只影响侧栏可运行列表与派发表单；服务端仍复核来源生命周期/注册版本。Required配置及最终合并门禁没有从无效来源列表删除。无来源读权者仅见编号及通用原因；管理链接只给有权Owner/admin。PG门禁/派发回归及真实Run667成功支持结论。
- F2实际存在的归档恢复旧队列/旧task token重新授权P1已经在F4关闭，不继续误列当前阻塞：四个生命周期入口同一治理事务取消全部旧事件并清空Running/Cancelling token盐；缓存重读、JWT与后续资源认证均拒空盐，后续状态更新不会回填旧盐。正常用户取消仍保留合法收尾，新Attempt创建新任务与盐。F4真Runner+UI 75项通过；F6三项PG核心回归再过。

完整[本轮UI/协议证据](../outputs/group-subgroup-phase1-20260923/daily-owner-entry-acceptance.md)、[来源失效/恢复/执行](../outputs/group-subgroup-phase1-20260923/scoped-consumer-availability.md)、[F6定向整合与静态检查](../outputs/group-subgroup-phase1-20260923/f5-f6-final-regression.md)和[生命周期首错及复验](../outputs/group-subgroup-phase1-20260923/s05-s06-lifecycle-evidence.md)均可追溯。原红例、构建版本和真实执行数据库没有被覆盖。

**UNVERIFIED RISKS：** 指定Linux amd64主机、MSSQL、千仓容量、真人效率、复杂迁移/改名/故障全组合及九种连接器的全部协议尚未在本候选实测；保留原证据与限制，不影响本次已批准范围，也不视为对应支持已验证。

**NON-BLOCKING FINDINGS：** P2：Fork能力判断在个人目的不可用时逐群组查权，现行目的地导航还逐仓查询；没有目标性能数据，不猜测升级P1。P2：来源仓的“已有Fork”快捷列表只纳入直接组织成员，继承Owner可从子组项目列表或实际仓库URL使用已创建Fork。P3：既有Hook重投图标缺可访问名。全仓lint原有52项未修；本轮增量为0 issues，不混称全仓绿。

**交付判定：本轮识别的三项必要日常缺口与归档恢复安全问题已完成修复及闭环。** 现行计划不再要求原69项/专项清零；本结论不称完整GitLab企业版对齐或生产发布验收完成。

## F2/F3阶段历史审查（以下失败已由F4/F6证据更新，保留原因果链）

实际基线为 `codex/group-subgroup-phase1-alignment @ 7dbdfb23570c4e164061e48707837f5e719d0985` 加本轮未提交改动。未切换、覆盖用户改动或推送。下方原 `main @ 9a3f938` 及 v16 记录是历史审查；当前操作状态只以[唯一台账](group-subgroup-phase1-matrix.md#一期剩余关闭台账唯一明细)为准，不能从历史限制重复推导缺陷。

**当前阶段判定：已部署F2为FAIL；工作树修复等待新候选实际复核，不沿用此前PASS。** 新反证确认旧workflow_dispatch在归档恢复后自动领取，且运行任务token可在恢复后重新授权；其具体因果和PG红绿见下述生命周期补充。历史通过证据仍保留，不因此重开无关条目。

本轮实际修复及反证：

- MySQL 的完整群组路径唯一索引超出上限，造成正常新建数据库失败；改为完整路径保留、短摘要唯一索引，并在查找时核对原文。核对创建、改名、移动、个人路径和历史别名的写入点，摘要冲突保持拒绝，未截断路径。正常迁移先回填并确认新唯一索引，再处理 PostgreSQL 旧约束；MySQL 未变值更新返回零行不再误报治理冲突。四库实际结果见[数据库证据](../outputs/group-subgroup-phase1-20260923/db-compatibility-acceptance.md)。
- AGit 使用虚拟分支名读取审批快照，导致支持的 PR 流程不可合并；现在读取真实隐藏引用，内部更新携带准确 PR 和推送者，引用事务同步推进审批代次，并返回真实更新错误。反查外部隐藏引用写入、初次创建的事务重入、后续提交和目标分支改变。真实 Git、审批失效、重新审批及合并结果见[合并证据](../outputs/group-subgroup-phase1-20260923/p02-p03-pr-gate-results.json)。
- 作业令牌设置页遗漏 Packages 字段，保存其他项会清除原配置；补齐既有权限表行，不改授权算法。三层原生 UI 和 Run282 的权限交集见[token 证据](../outputs/group-subgroup-phase1-20260923/token-limit-closure-results.json)。
- Webhook PATCH 仅改名称时重置事件、分支过滤和授权头；现在区分省略与显式空值，非法 glob 仍拒绝。复核未改变 Hook 权限、签名、SSRF 或 TLS 规则，事件空数组保持既有默认 push 语义。首次失败、定向回归及真实投递分别保留，完整关闭以 H01–H03 各自断言为准。
- 无创建权用户打开群组创建入口时，模板在空 Group 上读取能力造成半页500，原生入口还主动返回500；补齐空值短路并用既有404拒绝。新候选普通账号创建组74、刷新、治理／原生同步及无权拒绝通过，见[角色实测](../outputs/group-subgroup-phase1-20260923/top-level-group-roles-acceptance.md)。
- 群组归档取消原生定时任务时用短分支匹配完整 ref，旧 Run287 在恢复后实际复活。六处应取消同仓全部定时任务的调用改为空 Ref，仍限定仓库、schedule 事件和非终态，不影响 push／PR 或别仓。F2 旧 Run322 归档时取消，普通 Owner 从 UI 恢复父组后仍取消；独立待删仓仍423，新 Run333 真实成功。独立反证及静态检查见[F2 审查](../outputs/group-subgroup-phase1-20260923/f2-static-review.md)。

新增反证与修复（F2/F3尚不含最终修复）：

- 运行时Run368在父组恢复后由合法新Run403唤醒扫描，旧Attempt1仍自动执行，说明只取消schedule不足。四个归档/待删入口现在取消该仓所有非终态事件；默认分支改名/变更仍只清理schedule，不扩大到其他事件。
- 更深反证：支持取消的Runner使旧Task进入Cancelling；归档时认证拒绝，但恢复后旧Task的token和缓存查询再次合法。真实PG红例固定archive→restore并证明这一授权ABA，不依赖猜测。工作树最小修复在同一治理事务取消后清空运行/取消中Task的TokenSalt，并让凭据检查拒绝空Salt；cache仍重读DB、JWT产物按TaskID重读，四处生命周期入口共享该处理。普通用户取消不清空，保留既有收尾能力；人工重跑创建新Task及新Salt。没有新数据库迁移。
- 达到P1的理由是被生命周期明确撤销的旧执行授权能在恢复后重新执行/写入；不是将已经披露数据可追回作为要求。反证已排除新Attempt显式授权、原生取消收尾例外及仅静态疑虑。三项真实PG生命周期回归通过，真实Runner/UI新候选仍待执行。因此不得把旧F2/F3当作可发布通过版本。

当前 F2 候选的 macOS 二进制 SHA-256 为 `2d64a75d8442565b31762279db142cd63fac50cbece5d762d1f558f074b511de`，源码指纹为 `2fd0a1e11ea46698f8ac6bb7d348dcff9cfddc71e20bb01e30ed547ee2b4e9cb`；[构建清单](../outputs/group-subgroup-phase1-20260923/candidate-lifecycle-fixed-20260924/source-manifest.json)保存 HEAD、工作树差异、文件摘要、命令及构建结果。构建后的测试、脚本和证据补充不反写为该二进制的输入。前一 F 候选相关定向测试 38 个顶层通过，其中集成入口 25 个使用 PostgreSQL，其余包级 13 个使用原生 SQLite TestMain；此前把环境变量误当作引擎选择的标注已纠正，见[逐测试记录](../outputs/group-subgroup-phase1-20260923/current-candidate-targeted-regression.json)。F2 的新增／受影响测试及运行另记专项证据，不能把这38项全改标为F2重新执行。F2 独立副本 `make fmt` 退出0、35个本轮 Go 文件无格式差异；增量 `golangci-lint ./...` 0 issues。

**UNVERIFIED RISKS：** 一期仍未关闭的真实运行／角色／并发／恢复断言、MSSQL 原生目标兼容、Linux amd64 安装与指定容量、最终候选整体回归及真人效率。迁移大数据耗时未测；不能由四库小样本证明容量。它们不作为缺少因果证据的 P1，也不算正式发布通过。

**NON-BLOCKING FINDINGS：** 原生 Hook 历史重投图标缺少可访问名称（P3）；已删除 Runner 的运行按既有超时机制失败，诊断可进一步直达原因（P3）。全仓 lint 仍有基线问题，已改源码和新增测试的增量检查单列记录；不以删除断言或隐藏日志消除首错。

## 历史审查记录（保留原基线）

日期：2026-09-24。基线为 `main @ 9a3f93813847f53b4762e28863b09b9266f75943`；结论覆盖本期已经实施、审查并有下列证据的改动及其实际调用链，不是完整一期发布批准。源码仍在用户工作树，未提交、切分支或推送。协议验收只向本机一次性测试仓库推送。

## VERDICT: PASS

本轮继续完成原生 OAuth／屏蔽的最终授权、组织应用审计范围，以及 push 事件提交与定时计划一致性修复。父任务整合测试、真实 PostgreSQL 分支锁屏障、v15 普通 Owner／无关用户页面及真实 Runner 均有独立证据。v16 又补屏蔽三类事务审计和定时冲突不吞普通 push，并完成最终 PG 七项与真实审计／Runner 复验。下述已审改动与调用链中没有保留成立的 P0／P1；历史缺陷、修复和未验边界分别保留，不把上一批 PASS 直接当作本轮通过。

**一期交付判定：未完成。** 操作矩阵仍有未验证的角色、入口、事件、生命周期和恢复组合，目标容量及人工效率无合格证据。祖先 CI 与可信门禁已实现且有真实执行片段，但不能称为全部矩阵通过、一期可发布或 GitLab 企业版全部对齐。

## P0/P1 BLOCKERS

无当前仍成立的 P0／P1。本批新发现的归档删除问题已修复并复验；完整首错、因果链、修复和证据见[v14 父任务报告](../outputs/group-subgroup-phase1-20260923/parent-v14-actions-validation.md)。

- 原因：原四个用户入口只有单元写权，已归档仓库仍能修改产物状态、删除运行／任务并排队清理，破坏归档只读约束。v13 专用仓库 19 的产物和 Run 80 实际被删除，严重程度达到 P1；排除了跨仓 ID 混淆、token scope 和内部到期清理等反证。
- 修复：用户服务在同一短事务内重读身份、生命周期、资源绑定和当前 Run 状态，再生成清理快照及提交删除；内部 TTL 不改。重跑同步最新 Run 状态，旧完成快照不足以删除新 Attempt。
- 最终证据：四个归档入口、陈旧快照、身份失效、异仓及 attempt、正常删除和重跑等真正 PG 回归 9 个顶层全部通过；v14 原对象 Web 单件、Web 整次、REST 整次删除均为 423，原产物每次重新下载内容一致。v4 单件路径有 PG HTTP 证据，本机 v3 对象没有冒充其正反例。
- 边界：v14 时浏览器测试确认框受阻，v15 已解除；本轮未重新执行 Actions 删除控件的最终 UI 操作，仍未验收，不以安全 PASS 代替产品完成。

下表记录此前修复及反证。

| 风险面 | 因果链与修复 | 反证与证据边界 |
| --- | --- | --- |
| 旧 Runner 领取目的范围任务／凭据 | 候选扫描后转移使旧范围授权与新 Owner 凭据混用；最终领取、当前范围与 payload 选择进入同一短事务 | 不能仅靠防重复领取；固定候选 SQL 屏障、SQLite Secret 负例、PG 并发领取及真实合法运行分别验证。提交为披露授权点，不承诺追回已披露明文 |
| 移动或转移恢复旧信任 | 群组 OwnerID 不变及移出再移回会绕过单次 Owner 比较；单调 Actions 修订、旧 Run 失效、旧计划／注册令牌清理与移动同事务 | 真实群组移动服务的 PG 入库屏障通过；普通 UI 转移并转回后旧 Run 仍 409，新 Run 15 真实执行成功 |
| 来源归档／删除／转移 | 来源注册仍在时，发现和执行可能继续消费失效来源；新增当前生命周期与注册／Run 修订核对 | 可执行来源不能从 Required 配置列表直接过滤，否则会删除门禁要求；已撤回该候选做法。归档屏障、可恢复删除及最终 Scoped 整组通过 |
| 撤权后的注册凭据／部署密钥写入 | 入口已通过时发生撤权／转移，旧请求仍可能披露注册 token 或插入部署密钥 | 当前操作者、代办身份和目标权限在 `WithActorWrite` 内重检；Handler 红例分别为 200／201，修复后 403。SSH 文件同步在提交后 |
| 统一工作流设置旧权限 | Add、Required、Remove 仅依赖入口的旧 Owner/admin 身份 | 父代理追加发现后修复；三种撤权固定红绿、旧管理员 Handler 403、当前 Owner 正例及 instance/group/repository 占用通过；最终完整 Scoped PG 再次通过 |
| 系统调度身份误拒／重跑旧身份 | 普通用户检查不能把系统 `-2` 拒绝，也不能无条件放行；重跑不能只读取原始 Run 触发者 | 系统身份绑定当前持久计划，本次 Job 绑定 Attempt；真实 schedule 和手动触发成功，PG schedule／rerun／reuse 回归通过 |
| 作业 token、JWT、独立产物端点 | token 签名有效不代表任务当前仍有授权；缓存命中也可能陈旧 | 每次认证重读任务和当前权限；归属改变、停用、归档、身份停用的失效测试与 artifact v3/v4 集成通过。完整外部协议仍未验收 |
| 合并回调被自身占用阻断 | Git 已更新，post-receive 补写 PR 被自身 reservation 拒绝，Actions／schedule 通知中断 | 传入并核对精确持久授权，而非提前删除 reservation；错误 PR、仓库、用户、旧／新提交、引用和非内部合并均拒绝。保留占用直到原有结果核对；PG 六种合并方式通过 |
| 标签数据损失与物理目录误判 | 旧转移删除组织标签关联／历史；返回自身原物理目录又被误判重名 | 无法保留标签的组合拒绝并回滚，仓库本级标签正例保留。目录修复先保留真实 DB 冲突，只排除当前仓库自身物理路径；两个红绿服务用例及 UI 转回通过 |
| 同名状态伪造与无原生保护绕过 | Required 从普通状态名改为当前提交、来源注册／修订、工作流身份、运行记录和可信 Runner 的证据；最终门禁先于原生保护空值返回 | 不可用来源仍保留要求；旧配置成功不能满足新要求。PG Scoped 回归及无本级 YAML／无原生保护的真实 PR3、PR4 审批失效和自动合并通过 |
| 原生 OAuth 与 sudo 绕行 | 组织、实例、个人 Web／API 的最终写入重新读取当前主体和原 sudo 管理员；个人 API 拒绝组织身份；来源生命周期与操作同事务 | 旧接口 201／200 红例、最终服务测试与真正 PG 路由通过。普通 Owner UI 归档编辑拒绝、恢复编辑成功，无关账号 404；创建／轮换／删除 UI 全矩阵仍未验 |
| 屏蔽与解除的最终边界 | 当前操作者、原 sudo 身份、组织管理权在最终事务核对；归档允许屏蔽与备注，禁止解除后放开；边删边分页改为第一页处理 | 26 个以上协作者的残留红例修复，相关包通过；普通 Owner UI 归档拒绝解除、恢复后正常，未夸称所有个人相互屏蔽组合通过 |
| 组织应用审计来源 | 旧 OAuth 应用事件统一记 user，使群组与祖先查询遗漏；按实例／个人／组织分别写当前范围，组织投影到祖先 | PG user/group 断言红绿，真实父级和子级审计页面均见应用 5 的修改；没有在详情中加入密钥或哈希 |
| push 与 schedule 分支版本 | push Run 读取事件 after，schedule 独立读取当前默认 HEAD；计划替换、Run 入库和领取复核锁定持久分支 | 固定 H1/H2 红绿、PG 分支行锁屏障、真实 Run88／89 各用自身提交、skip-ci 更新及移除 cron 通过；授权线性化是持久分支提交，不是 Git ref 改变瞬间 |
| 原生 Team 写入与邀请授权窗口 | 新增／移除成员、Team 管理、邀请接受在治理写边界内重检当前操作者及生命周期；撤销不受归档新增限制阻断 | 旧操作者快照红例及 PG、普通 Owner UI 创建、指定仓库授权、撤权、邀请撤销和删除通过；原生最后 Owner 操作拒绝并显示原因 |
| Release、LFS 与独立下载端点 | 完成上传时重检授权和归档，拒绝的暂存对象安全清理；部署密钥与组织主体分开判断，旧 LFS JWT 不只凭签名放行 | PG／MinIO 最终检查与并发通过；同一部署密钥真实 LFS 下载／上传通过，UI 撤销后 SSH 拒绝、尚未过期 JWT 401 |
| 本人自退与多来源 | 新入口只删除本人直接来源，永久人类 Owner、修订和审计同事务；其他合法来源不假装已撤销 | SQLite／PG、独立审查及 v6 UI 自退后旧 artifact 404、新 Git 请求拒绝通过 |
| 组织身份触发任务永远排队 | 部署密钥以组织身份生成的首次 Run／Attempt／Jobs 同事务取消，不进入并发抢占或任务领取；有权人类可创建新 Attempt | v7 真实推送立即取消且无 TaskID；Owner UI 重跑 Job42 成功上传产物，旧取消提示消失；没有把组织视为可使用人类凭据的身份 |
| 审计范围与错误页面 | 查询、导出创建／分段／下载都重检当前权限，事件绑定当时祖先；公共 Web 未找到／无权使用原生 HTML 404 | 先保留旧纯文本响应的红例；v9 PG 四项通过，真实 Owner 子树查询及筛选通过，Developer 页面及同一导出下载 404；Secret 固定样本详情无明文 |
| 受限共享与群组删除 Owner 拼接 | 自定义 Reporter 的 ManageGroup 与另一条 Reporter 上限共享的原始 Owner 角色共同骗过旧检查；空群组可越权安排删除 | v10 正式 API 建立真实可赋来源，父任务浏览器实际安排删除成功。v11 复用单条完整 HasOwnerGrant，同身份预览／恢复／永久删除及恢复后再安排均 404；真正 Owner 仍可 UI 恢复，没有进行物理删除 |
| 受限共享读取群组审计 | 受邀组 Owner 经 Maintainer 上限共享仍有原始 Role Owner 与 ReadAudit，旧审计检查错误放行 | 父任务 v10 真 UI 可看审计、API 200 返回 15 条合成事件；v11 同身份页面、查询和导出均 404。合法自定义 ReadAudit 与完整 Owner 正例保留 |
| 原生 Owner 身份派生 | 多条不完整授权的能力并集可拼齐 Owner 能力并被原生 helper 当作 Owner | 公开角色／成员／共享服务固定红例；只修改 Owner 判定为单条完整来源，普通能力并集不变。保留原生 Owners Team、祖先 Owner 及完整 Owner 共享；7 个受影响包扩大回归通过 |
| 申请快照及转移邀请路径 | 申请人无当前访问权后不应因改名读取新路径；邀请转移则必须显示当前完整仓库目标 | v10 私有后改名、原路径快照、仍可撤回及 Owner 批准 UI 通过。v11 同名子组转移的邀请完整路径与新旧 HTTP Git 正反例通过；反证旧临时 OwnerNamespace 会被模型纠正，未误报为持久化路径错误 |
| 审计导出元数据与兼容性 | 全局快照序号不再直接作为范围导出的元数据；保留现有 `max_sequence` 字段并按授权筛选计算 | 父审查退回直接删除字段的初稿。内部快照不变，POST 原事务、GET 稳定读取包含授权和计算；真实 API 对外 375／0、内部上界 386／387，两份下载及最终 PG 范围、游标测试通过。属于已修复 P2，未夸大为安全 P1 |
| 失败工作流诊断与审计员入口 | v12 区分绑定后最新运行的等待、失败、取消，仍拒绝未满足必选任务及可信 Runner 的证据；现用协作者页显示审计员只读边界 | 同一真实 PR5 由错误 Runner 提示变为实际运行失败，仍阻止合并；审计员旧入口跳转后只读，API 写入 403，撤销身份后旧会话及 API 404。旧回归断言失败已追查并更新最终页面验证，没有跳过 |
| 带仓库归档恢复及冲突提示 | v12 两仓真实验证归档只读与单仓恢复被父组拒绝；v13 仅为 `ErrConflict` 增加普通 Owner 可操作的恢复提示 | 授权和事务未改。UI 群组恢复后原活动仓及原独立归档仓均可推送，Issue 写入由 423 变为 201，无关用户仍 404；当前统一恢复方向有 GitLab 19.4 固定源码及测试支持。PG 四项及增量 lint 通过 |
| API 分支保护及受保护凭据反证 | API 内容写、HTTP Git 普通推送／强推分别服从群组规则；Owner 不能绕过推送无人或禁强推 | 无关用户拒绝、同对象读取及 main 未变作对照。中途凭据 false 的两个历史 Run 在领取前已非 HEAD；稳定 HEAD 的 API 正例 Run60／61 均成功，不把历史失败误判为 API 凭据路径缺陷 |
| 制品版本删除 | Generic 及 OCI tag／digest 均验证拒删、合法删除、删除后客户端失败，其他版本和旧样本仍在；v13 Generic 原生 UI 也完成删除 | Reporter 页无删除控件；真实协议拒绝另有证据。ORAS `not found` 曾被验收脚本误分，不是产品未删除；未声称多 tag 共享摘要和物理 blob 清理已验 |

审查还检查了缓存命中、错误 payload 回滚、临时 Runner 二次领取、迁移顺序、定时身份、PR 合并占用、原生角色、私有来源详情和稳定存储路径。网络发送与 Runner 执行没有放进领取的短授权事务；但 Release 等其他写路径仍存在下列需量化的锁内操作，不能把这点泛化成“所有 Git 解析均已移出锁”。

## 先前缺口的当前状态

早期报告中“无原生保护即可绕过 Required”和“只按普通状态名判定成功”两项已经被后续可信证据链替换，不再列作当前尚未开发的能力。父任务追到 `services/pull/merge.go` 的最终 `checkRequiredScopedRuns`，以及 `services/pull/commit_status.go` 的页面快照检查；真实 PR3 的旧配置成功曾被拒绝，新运行后才可合并，PR4 又验证了审批失效与自动合并。

这不免除剩余矩阵：源不可用、所有绕过身份、更多并发入口和全部生命周期还须逐项验收。拒绝没有强制配置的普通仓库、仅匹配同名状态、或安全通过但用户无法完成正常合并，均不是本计划允许的结果。

受保护 Secret 已新增真实 UI／Runner 正反例：受保护分支取得凭据，普通分支和 Developer 私有 fork PR 未取得，详见[凭据实测](../outputs/group-subgroup-phase1-20260923/parent-protected-secret-ui.md)。首次探针同时发现 Runner v1.0.8 不识别 `github.ref_protected`／`gitea.ref_protected`。服务端下发门禁独立执行，该兼容缺口没有绕过门禁的可达路径；不能把字段的服务端单测当成表达式已可用。

## UNVERIFIED RISKS

- 本轮 PG 已验证分支行锁与 schedule 授权的固定交错；MySQL／MSSQL 锁语义仍只有实现与编译证据。分支同步晚到回调测试是顺序旧事件样本，不冒称同步调用之间的并发屏障。
- Git ref 已改变但 post-receive 尚未提交持久分支表的窗口仍在；已经授权披露的凭据无法撤回。跨节点共享锁／租约未验，本期约定单应用节点。
- 原生 OAuth 的当前主体与 sudo 回调已做红绿回归，但所有 UI 创建／轮换／删除、仅祖先 Owner、撤权与待删除的组合仍不完整。浏览器固定 Owner 21 同时有父子原生 Owners Team。

- 生命周期预览、改名／别名、含仓移动、延迟删除／恢复，以及活动任务与 Runner 停用的完整真实组合仍未验收。v10 已验私有后改名和 HTTP Git 新旧地址；v11 已验空群组延迟删除、重启后记录保留与恢复，并不代表所有后代消费者通过。
- v12／v13 已验含两仓群组的归档／恢复及原独立归档仓的恢复，Git 与 Issue 结果一致；待删除项目、执行中任务及其他协议的混合状态仍未验。GitLab 19.4 的独立归档语义已核实，不能继续列为未知。
- Runner 排队期间停用／恢复已在 v9 真 UI 与真实进程验证：两个 Runner 都停用时 Job55 没有 Task，单独恢复根 Runner 后 Task48 由 Runner2 完成。删除、执行中停用及全部披露交错仍未验，不把此例扩大为全矩阵结论。
- v13 作业 token 同 Owner 私有 allow-list Code 读取和源／目标上限已由真实 Runner 验证，跨仓写及当前不支持的 Actions 读被拒；不同 Owner、Git／包、fork、任务中撤权／转移仍未验。受保护引用的完整定时／手动／重跑、`pull_request_target` 与复用调用矩阵也未完成。
- v10 成员申请批准为 Reporter、拒绝、本人撤回和退出已通过真实 UI／Git；完整角色及来源集合、到期、归档恢复独立状态、所有审计筛选、分页、窄屏与错误恢复尚未全覆盖。
- v11 补充验证转移后接受仓库 Developer 邀请及真实推送；Owner 移除直接来源后保留群组 Reporter，再移除最后来源使新旧 Git 地址拒绝。90 秒授权通过正式 API 创建，到期后 UI／Git 拒绝且有自动清理审计；检查晚于清理，不能称为清理前瞬间或全部到期交错的证据。
- 当前有 PostgreSQL／MinIO 和 SQLite 结果；受影响的 MySQL／MariaDB／MSSQL 兼容，以及应用重启后全部恢复场景仍未完成。
- `max_sequence` 保留字段名及 JSON 类型，但数值含义收窄为当前授权筛选范围。仓库内未发现依赖旧全局含义的消费者；外部客户端的语义兼容尚未验证。
- 目标 Linux amd64、8 vCPU、16 GiB 环境暂未提供。千仓、千成员、20 层、50 用户和 10 Runner 的锁等待、吞吐、延迟无合格结论。
- 冻结任务集的 80% 人工活跃时间及重复操作减少没有人工对照记录。自动化耗时或点击数不能替代。

- Runner v4 DeleteArtifact 的任务认证与状态写入之间、legacy Run 的 attempt 回填与用户删除之间，尚无专项固定交错；在途已授权请求不能仅凭步骤分离判成 P1。用户身份降级后仍有来源读取资格的旧触发任务，token 按任务声明与当前层级策略计算，不能宣称必然逐单元交集人类新角色；该身份语义需单独验收。

这些缺口不自动构成安全 FAIL，但按计划阻止一期交付完成。

## NON-BLOCKING FINDINGS

- **P2，统一工作流触发能力仍有缺口。** `detectAndHandleScopedWorkflows` 显式跳过 schedule／workflow_run，定时检测仅扫描本仓；本级定时实测不代表祖先来源定时闭环。该缺口阻止相关一期操作完成，但没有证明安全 P0／P1。矩阵 CI05 已明确为待开发。

- **P2，定时当前 HEAD 的保守限制。** 默认分支推进会使旧计划 Run 的后续授权失效；重复同一 HEAD 通知替换等价计划也可能取消旧运行，未改为通知幂等。当前系统用户零 cron 已会清理，不再列旧猜测。
- **P2，非默认分支删除兼容路径。** 未安装引用事务 Hook 时，手动删除与并发重建可能使数据库分支状态短时误标，后续同步可恢复；默认分支删除被拒绝，不将其推导成旧定时凭据绕过。

- **P2，现用 Runner 与官方 v4 产物 action 兼容不足。** 实际 v3 上传成功，官方 v4.6.2 报 GHESNotSupportedError；未修改官方 action，也没有获得 v4 实物下载正例。不能将服务端 v4 集成通过宣称为该客户端可用，见[令牌／产物证据](../outputs/group-subgroup-phase1-20260923/ci06-runner-token-acceptance.md)。

- **P2，短事务范围仍需收紧或用测量证明。** Release 引用操作回调中仍有 Git 查询，原生 Team 批量访问重算在治理写事务中逐仓执行，自退后的审查待办同步会扫描相关 PR。未取得千仓死锁或持续饥饿的证据，不据此虚构 P1；也不能据小样本声称满足性能门槛。
- **P2，Runner 上下文字段不完整。** 已验 Runner 的 typed context 丢弃 `ref_protected`，工作流不能依赖该表达式判断保护状态。后续如扩展 Runner，必须使用实际获授权的源码、声明必要能力并做协议验收；当前不伪称已经升级交付。
- **P2，审计序号仍有粗粒度活动侧信道。** 可见事件继续使用现有全局自增 ID，两次有权事件之间的序号间隔可能透露站点其他活动的大致变化。v12 已消除空筛选和无关新事件直接暴露全局末序号的问题，但没有更换既有事件 ID 或导出格式。没有由此读取其他范围事件内容的可达路径，不列为 P0／P1。
- 受保护 Secret 当前要求领取时提交仍为受保护分支 HEAD；新提交可能使旧排队任务得不到凭据。此保守实现限制已登记为计划 D-22，其他触发与引用语义仍待补齐，不能据本次稳定 HEAD 正例宣称全部 GitLab 变量语义等价。
- 包页面使用层级 Web 路径，原生协议安装命令仍使用组织标识。此次按安装命令验收有效，未承诺把群组全路径直接替换进旧包 API。UI 隐藏管理入口不是服务端鉴权证据，二者分开记录。
- 本轮全仓 `make lint-go` 实际报告 46 个问题、退出 2；本轮相关 CI 增量检查为 0 issues，其他既有问题单列。`make fmt` 在隔离副本执行，未将无关格式变化回写用户工作树。

先前记录的失败运行误报 Runner、全局 `max_sequence` 外显、邀请预览措辞三项 P2 已在 v12 修复并核对真实页面／API，不再作为当前遗留问题。邀请现显示“当前邀请目标”，访问申请仍保留旧路径快照。剩余状态的全部真实组合仍按未验证项处理，不能由文案测试替代执行证据。

## 父任务证据索引

- [v16 最终整合及反证](../outputs/group-subgroup-phase1-20260923/parent-v16-integrated-validation.md)：屏蔽审计三类事件、PG 七项、定时冲突固定红绿与最终 Run92。

- [v15 整合、首错与修复](../outputs/group-subgroup-phase1-20260923/parent-v15-integrated-validation.md)：九包原生与五包 CI 共 324 个顶层测试通过；真正 PG 十二个不同目标分别有最终通过记录，保留 fixture 首错，未冒称一次整组退出 0。
- [v15 原生真实界面](../outputs/group-subgroup-phase1-20260923/parent-v15-native-ui.md)：同对象归档拒绝、恢复成功、完整路径、父级审计与无关用户 HTML 404。
- [v15 真实 Runner](../outputs/group-subgroup-phase1-20260923/parent-v15-ci-ui.md)：push 事件绑定、schedule、skip-ci 清理。旧 push HEAD P2 已修复并不再列为开放问题。

- [20 包单元与迁移检查](../outputs/group-subgroup-phase1-20260923/parent-final-unit.md)：退出 0，19 包通过、1 包无测试；条件性数据库跳过不算通过。
- [PostgreSQL 合并回归](../outputs/group-subgroup-phase1-20260923/parent-postgres-combined.md)：46 个顶层测试，退出 0，250.849 秒。
- [最后设置鉴权修改后的完整 Scoped 回归](../outputs/group-subgroup-phase1-20260923/parent-scoped-final.md)：退出 0，81.537 秒。
- [真实 UI 与 Runner](../outputs/group-subgroup-phase1-20260923/runner-ui-evidence.md)：普通 Owner、子组仓库、执行／重跑／定时、受限移动、两次仓库转移、旧 Run 拒绝、新手动 Run 成功。
- [v5 整合回归](../outputs/group-subgroup-phase1-20260923/parent-v5-integrated-validation.md)：PG／MinIO、仓库服务整包、原生 Team 定向通过；[v5 真 UI／协议](../outputs/group-subgroup-phase1-20260923/parent-v5-ui-validation.md)记录实际 LFS、Team 和叶子恢复。
- [v6 自退定向回归](../outputs/group-subgroup-phase1-20260923/parent-v6-integrated-validation.md)与[真实 UI](../outputs/group-subgroup-phase1-20260923/parent-v6-ui-validation.md)。
- [v7／v8 整合与 UI](../outputs/group-subgroup-phase1-20260923/parent-v7-v8-validation.md)：Actions 全包 70 项通过、PG 五项通过；真实组织触发取消／人类重跑、最后 Owner 可见错误、撤销邀请 HTML 404。
- [受保护凭据正反例与 fork](../outputs/group-subgroup-phase1-20260923/parent-protected-secret-ui.md)：记录首次探针失败、准确根因及三种实际下发结果。
- [v9 审计页面及导出](../outputs/group-subgroup-phase1-20260923/parent-v9-audit-validation.md)：通用 404 的父任务复核、PG 四项通过、Owner／Developer 真实页面及下载正反例。
- [v9 Runner 暂停与恢复](../outputs/group-subgroup-phase1-20260923/parent-v9-runner-pause.md)：两项 UI 停用、真实排队、只恢复祖先 Runner 后完成受保护凭据探针；随后恢复原状态。
- [v10 申请、可见性、改名及 Git](../outputs/group-subgroup-phase1-20260923/parent-v10-access-request-validation.md)：正常批准／拒绝／撤回、自退后的新旧地址拒绝；真正 PG 三项通过，不能扩成所有角色矩阵。
- [v11 Owner 与生命周期](../outputs/group-subgroup-phase1-20260923/parent-v11-owner-lifecycle-validation.md)：两个真实越权红例修复后同身份拒绝、真 Owner 重启恢复；真正 PG 四项 8.405 秒，公共 helper 的 7 包／263 顶层（含子项 573）SQLite 回归通过。
- [PG 口径纠正](../outputs/group-subgroup-phase1-20260923/owner-identity-db-evidence.md)：两份曾标 PG 的服务单测其实使用固定 SQLite，不计 PG 通过；随后使用集成测试和实际数据库引擎断言补验。保留首错及原始文件。
- [v11 仓库邀请和转移](../outputs/group-subgroup-phase1-20260923/parent-v11-transfer-invitation-validation.md)：当前协作者页入口、实际 SMTP、目标末级与仓库同名的转移、收件人预览及新旧 HTTP Git 权限。
- [v11 接受邀请、多来源及到期](../outputs/group-subgroup-phase1-20260923/parent-v11-multisource-invitation-validation.md)：转回原群组后收件人接受 Developer 并推送；逐来源撤销与到期正反例，以及实测时间边界。
- [v12 兼容性、诊断与审计员整合](../outputs/group-subgroup-phase1-20260923/parent-v12-integrated-validation.md)：真实页面／API、模板和语言资源重建、最终 PG 六个顶层测试（含子项 26 个 pass，0 fail），包耗时 92.865 秒。仅移除字段的初稿没有作为最终方案交付。
- [v13 归档恢复与协议删除](../outputs/group-subgroup-phase1-20260923/parent-v13-lifecycle-validation.md)：两仓真实归档／恢复、原生单仓冲突反馈、Generic UI 删除；新增 PG 四项通过、包耗时 12.866 秒。该记录链接到 Generic／OCI 真实删除、API 分支保护和 GitLab 固定版本证据。
- [v13 Web 编辑](../outputs/group-subgroup-phase1-20260923/parent-v13-web-edit-validation.md)、[Developer 协议](../outputs/group-subgroup-phase1-20260923/developer-branch-protection-evidence.md)及[真实作业 token](../outputs/group-subgroup-phase1-20260923/ci06-runner-token-acceptance.md)：有权成功、只读拒写、禁推规则与跨仓上限。
- [v14 Actions 删除复验](../outputs/group-subgroup-phase1-20260923/parent-v14-actions-validation.md)：真实红例、最终事务修复、9 项顶层 PG（含子项 36，通过且无跳过）及同对象 423／产物保留。首次 fixture 失败和未选中子测试的证据纠正保留。
- 构建仍为本机 macOS 开发二进制，工作树仅保留改动，未形成候选发布。

后续按冻结矩阵补齐上述实际入口、协议和生命周期证据；已通过子项保留为回归，不重复开发。无法取得的目标环境及人工样本维持未验证，不将失败或缺证据条目临时移到二期。

第十六批补充审查：屏蔽业务变化和三类成功事件共用事务，故障注入回滚业务与审计；真实父子页面和 PG HTTP 均核对。仅放行计划更新的 ErrConflict 继续普通事件，Run 最终 Owner／Namespace／Revision／归档检查不变，范围变化固定反例仍拒绝。没有新增成立 P0／P1；详见 v16 报告，不改变一期未完成判定。

## 2026-09-24 祖先定时收口审查

适用业务候选：HEAD `a488feb7a4ac2df8a7c17c01c536374b10f33098` 加[逐文件清单](../outputs/group-subgroup-phase1-20260923/ancestor-schedule-results.json)，源码指纹 `1b485408ab940a5693a89dfd29581f6e5e73d355344ccf11a83a0f6702f9c547`，macOS二进制 `71d19b0d1687…`。父任务复核 Sol high 子代理修改，沿最终插入、领取、来源和消费者版本、任务取消、正常 push、原生 cron 及迁移路径检查，并运行真实浏览器和 Runner。

**VERDICT: PASS。P0/P1 BLOCKERS：无当前成立项。** 本结论覆盖本次差异，不等于一期全部交付。

审查过程中实际成立并修复了一处 P1：仅祖先来源 A 更版时，`replaceSchedulesForRepo` 原全量清理会将消费者本仓及来源 B 未变的在途 schedule 一并取消，可中断整棵子树无关的长期任务。反证确认旧本仓 push 的全量更新不能豁免本轮新增的仅祖先变化路径；PG固定红例显示未变计划ID被替换。最终按计划内容和授权版本匹配，只替换失效计划；旧计划缺失的非终态 schedule 同时收敛，合法计划及 push 排除。PG保留ID、Next和任务状态的断言全部通过；真实Run234执行中只更新来源，73秒后仍为同一原生计划及任务，释放屏障后2分8秒成功。

定时消费使用最终插入事务中的规格 CAS，来源注册版本、两侧范围和持久分支版本共同校验；来源移动再移回不能复活旧登记，Owner必须在UI重新登记。可选来源解析失败不吞掉原生push或cron。旧排队不收敛另有确定性红绿，未用降低断言处理。详见[验收及首错](../outputs/group-subgroup-phase1-20260923/ancestor-schedule-acceptance.md)和[真实运行原文](../outputs/group-subgroup-phase1-20260923/ancestor-schedule-runtime-records.json)。

**UNVERIFIED RISKS：** 千仓扫描开销与目标规格容量未测；MySQL/MariaDB/MSSQL、跨节点与一期其他未关闭操作仍按矩阵保留。已披露内容无法追回。Linux arm64仅完成独立安装／替换启动及健康、登录页，不证明Linux Git/Runner实际链路。

**NON-BLOCKING FINDINGS：** 取消页面沿用现有状态，取消原因需结合来源版本记录解释（P2）；尚未增加独立取消原因展示模型。没有扩展 `workflow_run`、群组事件目录或跨子群组制品聚合。

后续父任务继续审查并实测 M11-a、M02-a、M03-a。两名永久人类Owner并发退出的数据库写事务由同一持久行锁串行，最后Owner检查与删除同事务，失败回滚；启动通道仅同步请求，不冒称内部交错。临时Owner到期前后均不能充当永久兜底。纯继承Owner创建及资料／可见性修改真实成功，父级／兄弟范围拒绝；受限共享可读不能改设置，公开转私有后匿名和无关账号的真实Git请求拒绝。首轮把Maintainer创建误判为应拒绝，已按冻结D-02和既有R07角色规范反证：该角色本来具有CreateGroup，无需改业务或收紧权限。9条集成测试lint问题已以等价断言和局部命名修正；最终相关6项PG与集成包lint通过，两份测试版本单独留证。此补验未发现新的成立P0/P1，**VERDICT仍为PASS**；一期其他未关闭条目、两项客户端兼容失败、容量与人工效率不因此获得通过结论。
