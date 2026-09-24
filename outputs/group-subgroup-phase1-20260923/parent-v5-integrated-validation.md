# 父任务第五版整合验证

构建 SHA256：`92854b4ec77bb5b3c3504f48aea60fffe85f0a7e3ba95b653791b9b0310e7407`。
本机 macOS arm64；PostgreSQL 与 MinIO 为隔离 Colima 环境。不是容量目标环境。

## postgres-integrated

退出码：0；墙钟耗时：20.509 秒。
实际命令：`/Users/archer/.cache/gitea-governance/go/bin/go test -json -count=1 -p 1 -tags=bindata sqlite sqlite_unlock_notify -run ^(TestArchivedGroupDeveloperCannotMutateReleaseAPI|TestLFSSettingsDeletePreservesOtherRepositoryReference|TestLFSConcurrentSameObjectStagingIsNotCleanedByRejectedUpload|TestLFSSlowSameOIDUploadDoesNotHoldGovernanceWriteLock|TestLFSDelayedCleanupDoesNotDeleteRestagedContent|TestLFSDelayedCleanupDeletesUnreferencedContentOnce|TestLFSBothRejectedStagesEventuallyCleanOrphan|TestOrgTeamEmailInvite.*|TestOrgTeamInviteRevokedInviterCannotGrantOwner|TestOrgTeamInviteRevokedGovernanceOwnerCannotGrantNativeOwner|TestOrgTeamInviteArchivedGroupRejectsNewGrantButAllowsRevocation|TestWebArtifactDownloadUsesRunIDAndAttemptNumber|TestGovernanceInvitationHTTP|TestGovernanceInvitationWebForm)$ ./tests/integration`

完整原始输出：`/Users/archer/.cache/gitea-phase1-v5-validation/postgres-integrated.jsonl`（测试数据仅限隔离环境）。

| 用例 | 结果 | 秒 |
| --- | --- | --- |
| `TestWebArtifactDownloadUsesRunIDAndAttemptNumber` | 通过 | 0.92 |
| `TestGovernanceInvitationHTTP` | 通过 | 0.57 |
| `TestGovernanceInvitationWebForm` | 通过 | 0.55 |
| `TestArchivedGroupDeveloperCannotMutateReleaseAPI` | 通过 | 0.81 |
| `TestLFSSettingsDeletePreservesOtherRepositoryReference` | 通过 | 0.61 |
| `TestLFSConcurrentSameObjectStagingIsNotCleanedByRejectedUpload` | 通过 | 0.38 |
| `TestLFSSlowSameOIDUploadDoesNotHoldGovernanceWriteLock` | 通过 | 0.62 |
| `TestLFSDelayedCleanupDoesNotDeleteRestagedContent` | 通过 | 0.39 |
| `TestLFSDelayedCleanupDeletesUnreferencedContentOnce` | 通过 | 0.35 |
| `TestLFSBothRejectedStagesEventuallyCleanOrphan` | 通过 | 0.41 |
| `TestOrgTeamInviteRevokedInviterCannotGrantOwner` | 通过 | 0.21 |
| `TestOrgTeamInviteRevokedGovernanceOwnerCannotGrantNativeOwner` | 通过 | 0.53 |
| `TestOrgTeamInviteArchivedGroupRejectsNewGrantButAllowsRevocation` | 通过 | 0.42 |
| `TestOrgTeamEmailInvite` | 通过 | 0.27 |
| `TestOrgTeamEmailInviteRedirectsExistingUser` | 通过 | 0.31 |
| `TestOrgTeamEmailInviteRedirectsNewUser` | 通过 | 0.32 |
| `TestOrgTeamEmailInviteRedirectsNewUserWithActivation` | 通过 | 0.35 |
| `TestOrgTeamEmailInviteRedirectsExistingUserWithLogin` | 通过 | 0.33 |

## repository-all

退出码：0；墙钟耗时：7.913 秒。
实际命令：`/Users/archer/.cache/gitea-governance/go/bin/go test -json -count=1 -p 1 -tags=bindata sqlite sqlite_unlock_notify ./services/repository`

完整原始输出：`/Users/archer/.cache/gitea-phase1-v5-validation/repository-all.jsonl`（测试数据仅限隔离环境）。

