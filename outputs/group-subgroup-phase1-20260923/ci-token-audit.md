# 祖先作业令牌权限审查与定向修复证据

日期：2026-09-23。范围：当前作业令牌的祖先默认权限、绝对上限、工作流声明、fork／跨仓目标授权；附带检查受保护引用与凭据消费者。只使用本地单元测试，未运行共享 PostgreSQL 测试库、真实 Runner 或协议验收。测试数据均为仓库夹具与合成配置。

## 实际消费者与既有保护

- `services/auth/basic.go` 和 `services/auth/oauth2.go` 把有效作业令牌映射为带任务 ID 的 Actions 用户；两者在认证时重新检查任务凭据。`models/perm/access/repo_permission.go` 的 `GetDoerRepoPermission` 将该身份送入 `GetActionsUserRepoPermission`，供 API、HTTP Git、私有 Git 钩子、LFS 和部分关联仓库包接口调用。
- 仓库权限服务先重检当前任务、Runner、来源读取资格，再读取工作流作业声明。`services/actions/permission_parser.go` 按作业声明优先、工作流声明其次、未声明采用默认值；显式空映射得到全 `none`。fork 与跨仓权限在计算末尾限制为只读，私有目标另需同 Owner 明示许可或目标的协作 Owner 许可，fork 不得借此读取第三方私库。
- `services/actions/task.go` 在组装 Runner 任务前再次检查凭据；`models/secret/secret.go` 校验当前归属、阻断一般 fork PR 的 Secret，并按复用工作流调用链过滤。`pull_request_target` 例外读取基准分支工作流，现有设计允许该事件获得 Secret。

## 已复现问题和修复

固定场景为根组 `org3` → 子组 `org6` → 仓库 `repo1`。根组默认 `restricted`、Code 绝对上限 `read`，子组未设令牌默认值。修复前计算结果为 Code `write`：原实现只读取直属 Owner，且仓库 `OverrideOwnerConfig` 会跳过直属 Owner 的上限。目标仓库的跨仓上限也只在仓库配置与直属 Owner 之间二选一。公开仓库权限服务还会把已经算出的 `none` 抬成 `read`。这些路径都违背已冻结的祖先绝对上限交集语义。

当前变更：`models/actions/token_permissions.go` 使用治理命名空间的真实父子 `Ancestors` 链；最近明确配置的默认模式优先，未配置沿祖先查找，完全未配置保留既有 permissive 默认。仓库覆盖只决定默认模式，仓库、直属 Owner、所有祖先的最大权限始终取交集。仓库目标消费者在跨仓时再应用目标仓库及其祖先上限。共享访问边未进入配置链。`models/perm/access/repo_permission.go` 不再用公开可见性抬升任务令牌的单元权限；公开可见性仍可授权访问该目标，实际权限仍受令牌声明和绝对上限约束。显式空上限拒绝全部，空指针表示未设额外上限，显式 `none` 维持拒绝。

回归测试覆盖祖先未配置继承、子组显式覆盖默认、仓库覆盖受祖先上限约束、工作流声明无法越过上限、显式空 YAML 声明、空上限与未配置区分、fork 与跨仓只读、跨仓目标链上限、直属 Owner 上限及公开仓库不能抬升 `none`。原集成用例中“仓库覆盖可绕过 Owner 上限”的旧期望已同步改为拒绝，但未运行该集成测试。

## 命令与结果

- `/Users/archer/.cache/gitea-governance/go/bin/go test -run '^TestTaskTokenAncestorDefaultAndAbsoluteMaximum$' ./models/actions`：修复前退出 1，预期 Code `read`，实际 `write`；修复后退出 0。
- `/Users/archer/.cache/gitea-governance/go/bin/go test -run '^TestGetActionsUserRepoPermission/PublicRepo_CannotRaiseAbsoluteMaximum$' ./models/perm/access`：修复前退出 1，预期 `none`，实际 `read`；修复后随整组测试退出 0。
- `/Users/archer/.cache/gitea-governance/go/bin/go test -run '^(TestTaskTokenAncestorDefaultAndAbsoluteMaximum|TestGetActionsUserRepoPermission)$' ./models/actions ./models/perm/access`：退出 0。
- `/Users/archer/.cache/gitea-governance/go/bin/go test ./models/actions ./models/perm/access ./services/actions ./services/auth ./routers/private`：五包退出 0。
- `PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH /Users/archer/go/bin/golangci-lint run --new-from-rev=HEAD ./models/actions ./models/perm/access ./services/actions ./services/auth ./routers/private`：退出 0，新增问题 0。
- `/Users/archer/.cache/gitea-governance/go/bin/gofmt -d` 定向检查相关五个文件：无差异；`git diff --check`：退出 0。第一次直接调用 `gofmt` 和 lint 因系统 PATH 不含 Go 失败，改用固定 Go 路径后通过；这两次是工具环境错误，不是源码失败。

## 对抗式审查结论

VERDICT: PASS

P0/P1 BLOCKERS：本次已修复范围内未发现仍成立的 P0/P1。修复前祖先上限绕过属于 P1：根组明确禁止写 Code、子组仓库覆盖 permissive、运行中任务凭据经 API／Git 权限服务取得写权限，可修改本应被根组阻止的仓库内容；对 `OverrideOwnerConfig`、公开仓库抬升和目标仓库路径均作了反证检查并补上定向回归。

UNVERIFIED RISKS：尚未用共享 PostgreSQL、真实 Runner、HTTP／SSH Git、LFS 和跨仓包协议做冻结快照验收；父代理负责统一执行。旧集成用例期望已调整，未在本工作包运行。权限配置设置页仍主要显示直属 Owner 的值，未展示最近继承默认及逐级有效上限；页面展示与实际运行权限可能不同，需在原生 UI 验收中单列。并发配置变动时的事务一致性未作专门压力验证。

NON-BLOCKING FINDINGS：`services/context/package.go` 的包 Owner 权限计算留有 Actions 用户待实现项，组织包通常只按团队资格、个人包按 Owner 身份判定；本次代码没有证明可借此提升私有包写权限，但也不能把仓库关联包权限单测推广成所有包协议已通过。`Secret` 模型目前没有“仅受保护引用可用”的字段，`github.ref_protected` 当前固定为 `false`；已确认一般 fork Secret 隔离及 `pull_request_target` 基准分支例外，独立的受保护引用 Secret 能力尚未实现或验收，不在本次令牌上限修复中扩建。
