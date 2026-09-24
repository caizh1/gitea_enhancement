# 可信工作流门禁诊断修正证据

## 问题与改动

最新必选运行的任务等待、失败或取消时，原检查先寻找成功任务的可信 Runner 证明。没有成功任务会得到“Runner 不可信”提示，掩盖了运行本身的状态。现在先核对当前来源、提交、配置版本与最新运行，再确认最新 Attempt 属于该运行；据已确定的运行状态提示等待、失败或取消。必选任务模式仍须全部成功，随后仍须验证任务与 Runner 可信证明；任何新提示都返回原有合并阻断错误。来源失效、来源提交更新和旧配置修订继续由原有分支阻断。

中英文 PR 提示新增等待、失败、取消三种原因，Runner 提示改为缺少可验证的可信完成证明。页面不直接显示无权用户不可读的来源路径。其他语言沿用现有翻译回退。

## 固定复现与回归

- 红例：`GITEA_TEST_DATABASE=sqlite3 go test -tags='bindata sqlite sqlite_unlock_notify' -run '^TestRequiredScopedSourceReferenceSnapshot$' -count=1 ./services/pull/` 退出 1。等待态的最新 Run、Attempt、Job 被错误归为 Runner；原始日志为 `trusted-workflow-diagnostic-red.log`。
- 修复后：同包运行 `TestRequiredScopedSourceReferenceSnapshot`、`TestRequiredScopedWorkflowFinalMergeProof`、`TestScopedRunRunnerProofFollowsReusedTask`，退出 0；原始日志为 `trusted-workflow-diagnostic-green.log`。回归同时核对等待、运行、失败、取消、成功、最新 Attempt 失败、必选任务缺失、成功任务使用不可信 Runner，以及原有旧来源和旧配置阻断。
- PR 文案映射 `TestScopedWorkflowReadinessKey` 退出 0；原始日志为 `trusted-workflow-diagnostic-ui-green.log`。中英文 locale JSON 解析通过。
- 首次增量 lint 因 shell 的 `PATH` 缺少 Go 退出 3，首因保存在 `trusted-workflow-diagnostic-lint.log`；补全 Go 路径后同一范围 `golangci-lint run --new-from-rev=HEAD ./services/pull/... ./routers/web/repo/...` 报 `0 issues`，日志为 `trusted-workflow-diagnostic-lint-corrected.log`。定向 `gofmt -l`、`git diff --check` 均无输出。

## 尚未验证

本轮没有操作真实 PR 页面或共享 PostgreSQL。实际页面文案需要隔离构建重新生成 locale 资源，并在最新真实失败运行上确认；定向测试不替代该验收。

后续父任务已生成 v12 资源并验证同一真实 PR 5：必选 Run 48 失败时显示运行失败提示，合并继续被阻止；最终 PostgreSQL Scoped 整合通过。见[v12 复验](parent-v12-integrated-validation.md)。其他诊断状态仍不计为新的真实 Runner 场景。
