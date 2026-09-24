# 群组／子群组一期阶段验收记录

记录更新：2026-09-24。

## 当前结论

本记录保留 A／I-01 首批固定时序和父代理复验的原始结果，并累计标注至 v15 的明确操作证据。祖先 Runner／工作流、Webhook、标签、群组共享、Git/LFS、OCI/Generic、Release、产物下载、审批、邀请、原生 Team、成员自退、审计与访问申请已有部分真实 UI／客户端正反例；每项只对下文写明的身份、资源和动作成立。v7／v8 已真实验证组织部署密钥触发 CI 自动取消后由 Owner 重跑、最后人类 Owner 的可见错误、撤销治理邀请的 HTML 404；v8 三种受保护 Secret 探针验证了实际下发边界，但 Runner v1.0.8 的 `ref_protected` 表达式不可用。v9 实测审计过滤／导出与祖先 Runner 暂停／恢复；v10 实测内部群组申请、审批／拒绝、自退及旧别名 HTTP Git 子项；v11 实测空组延迟删除恢复、转移后邀请接受及多来源成员撤权；v12 实测审计员撤权、范围导出、可信失败诊断、Generic／OCI 删除协议及 API／HTTP Git 分支保护子项；v13 实测带仓库后代的归档恢复、Generic UI 删除及稳定 HEAD 的 API 触发凭据正例；v14／v15 记录原生资料、OAuth／屏蔽入口，v15 真实 Runner 核对 push 事件提交和 `[skip ci]` 定时计划。跨角色全矩阵及目标容量仍未完成，不能宣布候选发布或人工效率达标。逐操作状态见[操作矩阵](group-subgroup-phase1-matrix.md)。

## 累计操作证据与边界

