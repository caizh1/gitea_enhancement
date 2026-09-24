# I-01 移动与转移影响预览证据

时间：2026-09-23。此记录只覆盖本工作包的源码与本机定向验证，不代表真实页面或 Git/Runner 链路验收完成。

## 实际路径与行为

- 项目转移沿用 `PreviewRepositoryTransfer` 的稳定读与 `StartRepositoryTransferAfterPreview` 的现有写锁重算。预览增加旧/新祖先 Runner、变量、Secret、Hook、标签、基础分支保护、统一及强制工作流来源的元数据计数，以及活跃任务、专属 Runner、部署密钥、排队任务、注册令牌和定时计划的现状与阻断原因。影响指纹纳入操作者可见的来源变化和执行限制变化。
- 群组移动沿用 `MoveNamespace` 的最终鉴权、修订及写事务。Web 预览表单携带指纹；提交时在写边界重新预览并比较，过期返回冲突并要求重预览。API 调用未携带指纹时保留原兼容路径，仍执行既有最终鉴权和限制。
- 受限来源仅展示来源 ID，不展示路径、配置数量或名称。可读但不可管理的来源仅展示汇总数量；其不可见配置值不会进入预览指纹，避免通过反复预览探测 Secret/Hook 的隐藏变化。Secret 密文、变量值与 Hook 端点不进入响应。
- 提示中的实际处理对应现有 `prepareActionsForTransfer`、`prepareActionsForGroupMove` 和标签来源检查：活跃任务、专属 Runner、部署密钥、无法保留的标签关联阻断操作；变更父级时失效旧运行凭据、取消等待任务、停用注册令牌并清除定时计划。普通改名不触发父级变更处理。
- 顶级群组基础分支保护表单的已有与新增 push/merge 下拉框均已绑定唯一 `id` 与 `label for`，便于键盘及辅助技术定位。

## 已执行验证

- 定向测试：`/Users/archer/.cache/gitea-governance/go/bin/go test -run '^TestLifecycleSourceImpactDoesNotExposeCredentialValues$|^TestPreviewGroupMoveShowsChangedGovernanceScopes$|^TestRepositoryTransferPreviewRejectsChangedInheritedVariable$|^TestRepositoryTransferPreviewRejectsChangedRelevantGovernance$|^TestMoveGroup' ./services/governance/ ./services/repository/ ./routers/web/governance/`。最终复跑通过：治理服务及仓库服务通过；Web 治理包无测试文件。
- 定向 lint：`PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH /Users/archer/go/bin/golangci-lint run --new-from-rev=HEAD ./services/governance ./services/repository ./routers/web/governance`。最终复跑为 `0 issues`。
- `git diff --check`：通过。
- 固定负例：预览后目标祖先增加变量时，仓库转移和群组移动均返回冲突且归属未改变；受限个人来源不返回路径、变量值及数量，隐藏变量值更新不改变公开指纹。

## 尚未验证

- 未运行共享 PostgreSQL 测试库、未重建模板嵌入资源、未在真实 UI 操作确认表单，也未在真实 Runner/Git 链路执行移动与转移；这些由父代理整合验收。
- API 兼容调用不要求预览指纹，仍依赖既有最终权限和事务限制；Web 才要求显式预览指纹。

## 对抗式自审

VERDICT: PASS

P0/P1 BLOCKERS：无。逐项反证了预览表单过期、受限来源名称与配置值泄漏、隐藏值哈希探测、转移限制与提示不一致、旧运行凭据在改变归属后继续有效、普通改名误触发取消，以及 Runner 心跳/Hook 投递/标签使用计数造成不必要的指纹冲突。固定测试覆盖预览后配置改变和受限来源，最终权限与实际阻断仍由既有写事务执行。

UNVERIFIED RISKS：模板尚未重新嵌入与真实页面操作；大型群组树的预览查询开销未在目标数据规模测量；并行改动合并后的 PostgreSQL 回归由父代理统一执行。这些不构成本包已证实的阻断缺陷。

NON-BLOCKING FINDINGS：API 兼容路径继续允许不带预览指纹；其最终鉴权仍生效，但不会提供 Web 的确认前变化提醒。未新增祖先配置的逐项名称列表，当前页面只给来源及数量，符合对受限元数据的保守展示。
