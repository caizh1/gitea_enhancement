# 第六版自退与规范修正验证

开发构建 SHA256：`9308ff85f8ad9ff76f9dd0ce30a05a5721e56838bec8562d9565a1c959100c1e`。
本次基于已通过第五版整合检查的源码，新增治理直接成员自退；其余变更仅模板关闭标签和测试规范修正。

## v6-self-leave-pg

退出码：0；墙钟耗时：11.973 秒。
实际命令：`/Users/archer/.cache/gitea-governance/go/bin/go test -json -count=1 -p 1 -tags=bindata sqlite sqlite_unlock_notify -run ^(TestGroupMemberLeavesOwnDirectSource|TestOrgTeamInviteArchivedGroupRejectsNewGrantButAllowsRevocation|TestLFSBothRejectedStagesEventuallyCleanOrphan)$ ./tests/integration`

原始记录：`/Users/archer/.cache/gitea-phase1-v5-validation/v6-self-leave-pg.jsonl`。

| 用例 | 结果 |
| --- | --- |
| `TestGroupMemberLeavesOwnDirectSource` | 通过 |
| `TestLFSBothRejectedStagesEventuallyCleanOrphan` | 通过 |
| `TestOrgTeamInviteArchivedGroupRejectsNewGrantButAllowsRevocation` | 通过 |

## v6-self-leave-unit

退出码：0；墙钟耗时：7.019 秒。
实际命令：`/Users/archer/.cache/gitea-governance/go/bin/go test -json -count=1 -p 1 -run ^(TestLeaveGroup.*|Test.*Attachment.*)$ ./services/governance ./services/attachment`

原始记录：`/Users/archer/.cache/gitea-phase1-v5-validation/v6-self-leave-unit.jsonl`。

| 用例 | 结果 |
| --- | --- |
| `TestLeaveGroupOnlyRemovesOwnDirectMembership` | 通过 |
| `TestLeaveGroupRejectsLastPermanentOwnerAndDisabledActor` | 通过 |
| `TestReleaseAttachmentFinalAuthorizationAfterArchive` | 通过 |
| `TestReleaseAttachmentFinalAuthorizationAfterDeactivation` | 通过 |
| `TestUploadAttachment` | 通过 |