| 操作 | 已执行并可复核的结果 | 证据等级与未完成部分 |
| --- | --- | --- |
| 祖先 CI 与 Runner | 父组工作流来源供无本级 YAML 的深层仓库执行 push／PR；父组 Runner Run19–28 成功，父级变量与掩码 Secret 生效；同名变量覆盖曾使重跑失败，修正后成功。v9 父／子 Runner 均停用时，Run45 二次运行的 Job55 超过两个正常轮询周期仍等待且无 Task；仅恢复父 Runner 后由其领取成功，受保护凭据存在，随后恢复子 Runner | [第二批](../outputs/group-subgroup-phase1-20260923/parent-ancestor-ui.md)、[第三批](../outputs/group-subgroup-phase1-20260923/parent-third-batch-validation.md)、[v9 暂停](../outputs/group-subgroup-phase1-20260923/parent-v9-runner-pause.md)为真实 UI／Runner；删除、执行中停用、注册令牌轮换及固定交错仍未验 |
| 统一工作流有效配置展示 | 原生群组 Actions 设置复用 `shared/actions/scoped_workflows`，已有本级与继承来源的配置展示；父组来源的工作流驱动深层仓库真实 push／PR，策略改变后旧 Run 不满足新配置 | [操作矩阵 CI05](group-subgroup-phase1-matrix.md)及[第四批](../outputs/group-subgroup-phase1-20260923/parent-fourth-batch-validation.md)为现有入口与执行证据；展示存在不等于全部来源可见性、移动／停用、复用和触发身份矩阵通过 |
| push 事件提交与定时计划 | v15 普通 Owner 连续推送两次，Run88／89 真实 Runner 日志分别核对事件 after 与各自提交 SHA；`[skip ci]` 更新 cron 无 push Run，计划 4 生成成功的 Run90／91；再次 `[skip ci]` 删除 cron 后计划数 0、该 SHA 无 Run | [v15 CI UI](../outputs/group-subgroup-phase1-20260923/parent-v15-ci-ui.md)为真实 Runner／页面子项；[v15 整合](../outputs/group-subgroup-phase1-20260923/parent-v15-integrated-validation.md)有异步乱序确定性回归和真正 PG 持久分支行锁屏障。真实连续推送本身不固定旧乱序；Git ref 到持久分支提交的短窗口与其他触发组合仍未验 |
| 生命周期预览与移动 | 仓库转出、转回 UI 成功，旧队列取消及旧 Run 重跑 409；v4 空叶子组预览后移动到兄弟组并移回，受限专属 Runner 组合 409 | [影响预览定向测试](../outputs/group-subgroup-phase1-20260923/lifecycle-impact-evidence.md)、[第四批 UI](../outputs/group-subgroup-phase1-20260923/parent-fourth-batch-validation.md)；含仓库／包／历史标签的群组移动仍待验，含仓库后代归档恢复的有限样本见下文 |
| 同名目标子群组转移与邀请路径 | v11 普通 Owner 从原生转移页把私有仓库 12 转至末级名称也为 `request-private` 的子群组 16；预览与设置页显示完整目标路径，Owner 新旧 HTTP Git 路径均可读、未接受邀请者两路径均拒绝。随后转回群组 15，转移前邮件预览当前新路径、接受 Developer 后新旧 Git 可读且测试分支真实 push 成功 | [v11 转移邀请](../outputs/group-subgroup-phase1-20260923/parent-v11-transfer-invitation-validation.md)、[v11 多来源](../outputs/group-subgroup-phase1-20260923/parent-v11-multisource-invitation-validation.md)为真实 UI／HTTP Git 子项；其他角色、SSH／LFS／包旧别名和完整转移／接受并发未验 |
| 基础分支保护及强制工作流 | 根规则从 UI 保存，HTTP／SSH 对受保护分支的推送实际被拒；PR 3 在旧工作流成功但配置改变后被阻断，新 Run 26/27 成功后普通 Owner UI 合并。v12 仓库 6 的 Owner 通过 API 创建／修改普通分支文件，禁推分支 API 403 且 HTTP Git 拒绝；普通分支强推成功，受保护分支强推拒绝并保持原 SHA，无关账号修改返回 404 | [第三批](../outputs/group-subgroup-phase1-20260923/parent-third-batch-validation.md)、[第四批](../outputs/group-subgroup-phase1-20260923/parent-fourth-batch-validation.md)、[API／Git 证据](../outputs/group-subgroup-phase1-20260923/api-branch-protection-evidence.md)；v13 的 Owner／继承 Developer Web 与 Developer API／HTTP Git 补验见下文，全部绕过角色和自动合并组合仍未验 |
| 父群组审批 | 根组 UI 建一票规则，PR 4 无批准时 API 合并 403；子级 Owner 批准后就绪，新提交使旧批准不计数。重新登录后缺批准时启用自动合并，二次阅读并批准新差异后于 22:19:25 自动合并，merge commit `da33580fc4d4eb315a02943ab207047ae0b784da` | [第五批](../outputs/group-subgroup-phase1-20260923/parent-fifth-batch-validation.md)记录完整时序；审批撤权、其他规则及自动合并竞态矩阵未验 |
| 可信失败诊断 | v12 真实 PR 5 的必选 Run 48 失败，页面从模糊的 Runner 信任提示改为明确指出运行失败、要求修复并重跑，合并继续阻断 | [v12 整合](../outputs/group-subgroup-phase1-20260923/parent-v12-integrated-validation.md)只证明这一个失败状态的真实 UI；等待、取消及伪造成功分支为定向测试，非新的真实 Runner 样本 |
| 治理邮件邀请与撤销 | 子级 Owner 从 UI 发 deep Reporter 邀请，本地隔离 SMTP 收信；匹配账号从邮件链接预览并接受，刷新可见三个子树仓库、可下载产物，HTTP Git 可读而兄弟仓不可读。v6 创建 invite2、收信并从 UI 撤销；v8 收件人打开同一已撤销邮件显示原生 HTML 404，未新增成员 | [第五批](../outputs/group-subgroup-phase1-20260923/parent-fifth-batch-validation.md)、[v6 UI](../outputs/group-subgroup-phase1-20260923/parent-v6-ui-validation.md)、[v7／v8](../outputs/group-subgroup-phase1-20260923/parent-v7-v8-validation.md)、[邀请页固定回归](../outputs/group-subgroup-phase1-20260923/invitation-web-evidence.md)；v7 已证实旧页面停滞时服务端实际返回 404；过期／并发未验，与原生 Team 邀请区分 |
| 仓库邮箱邀请与收件人拒绝 | v11 普通 Owner 从仓库 12 当前协作者页进入邮箱邀请，邀请 Reporter 后本地隔离 SMTP 收到邀请 3；仓库转移后收件人预览新路径，主动拒绝且未获成员资格。另发 Developer 邀请 4，转移后接受并实际 push。v12 邀请 5 显示“当前邀请目标”，Owner UI 撤销后收件人打开同一邮件得到原生 404、未获授权 | [v11 转移邀请](../outputs/group-subgroup-phase1-20260923/parent-v11-transfer-invitation-validation.md)、[v11 多来源](../outputs/group-subgroup-phase1-20260923/parent-v11-multisource-invitation-validation.md)、[v12 整合](../outputs/group-subgroup-phase1-20260923/parent-v12-integrated-validation.md)为对应真实 UI／SMTP／Git 子项；主动拒绝不等于管理者撤销，邀请到期／并发和其他角色未验 |
| 原生 Team 创建与配置 | v5 Owner 从统一导航进入高级团队权限，新建私有 `phase1-native-readers`，只授 Code Read 并限定指定仓库；描述保存后回显 | [v5 UI](../outputs/group-subgroup-phase1-20260923/parent-v5-ui-validation.md)为真实页面；其他单元、角色和 LDAP 未验 |
| 原生 Team 授权与撤权 | v5 UI 加合成成员并分配 `root-policy`：其 HTTP Git 可读目标仓库，未分配仓库 128，目标未授权 Issue API 404；移除仓库授权后新 Git 请求 128，再移除成员回显 0，深层治理 Reporter 独立来源不受影响 | [v5 UI](../outputs/group-subgroup-phase1-20260923/parent-v5-ui-validation.md)为真实页面和客户端；[团队写入证据](../outputs/group-subgroup-phase1-20260923/native-team-actor-evidence.md)有最终授权定向测试；并发、全单元和 LDAP 未验 |
| 原生 Team 邀请撤销与删除 | v5 空 Team 的原生邮件邀请刷新后仍待接受，Owner 点击 Remove 后消失；清空仓库授权和成员后，经确认框删除 Team，列表仅剩 Owners | [v5 UI](../outputs/group-subgroup-phase1-20260923/parent-v5-ui-validation.md)为真实页面；[v5 整合](../outputs/group-subgroup-phase1-20260923/parent-v5-integrated-validation.md)含邀请 HTTP PG 回归；收件人使用已撤销原生邀请的真实页面结果及非空删除未验 |
| 治理 Reporter 自退 | v6 受邀 Reporter 从 deep 成员页退出自己的直接授权，旧 Run30 产物 URL 新请求 404、HTTP Git 新请求 128；v10 独立群组 15 的 Reporter 从成员页退出后，新路径与旧别名 HTTP Git 下一次请求均 128，最终无直接成员 | [v6 UI](../outputs/group-subgroup-phase1-20260923/parent-v6-ui-validation.md)、[v6 整合](../outputs/group-subgroup-phase1-20260923/parent-v6-integrated-validation.md)及[v10 申请](../outputs/group-subgroup-phase1-20260923/parent-v10-access-request-validation.md)；本人只退出直接来源，不能推导已下载 ZIP 被收回，也未覆盖管理者撤权和多来源组合 |
| 管理者逐来源撤权与短期到期 | v11 同一用户有仓库直接 Developer 与群组继承 Reporter，Owner 从 UI 移除直接来源后预览和刷新为 Reporter；新旧 HTTP Git 可读、push 拒绝。再移除群组 Reporter 后私有页面 404、两路径 Git 均拒绝。另经正式 API 赋予 90 秒群组 Reporter，到期并由后台清理后页面／Git 拒绝，审计有 `member.expired` | [v11 多来源](../outputs/group-subgroup-phase1-20260923/parent-v11-multisource-invitation-validation.md)为真实 UI／API／HTTP Git 子项；到期后检查晚于清理，不证明清理前时间边界或并发屏障；秒级设置不是 UI 操作，Team／共享等更多来源未验 |
| 最后人类 Owner 保护 | v5 最后一名父组 Owners Team 人类 Owner 点击 Leave，服务端拒绝退出且授权保留；v7 重试时页面明确提示必须保留有效且不过期的 Owner，成员仍保留 | [v5 UI](../outputs/group-subgroup-phase1-20260923/parent-v5-ui-validation.md)及[v7／v8](../outputs/group-subgroup-phase1-20260923/parent-v7-v8-validation.md)；并发和其他 Owner 来源未验 |
| 部署密钥触发 Actions 的安全取消与重跑 | v7 实际 SSH 推送触发 Run37，组织身份的首次 Attempt41／Job41 立即取消并显示原因；Owner 点击重跑后 Job42 由真实根群组 Runner 成功，46 字节产物上传，刷新后旧提示消失 | [v7／v8](../outputs/group-subgroup-phase1-20260923/parent-v7-v8-validation.md)；机器推送仍需有权人类显式重跑，不等于交付服务账号或全部机器触发矩阵 |
| 受保护 Secret 的实际下发 | v8 父组 UI 保存受保护凭据并在深层显示只读来源；受保护分支 push Run45 凭据存在，未保护分支 Run47 和 Developer 私有 fork PR Run49 凭据不存在，三例均由真实 Runner 完成。v9 Run45 的新 Attempt55／Job55 在父 Runner 恢复后成功。v12 旧提交不再是受保护分支 HEAD 时 Run53／55 保守拒发；v13 保持新 API 提交为 HEAD 后 Run60／61 真实成功，Owner 打开 Run61／Job67 的 Actions 页面看到 Success 与布尔凭据存在结果 | [受保护凭据](../outputs/group-subgroup-phase1-20260923/parent-protected-secret-ui.md)、[v9 暂停](../outputs/group-subgroup-phase1-20260923/parent-v9-runner-pause.md)、[API／Git 反证](../outputs/group-subgroup-phase1-20260923/api-branch-protection-evidence.md)及[v13 页面](../outputs/group-subgroup-phase1-20260923/parent-v13-lifecycle-validation.md)；首次 Run40／42 因 Runner v1.0.8 缺 `ref_protected` 表达式失败，fork PR 的另一必选工作流 Run48 仍失败；旧 SHA 拒发是当前可信 HEAD 限制，轮换／移除、其他事件与撤权交错未验 |
| Hook、标签、导航与包 | 深层事件真实投递根／子 Hook，签名正确；UI 测试投递 503 后重投 200；祖先标签由父组创建后在深层 Issue 选用、刷新和筛选；共享项目入口和 OCI/Generic 原生制品页真实可达，受限共享读／拒写／撤权由客户端核验。v12 Generic 按版本、OCI 按 tag／digest 真实客户端删除正反例通过；v13 Generic 原生 UI 中 Reporter 无删除入口，Owner 删除测试版本后旧详情 404，独立版本和旧包仍可下载 | [Hook 记录](../outputs/group-subgroup-phase1-20260923/parent-members-and-webhook-ui.md)、[第四批](../outputs/group-subgroup-phase1-20260923/parent-fourth-batch-validation.md)、[包客户端](../outputs/group-subgroup-phase1-20260923/package-client-acceptance-evidence.md)、[包删除](../outputs/group-subgroup-phase1-20260923/package-delete-acceptance-evidence.md)、[v13 生命周期](../outputs/group-subgroup-phase1-20260923/parent-v13-lifecycle-validation.md)；Generic 是否符合用户实际常用协议的异步问题尚未获答；事件全目录、标签多来源、OCI UI 删除、其他包格式及并发撤权未验 |
| Git、LFS、Release 与 artifact | HTTP／SSH Git 正常读写及保护分支拒写、HTTP LFS 780000 字节上传下载摘要一致且成员撤权后拒绝；v5 同一部署密钥 LFS 下载 780000 字节、SSH Git＋LFS 上传 324000 字节成功，UI 撤销后 SSH 拒绝且未到期 LFS JWT batch 401；Release 55000 字节附件三种有权身份下载摘要一致；v4 Run 30 真实上传，三种有权身份下载 ZIP 内容一致 | [第三批](../outputs/group-subgroup-phase1-20260923/parent-third-batch-validation.md)、[v5 UI](../outputs/group-subgroup-phase1-20260923/parent-v5-ui-validation.md)、[第五批](../outputs/group-subgroup-phase1-20260923/parent-fifth-batch-validation.md)。浏览器下载监听超时及错误 Basic Web 404 已反证；Release 全部最终授权、删除及签名地址撤权未验 |
| 访问申请、审批与撤回 | v10 独立内部群组 15 的申请人先从个人页撤回，再于私有改名后按申请时旧路径撤回；恢复内部后经群组公共入口申请，普通 Owner 批准 Reporter，真实 clone 私有仓库 12 成功但 push 拒绝。本人退出后第二份申请被 Owner 从 UI 拒绝，页面及 Git 均无残留授权 | [v10 申请](../outputs/group-subgroup-phase1-20260923/parent-v10-access-request-validation.md)；SQLite 两项入口／HTTP、PG 三项定向通过。仅此内部群组、Reporter 和申请人样本；到期、多来源、其他角色及并发未验 |
| 可见性、私有改名与旧别名 | v10 Owner 把内部群组改私有、预览并改名，再恢复内部；申请人只见原申请路径，未授权旧地址返回 HTML 404。批准 Reporter 后新旧 HTTP Git 路径均可读，新路径 clone 成功、push 拒绝；本人退出后两路径均拒绝 | [v10 申请](../outputs/group-subgroup-phase1-20260923/parent-v10-access-request-validation.md)；旧申请路径快照在私有改名后保留。v11 跨根仓库转移真实子项另见上行；SSH、Wiki、LFS、包及其他角色／并发未验 |
| 原生群组资料编辑 | v14 普通 Owner 从子群组原生设置保存描述，刷新后内容仍在；v15 同一子群组归档及恢复操作另有记录 | [v15 原生 UI](../outputs/group-subgroup-phase1-20260923/parent-v15-native-ui.md)明确区分 v14 资料正例与 v15 后续操作；没有把资料保存冒充 v15 后重新执行，纯继承 Owner、其他资料字段和并发未验 |
| 原生组织 OAuth 应用 | v14 普通 Owner 创建应用 5，归档后仍能编辑为修复前红例；v15 同对象归档后编辑被拒并给出生命周期原因，恢复后改名保存、刷新仍在且回跳为完整子群组路径；无关非管理员访问应用页 HTML 404 | [v15 原生 UI](../outputs/group-subgroup-phase1-20260923/parent-v15-native-ui.md)为创建／编辑／拒写／恢复及 404 页面证据；[v15 整合](../outputs/group-subgroup-phase1-20260923/parent-v15-integrated-validation.md)覆盖最终授权定向测试。浏览器未做轮换／删除，Owner 同时属于父子 Owners Team，不能冒充纯继承来源 |
| 原生群组屏蔽与备注 | v14 普通 Owner 屏蔽合成无关用户并填备注；v15 归档期间重新屏蔽可保存、解除被拒且刷新仍在，恢复后备注保存及解除成功；无关非管理员访问屏蔽页 HTML 404 | [v15 原生 UI](../outputs/group-subgroup-phase1-20260923/parent-v15-native-ui.md)为同对象页面正反例，461px 布局可读；[v15 整合](../outputs/group-subgroup-phase1-20260923/parent-v15-integrated-validation.md)有最终授权及分页清理定向测试。v15 尚无屏蔽审计；v16 已补三类事件，父子实际页面可查且不含私有备注，见[v16 整合](../outputs/group-subgroup-phase1-20260923/parent-v16-integrated-validation.md) |
| 归档恢复 | v5 普通 Owner 从 UI 恢复空叶子群组 14；v12 普通 Owner 对群组 16 及两个真实仓库 13／14 归档，预览显示仓库 14 原先独立归档；归档后两仓可读但 push 均 403、Issue 创建 423、无关用户读取 404。父组归档期间单仓解档在 v12／v13 均拒绝，v13 页面明确给出处理路径；父组统一恢复后两个仓库均解档，真实 push 成功，Issue 创建 201 | [v5 UI](../outputs/group-subgroup-phase1-20260923/parent-v5-ui-validation.md)、[v13 生命周期](../outputs/group-subgroup-phase1-20260923/parent-v13-lifecycle-validation.md)及[GitLab 固定源码／测试](../outputs/group-subgroup-phase1-20260923/gitlab-19-4-archive-state-evidence.md)分别对应本地操作和官方语义；运行中任务、待删项目混合状态、其他协议及长任务重启仍未验 |
| 空群组延迟删除与恢复 | v10 真 Owner 给空叶子群组 14 安排 30 天延迟删除后，创建子组 API 409；v11 正常重启后待删路径、期限和恢复表单保留，Owner 从真实 UI 恢复原路径及创建入口。受限共享身份针对群组 18 的旧越权安排删除／恢复在 v11 被拒，真正 Owner 完成恢复 | [v11 生命周期](../outputs/group-subgroup-phase1-20260923/parent-v11-owner-lifecycle-validation.md)为真实 UI／API 子项；未执行物理永久删除，含仓库／包和运行中任务的完整生命周期未验 |
| 治理审计过滤与导出 | v9 Owner 筛选两条 Secret 创建事件并下载 CSV、Developer 拒绝；v10 受 Maintainer 上限共享的原始 Owner 曾读到 15 条合成记录，v11 同身份页面／API 404。v12 导出保留公开 `max_sequence` 并按授权范围返回 375／0；两份真实 API 下载分别含 1／0 条，页面显示正确行数及下载链接并点击第一份。v15 组织 OAuth 修改按 group 作用域记录，子群组与父群组审计页面均显示应用 5、操作者 21 和成功结果；无关用户访问父群组审计为 HTML 404 | [v9 审计](../outputs/group-subgroup-phase1-20260923/parent-v9-audit-validation.md)、[v11 生命周期](../outputs/group-subgroup-phase1-20260923/parent-v11-owner-lifecycle-validation.md)、[v12 整合](../outputs/group-subgroup-phase1-20260923/parent-v12-integrated-validation.md)及[v15 原生 UI](../outputs/group-subgroup-phase1-20260923/parent-v15-native-ui.md)为对应真实 UI／API 子项；v16 已补屏蔽三类事件的父子 UI 审计，浏览器最终导出文件落盘未核对，历史移动范围、全过滤／分页、导出期间撤权及重启恢复仍未验 |
| 全站审计员只读与撤销 | v12 非管理员测试账号经正式 API 获审计员身份，真实浏览器旧成员入口跳至当前协作者页，显示只读提示且无添加／邀请／修改／移除按钮；API 读仓库 200、Issue 创建 403。撤销身份后 API 读写均 404，同一浏览器会话刷新为原生 404 | [v12 整合](../outputs/group-subgroup-phase1-20260923/parent-v12-integrated-validation.md)为真实 UI／API 子项；没有创建 Issue，最终只读库核对账号仍非管理员且审计员身份已撤；其他角色与所有读写入口未遍历 |

