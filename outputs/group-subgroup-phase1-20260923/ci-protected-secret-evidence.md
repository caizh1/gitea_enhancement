# 受保护引用 Secret 一期证据

记录时间：2026-09-23 19:52（北京时间）。本记录仅覆盖当前源码与本机定向验证，不代表真实 Runner、原生页面或目标数据库验收完成。

## 实施边界与消费者

- Secret 新增 `protected` 布尔属性，默认 `false`。网页和三类 Secret API 可设置该属性；API 更新未传入时保持原值。列表只返回名称、描述、时间和保护标记，不返回明文。审计只记保护标记及变更字段，不记 Secret 值。
- 任务在最终领取并组装 Runner 负载时，沿真实命名空间祖先链选取同名 Secret 的最终来源：仓库优先，其次最近群组，再到上级群组。仅对选中项判定保护资格，不合格的近层覆盖不会回退到同名父级值；选中项密文损坏则报错并回滚领取。
- 一期受保护 Secret 只对 `push`、`workflow_dispatch`、`schedule` 的可信受保护分支开放：当前保护规则成立、分支未删除、数据库中的分支 SHA 与运行提交 SHA 相同。fork、所有 PR 与 PR-target、标签一律拒绝该类 Secret。普通 Secret 保留现有 fork/PR-target 边界。标签即使当前有保护规则仍拒绝，因为旧任务缺少可信的标签引用快照，不能凭后来新增的规则获得凭据。这是一期受限语义，不是 GitLab 受保护 MR 凭据的完整对齐。
- `github.ref_protected` 复用相同保守判定，避免上下文显示不受保护却下发凭据。最终凭据组装位于 `CreateTaskForRunnerWithPayload` 的 `governance.WithWrite` 中；保护分支规则修改同用 `WithWrite`，因此当前规则读取与任务下发处于同一写入授权边界。旧队列任务若分支前进或保护规则撤销，将失去受保护 Secret。
- 迁移文件 `models/migrations/v1_27/v346.go` 使用默认 `false` 的列扩展；父代理已在 `models/migrations/migrations.go` 注册 374。本记录只验证迁移单测，未执行目标数据库升级。

## 实际验证

- `/Users/archer/.cache/gitea-governance/go/bin/go test ./models/secret ./models/git ./services/secrets ./services/actions ./services/forms ./routers/web/shared/secrets ./routers/api/v1/repo ./routers/api/v1/org ./routers/api/v1/user ./models/migrations/v1_27`：退出码 0，十个包通过或无测试文件；使用合成 Secret 数据。覆盖同名近层拒绝且不回退、分支提交前进、撤销规则、fork、PR-target、标签、手动与定时触发、密文损坏、API 更新省略保护字段、迁移默认值与幂等。
- `PATH=/Users/archer/.cache/gitea-governance/go/bin:$PATH /Users/archer/go/bin/golangci-lint run --new-from-rev=HEAD ./models/secret ./models/git ./services/secrets ./services/actions ./services/forms ./routers/web/shared/secrets ./routers/api/v1/repo ./routers/api/v1/org ./routers/api/v1/user ./models/migrations/v1_27`：退出码 0，`0 issues.`。
- `git diff --check`：退出码 0。对两份 locale JSON 和两份 Swagger JSON 模板运行 `python3 -m json.tool`：退出码 0。
- 未运行共享 PostgreSQL 测试库，未运行真实 Runner 任务，未执行网页鼠标验收或 API 协议验收；这些由父代理统一集成。

## 对抗式自审

VERDICT: PASS

P0/P1 BLOCKERS: 无。已反证近层受保护覆盖回退、旧队列随保护规则开启取得标签凭据、分支 SHA 前进后仍取得凭据、fork/PR-target 意外获得受保护 Secret、错误密文被父级兜底等候选路径；当前定向测试与最终领取路径均显示拒绝或报错。

UNVERIFIED RISKS: 目标数据库迁移执行、真实 Runner 负载、原生 UI/API 交互、跨数据库并发线性化尚未由本记录验证。分支数据库 SHA 若暂时落后于实际引用，会保守地拒绝受保护 Secret。

NON-BLOCKING FINDINGS: 一期故意不提供受保护标签和受保护 PR/MR 的 Secret；现有网页编辑仍需重新填写 Secret 值，不能单独切换保护标记。
