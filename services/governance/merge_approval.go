// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/json"
)

// AuthorizePullMerge 仅为已接入两端引用事务的 PR 创建授权；候选合并先计算，锁内不执行 Git。
func AuthorizePullMerge(ctx context.Context, actor governance_model.Actor, pr *issues_model.PullRequest, expectedHead, oldTarget, newTarget string, check func(context.Context, *issues_model.PullRequest) error) (authorization *governance_model.MergeAuthorization, err error) {
	ctx = governance_model.WithAuditActor(ctx, actor)
	defer func() {
		if errors.Is(err, governance_model.ErrForbidden) || errors.Is(err, governance_model.ErrConflict) || errors.Is(err, governance_model.ErrNotFound) {
			if auditErr := recordMergeAuthorizationDenial(ctx, actor, pr, expectedHead, oldTarget, err); auditErr != nil {
				err = errors.Join(err, auditErr)
			}
		}
	}()
	if err := ensurePullReferenceHooks(ctx, actor, pr); err != nil {
		return nil, err
	}
	snapshot, err := CaptureInitialPullApprovalSnapshot(ctx, pr)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, fmt.Errorf("%w：源或目标项目引用事务接入后仍无法建立审批快照", governance_model.ErrConflict)
	}
	if snapshot.Version.Head != expectedHead || snapshot.BaseHead != oldTarget || snapshot.ExpectedBaseBranch != pr.BaseBranch || check == nil {
		return nil, governance_model.ErrConflict
	}
	base, err := repo_model.GetRepositoryByID(ctx, pr.BaseRepoID)
	if err != nil {
		return nil, err
	}
	head, err := repo_model.GetRepositoryByID(ctx, pr.HeadRepoID)
	if err != nil {
		return nil, err
	}
	emails, err := gitrepo.ApprovalCommitterEmails(ctx, base, head, oldTarget, expectedHead)
	if err != nil {
		return nil, err
	}
	operation := &governance_model.MergeAuthorization{PullID: pr.ID, RepoID: base.ID, Head: expectedHead, OldTarget: oldTarget, NewTarget: newTarget, Branch: pr.BaseBranch, Actor: actor}
	err = withActorWrite(ctx, actor, []string{governance_model.Resource("pull", pr.ID), governance_model.Resource("repository", base.ID), governance_model.Resource("repository", head.ID)}, func(ctx context.Context) error {
		completed, err := db.GetEngine(ctx).Where("pull_id = ? AND state = ?", pr.ID, "succeeded").Exist(new(governance_model.MergeAuthorization))
		if err != nil {
			return err
		}
		if completed {
			return fmt.Errorf("%w：此 PR 已有成功引用写入，必须先恢复原生合并结果", governance_model.ErrConflict)
		}
		fresh, err := issues_model.GetPullRequestByID(ctx, pr.ID)
		if err != nil {
			return err
		}
		if fresh.HasMerged || fresh.BaseRepoID != base.ID || fresh.HeadRepoID != head.ID || fresh.BaseBranch != pr.BaseBranch || fresh.HeadBranch != pr.HeadBranch {
			return governance_model.ErrConflict
		}
		if err := fresh.LoadIssue(ctx); err != nil {
			return err
		}
		if fresh.Issue.IsClosed || fresh.IsWorkInProgress(ctx) {
			return governance_model.ErrConflict
		}
		base, err = repo_model.GetRepositoryByID(ctx, pr.BaseRepoID)
		if err != nil {
			return err
		}
		if base.IsArchived {
			return governance_model.ErrConflict
		}
		operation.ObjectPath = base.FullPath()
		if err := ApplyPullApprovalSnapshot(ctx, snapshot); err != nil {
			return err
		}
		version, has, err := db.GetByID[governance_model.PullVersion](ctx, pr.ID)
		if err != nil {
			return err
		}
		if !has {
			return governance_model.ErrConflict
		}
		result := &PullApprovalResult{FullPath: base.FullPath(), EligibleRuleIDs: []int64{}}
		if err := evaluatePullApprovalState(ctx, actor.EffectiveUserID(), fresh, base, *version, emails, result); err != nil {
			return err
		}
		if !result.VersionReady || !result.State.Satisfied {
			return fmt.Errorf("%w：当前审批要求尚未满足", governance_model.ErrForbidden)
		}
		if err := check(ctx, fresh); err != nil {
			return err
		}
		operation.ApprovalProof, err = json.Marshal(result)
		if err != nil {
			return err
		}
		dependencies := []string{governance_model.Resource("repository", head.ID), governance_model.Resource("instance", 0)}
		chain, err := governance_model.Ancestors(ctx, base.OwnerID)
		if err != nil {
			return err
		}
		for _, ancestor := range chain {
			dependencies = append(dependencies, governance_model.Resource("group", ancestor.ID))
			if ancestor.Archived || ancestor.DeleteAfter != 0 {
				return governance_model.ErrConflict
			}
			if ancestor.Kind == "group" {
				operation.AncestorIDs = append(operation.AncestorIDs, ancestor.ID)
			}
		}
		users := map[int64]bool{actor.EffectiveUserID(): true}
		for _, rule := range result.State.Rules {
			for _, id := range rule.ApprovedUserIDs {
				users[id] = true
			}
		}
		for id := range users {
			dependencies = append(dependencies, governance_model.Resource("user", id))
			grants, err := governance_model.RepositoryGrants(ctx, base.ID, base.OwnerID, id, time.Now())
			if err != nil {
				return err
			}
			for _, grant := range grants {
				dependencies = append(dependencies, governance_model.Resource(grant.ScopeType, grant.ScopeID))
				if grant.CustomRoleID != 0 {
					dependencies = append(dependencies, governance_model.Resource("custom_role", grant.CustomRoleID))
				}
				if grant.ShareID != 0 {
					share, has, err := db.GetByID[governance_model.Share](ctx, grant.ShareID)
					if err != nil {
						return err
					}
					if !has {
						return governance_model.ErrConflict
					}
					dependencies = append(dependencies, governance_model.Resource("group", share.GroupID), governance_model.Resource(share.ScopeType, share.ScopeID))
				}
			}
		}
		rules, err := ListPullApprovalRules(ctx, actor.EffectiveUserID(), pr.ID)
		if err != nil {
			return err
		}
		allRules := append([]governance_model.ApprovalRule{}, rules.Version.Rules...)
		for _, rule := range rules.Policies {
			allRules = append(allRules, *rule)
		}
		for _, rule := range allRules {
			for _, id := range rule.GroupIDs {
				dependencies = append(dependencies, governance_model.Resource("group", id))
			}
			for _, id := range rule.TeamIDs {
				dependencies = append(dependencies, governance_model.Resource("team", id))
			}
		}
		return governance_model.AuthorizeMerge(ctx, operation, dependencies, func(context.Context) error { return nil })
	})
	if err != nil {
		return nil, err
	}
	return operation, nil
}