## 实际环境与已执行检查

| 检查 | 实际结果 | 证据级别 |
| --- | --- | --- |
| `git status --short`、分支、HEAD | 开工 `main @ 9a3f938`；无跟踪改动，三处原有未跟踪资料保留 | 当前命令结果 |
| `make help` | 成功列出原生开发／测试目标 | 已执行；不是编译或业务通过 |
| Go | `/Users/archer/.cache/gitea-governance/go/bin/go version` 返回 `go1.26.4 darwin/arm64` | 与 `go.mod` 要求一致 |
| 前端与 Runner 依赖 | `package.json` 指定 pnpm 11.9.0、Node ≥22.18；`go.mod` 为 runner 1.0.8、actions-proto-go 0.6.0 | 源码版本，不冒充实际 Runner 客户端 |
| 原有 Docker／Colima | 开工时默认 Docker daemon 不可用，已有 Colima profile 停止 | 未启动或修改旧实例 |
| 隔离环境 | 新建 `gitea-phase1` Colima，2 CPU、4 GiB、Linux arm64，独立 Docker socket；没有挂载用户工作区 | 功能测试环境；不满足目标 amd64 容量配置 |
| PostgreSQL | 独立 `gitea-phase1-pg`，PostgreSQL 17.11，本机 15433；数据库 `gitea_phase1_test` | 已运行固定并发测试，不是容量结论 |
| MinIO | 独立 `gitea-phase1-minio`，本机 19000；只供仓库集成测试 | 测试依赖正常，不代表真实 artifact 客户端通过 |
| Git LFS | 已安装 Git LFS 3.8；未执行全局 `git lfs install` | 隔离仓库本地安装后，HTTP LFS 780000 字节正例及撤权拒绝已实测；v5 部署密钥 LFS 下载、上传与撤销后 batch 401 也已真实验证，见[v5 UI](../outputs/group-subgroup-phase1-20260923/parent-v5-ui-validation.md) |
| 首批 UI 实例 | 本机 loopback 13043，全新 SQLite，最初邮件及 SSH 服务关闭；仅一次性测试身份 | 此行是首批初态，不能代表后续 v3／v4；后续隔离实例启用 SSH 与本地 SMTP |
| 本地构建 | `go build -o outputs/group-subgroup-phase1-20260923/gitea-ui-next .` 退出 0 | macOS arm64 开发构建；非 Linux 候选包，未嵌入静态资源 |
| 最终首批构建 | `go build -o outputs/group-subgroup-phase1-20260923/gitea-phase1-final .` 退出 0；包含迁移 371、群组移动、定时及部署密钥修复 | 已替换本次隔离 UI 实例；用户根目录原有 Linux 二进制未动 |
| 审查后构建 | 在隔离源码副本 `go build -o gitea .` 退出 0，复制为 `outputs/group-subgroup-phase1-20260923/gitea-phase1-stage-a-i01` | 包含来源修订、设置写入重鉴权、合并回调和返原目录修复；真实 UI／Runner 已复验。最后仅更新两条身份说明注释 |
| 第五批运行边界 | 第五批浏览器、真实 Runner、SMTP、Git、制品和 artifact 观察来自 v4；部分安全修复仅在当前源码和自动化中 | [第五批](../outputs/group-subgroup-phase1-20260923/parent-fifth-batch-validation.md)及各专项证据；不可拿 v4 结果给之后的修复背书 |
| v5 整合与真实 UI／协议 | macOS arm64 构建 SHA-256 `92854b4ec77bb5b3c3504f48aea60fffe85f0a7e3ba95b653791b9b0310e7407`；隔离 PG／MinIO 18 项 HTTP 集成、`services/repository` 整包及原生 Team／归档定向退出 0；真实页面和客户端覆盖部署密钥 LFS、Team、叶子恢复 | [v5 整合](../outputs/group-subgroup-phase1-20260923/parent-v5-integrated-validation.md)与[v5 UI](../outputs/group-subgroup-phase1-20260923/parent-v5-ui-validation.md)分别佐证；不代表一期全矩阵或目标容量通过 |
| v6 自退与定向整合 | macOS arm64 构建 SHA-256 `9308ff85f8ad9ff76f9dd0ce30a05a5721e56838bec8562d9565a1c959100c1e`；隔离 PG 自退、Team 邀请归档与 LFS 清理定向通过；真实 Reporter 自退后页面与 HTTP Git 新请求拒绝 | [v6 整合](../outputs/group-subgroup-phase1-20260923/parent-v6-integrated-validation.md)、[v6 UI](../outputs/group-subgroup-phase1-20260923/parent-v6-ui-validation.md)；v7 新错误反馈及组织触发自动取消原因不在此构建内 |
| v7／v8 真实 UI 与 Runner | v7、v8 分别完成隔离 macOS arm64 构建；`services/actions` 70 个顶层测试通过；真实 UI 核对组织触发取消与 Owner 重跑、最后 Owner 错误，v8 核对已撤销治理邀请的 HTML 404 | [v7／v8 记录](../outputs/group-subgroup-phase1-20260923/parent-v7-v8-validation.md)分别给出构建摘要、测试与现场结果；不代替 v9 审计 UI 或全矩阵 |
| v8 受保护 Secret | 真实本机 Runner 执行受保护分支 Run45 允许、未保护分支 Run47 拒绝、fork PR Run49 拒绝的三种凭据探针 | [受保护凭据记录](../outputs/group-subgroup-phase1-20260923/parent-protected-secret-ui.md)；Runner v1.0.8 的 `ref_protected` 表达式不可用，不能把本项当作该字段兼容验收 |
| v9 审计与 Runner 暂停 | macOS arm64 开发构建 SHA-256 `e8d62e8f3f9557ec0f5b9e32a0607e1aa31f056cb025fdab645045075ad8ca34`；隔离 PG 审计／邀请 4 个顶层测试通过；真实 UI／API 审计正反例、CSV 筛选下载，以及父／子 Runner 暂停、Job55 排队与恢复后成功 | [v9 审计](../outputs/group-subgroup-phase1-20260923/parent-v9-audit-validation.md)和[v9 暂停](../outputs/group-subgroup-phase1-20260923/parent-v9-runner-pause.md)；不代表全部审计、Runner 生命周期或目标平台通过 |
| v10 访问申请与别名 | macOS arm64 开发构建 SHA-256 `87f4b7d154356182460c601ffd8c014e2df90e011af64c80252599f26edbb8bd`；SQLite 两项入口／HTTP 定向通过，包耗时 2.187 秒；隔离 PG 申请／审计回滚 3 个顶层测试通过，包耗时 5.73 秒。独立群组 15、仓库 12 完成真实 UI 申请／审批／撤回／拒绝及 HTTP Git 新旧路径正反例 | [v10 申请](../outputs/group-subgroup-phase1-20260923/parent-v10-access-request-validation.md)；当时跨根仓库转移仅有服务红绿回归，邀请路径转移修复尚未真实验收；v11 后续结果另列 |
| v11 Owner 边界与空组恢复 | macOS arm64 开发构建 SHA-256 `f89cd8fcf1a1128c8cc03c1549b22528f1466f5c2f903411a38ca18bdf2180a7`；普通 Owner 空组 14 延迟删除后重启仍待删、真实 UI 恢复；受限共享身份对组 18 的安排删除／恢复和审计读取／导出均拒绝，真 Owner 恢复。多条非 Owner 来源能力并集不得拼成 Owner 的红绿回归通过 | [v11 生命周期](../outputs/group-subgroup-phase1-20260923/parent-v11-owner-lifecycle-validation.md)、[数据库口径](../outputs/group-subgroup-phase1-20260923/owner-identity-db-evidence.md)；两个名称含 `pg` 的服务包日志实际使用 SQLite，不计 PG；真正 `tests/integration` 四个顶层测试 8.405 秒通过，含数据库引擎与库名断言。扩大 SQLite 回归 7 包、263 个顶层测试、含子项 573 个 pass 事件、0 fail |
| v11 仓库邀请与转移 | 仓库 12 的当前协作者页邮箱入口、本地隔离 SMTP 收信、转移后收件人预览新路径并主动拒绝；普通 Owner 向同名末级子群组 16 转移，Owner 新旧 HTTP Git 可读、未接受收件人均拒绝 | [v11 转移邀请](../outputs/group-subgroup-phase1-20260923/parent-v11-transfer-invitation-validation.md)为该次真实 UI／SMTP／HTTP Git 子项；另一次转回后接受见下行，其他角色与协议及并发仍未验 |
| v11 多来源与正式 API 到期 | 开发构建继续为 v11；仓库 12 转回群组 15 后，收件人接受既有 Developer 邀请并真实 push。Owner 移除直接 Developer 仅留下继承 Reporter，再移除群组 Reporter 后完全拒绝；90 秒授权经正式 API 建立，到期及后台清理后页面／Git 拒绝 | [v11 多来源](../outputs/group-subgroup-phase1-20260923/parent-v11-multisource-invitation-validation.md)为 UI／HTTP Git 子项；晚于清理的检查不证明时间边界并发安全，不把 API 秒级设置冒充 UI 操作 |
| v12 审计、导航和诊断 | macOS arm64 开发构建 SHA-256 `e80d141963ce43ff15e9d3e0695eede84062f8a8a803380b025fb4b7069a098f`；模板与选项 bindata 重新生成后部署。审计员只读／撤销、邀请 5 撤销后同邮件 404、范围审计导出 1／0 条、PR5 失败诊断为真实 UI／API 子项 | [v12 整合](../outputs/group-subgroup-phase1-20260923/parent-v12-integrated-validation.md)；真正 PG 集成 6 个顶层测试、含子项 26 个 pass 事件、0 fail，包耗时 92.865 秒，数据库断言为 `postgres / gitea_phase1_test`；不是目标 Linux 构建或全矩阵通过 |
| v13 归档恢复与包 UI 删除 | macOS arm64 开发构建 SHA-256 `4d70a5a01367f3c33e289395416470fd05a7343b6a86e30c949ff80697c56ae0`；带两仓且其中一仓原独立归档的群组归档／恢复、单仓越级解档拒绝与可操作提示、Generic UI 删除为真实页面及协议子项 | [v13 生命周期](../outputs/group-subgroup-phase1-20260923/parent-v13-lifecycle-validation.md)；真正 PG 四个顶层测试、0 fail、包耗时 12.866 秒，不能与 v12 六个顶层历史结果或包客户端 42 条记录相加冒充全矩阵 |
| v15 原生设置与 CI | macOS arm64 开发构建 SHA-256 `cbd72c5fd98cddc82e48c14091b58d8149d24945ada72b33660479051425ac19`；普通 Owner 的同一子群组归档拒写、恢复后 OAuth／屏蔽编辑、父子审计投影及无关用户 404；真实 Runner 的 Run88／89 绑定各自 push 提交，`[skip ci]` 更新 cron 生成 Run90／91、删除 cron 后计划数 0 | [v15 整合](../outputs/group-subgroup-phase1-20260923/parent-v15-integrated-validation.md)、[原生 UI](../outputs/group-subgroup-phase1-20260923/parent-v15-native-ui.md)、[CI UI](../outputs/group-subgroup-phase1-20260923/parent-v15-ci-ui.md)。原生九包 SQLite 117 顶层／289 含子项、CI 五包 207／430 全通过；真正 PG 原生五项通过，CI／原生十二项首轮 11 通过、修正 fixture 后失败项与数据库断言两项单独通过，不能称一次十二项全绿。全仓 lint 退出 2；仅本机开发构建，非目标环境或候选 |

