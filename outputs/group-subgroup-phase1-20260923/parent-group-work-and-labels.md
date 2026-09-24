# 父代理实际回归结果

时间：2026-09-23T19:12:22.689458+08:00

工作目录：`/Users/archer/Work/gitea-enhancement`

命令：

```sh
/Users/archer/.cache/gitea-governance/go/bin/go test -json -count=1 -run ^(TestGroupIssuesFilterScopeBeforeCountsAndPagination|TestGroupMovePreservesAncestorLabelHistory|TestTransferKeepsLabelsAvailableFromTargetAncestors|TestAcceptTransferOwnershipRejectsOldOrganizationLabelLoss|TestAcceptTransferOwnershipKeepsRepositoryLabels)$ ./services/governance ./services/repository
```

退出码：`0`。环境变量中的一次性口令不写入证据。

本记录只证明下列实际运行用例；不代表真实浏览器、协议客户端或容量验收。

| 用例或包 | 结果 | 秒 |
| --- | --- | --- |
| `TestGroupMovePreservesAncestorLabelHistory/current` | pass | 0.09 |
| `TestGroupMovePreservesAncestorLabelHistory/history` | pass | 0.05 |
| `TestGroupMovePreservesAncestorLabelHistory/internal` | pass | 0.05 |
| `TestGroupMovePreservesAncestorLabelHistory` | pass | 0.19 |
| `TestGroupIssuesFilterScopeBeforeCountsAndPagination` | pass | 0.09 |
| `gitea.dev/services/governance` | pass | 1.203 |
| `TestAcceptTransferOwnershipRejectsOldOrganizationLabelLoss/association_and_history` | pass | 0.04 |
| `TestAcceptTransferOwnershipRejectsOldOrganizationLabelLoss/history_without_current_association` | pass | 0 |
| `TestAcceptTransferOwnershipRejectsOldOrganizationLabelLoss` | pass | 0.04 |
| `TestAcceptTransferOwnershipKeepsRepositoryLabels` | pass | 0.01 |
| `TestTransferKeepsLabelsAvailableFromTargetAncestors/current` | pass | 0.01 |
| `TestTransferKeepsLabelsAvailableFromTargetAncestors/history` | pass | 0.01 |
| `TestTransferKeepsLabelsAvailableFromTargetAncestors` | pass | 0.02 |
| `gitea.dev/services/repository` | pass | 1.196 |
