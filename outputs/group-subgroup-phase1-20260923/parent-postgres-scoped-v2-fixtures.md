# 父代理实际回归结果

时间：2026-09-23T20:08:50.292477+08:00

工作目录：`/Users/archer/.cache/gitea-phase1-integration-lco96594`

命令：

```sh
go test -json -tags=bindata sqlite sqlite_unlock_notify -count=1 -run ^(TestActionsScopedWorkflows|TestAPIRenameBranch)$ ./tests/integration
```

退出码：`1`。环境变量中的一次性口令不写入证据。

本记录只证明下列实际运行用例；不代表真实浏览器、协议客户端或容量验收。

| 用例或包 | 结果 | 秒 |
| --- | --- | --- |
| `TestActionsScopedWorkflows/Trigger_and_run_creation` | pass | 5.91 |
| `TestActionsScopedWorkflows/Opt-out` | pass | 7.61 |
| `TestActionsScopedWorkflows/Local_uses_resolves_to_source` | pass | 6.22 |
| `TestActionsScopedWorkflows/Workflow_dispatch` | pass | 3.36 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge/pending_blocks,_success_allows` | pass | 8.2 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge/Actions_disabled_blocks_merge_(no_bypass)` | pass | 3.71 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge/status_check_disabled:_the_scoped_check_still_gates` | pass | 5.39 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge/unprotected_branch_still_needs_the_scoped_run` | pass | 5.51 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge` | pass | 24.88 |
| `TestActionsScopedWorkflows/Filtered_required_scoped_check_has_no_trusted_run_and_blocks_merge` | pass | 5.23 |
| `TestActionsScopedWorkflows/fork_pull_request_target_binds_the_head` | pass | 6.13 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns/renders_the_saved_pattern_and_display-name_default` | fail | 0.07 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns/live_pattern_kept_as_history_after_un-require` | pass | 0.05 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns/orphan_config_dropped_when_un-required` | pass | 0.07 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns/warns_when_a_workflow_posts_no_status_checks` | pass | 0.02 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns` | fail | 3.24 |
| `TestActionsScopedWorkflows/Stale_source_registration_requires_reconfirmation` | fail | 2.1 |
| `TestActionsScopedWorkflows/Distinct_sources_same_filename` | pass | 6.63 |
| `TestActionsScopedWorkflows/Detection_cache_invalidates_on_source_push` | pass | 6.18 |
| `TestActionsScopedWorkflows/Deletion_suspends_source_until_purge` | pass | 4.17 |
| `TestActionsScopedWorkflows` | fail | 82.17 |
| `TestAPIRenameBranch/RenameBranchWithEmptyRepo` | pass | 0.03 |
| `TestAPIRenameBranch/RenameBranchWithSameBranchNames` | pass | 0.03 |
| `TestAPIRenameBranch/RenameBranchThatAlreadyExists` | pass | 0.03 |
| `TestAPIRenameBranch/RenameBranchWithNonExistentBranch` | pass | 0.03 |
| `TestAPIRenameBranch/RenameBranchWithNonAdminDoer` | pass | 0.87 |
| `TestAPIRenameBranch/RenameBranchWithGlobedBasedProtectionRulesAndAdminAccess` | pass | 0.57 |
| `TestAPIRenameBranch/RenameBranchToMatchProtectionRulesWithAllowedUser` | pass | 0.63 |
| `TestAPIRenameBranch/RenameBranchToMatchProtectionRulesWithUnauthorizedUser` | pass | 0.58 |
| `TestAPIRenameBranch/RenameBranchNormalScenario` | pass | 0.14 |
| `TestAPIRenameBranch` | pass | 3.52 |
| `gitea.dev/tests/integration` | fail | 87.687 |