func ensurePullReferenceHooks(ctx context.Context, actor governance_model.Actor, pr *issues_model.PullRequest) error {
	for _, id := range []int64{pr.BaseRepoID, pr.HeadRepoID} {
		repo, err := repo_model.GetRepositoryByID(ctx, id)
		if err != nil {
			return err
		}
		installed, err := gitrepo.ReferenceTransactionHookInstalled(repo)
		if err != nil {
			return err
		}
		if installed {
			continue
		}
		if err := gitrepo.InstallReferenceTransactionHook(ctx, repo); err != nil {
			cause := fmt.Errorf("%w：项目 %s 的引用事务入口无法接入（已有第三方入口时需先完成兼容迁移）：%v", governance_model.ErrConflict, repo.FullPath(), err)
			return errors.Join(cause, recordReferenceHookAudit(ctx, actor, repo.ID, "repository.reference_hook_install_failed", "failure", "merge_auto_install_rejected"))
		}
		installed, err = gitrepo.ReferenceTransactionHookInstalled(repo)
		if err != nil || !installed {
			cause := fmt.Errorf("%w：项目 %s 的引用事务入口安装后回读不一致", governance_model.ErrConflict, repo.FullPath())
			return errors.Join(cause, recordReferenceHookAudit(ctx, actor, repo.ID, "repository.reference_hook_install_failed", "failure", "merge_auto_install_verification_failed"))
		}
		if err := recordReferenceHookAudit(ctx, actor, repo.ID, "repository.reference_hook_installed", "success", "merge_auto_install_verified"); err != nil {
			return err
		}
	}
	return nil
}

