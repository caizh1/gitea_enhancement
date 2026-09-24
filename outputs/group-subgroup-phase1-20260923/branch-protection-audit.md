# 一期群组分支保护审查

记录时间：2026-09-23。目标为顶级群组配置基础保护规则，并实时作用于其当前子树内仓库的 HTTP/SSH Git、网页写入、API 和最终合并。本记录是现状审查与实施方案，不是功能验收。

本文件的 `VERDICT: FAIL` 对应实施前状态；后续代码与定向验证另见 `branch-protection-evidence.md`。最终真实 Git/UI 验收仍由整合方执行。

## 官方边界

- [GitLab 19.4 发布公告](https://about.gitlab.com/press/releases/2026-09-17-gitlab-19-4-brings-new-agentic-automation-at-a-lower-cost/)确认版本已发布；在线文档不是固定版本快照。[保护分支说明](https://docs.gitlab.com/user/project/repository/branches/protected/)和[群组保护分支 API](https://docs.gitlab.com/api/group_protected_branches/)均写明此类规则仅由顶级群组 Owner 配置，作用于组内项目，子群组本身不配置，项目页不能直接改来源规则；官方历史说明 17.6 已正式提供基础功能，19.4 的自定义角色参数正式提供。
- [规则组合说明](https://docs.gitlab.com/user/project/repository/branches/protection_rules/)写明，群组与项目的多个匹配规则对推送、合并、强推等多数设置取最宽松结果；代码所有者批准取最严格结果；精确名称不会天然压倒通配符。群组匹配名称大小写敏感。
- [默认分支说明](https://docs.gitlab.com/user/project/repository/branches/default/)中的“初始默认分支保护”主要影响新建项目，和作用于既有项目的群组保护分支规则是两类配置；不能靠创建仓库时复制一条规则替代实时继承。

## 现有消费者与成立缺口

| 位置 | 现状 | I-04 缺口 |
| --- | --- | --- |
| `models/git/protected_branch.go`、`protected_branch_list.go` | 规则只含 `RepoID`；按本仓库规则的优先级取首个匹配；普通精确名称用不区分大小写比较。 | 无顶级群组来源、无祖先动态范围，且首匹配不是 GitLab 的多规则字段组合。 |
| `routers/private/hook_pre_receive.go` 与 `cmd/hook.go` | Git HTTP/SSH 的引用写入都进入 pre-receive；保护、删除、强推、推送许可读取仓库首匹配规则。 | 群组规则不会约束真实 Git 写入。 |
| `services/governance/reference_approvals.go` 与 `models/governance/reference_transaction.go` | 引用事务 prepared 在治理写锁内复核原生强制审批和当前引用，并保留仓库占用。 | 没有重查基础群组保护；仅在 pre-receive 检查会与规则改动交错。顶级规则写入还须处理子树仓库现存引用／合并占用。 |
| `services/repository/files/update.go`、`services/repository/branch.go`、`services/context/repo.go` | 网页文件写入、分支创建／改名及编辑器能力读取仓库首匹配。 | 页面禁用状态和实际 Web 写入会漏掉群组规则。 |
| `routers/api/v1/repo/branch.go`、`services/convert/convert.go` | 分支 API 的保护状态、创建／改名及本仓库规则 CRUD 只认识本仓库。 | 有效保护结果和来源不可见；不能让仓库管理员编辑或删除群组来源。 |
| `services/pull/merge.go`、`check.go`、`commit_status.go`，以及 PR Web/API/自动合并入口 | 合并资格、状态／审批检查、最终合并读取仓库首匹配；部分最终授权已有治理短事务。 | 群组允许合并角色、当前保护状态及必选检查必须进入最终检查，不能只改页面提示。 |
| `services/governance/approval_candidates.go`、`branch_approvals.go`、`models/issues/review.go`、`services/actions/commit_status.go`、`models/secret/secret.go` | “受保护分支”与审批适用范围、保护 Secret、Actions 状态均引用仓库规则。 | 加入群组规则后这些派生判断也应读统一的有效状态；按原生保护 ID 绑定的本仓库高级审批必须保留独立身份。 |
| 群组设置页与群组 API | 目前没有顶级群组基础保护的配置、回显、来源、修订或删除入口。 | Owner 无法完成一次配置作用于子树既有仓库。 |

## 最小闭环方案与文件所有权建议

1. 增加独立的顶级群组基础规则表，字段仅为群组 ID、大小写敏感的分支名称或通配符、允许推送／合并角色、是否允许强推、修订及审计时间。角色只允许“无人、开发者及以上、维护者及以上”；创建和修改时确认当前根群组与当前 Owner 权限，写入与授权同处治理短事务。群组共享边不得作为配置继承边；仓库实时从当前 `OwnerID` 的 `Ancestors` 定位根。文件建议 `models/git/group_protected_branch.go`、`services/governance/group_branch_protection.go`、对应测试、迁移 `v347`／编号 375（编号由父代理统一注册）。
2. 新增按分支返回全部有效基础来源的只读决策，保留原生高级规则的原优先顺序与稳定 ID。群组基础规则与本级首匹配对推送／合并许可做允许集合并集；有效强推须同时有有效推送许可，且任一匹配规则允许强推；任一规则匹配即禁止 Git 删除。原生签名、状态、审批和受保护文件等高级字段维持原有处理，独立强制治理门禁不得因允许集合并集而失效。群组规则大小写敏感，原生规则现有不区分大小写行为是需标明的兼容差异。若产品承诺严格 GitLab 多规则语义，还须扩为所有本级匹配规则按字段组合；不得把当前首匹配称为完全对齐。
3. 统一有效决策后逐一接入 Git pre-receive、引用事务 prepared、网页文件／分支、分支 API 读写检查、PR Web/API/自动合并的最终授权与派生读路径。顶级群组规则变动须与受影响仓库的持久引用／合并占用排序；仅用进程内锁或只在请求入口校验不足以防止在途写入绕过。移动和转移后从当前祖先链求值，不复制到仓库。公共文件所有权涉及 `models/git/protected_branch_list.go`、`routers/private/hook_pre_receive.go`、`services/governance/reference_approvals.go`、`services/pull/*`、`services/repository/*`、`routers/api/v1/repo/branch.go`，需父代理协调后修改。
4. 在现有顶级群组设置中只增基础模式配置与只读有效来源；子群组及仓库显示继承来源和不可编辑状态。仓库本级高级规则 CRUD 维持原入口，仅允许改本级 ID。群组 Web/API 文件建议 `routers/web/governance/group_branch_protection.go`、`routers/api/v1/org/branch_protection.go`、`templates/governance/group_branch_protection.tmpl` 及窄范围 locale 键；不建设平行策略平台。

## 当前验证与对抗式结论

- 本机运行 `/Users/archer/.cache/gitea-governance/go/bin/go test -run '^(TestBranchRuleMatch|TestBranchRuleMatchPriority|TestBranchRuleSortLegacy|TestBranchRuleSortPriority|TestCanBypassBranchProtection)$' ./models/git/`：退出码 0。
- 本机运行 `/Users/archer/.cache/gitea-governance/go/bin/go test -run '^(TestPreReceiveCanWriteCodePerBranch|TestTrustedMergeAuthorizationRequiresExactServiceMerge)$' ./routers/private/`：退出码 0。这两项仅回归既有仓库规则和引用鉴权，不证明群组保护已实施。

VERDICT: FAIL（针对 I-04 基础群组分支保护可交付性；不是对已交付其他功能的回归判定）。

P0/P1 BLOCKERS: P1，`models/git/protected_branch_list.go` 只查询 `repo_id`，不存在顶级群组规则配置与实时继承；顶级群组 Owner 不能一次设置子树保护。即使仓库管理员逐仓复制，在新仓、转移和规则变更后也不保持当前有效范围，HTTP/SSH pre-receive 与最终合并照旧读取各仓数据，因此当前 I-04 目标不可交付。反证：已检查仓库保护表、原生设置、群组设置与 API、Git/PR 消费者，未发现另一条群组来源路径；现有仓库测试通过只证明本级功能。

UNVERIFIED RISKS: 目标数据库、真实 Git HTTP/SSH 客户端、Web/API、PR 自动合并、组移动/仓转移与多进程并发均未验收；实施时须使用固定根→子→仓库样本和并发屏障验证。

NON-BLOCKING FINDINGS: 当前本级精确名称大小写不敏感、重叠规则按优先级首匹配，与 GitLab 官方组合算法有差异；一期可明确只对群组基础规则实现组合，不能宣称完全对齐。代码所有者批准与完整自定义角色保护不在这次基础字段方案内。