## I-01 固定时序复现与复验

### C01：候选读取后改变仓库归属

子代理先执行以下失败回归；父代理随后独立运行相关包和 PostgreSQL 测试：

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH go test -run '^TestClaimJobRejectsOwnerChangedAfterScan$' ./models/actions/
```

- 修改前退出码 1：候选读取后仓库所有者从 2 改为 5，旧代码仍返回运行中的任务，其保存的所有者为 2。
- 可控顺序不依赖 `sleep` 或随机并发碰撞。
- 该证据证明领取检查缺失；真实转移服务、Secret 响应、PostgreSQL 竞争和真实 Runner 验收分别记录，不能从本项推导为全部已复现。
- 修复后退出 0。PostgreSQL 测试使用 SELECT hook 和通道固定旧候选扫描、归属写入提交、最终领取的顺序；三个 Runner 并发领取也通过。父代理独立结果为 `ok gitea.dev/tests/integration 7.526s`，退出 0。

### C02／C03／C09：凭据、转移与撤权

- `TestPickTaskDoesNotDiscloseNewOwnerSecretsToOldRun`：旧工作流与新归属不一致时不返回任务和新归属凭据，包回归通过。
- `TestPrepareActionsForTransferCancelsQueueAndPreservesHistory`：运行中／取消中任务、仓库专属 Runner、部署密钥阻止转移；等待任务取消，注册令牌失效；后续失败整笔回滚，历史完成任务保留。
- `TestTaskCredentialRejectsRevokedPrivateSourceRead`：私有来源读取权撤销后，作业凭据失效。
- `TestEphemeralRunnerCannotClaimSecondCandidate`：最终领取事务内再次检查单任务限制。
- `TestCreatedGroupWithInactiveUserFlagCanClaimActions`：通过真实群组创建服务建立组织，避免把正常组织未设置用户激活标志误当停用；合法任务正例通过。
- 原生 token、JWT 和 artifact v4 签名入口已接入当前有效性检查。`routers/api/actions` 当前无测试文件；包编译成功不能代替真实上传下载及签名链接撤权验收。

### C13：注册令牌旧权限快照

`TestRegistrationTokenRejectsOwnerChangedAfterAuthorization` 固定“原 Owner 已通过入口权限检查→仓库 owner 改变→调用注册令牌处理器”。旧代码返回 200，期望 403，退出 1；新服务在治理事务内重新授权后返回 403，退出 0。另有实例／个人／组织／仓库角色、轮换、降权管理员、失效代办身份及父组继承 Owner 撤权测试通过。测试和失败记录见[注册令牌证据](../outputs/group-subgroup-phase1-20260923/registration-token-regression.md)。

### C14：组织标签转移不丢历史

真实调用 `AcceptTransferOwnership`，来源组织标签分别只保留历史、同时保留历史与当前关联。旧代码转移成功却删除相关记录，两组断言失败，退出 1。修复后同一事务内拒绝该受限组合，保留归属、转移申请、标签关联和历史；仓库本级标签正例可转移，退出 0。此处为服务层 SQLite 证据，不是 UI 或 PostgreSQL 并发证据。

### C15：全新部署 CLI

在全新 SQLite 目录执行原生 `migrate` 后直接 `admin user create`，旧构建出现“治理写入锁尚未初始化”。修复在正常迁移完成后初始化已有治理锁；另一全新目录中两步都退出 0，没有通过 Web 启动或手工数据库写入补配置。证据见[CLI 初始化记录](../outputs/group-subgroup-phase1-20260923/cli-bootstrap.md)。

### C04／C11／C16：群组移动、定时计划与重跑身份

- 父代理继续追踪同根／跨根移动：群组本身 OwnerID 不变，原继承 Owner 持有的注册令牌不能随之保持有效。修复在移动治理事务内禁用子树令牌、阻止已有执行环境、取消旧队列并标记历史 Run。
- 迁移 371 新增仓库 Actions 单调修订、Run 失效标志及持久定时计划修订。旧 Run 不能因群组移出再移回而恢复；工作流准备与移动交错时，最终短授权事务检查原范围快照，失败整笔回滚。
- 父代理发现首次修复误把系统调度身份 `-2` 当普通用户；`TestScheduledTaskCredentialValid` 先退出 1，修复后通过。系统身份现在必须对应当前有效持久计划，非无条件绕过。
- 移动／转移删除旧计划与 spec。异步检测在末端同一事务比较快照、清理及建立计划，旧检测不能删新计划；在途失效计划跳过，不阻塞其余仓库调度。
- 重跑按本次 Job 对应 Attempt 的触发者重新判断，避免原触发者撤权后误拒绝新的合法触发者，也不能反向沿用原触发者权限放行已撤权的重跑者。
- 子代理 PostgreSQL 固定 SQL hook／通道测试覆盖普通 Run、定时 Run 已写入 Job 但尚未提交时，真实群组移动先完成，再放行旧事务；旧 Run 回滚。父代理最终复验另列，不将子代理结果当作父代理已执行。

详细红绿、同根移动、ABA、迁移和 PostgreSQL 证据见[子代理记录](../outputs/group-subgroup-phase1-20260923/runner-safety-evidence.md)。

### C17：部署密钥旧权限窗口

固定“旧 Owner 入口授权→归属改变→真实创建处理器”的顺序，原代码返回 201，预期 403；修复后返回 403。Web／API 创建和删除使用同一服务，在治理事务内复核当前 Admin，提交后才同步 SSH 文件。服务正反例和三包回归通过，子代理独立审查 PASS；详见[部署密钥证据](../outputs/group-subgroup-phase1-20260923/deploy-key-authorization.md)。这不是 SSH 客户端完整验收。

### C10：产物与作业凭据集成

新增四种失效状态分别验证原生 token 与 JWT：归属改变、Runner 停用、归档、触发人停用。初次发现无效原生 token 返回 500，改为明确的 401。原 fixture 不完整的正例关系由测试准备函数补齐，没有放宽生产鉴权。

原工作区跨仓库测试因已有 Linux 根目录二进制无法在 macOS Git hook 执行而失败。使用工作区外源码副本及本批 macOS 二进制，匹配 `TestActions(Artifact|JobSummary|JobToken|CrossRepoAccess)` 的全部测试退出 0，18.598 秒。详见[集成证据及边界](../outputs/group-subgroup-phase1-20260923/artifact-integration.md)。

### C18／C20：工作流来源与设置写入

来源归档／进入可恢复删除期后，旧消费者仍能创建 Scoped Run 的 PostgreSQL 红例已固定。现在发现、最终 Run 入库、领取、作业凭据和重跑都核对来源状态；来源不可执行不会删除已有 Required 配置。来源注册与 Run 保存单调范围修订，移出再移回后需要重新注册和新触发。失效注册的 UI 只显示 ID、重新确认提示及移除入口，不能继续读取已转走的私有来源详情。

父代理又发现设置页 Add／Required／Remove 只信任入口权限的问题。固定“入口已授权→Owner 被撤销→写入”三子例先退出 1；修复后当前用户、代办身份、来源归属及修订在已有短治理事务内重检，三处理器的旧管理员均返回 403。全局、组织、个人及来源仓库资源占用正反例通过。Git 解析仍在事务外。

本段证明已有同级／实例来源的安全边界，不证明祖先来源或不可绕过的可信合并门禁已交付。

### C19：合并回调与定时通知

扩展 PostgreSQL 回归首先暴露六种 PR 合并后的通知失败：Git 引用已经更新，内部 post-receive 的 `SetMerged` 却被自身尚未释放的治理占用拒绝，Actions／定时通知未继续。父代理核对 HEAD 中同一路径已存在，未声称做过独立 HEAD 运行对照。

修复让内部回调携带持久授权 ID 与仓库 ID，并精确核对 PR、仓库、操作者、单引用及旧／新提交。只允许该合并补写自身状态，占用继续保留到原有 Git 结果核对。固定处理器红例已转绿，错误身份／引用／提交／PR／仓库／非服务合并均不能借此放行。PostgreSQL `TestScheduleUpdate` 六种合并方式及并发通过，见[合并调度证据](../outputs/group-subgroup-phase1-20260923/merge-schedule-integration.md)。

### C21：返回原归属及原名称

浏览器实际转回原子群组时先遭遇“同名仓库”误拒。原因是群组层保留原物理目录，原冲突检查没有排除仓库自己的目录。两个真实服务红例分别覆盖转回原归属、改回原名，均先报 `repository already exists`。最小修复保留真实数据库冲突和他人目录检查，只排除当前仓库自身目录；仓库服务完整包退出 0。最终构建经 UI 成功转回，旧 Run 仍 409，新手动 Run 15／Job 16 由真实 Runner 成功执行。

## 本次实际运行命令

以下省略的数据库口令仅在本机测试进程环境提供，未写入记录。

```sh
PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH \
go test -count=1 -p 1 ./models/actions ./models/secret ./models/perm/access ./models/governance ./services/actions ./services/auth ./services/repository ./routers/api/actions ./routers/api/v1/repo ./routers/web/repo ./routers/web/repo/setting ./routers/api/v1/shared ./routers/web/shared/actions

PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH \
GITEA_TEST_DATABASE=pgsql TEST_PGSQL_HOST=127.0.0.1:15433 \
TEST_PGSQL_DBNAME=gitea_phase1_test TEST_PGSQL_USERNAME=phase1_test \
TEST_PGSQL_SCHEMA=public TEST_MINIO_ENDPOINT=127.0.0.1:19000 \
go test -count=1 -run '^TestCreateTaskForRunner(ConcurrentClaim|RejectsOwnerChangeAfterCandidateScan)$' ./tests/integration

PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH \
go test -count=1 ./cmd ./services/repository ./routers/web/shared/actions ./routers/api/v1/shared
```

以上为早先首批命令，均退出 0。13 个目标包中 `routers/api/actions` 无测试文件，其余通过。原始结果见[父代理回归](../outputs/group-subgroup-phase1-20260923/parent-regression.md)、[父代理 PostgreSQL 复验](../outputs/group-subgroup-phase1-20260923/parent-postgres.md)、[子代理红绿及转移记录](../outputs/group-subgroup-phase1-20260923/runner-safety-evidence.md)。

审查收口后的父代理实际结果：

| 命令范围 | 结果 | 证据 |
| --- | --- | --- |
| `go test -json -count=1 -p 1`：cmd、模型、迁移、Actions、鉴权、部署密钥、治理、仓库、pull、Web/API 与 private 共 20 个目标包 | 退出 0；19 包通过、1 包无测试；有明确条件的其他数据库迁移子例跳过，不算其通过 | [逐包及用例结果](../outputs/group-subgroup-phase1-20260923/parent-final-unit.md) |
| PostgreSQL：领取、转移／移动入库屏障、来源归档屏障、Scoped、定时、并发 dispatch／rerun、复用、job token、跨仓库、artifact v3/v4、摘要，共 46 个顶层测试 | 退出 0，250.849 秒；记录包含 172 个顶层或子例结果，不按重复层级宣称独立覆盖数量 | [合并回归](../outputs/group-subgroup-phase1-20260923/parent-postgres-combined.md) |
| 最后补充设置写入鉴权和资源占用后，重新运行完整 `TestActionsScopedWorkflows` | 退出 0，81.537 秒 | [最终 Scoped 整组](../outputs/group-subgroup-phase1-20260923/parent-scoped-final.md) |

完整命令及环境记录在对应证据中；测试副本位于 `/Users/archer/.cache/gitea-phase1-integration-lco96594`。独立构建避免覆盖工作区原有 Linux 二进制。数据库和真实 Runner 的结果分开，不互相替代。

静态检查分开记录：

- 已执行 `make lint-go`，退出 2，报告 65 项。含既有文件问题和当时本次改动的格式问题；没有全量逐项证明为基线问题，不能声称全仓通过。原始输出保留在 `outputs/group-subgroup-phase1-20260923/lint-go.md`。
- 子代理曾运行 `make fmt` 并带入无关格式变更；父代理逐项检查并反向撤销这 20 处无关文件／代码块，只保留本批功能和必要格式。最终 `GOOS=linux golangci-lint run --new-from-rev=HEAD --build-tags=linux,bindata` 退出 0、`0 issues`；这是相对 HEAD 的增量结果。
- `node tools/lint-templates-svg.ts` 退出 0。缺少 `uv`，改用工作区外隔离 Python 环境的相同 `djlint 1.39.4`；最终检查 `groups.tmpl`、`runner_list.tmpl`、`scoped_workflows.tmpl`、仓库 `options.tmpl` 四个修改模板，退出 0、0 错误。未声称执行完整 `make lint-templates`。
- `git diff --check` 退出 0。未更改 `go.mod`、锁文件、JS/TS 或依赖定义。

## 真实浏览器记录

使用 Codex 浏览器工具逐项操作，本地实例 `http://127.0.0.1:13043`。`phase1-owner` 是普通用户，API 佐证 `is_admin=false`；管理员账号仅用于初始化。