func approvalRulePointers(rules []governance_model.ApprovalRule) []*governance_model.ApprovalRule {
	result := make([]*governance_model.ApprovalRule, 0, len(rules))
	for i := range rules {
		result = append(result, &rules[i])
	}
	return result
}

// ReferenceMergeAuthorizationContext 只允许服务端授权对应的单个目标引用及操作者继续。
func ReferenceMergeAuthorizationContext(ctx context.Context, operation *governance_model.ReferenceTransaction) (context.Context, error) {
	if operation.MergeAuthorizationID == "" {
		return ctx, nil
	}
	authorization := new(governance_model.MergeAuthorization)
	has, err := db.GetEngine(ctx).ID(operation.MergeAuthorizationID).Get(authorization)
	if err != nil {
		return ctx, err
	}
	if !has || authorization.State != "authorized" || authorization.RepoID != operation.RepoID || authorization.Actor.EffectiveUserID() != operation.Actor.EffectiveUserID() || len(operation.Changes) != 1 {
		return ctx, governance_model.ErrConflict
	}
	change := operation.Changes[0]
	if change.Ref != "refs/heads/"+authorization.Branch || change.Old != authorization.OldTarget || change.New != authorization.NewTarget {
		return ctx, governance_model.ErrConflict
	}
	return governance_model.WithAuditActor(governance_model.WithMergeAuthorizationContext(ctx, authorization.ID), authorization.Actor), nil
}

// 拒绝事务已经回滚，单独保存拒绝事实；不能写入包含 Git 输出或用户正文的错误字符串。
func recordMergeAuthorizationDenial(ctx context.Context, actor governance_model.Actor, pr *issues_model.PullRequest, head, target string, cause error) error {
	reason := "审批或访问条件不满足"
	if errors.Is(cause, governance_model.ErrConflict) {
		reason = "最终授权依据已变化或存在待核对操作"
	}
	return RecordPullMergeDenial(ctx, actor, pr, head, target, "final_authorization", reason)
}

// RecordPullMergeDenial 在失败事务结束后记录明确原因，禁止传入原始 Git 输出或用户正文。
func RecordPullMergeDenial(ctx context.Context, actor governance_model.Actor, pr *issues_model.PullRequest, head, target, stage, reason string) error {
	return recordPullActionDenial(ctx, "merge.denied", actor, pr, head, target, stage, reason)
}

func RecordPullApprovalDenial(ctx context.Context, actor governance_model.Actor, pr *issues_model.PullRequest, head, reason string) error {
	return recordPullActionDenial(ctx, "approval.denied", actor, pr, head, "", "native_review", reason)
}

func recordPullActionDenial(ctx context.Context, eventType string, actor governance_model.Actor, pr *issues_model.PullRequest, head, target, stage, reason string) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		repo, err := repo_model.GetRepositoryByID(ctx, pr.BaseRepoID)
		if err != nil {
			return err
		}
		scope, err := pullVersionAuditScope(ctx, repo)
		if err != nil {
			return err
		}
		details, err := json.Marshal(map[string]any{"head": head, "target": target, "branch": pr.BaseBranch, "stage": stage, "reason": reason})
		if err != nil {
			return err
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: eventType, Actor: actor, ScopeType: "repository", ScopeID: scope.RepoID, AncestorIDs: scope.AncestorIDs, ObjectType: "pull", ObjectID: pr.ID, ObjectPath: scope.Path, Result: "denied", Details: details})
	})
}
