# 父代理实际回归结果

时间：2026-09-23T20:23:24.698212+08:00

工作目录：`/Users/archer/.cache/gitea-phase1-integration-lco96594`

命令：

```sh
/Users/archer/.cache/gitea-governance/go/bin/go test -json -tags=bindata sqlite sqlite_unlock_notify -count=1 -run ^TestActionsScopedWorkflows$|^TestAPIRenameBranch$ ./tests/integration
```

退出码：`0`。环境变量中的一次性口令不写入证据。

本记录只证明下列实际运行用例；不代表真实浏览器、协议客户端或容量验收。

| 用例或包 | 结果 | 秒 |
| --- | --- | --- |
| `TestActionsScopedWorkflows/Trigger_and_run_creation` | pass | 9.86 |
| `TestActionsScopedWorkflows/Opt-out` | pass | 7.76 |
| `TestActionsScopedWorkflows/Local_uses_resolves_to_source` | pass | 6.29 |
| `TestActionsScopedWorkflows/Workflow_dispatch` | pass | 3.51 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge/pending_blocks,_success_allows` | pass | 9.96 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge/Actions_disabled_blocks_merge_(no_bypass)` | pass | 3.44 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge/status_check_disabled:_the_scoped_check_still_gates` | pass | 7.57 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge/unprotected_branch_still_needs_the_scoped_run` | pass | 5.34 |
| `TestActionsScopedWorkflows/Required_scoped_check_gates_the_PR_merge` | pass | 28.54 |
| `TestActionsScopedWorkflows/Filtered_required_scoped_check_has_no_trusted_run_and_blocks_merge` | pass | 5.31 |
| `TestActionsScopedWorkflows/fork_pull_request_target_binds_the_head` | pass | 7.65 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns/renders_the_saved_pattern_and_display-name_default` | pass | 0.08 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns/live_pattern_kept_as_history_after_un-require` | pass | 0.05 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns/orphan_config_dropped_when_un-required` | pass | 0.07 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns/warns_when_a_workflow_posts_no_status_checks` | pass | 0.02 |
| `TestActionsScopedWorkflows/Settings_page_required_patterns` | pass | 3.8 |
| `TestActionsScopedWorkflows/Stale_source_registration_requires_reconfirmation` | pass | 2.24 |
| `TestActionsScopedWorkflows/Distinct_sources_same_filename` | pass | 6.68 |
| `TestActionsScopedWorkflows/Detection_cache_invalidates_on_source_push` | pass | 6.3 |
| `TestActionsScopedWorkflows/Deletion_suspends_source_until_purge` | pass | 4.5 |
| `TestActionsScopedWorkflows` | pass | 92.96 |
| `TestAPIRenameBranch/RenameBranchWithEmptyRepo` | pass | 0.03 |
| `TestAPIRenameBranch/RenameBranchWithSameBranchNames` | pass | 0.03 |
| `TestAPIRenameBranch/RenameBranchThatAlreadyExists` | pass | 0.03 |
| `TestAPIRenameBranch/RenameBranchWithNonExistentBranch` | pass | 0.03 |
| `TestAPIRenameBranch/RenameBranchWithNonAdminDoer` | pass | 0.87 |
| `TestAPIRenameBranch/RenameBranchWithGlobedBasedProtectionRulesAndAdminAccess` | pass | 0.58 |
| `TestAPIRenameBranch/RenameBranchToMatchProtectionRulesWithAllowedUser` | pass | 0.62 |
| `TestAPIRenameBranch/RenameBranchToMatchProtectionRulesWithUnauthorizedUser` | pass | 0.57 |
| `TestAPIRenameBranch/RenameBranchNormalScenario` | pass | 0.13 |
| `TestAPIRenameBranch` | pass | 3.5 |
| `gitea.dev/tests/integration` | pass | 98.164 |