| 操作 | 实际观察 | 边界 |
| --- | --- | --- |
| 登录→群组→创建顶级群组 | 成功创建私有 `phase1-parent`，页面可刷新 | 只覆盖此普通身份正例 |
| 父组→创建子群组 | 成功创建私有 `phase1-parent/child`，可进入其设置 | 尚未覆盖十仓／完整角色矩阵 |
| 子组设置→原生群组设置→Actions→Runners | 页面可达、刷新可用；本级 Runner 已注册并执行 | 不是祖先 Runner 范围验收 |
| 390px × 844px 窄屏 | 先观察到创建 Runner 按钮遮挡标题；改用已有 flex 布局后重新观察，标题与按钮完整显示 | 不用绝对定位；其他语言和宽度未穷举 |
| 退出→无关用户登录→同一子组 Runner URL | 显示 404／无权页面，不返回设置内容 | 保留原生防资源枚举行为 |
| 子组创建私有仓库→UI 新建 YAML→真实 Runner | Run 1／Job 1 成功；本机仓库 API 请求使用作业 token 且 `curl --fail` 成功 | 只使用本级 Runner、一次性数据 |
| UI 重跑及 cron 调度 | Run 1 Attempt 2 成功；Run 3 为真实 `schedule`、系统身份 `gitea-actions`，执行成功 | 后续完整 fork／祖先矩阵未验收 |
| 群组移动受限组合 | 有专属 Runner 时服务端 409，页面明确显示先删除并重新注册的处理路径 | 不是完整移动成功正例 |
| 仓库转到个人→转回原子组 | 两次均通过普通 Owner UI；排队 Run 10–14 取消；旧 Run 1 两次均重跑 409 | 最终构建已修复返原目录误拒；完整来源预览与协议矩阵仍未完成 |
| 转回后从 main 手动触发 | 最终构建 Run 15／Job 16 `Success`，页面 `Triggered via workflow_dispatch`，步骤日志显示“一期真实 Runner 已执行” | 证明新合法任务仍可运行，不代表旧信任恢复 |