| 用例 | 结果 | 秒 |
| --- | --- | --- |
| `TestCheckUnadoptedRepositories_Add` | 通过 | 0 |
| `TestCheckUnadoptedRepositories` | 通过 | 0.04 |
| `TestListUnadoptedRepositories_ListOptions` | 通过 | 0.01 |
| `TestAdoptRepository` | 通过 | 0.05 |
| `TestUploadAvatar` | 通过 | 0.01 |
| `TestUploadBigAvatar` | 通过 | 0 |
| `TestDeleteAvatar` | 通过 | 0 |
| `TestGenerateAvatar` | 通过 | 0 |
| `TestRemoveEmptyGroupGitDirectoryKeepsTransferredRepositories` | 通过 | 0 |
| `TestRemoveEmptyGroupGitDirectoryNeverUsesWorkingDirectory` | 通过 | 0 |
| `TestRepository_AddCollaborator` | 通过 | 0.04 |
| `TestRepository_DeleteCollaboration` | 通过 | 0.01 |
| `TestRepository_DeleteCollaborationRemovesSubscriptionsAndStopwatches` | 通过 | 0.01 |
| `TestRepository_ContributorsGraph` | 通过 | 0.03 |
| `TestCreateRepositoryDirectly` | 通过 | 0.08 |
| `TestRepositoryCreationRecoveryRequiresServiceCompletionMarker` | 通过 | 0.02 |
| `TestCompleteRepositoryCreationIsIdempotent` | 通过 | 0 |
| `TestRepositoryCreationRecoveryCompletesVerifiedStorage` | 通过 | 0.03 |
| `TestRequiredWorkflowDefaultBranchChangesRejected` | 通过 | 2.21 |
| `TestDefaultBranchWaitsForRequiredSourceConfiguration` | 通过 | 0.11 |
| `TestDefaultBranchAllowsOptionalSource` | 通过 | 0.37 |
| `TestForkRepository` | 通过 | 0.01 |
| `TestForkRepositoryCleanup` | 通过 | 0.09 |
| `TestGiteaTemplate` | 通过 | 0 |
| `TestFilePathSanitize` | 通过 | 0 |
| `TestProcessGiteaTemplateFileGenerate` | 通过 | 0 |
| `TestProcessGiteaTemplateFileRead` | 通过 | 0 |
| `TestTransformers` | 通过 | 0 |
| `TestRepositoryDeletionRestoresPathAndState` | 通过 | 0.07 |
| `TestRepositoryDeletionRevokedInitiatorRestores` | 通过 | 0.04 |
| `TestRepositoryDeletionOuterRollback` | 通过 | 0.04 |
| `TestRepositoryDeletionClosesExternalPulls` | 通过 | 0.04 |
| `TestRepositoryUpdatesRespectFinalMergeAuthorization` | 通过 | 0.05 |
| `Test_detectLicense` | 通过 | 0.07 |
| `TestApplyReferenceBusinessOperationRecoversBranchDeletion` | 通过 | 0.05 |
| `TestApplyRegisteredReferenceBusinessOperationRestoresActor` | 通过 | 0.04 |
| `TestTeamRepositoryActorWriteRechecksDelegationAndAdmin` | 通过 | 0.01 |
| `TestTeamRepositoryActorWriteRejectsRevokedOwnerAndMissingTeam` | 通过 | 0 |
| `TestTeamRepositoryArchiveBlocksAddsButAllowsRemovals` | 通过 | 0.01 |
| `TestTeam_AddRepository` | 通过 | 0.03 |
| `TestAttachLinkedTypeAndRepoID` | 通过 | 0 |
| `TestUpdateRepositoryVisibilityChanged` | 通过 | 0 |
| `TestRepository_HasWiki` | 通过 | 0.03 |
| `TestMakeRepoPrivateClearsWatches` | 通过 | 0 |
| `TestUpdateRepositoryClearsWatchesOnVisibilityChange` | 通过 | 0.01 |
| `TestTransferOwnership` | 通过 | 0.02 |
| `TestRepositoryReturnsToItsOwnStoragePath` | 通过 | 0.03 |
| `TestAcceptTransferOwnershipRejectsOldOrganizationLabelLoss` | 通过 | 0.01 |
| `TestAcceptTransferOwnershipKeepsRepositoryLabels` | 通过 | 0.01 |
| `TestTransferKeepsLabelsAvailableFromTargetAncestors` | 通过 | 0.02 |
| `TestPrepareActionsForTransferCancelsQueueAndPreservesHistory` | 通过 | 0.03 |
| `TestPrepareActionsForTransferScopeRevisionSurvivesReturn` | 通过 | 0.01 |
| `TestStartRepositoryTransferSetPermission` | 通过 | 0.02 |
| `TestRepositoryTransferPreviewRejectsChangedTarget` | 通过 | 0.01 |
| `TestRepositoryTransferPreviewRejectsChangedRelevantGovernance` | 通过 | 0.01 |
| `TestRepositoryTransferPreviewRejectsChangedInheritedVariable` | 通过 | 0.01 |
| `TestAcceptRepositoryTransferUsesRecipientAuditActor` | 通过 | 0.01 |
| `TestAcceptRepositoryTransferRejectsRevokedSourceOwner` | 通过 | 0.01 |
| `TestRepositoryTransfer` | 通过 | 0.01 |
| `TestRepositoryTransferRejection` | 通过 | 0.01 |
| `TestTeam_HasRepository` | 通过 | 0.01 |
| `TestTeam_RemoveRepository` | 通过 | 0.03 |
| `TestDeleteOwnerRepositoriesDirectly` | 通过 | 0.12 |
| `TestDeleteRepositoryDirectlyPurgesRepoScopedRows` | 通过 | 0.1 |
| `TestDeleteRepositoryRetainsRedactedWebhookFacts` | 通过 | 0.08 |
| `TestGarbageCollectLFSMetaObjects` | 通过 | 0.07 |
| `TestGarbageCollectLFSMetaObjectsForRepoAutoFix` | 通过 | 0.04 |

