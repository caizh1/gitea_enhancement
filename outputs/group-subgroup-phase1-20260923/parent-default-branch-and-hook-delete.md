# 父代理实际回归结果

时间：2026-09-23T19:56:00.644130+08:00

工作目录：`/Users/archer/Work/gitea-enhancement`

命令：

```sh
go test -json -tags=sqlite sqlite_unlock_notify -count=1 -run ^(TestRequiredWorkflowDefaultBranchChangesRejected|TestDefaultBranchWaitsForRequiredSourceConfiguration|TestDefaultBranchAllowsOptionalSource|TestDeleteRepositoryRetainsRedactedWebhookFacts|TestDeleteRepositoryDirectlyPurgesRepoScopedRows)$ ./services/repository
```

退出码：`0`。环境变量中的一次性口令不写入证据。

本记录只证明下列实际运行用例；不代表真实浏览器、协议客户端或容量验收。

| 用例或包 | 结果 | 秒 |
| --- | --- | --- |
| `TestRequiredWorkflowDefaultBranchChangesRejected/switch` | pass | 0.05 |
| `TestRequiredWorkflowDefaultBranchChangesRejected/rename` | pass | 0.02 |
| `TestRequiredWorkflowDefaultBranchChangesRejected/stale_settings` | pass | 0.02 |
| `TestRequiredWorkflowDefaultBranchChangesRejected` | pass | 0.08 |
| `TestDefaultBranchWaitsForRequiredSourceConfiguration` | pass | 0.11 |
| `TestDefaultBranchAllowsOptionalSource` | pass | 0.03 |
| `TestDeleteRepositoryDirectlyPurgesRepoScopedRows` | pass | 0.06 |
| `TestDeleteRepositoryRetainsRedactedWebhookFacts` | pass | 0.08 |
| `gitea.dev/services/repository` | pass | 1.764 |