截图由浏览器工具在会话中返回，没有另存图片文件；不伪造截图路径。补充 API 正例使用现有 **POST** 注册令牌路由：普通 Owner 200，无关用户 403；初次误用 GET 的 404 另行保留，不算产品失败。刷新及 API 调用后只有一个有效注册令牌记录。原始佐证见[API／数据库记录](../outputs/group-subgroup-phase1-20260923/ui-api-check.md)，未输出令牌值。

## 第十三版补验与第十四版删除安全

- [Web 编辑](../outputs/group-subgroup-phase1-20260923/parent-v13-web-edit-validation.md)：普通 Owner 与继承 Developer 均可保存普通新分支、触发真实祖先 CI；禁推分支被拒且表单保留，Reporter 只能经合法 fork 提议入口，不能直接写原仓。另有 [Developer API／HTTP Git](../outputs/group-subgroup-phase1-20260923/developer-branch-protection-evidence.md) 同对象正反例。
- [真实作业令牌](../outputs/group-subgroup-phase1-20260923/ci06-runner-token-acceptance.md)：同 Owner 私有目标 allow-list Code 读允许，未授权、源／目标上限和 YAML 空声明拒绝，跨仓写拒绝；父任务看到 Run75／84 成功结果页。当前跨仓 Actions 权限及 v3 跨 Run 校验拒绝产物读，官方 v4 上传在本 Runner 组合报不兼容，未形成跨仓产物下载正例。
- [Release／产物删除](../outputs/group-subgroup-phase1-20260923/release-artifact-delete-evidence.md)：v13 正常删除与越权拒绝有实际协议证据，同时归档后删除产物及运行记录的 P1 已真实复现；不能把红例当通过。
- [v14 修复与父任务复验](../outputs/group-subgroup-phase1-20260923/parent-v14-actions-validation.md)：最终事务复核当前权限、生命周期、资源绑定与运行状态。真正 PostgreSQL 9 个顶层、含子项 36 个 pass，0 fail／skip，10.052 秒；先前正常删除 fixture 缺 Actions 单元的失败保留并修正。当前 v14 同对象 Web 单件、Web 整次、REST 整次删除均 423，产物每次重新下载内容一致。新构建 SHA-256 为 `da6bb584f752034da06824f29db71363bb3992bd0351088843a703ad358dd2c6`。当时浏览器确认框阻塞新交互；v15 原生导航的遗留阻塞已解除，但没有新增 artifact 按钮及最终 UI 删除证据，相关子项仍未计通过。
- 连续 push 的旧 P2 首错保留在[原证据](../outputs/group-subgroup-phase1-20260923/push-run-head-coalescing-evidence.md)；v15 已让 push Run 绑定事件 `after`，定时计划另读当前默认分支。确定性异步红绿、真正 PG 持久 Branch 行锁屏障见[v15 整合](../outputs/group-subgroup-phase1-20260923/parent-v15-integrated-validation.md)；Run88／89 的提交与事件 SHA 相同，`[skip ci]` 更新／删除 cron 的 Run90／91 和计划清零见[Runner 实测](../outputs/group-subgroup-phase1-20260923/parent-v15-ci-ui.md)。这些子项不代表全部事件与调度组合。

