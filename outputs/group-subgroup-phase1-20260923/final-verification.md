# 首批审查最终检查

时间：2026-09-23T18:25:13.483034+08:00

- 20 个目标包检查退出 0：19 包通过，1 包无测试，条件性数据库测试跳过另记。
- PostgreSQL 合并回归退出 0，250.849 秒；最终 Scoped 整组退出 0，81.537 秒。
- `GOOS=linux golangci-lint run --new-from-rev=HEAD --build-tags=linux,bindata` 退出 0，0 issues。
- 四个修改模板 djlint 退出 0，0 错误；`node tools/lint-templates-svg.ts` 退出 0。
- `git diff --check` 退出 0。
- 普通 Owner UI 转回原群组成功，旧 Run 重跑 409，新 Run 15／Job 16 真实 Runner 成功。
- 本机开发构建为 Mach-O arm64；原根目录二进制仍是 Linux x86_64 ELF。
- 用户原有未跟踪资料保留；未提交、推送或切换分支。

## 最终跟踪文件改动统计

仅为 diff 统计，不代表每行都已完成产品验收；未跟踪新增文件另见 git status。

```text
3	0	cmd/hook.go
3	1	cmd/migrate.go
5	3	models/actions/run.go
1	1	models/actions/runner_token.go
1	0	models/actions/schedule.go
44	2	models/actions/scoped_workflow.go
62	9	models/actions/scoped_workflow_test.go
244	4	models/actions/task.go
134	0	models/actions/task_test.go
2	0	models/governance/audit.go
1	0	models/migrations/migrations.go
15	0	models/perm/access/actions_repo_permission_test.go
47	0	models/perm/access/repo_permission.go
1	0	models/repo/repo.go
3	0	models/secret/secret.go
2	0	options/locale/locale_en-US.json
2	0	options/locale/locale_zh-CN.json
12	2	routers/api/actions/artifacts.go
3	2	routers/api/actions/artifactsv4.go
2	1	routers/api/v1/repo/action.go
10	3	routers/api/v1/repo/key.go
5	0	routers/api/v1/repo/transfer.go
8	4	routers/api/v1/shared/runners.go
35	1	routers/private/hook_post_receive.go
62	0	routers/private/hook_post_receive_test.go
1	1	routers/web/repo/actions/view.go
3	0	routers/web/repo/repo.go
13	2	routers/web/repo/setting/deploy_key.go
2	0	routers/web/repo/setting/setting.go
14	9	routers/web/shared/actions/runners.go
155	8	routers/web/shared/actions/scoped_workflows.go
110	0	routers/web/shared/actions/scoped_workflows_test.go
1	1	services/actions/auth.go
2	2	services/actions/context.go
24	1	services/actions/context_test.go
55	21	services/actions/notifier_helper.go
33	0	services/actions/rerun.go
75	0	services/actions/rerun_test.go
39	1	services/actions/run.go
8	0	services/actions/schedule_tasks.go
40	0	services/actions/schedule_tasks_test.go
24	10	services/actions/task.go
161	0	services/actions/task_test.go
2	1	services/actions/workflow.go
5	0	services/auth/basic.go
7	3	services/auth/oauth2.go
32	6	services/auth/oauth2_test.go
12	0	services/governance/group.go
157	0	services/governance/group_test.go
95	29	services/repository/transfer.go
176	0	services/repository/transfer_test.go
3	2	templates/governance/groups.tmpl
2	0	templates/repo/settings/options.tmpl
3	3	templates/shared/actions/runner_list.tmpl
13	4	templates/shared/actions/scoped_workflows.tmpl
97	0	tests/integration/actions_concurrent_claim_test.go
22	13	tests/integration/actions_job_token_test.go
46	1	tests/integration/actions_scoped_workflow_test.go
31	0	tests/integration/api_actions_artifact_test.go
```
