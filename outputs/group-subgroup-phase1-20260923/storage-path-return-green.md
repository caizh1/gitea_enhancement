# 父代理实际回归结果

时间：2026-09-23T18:08:39.621330+08:00

工作目录：`/Users/archer/Work/gitea-enhancement`

命令：

```sh
go test -json -count=1 ./services/repository/
```

退出码：`0`。环境变量中的一次性口令不写入证据。

本记录只证明下列实际运行用例；不代表真实浏览器、协议客户端或容量验收。

| 用例或包 | 结果 | 秒 |
| --- | --- | --- |
| `TestCheckUnadoptedRepositories_Add` | pass | 0 |
| `TestCheckUnadoptedRepositories` | pass | 0.04 |
| `TestListUnadoptedRepositories_ListOptions` | pass | 0.01 |
| `TestAdoptRepository` | pass | 0.05 |
| `TestUploadAvatar` | pass | 0.01 |
| `TestUploadBigAvatar` | pass | 0 |
| `TestDeleteAvatar` | pass | 0 |
| `TestGenerateAvatar` | pass | 0 |
| `TestRemoveEmptyGroupGitDirectoryKeepsTransferredRepositories` | pass | 0 |
| `TestRemoveEmptyGroupGitDirectoryNeverUsesWorkingDirectory` | pass | 0 |
| `TestRepository_AddCollaborator` | pass | 0.04 |
| `TestRepository_DeleteCollaboration` | pass | 0.01 |
| `TestRepository_DeleteCollaborationRemovesSubscriptionsAndStopwatches` | pass | 0.01 |
| `TestRepository_ContributorsGraph` | pass | 0.03 |
| `TestCreateRepositoryDirectly` | pass | 0.07 |
| `TestRepositoryCreationRecoveryRequiresServiceCompletionMarker` | pass | 0.02 |
| `TestCompleteRepositoryCreationIsIdempotent` | pass | 0 |
| `TestRepositoryCreationRecoveryCompletesVerifiedStorage` | pass | 0.03 |
| `TestForkRepository` | pass | 0 |
| `TestForkRepositoryCleanup` | pass | 0.09 |
| `TestGiteaTemplate` | pass | 0 |
| `TestFilePathSanitize` | pass | 0 |
| `TestProcessGiteaTemplateFileGenerate` | pass | 0 |
| `TestProcessGiteaTemplateFileRead` | pass | 0 |
| `TestTransformers` | pass | 0 |
| `TestRepositoryDeletionRestoresPathAndState` | pass | 0.07 |
| `TestRepositoryDeletionRevokedInitiatorRestores` | pass | 0.04 |
| `TestRepositoryDeletionOuterRollback` | pass | 0.04 |
| `TestRepositoryDeletionClosesExternalPulls` | pass | 0.04 |
| `TestRepositoryUpdatesRespectFinalMergeAuthorization` | pass | 0.05 |
| `Test_detectLicense/empty` | pass | 0 |
| `Test_detectLicense/no_detected_license` | pass | 0 |
| `Test_detectLicense/single_license_test:_0BSD` | pass | 0 |
| `Test_detectLicense/single_license_test:_AGPL-3.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_Apache-2.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_BSD-2-Clause` | pass | 0 |
| `Test_detectLicense/single_license_test:_BSD-3-Clause` | pass | 0 |
| `Test_detectLicense/single_license_test:_BSD-3-Clause-Clear` | pass | 0 |
| `Test_detectLicense/single_license_test:_BSL-1.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_CC-BY-4.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_CC-BY-SA-4.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_CC0-1.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_EPL-1.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_EPL-2.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_EUPL-1.2` | pass | 0 |
| `Test_detectLicense/single_license_test:_GPL-2.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_GPL-3.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_ISC` | pass | 0 |
| `Test_detectLicense/single_license_test:_LGPL-2.1` | pass | 0 |
| `Test_detectLicense/single_license_test:_LGPL-3.0` | pass | 0.01 |
| `Test_detectLicense/single_license_test:_MIT` | pass | 0 |
| `Test_detectLicense/single_license_test:_MIT-0` | pass | 0 |
| `Test_detectLicense/single_license_test:_MPL-2.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_MulanPSL-2.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_OFL-1.1` | pass | 0 |
| `Test_detectLicense/single_license_test:_OSL-3.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_UPL-1.0` | pass | 0 |
| `Test_detectLicense/single_license_test:_Unlicense` | pass | 0 |
| `Test_detectLicense/single_license_test:_WTFPL` | pass | 0 |
| `Test_detectLicense/single_license_test:_Zlib` | pass | 0 |
| `Test_detectLicense/multiple_licenses_test` | pass | 0 |
| `Test_detectLicense` | pass | 0.07 |
| `TestApplyReferenceBusinessOperationRecoversBranchDeletion` | pass | 0.04 |
| `TestApplyRegisteredReferenceBusinessOperationRestoresActor` | pass | 0.04 |
| `TestTeam_AddRepository` | pass | 0.03 |
| `TestAttachLinkedTypeAndRepoID/LinkedIssue` | pass | 0 |
| `TestAttachLinkedTypeAndRepoID/LinkedComment` | pass | 0 |
| `TestAttachLinkedTypeAndRepoID/LinkedRelease` | pass | 0 |
| `TestAttachLinkedTypeAndRepoID/Notlinked` | pass | 0 |
| `TestAttachLinkedTypeAndRepoID` | pass | 0 |
| `TestUpdateRepositoryVisibilityChanged` | pass | 0 |
| `TestRepository_HasWiki` | pass | 0.04 |
| `TestMakeRepoPrivateClearsWatches` | pass | 0 |
| `TestUpdateRepositoryClearsWatchesOnVisibilityChange` | pass | 0.01 |
| `TestTransferOwnership` | pass | 0.02 |
| `TestRepositoryReturnsToItsOwnStoragePath/transfer_back` | pass | 0.02 |
| `TestRepositoryReturnsToItsOwnStoragePath/rename_back` | pass | 0.02 |
| `TestRepositoryReturnsToItsOwnStoragePath` | pass | 0.04 |
| `TestAcceptTransferOwnershipRejectsOldOrganizationLabelLoss/association_and_history` | pass | 0.01 |
| `TestAcceptTransferOwnershipRejectsOldOrganizationLabelLoss/history_without_current_association` | pass | 0 |
| `TestAcceptTransferOwnershipRejectsOldOrganizationLabelLoss` | pass | 0.01 |
| `TestAcceptTransferOwnershipKeepsRepositoryLabels` | pass | 0.01 |
| `TestPrepareActionsForTransferCancelsQueueAndPreservesHistory` | pass | 0.03 |
| `TestPrepareActionsForTransferScopeRevisionSurvivesReturn` | pass | 0.01 |
| `TestStartRepositoryTransferSetPermission` | pass | 0.02 |
| `TestRepositoryTransferPreviewRejectsChangedTarget` | pass | 0.01 |
| `TestRepositoryTransferPreviewRejectsChangedRelevantGovernance` | pass | 0.01 |
| `TestAcceptRepositoryTransferUsesRecipientAuditActor` | pass | 0.01 |
| `TestAcceptRepositoryTransferRejectsRevokedSourceOwner` | pass | 0.02 |
| `TestRepositoryTransfer` | pass | 0.01 |
| `TestRepositoryTransferRejection` | pass | 0.01 |
| `TestTeam_HasRepository` | pass | 0.01 |
| `TestTeam_RemoveRepository` | pass | 0.03 |
| `TestDeleteOwnerRepositoriesDirectly` | pass | 0.12 |
| `TestDeleteRepositoryDirectlyPurgesRepoScopedRows` | pass | 0.11 |
| `TestGarbageCollectLFSMetaObjects` | pass | 0.07 |
| `TestGarbageCollectLFSMetaObjectsForRepoAutoFix` | pass | 0.04 |
| `gitea.dev/services/repository` | pass | 2.643 |