## 仍未完成的验证与实现

1. v5 已完成所列 PostgreSQL／MinIO、仓库及 Team 定向回归，并真实验证部署密钥 LFS、原生 Team 和空叶子组恢复；v6 真实验证治理 Reporter 本人退出直接来源。v7／v8 验证组织触发自动取消与 Owner 重跑、最后 Owner 可见错误、已撤销治理邀请的 HTML 404；v11 新增空组延迟删除恢复、转移后邀请接受及多来源撤权，v12 新增审计员、范围导出、Generic／OCI 客户端删除及 API／HTTP Git 分支保护子项，v13 新增带仓库后代归档恢复、Generic UI 删除及稳定 HEAD 的 API 触发凭据正例；v14／v15 有原生资料、OAuth／屏蔽入口与归档正反例，v15 有 push 事件提交及 `[skip ci]` 定时计划实测。各版结果只覆盖记录中的操作，不能互相代替。
2. 已接受治理邀请的 Reporter 本人自退后，旧产物 URL 和新 HTTP Git 请求均拒绝；v10 独立群组 Reporter 自退后新旧 HTTP Git 均拒绝。v11 的一个仓库直接 Developer＋群组继承 Reporter 样本，管理者逐条撤权后从可写到只读再到全拒绝；正式 API 90 秒授权到期并经后台清理后拒绝。它们不覆盖 Team／共享／LDAP 等多来源、清理前瞬间或并发。v11 转移后 Developer 邀请接受并真推送，v12 仓库邀请 5 撤销后同邮件 404；邀请到期、并发及原生 Team 旧邀请收件人的真实拒绝仍未验。
3. 受保护 Secret 已有可信受保护分支允许、未保护分支与 fork PR 拒绝的真实 Runner 三例；v9 暂停／恢复后 Job55 再次确认受保护凭据下发。首次 Run40／42 因 Runner v1.0.8 丢弃 `ref_protected` 而失败，`github.ref_protected`／`gitea.ref_protected` 不能作工作流门禁。PR5 的探针 Run49 成功，另一必选工作流 Run48 失败，不能称 fork 全部 CI 通过。v15 已有本级真实 Runner 的 `[skip ci]` 定时更新／删除与持久 Branch PG 屏障，不能据此扩为完整定时身份矩阵。Runner 删除／执行中停用、注册令牌轮换、领取／披露固定交错、跨仓库 job token Git／包、来源停用及移动后重新确认、`pull_request_target`／手动／重跑／撤权等完整身份矩阵仍未验。
4. v11 已覆盖空群组延迟删除跨正常重启的 Owner UI 恢复，以及仓库 12 转入末级同名子群组 16、转回原群组并接受旧邮件后的有限 HTTP Git 正反例。v13 两仓样本确认原独立归档标记在父组统一恢复后不保留；GitLab 19.4 固定源码与测试亦已核实这项语义，但待删项目混合状态没有本地实测。物理永久删除、运行中任务的归档交错、含仓库和包的群组移动、转移的全部来源差异、旧路径跨 SSH／LFS／包及 HTTP Git 的完整角色／并发组合仍未形成完整 UI／客户端矩阵。
5. v12 已实测仓库 6 的 Owner API 文件创建／修改、禁推分支 API／HTTP Git 拒绝、普通分支强推允许、受保护分支强推拒绝；v13 又补 Owner／继承 Developer Web 编辑及 Developer API／HTTP Git 正反例，Reporter 保持只读。全部绕过角色及其他规则仍缺真实操作。父群组审批撤权、自动合并竞态、访问申请到期／多来源／并发、审计历史移动范围／全部过滤分页／导出期间撤权，以及长任务重启恢复仍未验。v10 仅验证内部群组普通 Owner 的申请批准与拒绝子项；v11 固定受限共享审计越权样本，v12 核对 `max_sequence` 授权范围、1／0 条导出和审计员撤权，但仍非完整审计角色／下载矩阵。PR 4 的真实自动合并和 PR5 的真实失败诊断均不能覆盖其他门禁状态。
6. OCI／Generic 已有代表性真实客户端、原生制品浏览及 v12 按版本／tag／digest 删除正反例；v13 补了 Generic 原生 UI 删除，Release 与 artifact 已有真实下载，v5 部署密钥 LFS 下载、上传和撤销也已验。Generic 是否为用户实际常用协议的异步问题尚未获答，不能标为确认。OCI 原生 UI 删除、多 tag 共摘要、物理 blob 回收、其他包格式、artifact 新按钮与最终 UI 删除、归档／撤权并发及签名／预授权 URL 撤权边界仍待验。错误 Basic Web 404 与浏览器下载监听超时不再作为 artifact 产品缺陷。原始群组／成员 Webhook 事件及子树制品聚合分别是[计划 D-24／D-25](group-subgroup-phase1-plan.md)保留的独立差异，不能由现有仓库 Hook 继承或制品导航代替。
7. MySQL／MariaDB／MSSQL 兼容、Linux amd64／8 CPU／16 GiB 目标环境千仓容量、完整灾难恢复、人工活跃时间与重复操作降低 80% 的对照测量均未执行；用户明确目标机器目前未提供。十仓固定功能样本和本机 arm64 结果不能替代这些交付门槛。

## 容量与人工效率

目标环境、数据和任务已在[一期计划](group-subgroup-phase1-plan.md)冻结，但用户明确 Linux amd64／8 CPU／16 GiB 目标性能环境尚未提供。当前未运行千仓容量及人工对照测量，80% 目标无实测结论。v11 新增隔离群组样本 16／17／18 不改变已冻结的十仓效率样本；不得以本机 arm64、十仓、自动化点击时间或估算数据填入通过结果。

## 证据与交付规则

后续原始测试输出只记录一次性测试环境，说明、命令和结论用中文编写，英文文件名。业务 Secret、生产 token、私钥和未脱敏载荷不得写入本报告或原始证据。历史测试资料仅作定位线索；当前未执行的用例始终保留“未验证”。

## 第十六批补验

[v16 整合](../outputs/group-subgroup-phase1-20260923/parent-v16-integrated-validation.md)记录原生屏蔽审计红绿、PG 最终七项全部通过、Actions 全包 71 顶层、四个屏蔽／审计包 55 顶层与最终增量 lint 零问题。真实浏览器完成屏蔽、备注和解除，父子审计均有 638～640 三类事件且详情不含备注；真实 Runner 完成 Run92。定时计划冲突不能吞普通 push，并保留范围变化时不建 Run 的固定反例。本机已切换 v16，构建 SHA-256 为 `85167c3625a6550b2351b6656d3e22f05f7d1885642d994f6b609b62528e04b2`。其余未验条目、容量和效率保持原判定。

仍需开发的 CI 范围：`detectAndHandleScopedWorkflows` 当前显式跳过 `schedule`／`workflow_run`，定时检测仅扫描消费仓库自身。祖先统一来源的定时闭环尚未接通，不能将本级 Run90／91 的定时正例当成祖先统一工作流该触发能力已完成。此项保留在一期 CI05 待开发，不在本轮失败后移至二期。
