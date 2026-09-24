# 审计员入口与只读提示定向复查

记录日期：2026-09-24。此证据只覆盖 Auditor 定向集成测试；尚未进行 v12 真实浏览器验收。

## 首次失败与结论

宽回归原始日志：`/Users/archer/.cache/gitea-phase1-v5-validation/audit-export-sequence-green-sqlite.jsonl`。`TestGovernanceAuditorPermissions` 原断言在 `tests/integration/governance_auditor_test.go` 第 66 行期望 Issue 创建返回 404，实际返回 403，正文为“当前身份没有此项目的写入授权”。Auditor 可读取 Issue，但该自定义角色只授 Code Read；最终写入检查拒绝创建，403 是有效拒绝。测试仍覆盖独立 Developer 授权时创建成功、降为只读及撤权后写入拒绝。

原测试另在群组旧入口 `/governance/groups/3` 和仓库成员旧入口 `/governance/repositories/2/members` 期望 200，实际均为 303。现行入口分别迁移至 `/org3` 与 `/user2/repo2/collaborators`。修改后的测试核对 `Location` 并请求最终页面，二者均为 200；协作者页无添加协作者、邮箱邀请、保存直接授权和移除直接授权控件，Issue Web/API 写入继续拒绝。未发现此例的实际越权。

协作者当前模板 `templates/repo/settings/collaboration.tmpl` 在审计员且无独立成员管理权时显示“全站审计员只读查看成员来源；编辑成员须另有项目授权。”其他身份的管理控件仍按现有 `CanManage` 显示。

## 定向验证与边界

在源码动态模板模式执行：

```text
GITEA_TEST_DATABASE=sqlite /Users/archer/.cache/gitea-governance/go/bin/go test -tags='sqlite sqlite_unlock_notify' -p 1 -count=1 -run '^TestGovernanceAuditorPermissions$' ./tests/integration
```

结果：通过，包耗时 1.657 秒；目标 Go 文件已格式化，`git diff --check` 通过。带 `bindata` 标签会读取尚未更新的嵌入式模板资产，不能把该旧资产下缺少新提示的结果当成当前源码模板失败；最终资源生成与 v12 真实 UI 由父任务另行验证。本轮未使用 PostgreSQL，也未操作正在运行的浏览器实例。

后续父任务已在 v12 验证审计员真实只读页面、API 读 200／写 403、撤销身份后的同会话页面及 API 404，并通过包含本测试的真正 PostgreSQL 整合。见[v12 复验](parent-v12-integrated-validation.md)。
