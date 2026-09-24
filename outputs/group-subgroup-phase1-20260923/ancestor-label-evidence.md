# I-06 祖先标签定向证据

日期：2026-09-23。基线为 `main@9a3f938`，工作树中的其他脏改动原样保留。本文件只记录祖先标签包的源码与自动化证据，不代表一期整体验收完成。

## 消费者与实现边界

- 模型统一从仓库当前所属组织调用 `governance.Ancestors`，得到本组至根的真实父链；共享关系、兄弟组和其他根不进入链。缺少治理命名空间的原生旧组织只回退到自身。仓库标签和每个组织来源标签保留各自 ID；同名普通标签不合并。
- 读取接入仓库标签 API 列表与单项、Issue／PR 新建与侧栏、Issue 列表与项目筛选。按名称筛选返回每个实际来源的 ID；按 ID 写入时要求全部 ID 在当前仓库有效范围内。
- 写入接入底层 `NewIssueLabel(s)`、`ReplaceIssueLabels` 和 `NewIssueWithIndex`，在实际事务中重新读取当前归属与标签来源；伪造标签对象或来源变化后拒绝，避免部分写入。API 创建 Issue／PR、追加／替换／删除标签的上层入口同步校验和映射错误。
- 标签 API 增加 `source_type`、`source_id`，继承标签的 URL 指向可按其 ID 读取的当前仓库 API。页面来源路径仅在当前查看者拥有来源组 `ReadGroup` 时投影；管理链接同时要求 `ManageGroup` 和原生组织 Owner，受限来源仅显示稳定 ID。
- 原有斜杠互斥标签跨来源互斥规则保留；同名不同 ID 的独立性不改变互斥语义。组织标签的显式删除沿用原有解除关联及删除标签评论历史的语义，删除确认文案已明确提示对下级仓库的影响。
- 仓库转移和群组移动的来源变化保护由父任务在 `services/repository/transfer.go`、`services/governance/group_move_labels.go` 与 `models/issues/label_scope.go` 实现，此包未改这些文件。

## 自动化结果

- 定向测试覆盖三级父链、同名多来源、兄弟与共享不继承、伪造对象与外部 ID 拒绝、后代 Issue 使用祖先标签、跨来源互斥原语义、API 外部 ID 返回 404、来源路径和管理链接的角色判定。
- 完整相关 Go 包测试通过：`go test ./models/issues/ ./services/issue/ ./routers/api/v1/repo/ ./routers/web/repo/ ./routers/web/org/ ./routers/web/shared/issue/ ./services/convert/ ./modules/structs/`。
- `git diff --check` 通过。定向 `golangci-lint run` 在未由本标签包修改的 `models/issues/review.go:819`、`routers/api/v1/repo/branch.go:9-10`、`routers/web/repo/setting/protected_branch.go:9,16` 报现存的 `wastedassign`、`gci`、`gofumpt`；标签改动文件无报告项。

## 对抗自审

VERDICT: PASS

P0/P1 BLOCKERS：未发现成立项。已反证仓库归属外的标签 ID、伪造对象字段、共享来源误入链、同名覆盖、替换标签请求顺序、子组 Owner 编辑父组来源，以及来源路径向无权查看者披露。仓库转移与群组移动的保护需和父任务代码及 UI 联合复核。

UNVERIFIED RISKS：尚未在隔离运行实例通过鼠标完成父组 Owner 创建标签、子组 Owner 选择但无法编辑、保存回显、刷新和撤权后的实际页面/API 复核；尚未做 PostgreSQL 并发来源变化验证、20 层与目标数据量下 P95、人工时间减少 80% 的实测。标签显式删除的历史清理沿用原行为，已在 UI 提示，但真实用户确认流程未验。

NON-BLOCKING FINDINGS：只持有治理 `ManageGroup` 而不具备原生组织 Owner 的自定义角色不会显示原生标签设置链接；这是当前原生设置路由的实际授权边界。API 名称路径在跨来源同名时仍优先解析仓库本级标签，独立来源可通过稳定 ID 访问。

真实 UI 建议按如下路径验证：父组 Owner 从可导航群组设置创建标签，进入三级后代仓库的标签页和 Issue／PR 新建页，选择父/子/仓库同名不同 ID 标签并刷新；以子组 Owner 验证可用不可编辑父来源，以无关和共享受邀身份验证来源路径隐藏与外部 ID 拒绝；随后撤销父组授权重开页面和 API，最后在转移／移动入口核对带关联和仅历史的 409 保护。