## native-team-and-archive

退出码：0；墙钟耗时：15.049 秒。
实际命令：`/Users/archer/.cache/gitea-governance/go/bin/go test -json -count=1 -p 1 -tags=bindata sqlite sqlite_unlock_notify -run ^(TestTeamInvite.*|TestNativeTeamActorWrite.*|TestTeamActionCannotAddAfterOwnerRevocation|TestDeleteTeam|TestAddTeamPost.*|TestArchiveGroup.*|TestRestoreGroup.*)$ ./services/org ./routers/web/org ./routers/web/repo/setting ./services/governance`

完整原始输出：`/Users/archer/.cache/gitea-phase1-v5-validation/native-team-and-archive.jsonl`（测试数据仅限隔离环境）。

| 用例 | 结果 | 秒 |
| --- | --- | --- |
| `TestNativeTeamActorWriteRechecksRevokedOwner` | 通过 | 0.05 |
| `TestNativeTeamActorWriteRejectsDeletedTeam` | 通过 | 0 |
| `TestNativeTeamActorWriteAllowsCurrentOwner` | 通过 | 0.01 |
| `TestTeamInviteRechecksCurrentActorsAndScope` | 通过 | 0.01 |
| `TestTeamInviteRevocationAndAcceptanceAreSerialized` | 通过 | 0 |
| `TestTeamInviteRevokedOwnerCannotCreateOrRemove` | 通过 | 0 |
| `TestTeamInviteConcurrentRedeemAndRevoke` | 通过 | 0.01 |
| `TestTeamInviteLifecycleBlocksNewGrantsButAllowsRevocation` | 通过 | 0.02 |
| `TestTeamInviteAncestorArchiveBlocksChildGrants` | 通过 | 0.02 |
| `TestDeleteTeam` | 通过 | 0.01 |
| `TestTeamActionCannotAddAfterOwnerRevocation` | 通过 | 0.07 |
| `TestAddTeamPost` | 通过 | 0.1 |
| `TestAddTeamPost_NotAllowed` | 通过 | 0.04 |
| `TestAddTeamPost_AddTeamTwice` | 通过 | 0.03 |
| `TestAddTeamPost_NonExistentTeam` | 通过 | 0.03 |
| `TestDeleteTeam` | 通过 | 0.03 |
