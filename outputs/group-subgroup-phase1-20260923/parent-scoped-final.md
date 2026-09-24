# 父代理实际回归结果

时间：2026-09-23T18:15:50.454872+08:00

工作目录：`/Users/archer/.cache/gitea-phase1-integration-lco96594`

命令：

```sh
go test -json -count=1 -run ^TestActionsScopedWorkflows$ ./tests/integration/
```

退出码：`0`。环境变量中的一次性口令不写入证据。

本记录只证明下列实际运行用例；不代表真实浏览器、协议客户端或容量验收。

| 用例或包 | 结果 | 秒 |
| --- | --- | --- |
| `TestActionsScopedWorkflows/Trigger_and_run_creation` | pass | 7.54 |
| `TestActionsScopedWorkflows/Opt-out` | pass | 8.21 |
| `TestActionsScopedWorkflows/Local_uses_resolves_to_source` | pass | 7.11 |
| `TestActionsScopedWorkflows/Workflow_dispatch` | pass | 3.63 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge/pending_blocks,_success_allows` | pass | 6.25 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge/Actions_disabled_blocks_merge_(no_bypass)` | pass | 4.02 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge/status_check_disabled:_the_scoped_check_still_gates` | pass | 7.49 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge` | pass | 19.97 |
| `TestActionsScopedWorkflows/Filtered_required_scoped_check_passes_as_skipped_and_allows_merge` | pass | 7.84 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns/renders_the_saved_pattern_and_display-name_default` | pass | 0.04 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns/live_pattern_kept_as_history_after_un-require` | pass | 0.03 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns/orphan_config_dropped_when_un-required` | pass | 0.02 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns/warns_when_a_workflow_posts_no_status_checks` | pass | 0.01 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns` | pass | 3.39 |
| `TestActionsScopedWorkflows/Stale_source_registration_requires_reconfirmation` | pass | 2.29 |
| `TestActionsScopedWorkflows/Distinct_sources_same_filename` | pass | 7.26 |
| `TestActionsScopedWorkflows/Detection_cache_invalidates_on_source_push` | pass | 6.99 |
| `TestActionsScopedWorkflows/Deletion_suspends_source_until_purge` | pass | 4.91 |
| `TestActionsScopedWorkflows` | pass | 79.66 |
| `gitea.dev/tests/integration` | pass | 81.537 |
