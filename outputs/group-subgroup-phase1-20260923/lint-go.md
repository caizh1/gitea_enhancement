# Go 静态检查原始记录

开始时间：2026-09-23T16:22:08.156355+08:00

命令：`make lint-go`（Go 1.26.4）

退出码：2

```text
GO=go GOLANGCI_LINT_PACKAGE=github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2  go run ./tools/lint-go-all.go
lint go header ...
lint for linux ...
cmd/admin_governance.go:106:2: assigned to state, but reassigned without using the value (wastedassign)
	state := ""
	^
models/actions/governance_audit.go:92:6: func changedFields is unused (unused)
func changedFields(before, after map[string]any) []string {
     ^
models/actions/runner.go:466:11: error-format: fmt.Errorf can be replaced with errors.New (perfsprint)
			return fmt.Errorf("runner registration token is no longer active")
			       ^
models/actions/task.go:396:1: File is not properly formatted (gci)
		task, ok, err := claimJobForRunnerWithPayload(ctx, runner, v, build)
^
models/actions/task_test.go:385:1: File is not properly formatted (gofumpt)
	run := &ActionRun{Title: "stale-owner-run", RepoID: 1, OwnerID: 2, WorkflowID: "test.yaml", Index: 9912,
^
models/auth/source.go:138:15: error-format: fmt.Errorf can be replaced with errors.New (perfsprint)
		return nil, fmt.Errorf("认证源不支持事务化注册")
		            ^
models/auth/source.go:294:10: error-format: fmt.Errorf can be replaced with errors.New (perfsprint)
		return fmt.Errorf("认证源注册不能嵌套在外层数据库事务中")
		       ^
models/auth/source.go:391:10: error-format: fmt.Errorf can be replaced with errors.New (perfsprint)
		return fmt.Errorf("认证源注册不能嵌套在外层数据库事务中")
		       ^
models/auth/source_test.go:100:2: error-is-as: use require.ErrorIs (testifylint)
	require.True(t, errors.Is(err, governance_model.ErrConflict))
	^
models/governance/audit.go:24:1: File is not properly formatted (gofumpt)
var auditFailureCount atomic.Int64
^
models/governance/models.go:177:1: File is not properly formatted (gofumpt)
}
^
models/governance/native_approval.go:74:1: File is not properly formatted (gofumpt)
	branch := &NativeBranchApproval{RequiredApprovals: int64(rule.Required), EnableApprovalsWhitelist: !rule.AllEligible,
^
models/governance/reference_transaction.go:24:1: File is not properly formatted (gofumpt)
var referenceBusinessAppliers sync.Map
^
models/governance/reference_transaction_test.go:9:2: import 'encoding/json' is not allowed from list 'main': use gitea's modules/json instead of encoding/json (depguard)
	"encoding/json"
	^
models/governance/repository_creation.go:38:3: mapsloop: Replace m[k]=v loop with maps.Copy (modernize)
		details[key] = value
		^
models/governance/unified_approval_test.go:7:1: File is not properly formatted (gci)
	"gitea.dev/models/db"
^
models/issues/review.go:819:5: assigned to review, but reassigned without using the value (wastedassign)
				review = nil // 人工请求显式升级自动待办，并重新计算原生 official 状态。
				^
models/webhook/governance_audit.go:62:2: redefines-builtin-id: redefinition of the built-in function copy (revive)
	copy := *hook
	^
modules/gitrepo/branch.go:110:80: empty-lines: extra empty line at the end of a block (revive)
func RenameBranch(ctx context.Context, repo Repository, from, to string) error {
	return RenameBranchWithEnv(ctx, repo, from, to, nil)

}
modules/gitrepo/branch.go:112:1: File is not properly formatted (gofumpt)

^
routers/api/v1/governance/audit.go:29:6: exported: type name will be used as governance.GovernanceCapabilities by other packages, and that stutters; consider calling this Capabilities (revive)
type GovernanceCapabilities struct {
     ^
routers/api/v1/repo/branch.go:9:1: File is not properly formatted (gofumpt)
	"errors"
^
routers/api/v1/repo/branch.go:10:1: File is not properly formatted (gci)
	governance_model "gitea.dev/models/governance"
^
routers/private/hook_pre_receive.go:425:114: trustedMergeAuthorizationContext - result 0 (context.Context) is never used (unparam)
func trustedMergeAuthorizationContext(ctx *preReceiveContext, operation *governance_model.ReferenceTransaction) (context.Context, error) {
                                                                                                                 ^
routers/web/governance/approval_policy.go:8:1: File is not properly formatted (gci)
	governance_model "gitea.dev/models/governance"
^
routers/web/governance/approval_policy.go:94:1: File is not properly formatted (gofumpt)
}
^
routers/web/repo/setting/protected_branch.go:9:1: File is not properly formatted (gci)
	governance_model "gitea.dev/models/governance"
^
routers/web/repo/setting/protected_branch.go:16:1: File is not properly formatted (gofumpt)

^
services/actions/run.go:159:3: if-return: redundant if ...; err != nil check, just return error instead. (revive)
		if err := actions_model.AppendRunAudit(ctx, run, "actions.run_triggered"); err != nil {
			return err
		}
services/actions/task_test.go:89:1: File is not properly formatted (gofumpt)
	run := &actions_model.ActionRun{Title: "source-owner-run", RepoID: 1, OwnerID: 2, WorkflowID: "test.yaml", Index: 9913,
^
services/auth/source.go:19:10: error-format: fmt.Errorf can be replaced with errors.New (perfsprint)
		return fmt.Errorf("认证源注销不能嵌套在外层数据库事务中")
		       ^
services/auth/source/oauth2/assert_interface_test.go:21:1: File is not properly formatted (gofumpt)
var _ (sourceInterface) = &oauth2.Source{}
^
services/auth/source/oauth2/source.go:7:1: File is not properly formatted (gci)
	"gitea.dev/models/auth"
^
services/governance/approval_projection.go:102:1: File is not properly formatted (gofumpt)
func preserveHiddenApprovalSubjects(ctx context.Context, actorID int64, repo *repo_model.Repository, next *governance_model.ApprovalRule, previous *governance_model.ApprovalRule) error {
^
services/governance/branch_approvals.go:169:3: QF1003: could use tagged switch on rule.ScopeType (staticcheck)
		if rule.ScopeType == "instance" {
		^
services/governance/branch_approvals.go:210:1: File is not properly formatted (gofumpt)
	return &BranchApprovalConfiguration{Presentation: views, Protections: choices, CanReauthenticate: canReauthenticate, Protection: protection, Version: version, ProtectionID: protectionID, Rules: projected.Rules, Settings: projected.Settings,
^
services/governance/branch_approvals_test.go:14:1: File is not properly formatted (gci)
	"gitea.dev/models/unittest"
^
services/governance/merge_approval.go:206:6: func approvalRulePointers is unused (unused)
func approvalRulePointers(rules []governance_model.ApprovalRule) []*governance_model.ApprovalRule {
     ^
services/governance/navigation.go:10:2: import 'encoding/json' is not allowed from list 'main': use gitea's modules/json instead of encoding/json (depguard)
	"encoding/json"
	^
services/governance/navigation_query.go:284:6: slicesbackward: backward loop over slice can be modernized using slices.Backward (modernize)
	for i := len(chain) - 1; i >= 0; i-- {
	    ^
services/governance/navigation_query.go:340:6: slicesbackward: backward loop over slice can be modernized using slices.Backward (modernize)
	for i := len(chain) - 1; i >= 0; i-- {
	    ^
services/governance/navigation_test.go:6:1: File is not properly formatted (gci)
import (
^
services/mirror/governance_audit.go:261:3: if-return: redundant if ...; err != nil check, just return error instead. (revive)
		if err := markMirrorOperationUnknown(ctx, operation, "repository_missing"); err != nil {
			return err
		}
services/mirror/governance_audit.go:277:4: if-return: redundant if ...; err != nil check, just return error instead. (revive)
			if err := markMirrorOperationUnknown(ctx, operation, "mirror_missing"); err != nil {
				return err
			}
services/mirror/governance_audit.go:372:2: if-return: redundant if ...; err != nil check, just return error instead. (revive)
	if err := markMirrorOperationUnknown(ctx, operation, "unsupported_operation"); err != nil {
		return err
	}
services/mirror/mirror_push.go:70:2: if-return: redundant if ...; err != nil check, just return error instead. (revive)
	if err := completePushMirrorOperation(ctx, operation, mirror, "repository.mirror_configured", map[string]any{"action": "created", "remote_name": mirror.RemoteName, "remote": safeMirrorURL(mirror.RemoteAddress), "interval_seconds": mirror.Interval.Seconds(), "sync_on_commit": mirror.SyncOnCommit}); err != nil {
		return err
	}
services/mirror/mirror_push.go:105:2: if-return: redundant if ...; err != nil check, just return error instead. (revive)
	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", mirror.RepoID)}, func(ctx context.Context) error {
		fresh, has, err := repo_model.GetPushMirrorByIDAndRepoID(ctx, mirror.ID, mirror.RepoID)
		if err != nil || !has || fresh.RemoteName != mirror.RemoteName {
			return governance_model.ErrConflict
		}
		if err := repo_model.DeletePushMirrors(ctx, repo_model.PushMirrorOptions{ID: mirror.ID, RepoID: mirror.RepoID}); err != nil {
			return err
		}
		return completePushMirrorOperation(ctx, operation, mirror, "repository.mirror_updated", map[string]any{"action": "deleted", "remote_name": mirror.RemoteName})
	}); err != nil {
		return err
	}
services/pull/merge.go:641:153: empty-lines: extra empty line at the end of a block (revive)
func isUserAllowedToMergeInRepoBranch(ctx context.Context, repoID int64, branch string, p access_model.Permission, user *user_model.User) (bool, error) {
	if user == nil {
		return false, nil
	}

	pb, err := git_model.GetFirstMatchProtectedBranchRule(ctx, repoID, branch)
	if err != nil {
		return false, err
	}

	nativePermission := p.WithoutGovernance()
	if (nativePermission.CanWrite(unit.TypeCode) && pb == nil) || (pb != nil && git_model.IsUserMergeWhitelisted(ctx, pb, user.ID, nativePermission)) {
		return true, nil
	}
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return false, err
	}
	canMerge, err := access_model.HasGovernanceAbility(ctx, repo, user, governance_model.MergeCode)
	if err != nil || !canMerge {
		return false, err
	}
	if pb == nil || !pb.EnableMergeWhitelist {
		return true, nil
	}
	// 显式原生合并白名单仍约束自定义 MergeCode，且不会把 PushCode 当成 MergeCode。
	return git_model.IsUserMergeWhitelisted(ctx, pb, user.ID, nativePermission), nil

}
services/pull/merge.go:668:1: File is not properly formatted (gofumpt)

^
services/pull/protected_branch.go:7:1: File is not properly formatted (gofumpt)
	"context"
^
services/pull/protected_branch.go:8:1: File is not properly formatted (gci)
	governance_model "gitea.dev/models/governance"
^
services/release/reference_recovery.go:228:2: slicescontains: Loop can be simplified using slices.Contains (modernize)
	for _, value := range values {
	^
services/release/release.go:442:12: empty-lines: extra empty line at the end of a block (revive)
	if delTag {
		protectedTags, err := git_model.GetProtectedTags(ctx, rel.RepoID)
		if err != nil {
			return fmt.Errorf("GetProtectedTags: %w", err)
		}
		isAllowed, err := git_model.IsUserAllowedToControlTag(ctx, protectedTags, rel.TagName, doer.ID)
		if err != nil {
			return err
		}
		if !isAllowed {
			return ErrProtectedTagName{
				TagName: rel.TagName,
			}
		}

		if !setting.IsInTesting {
			if err := gitrepo.InstallReferenceTransactionHook(ctx, repo); err != nil {
				return fmt.Errorf("安装标签引用事务入口: %w", err)
			}
		}
		env := repository.FullPushingEnvironment(doer, doer, repo, repo.Name, 0, 0)
		if !setting.IsInTesting {
			gitRepo, err := gitrepo.OpenRepository(ctx, repo)
			if err != nil {
				return err
			}
			old, err := gitRepo.GetRefCommitID(string(git.RefNameFromTag(rel.TagName)))
			_ = gitRepo.Close()
			if err != nil {
				return err
			}
			tagOld = old
			operationID, err = planReleaseTagDelete(ctx, repo, rel, old)
			if err != nil {
				return err
			}
			finish := governance_model.BeginReferenceBusinessOperation(operationID)
			defer finish()
			defer func() { reconcileReleasePlan(ctx, repo, operationID, rel.TagName) }()
			env = appendReleaseReferenceEnv(env, operationID, "release_tag_delete", rel.TagName)
		}
		if stdout, _, err := gitrepo.RunCmdStringWithEnv(ctx, repo,
			gitcmd.NewCommand("update-ref", "-d").AddDynamicArguments(string(git.RefNameFromTag(rel.TagName)), tagOld),
			env,
		); err != nil && !strings.Contains(err.Error(), "not found") {
			log.Error("DeleteReleaseByID (git tag -d): %d in %v Failed:\nStdout: %s\nError: %v", rel.ID, repo, stdout, err)
			return fmt.Errorf("git tag -d: %w", err)
		}
		if operationID != "" {
			if err := governance_model.CompleteRegisteredReferenceBusinessOperation(ctx, operationID); err != nil {
				return err
			}
			if !rel.IsDraft {
				notify_service.DeleteRelease(ctx, doer, rel)
			}
			return nil
		}

		refName := git.RefNameFromTag(rel.TagName)
		objectFormat := git.ObjectFormatFromName(repo.ObjectFormatName)
		notify_service.PushCommits(
			ctx, doer, repo,
			&repository.PushUpdateOptions{
				RefFullName: refName,
				OldCommitID: rel.Sha1,
				NewCommitID: objectFormat.EmptyObjectID().String(),
			}, repository.NewPushCommits())
		notify_service.DeleteRef(ctx, doer, repo, refName)

	}
services/repository/cleanup_group_test.go:39:21: os.Chdir() could be replaced by t.Chdir() in TestRemoveEmptyGroupGitDirectoryNeverUsesWorkingDirectory (usetesting)
	require.NoError(t, os.Chdir(cwd))
	                   ^
services/repository/cleanup_group_test.go:40:25: os.Chdir() could be replaced by t.Chdir() in TestRemoveEmptyGroupGitDirectoryNeverUsesWorkingDirectory (usetesting)
	t.Cleanup(func() { _ = os.Chdir(previous) })
	                       ^
services/repository/governance_audit.go:23:1: File is not properly formatted (gofumpt)
type repositoryCreationContextKey struct{}
^
services/repository/governance_audit.go:177:3: mapsloop: Replace m[k]=v loop with maps.Copy (modernize)
		details[key] = value
		^
services/repository/transfer.go:143:6: func transferOwnership is unused (unused)
func transferOwnership(ctx context.Context, doer, requestedOwner *user_model.User, repo *repo_model.Repository, teams []*organization.Team) error {
     ^
services/repository/transfer_impact.go:11:2: import 'encoding/json' is not allowed from list 'main': use gitea's modules/json instead of encoding/json (depguard)
	"encoding/json"
	^
services/repository/transfer_impact.go:22:6: exported: type name will be used as repository.RepositoryTransferImpact by other packages, and that stutters; consider calling this TransferImpact (revive)
type RepositoryTransferImpact struct {
     ^
services/repository/transfer_impact.go:154:1: File is not properly formatted (gofumpt)
		return
^
services/repository/transfer_test.go:10:1: File is not properly formatted (gci)
	activities_model "gitea.dev/models/activities"
^
services/repository/transfer_test.go:159:2: equal-values: use assert.Equal (testifylint)
	assert.EqualValues(t, repo.OwnerID, unchanged.OwnerID)
	^
services/repository/transfer_test.go:190:2: equal-values: use assert.Equal (testifylint)
	assert.EqualValues(t, recipient.ID, transferred.OwnerID)
	^
tests/integration/unified_branch_approval_test.go:18:1: File is not properly formatted (gci)
	"gitea.dev/tests"
^
65 issues:
* depguard: 3
* gci: 11
* gofumpt: 17
* modernize: 5
* perfsprint: 5
* revive: 12
* staticcheck: 1
* testifylint: 3
* unparam: 1
* unused: 3
* usetesting: 2
* wastedassign: 2
exit status 1
exit status 1
make: *** [lint-go] Error 1

```
