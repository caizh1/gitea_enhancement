// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	asymkey_model "gitea.dev/models/asymkey"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/git"
	"gitea.dev/modules/git/gitcmd"
	"gitea.dev/modules/gitrepo"

	"xorm.io/builder"
)

// PrepareGitReferenceTransaction 在短数据库事务外读取 Git；修订号变化时拒绝过期快照。
func PrepareGitReferenceTransaction(ctx context.Context, operation *governance_model.ReferenceTransaction, gitEnv ...[]string) error {
	if operation == nil || operation.RepoID <= 0 || len(operation.PullChanges) != 0 {
		return governance_model.ErrInvalid
	}
	ctx, err := ReferenceMergeAuthorizationContext(ctx, operation)
	if err != nil {
		return err
	}
	if operation.MergeAuthorizationID != "" {
		operation.Actor = governance_model.AuditActor(ctx)
	}
	changes := make(map[string]governance_model.ReferenceChange, len(operation.Changes))
	for _, change := range operation.Changes {
		changes[change.Ref] = change
	}
	var pulls []*issues_model.PullRequest
	revisions := make(map[int64]int64)
	if err := governance_model.WithStableRead(ctx, func(ctx context.Context) error {
		var err error
		pulls, err = referenceAffectedPulls(ctx, operation.RepoID, changes)
		if err != nil {
			return err
		}
		revisions[operation.RepoID] = 0
		for _, pr := range pulls {
			revisions[pr.BaseRepoID], revisions[pr.HeadRepoID] = 0, 0
		}
		for id := range revisions {
			revisions[id], err = governance_model.ReadReferenceRevision(ctx, id)
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	repositories := make(map[int64]*repo_model.Repository)
	loadRepo := func(id int64) (*repo_model.Repository, error) {
		if repository := repositories[id]; repository != nil {
			return repository, nil
		}
		repository, err := repo_model.GetRepositoryByID(ctx, id)
		if err == nil {
			repositories[id] = repository
		}
		return repository, err
	}
	var dependencies []string
	forcePushes := make(map[string]bool)
	for _, change := range operation.Changes {
		if !strings.HasPrefix(change.Ref, "refs/heads/") || strings.Trim(change.Old, "0") == "" || strings.Trim(change.New, "0") == "" {
			continue
		}
		repository, err := loadRepo(operation.RepoID)
		if err != nil {
			return err
		}
		command := gitcmd.NewCommand("rev-list", "--max-count=1").AddDynamicArguments(change.Old, "^"+change.New)
		if len(gitEnv) > 0 {
			command = command.WithEnv(gitEnv[0])
		}
		output, _, err := gitrepo.RunCmdString(ctx, repository, command)
		if err != nil {
			return fmt.Errorf("无法核对目标分支强推：%w", err)
		}
		forcePushes[change.Ref] = len(output) > 0
	}
	for _, pr := range pulls {
		base, err := loadRepo(pr.BaseRepoID)
		if err != nil {
			return err
		}
		head, err := loadRepo(pr.HeadRepoID)
		if err != nil {
			return err
		}
		read := func(repo *repo_model.Repository, ref string) (string, string, error) {
			if change, exists := changes[ref]; exists && repo.ID == operation.RepoID {
				return change.Old, change.New, nil
			}
			gitRepo, err := gitrepo.OpenRepository(ctx, repo)
			if err != nil {
				return "", "", err
			}
			defer gitRepo.Close()
			value, err := gitRepo.GetRefCommitID(ref)
			if git.IsErrNotExist(err) {
				return "", "", nil
			}
			return value, value, err
		}
		baseBefore, baseAfter, err := read(base, "refs/heads/"+pr.BaseBranch)
		if err != nil {
			return err
		}
		headBefore, headAfter, err := read(head, pullHeadRef(pr))
		if err != nil {
			return err
		}
		version := func(baseID, headID string) (governance_model.PullVersion, error) {
			var patch string
			if strings.Trim(baseID, "0") == "" || strings.Trim(headID, "0") == "" {
				// 删除或尚未创建的分支不能保留旧批准，重新出现时也必须推进版本。
				sum := sha256.Sum256([]byte("审批分支不可用:" + baseID + ":" + headID))
				patch = hex.EncodeToString(sum[:])
				if headID == "" {
					headID = strings.Repeat("0", 40)
				}
			} else {
				var err error
				patch, err = gitrepo.ApprovalPatchID(ctx, base, head, baseID, headID, nil)
				if err != nil {
					return governance_model.PullVersion{}, err
				}
			}
			return governance_model.PullVersion{PullID: pr.ID, Head: headID, BaseBranch: pr.BaseBranch, PatchID: patch}, nil
		}
		before, err := version(baseBefore, headBefore)
		if err != nil {
			return err
		}
		after, err := version(baseAfter, headAfter)
		if err != nil {
			return err
		}
		operation.PullChanges = append(operation.PullChanges, governance_model.ReferencePullChange{Before: before, After: after})
		dependencies = append(dependencies, governance_model.Resource("repository", base.ID), governance_model.Resource("repository", head.ID))
	}
	return governance_model.PrepareReferenceTransaction(ctx, operation, dependencies, func(ctx context.Context) error {
		repository, err := repo_model.GetRepositoryByID(ctx, operation.RepoID)
		if err != nil {
			return err
		}
		if repository.IsArchived {
			return fmt.Errorf("%w：项目已归档，不能准备新的引用写入", governance_model.ErrForbidden)
		}
		if operation.Actor.ID == user_model.ActionsUserID {
			allowed, err := actionsReferenceWriteAllowed(ctx, repository, operation.Actor)
			if err != nil {
				return err
			}
			if !allowed {
				return governance_model.ErrForbidden
			}
		}

		if err := validateReferencePullSnapshot(ctx, operation.RepoID, changes, pulls, revisions); err != nil {
			return err
		}
		for i, pr := range pulls {
			base, err := repo_model.GetRepositoryByID(ctx, pr.BaseRepoID)
			if err != nil {
				return err
			}
			settings, err := governance_model.ResolveApprovalSettings(ctx, base.ID, base.OwnerID)
			if err != nil {
				return err
			}
			operation.PullChanges[i].ResetOnChange = settings.Settings.ResetOnChange
			operation.PullChanges[i].AuditScope, err = pullVersionAuditScope(ctx, base)
			if err != nil {
				return err
			}
		}
		for _, change := range operation.Changes {
			if !strings.HasPrefix(change.Ref, "refs/heads/") {
				continue
			}
			protection, err := git_model.GetFirstMatchProtectedBranchRule(ctx, operation.RepoID, strings.TrimPrefix(change.Ref, "refs/heads/"))
			if err != nil {
				return err
			}
			if protection != nil && protection.RequireGovernanceApproval && operation.MergeAuthorizationID == "" {
				return fmt.Errorf("%w：目标分支只接受通过最终审批授权的合并", governance_model.ErrForbidden)
			}
			if err := checkGroupBranchProtectionAtPrepared(ctx, repository, operation, change, forcePushes[change.Ref]); err != nil {
				return err
			}
		}
		return nil
	})
}

func checkGroupBranchProtectionAtPrepared(ctx context.Context, repository *repo_model.Repository, operation *governance_model.ReferenceTransaction, change governance_model.ReferenceChange, force bool) error {
	protection, err := git_model.EvaluateEffectiveBranchProtection(ctx, repository.ID, strings.TrimPrefix(change.Ref, "refs/heads/"))
	if err != nil || len(protection.Group) == 0 {
		return err
	}
	if strings.Trim(change.New, "0") == "" {
		return fmt.Errorf("%w：群组保护分支不能通过 Git 删除", governance_model.ErrForbidden)
	}
	if operation.MergeAuthorizationID != "" {
		return nil
	}
	if operation.Actor.Kind == "system" {
		if repository.IsMirror && operation.Actor.Transport == "internal_git" && operation.Actor.Name == "镜像后台同步" {
			return nil
		}
		return governance_model.ErrForbidden
	}
	if operation.Actor.ID == user_model.ActionsUserID {
		allowed, err := actionsReferenceWriteAllowed(ctx, repository, operation.Actor)
		if err != nil {
			return err
		}
		if !allowed || protection.Native == nil {
			return governance_model.ErrForbidden
		}
		user := user_model.NewActionsUserWithTaskID(operation.Actor.CredentialID)
		protection.Native.Repo = repository
		if force && !protection.Native.CanUserForcePush(ctx, user) || !force && !protection.Native.CanUserPush(ctx, user) {
			return governance_model.ErrForbidden
		}
		return nil
	}
	if operation.Actor.Kind == "deploy_key" {
		var key asymkey_model.DeployKey
		has, err := db.GetEngine(ctx).Where("repo_id = ? AND key_id = ?", repository.ID, operation.Actor.CredentialID).Get(&key)
		if err != nil {
			return err
		}
		if !has || key.Mode < perm.AccessModeWrite || !protection.CanDeployKeyPush(force) {
			return governance_model.ErrForbidden
		}
		return nil
	}
	if operation.Actor.Kind != "user" {
		return governance_model.ErrForbidden
	}
	user, err := user_model.GetUserByID(ctx, operation.Actor.EffectiveUserID())
	if err != nil || !user.IsActive || user.ProhibitLogin {
		return governance_model.ErrForbidden
	}
	permission, err := access_model.GetDoerRepoPermission(ctx, repository, user)
	if err != nil {
		return err
	}
	if !permission.CanWrite(unit.TypeCode) {
		return governance_model.ErrForbidden
	}
	var allowed bool
	if force {
		allowed, err = protection.AllowsForcePush(ctx, user)
	} else {
		allowed, err = protection.CanUserPush(ctx, user)
	}
	if err != nil {
		return err
	}
	if !allowed {
		return governance_model.ErrForbidden
	}
	return nil
}

func actionsReferenceWriteAllowed(ctx context.Context, repository *repo_model.Repository, actor governance_model.Actor) (bool, error) {
	if actor.Kind != "service_account" || actor.CredentialID <= 0 {
		return false, nil
	}
	user := user_model.NewActionsUserWithTaskID(actor.CredentialID)
	permission, err := access_model.GetActionsUserRepoPermission(ctx, repository, user, actor.CredentialID)
	return err == nil && permission.CanWrite(unit.TypeCode), err
}

// referenceAffectedPulls 在计算差异前和最终事务内使用同一个筛选条件，不能漏掉新建或改目标的 PR。
func referenceAffectedPulls(ctx context.Context, repoID int64, changes map[string]governance_model.ReferenceChange) ([]*issues_model.PullRequest, error) {
	var pulls []*issues_model.PullRequest
	if err := db.GetEngine(ctx).Where("has_merged = ?", false).And(builder.Or(builder.Eq{"head_repo_id": repoID}, builder.Eq{"base_repo_id": repoID})).Find(&pulls); err != nil {
		return nil, err
	}
	result := pulls[:0]
	for _, pr := range pulls {
		_, headChanged := changes[pullHeadRef(pr)]
		_, baseChanged := changes["refs/heads/"+pr.BaseBranch]
		if headChanged && pr.HeadRepoID == repoID || baseChanged && pr.BaseRepoID == repoID {
			result = append(result, pr)
		}
	}
	return result, nil
}

func pullHeadRef(pr *issues_model.PullRequest) string {
	if pr.Flow == issues_model.PullRequestFlowAGit {
		return pr.GetGitHeadRefName()
	}
	return "refs/heads/" + pr.HeadBranch
}

// validateReferencePullSnapshot 只依赖相关引用与 PR 身份，无关治理写入不使快照过期。
func validateReferencePullSnapshot(ctx context.Context, repoID int64, changes map[string]governance_model.ReferenceChange, pulls []*issues_model.PullRequest, revisions map[int64]int64) error {
	for id, revision := range revisions {
		current, err := governance_model.ReadReferenceRevision(ctx, id)
		if err != nil {
			return err
		}
		if current != revision {
			return fmt.Errorf("%w：计算差异期间相关仓库引用已变化", governance_model.ErrConflict)
		}
	}
	current, err := referenceAffectedPulls(ctx, repoID, changes)
	if err != nil {
		return err
	}
	if len(current) != len(pulls) {
		return governance_model.ErrConflict
	}
	byID := make(map[int64]*issues_model.PullRequest, len(pulls))
	for _, pr := range pulls {
		byID[pr.ID] = pr
	}
	for _, pr := range current {
		before := byID[pr.ID]
		if before == nil || before.HeadRepoID != pr.HeadRepoID || before.BaseRepoID != pr.BaseRepoID || before.HeadBranch != pr.HeadBranch || before.BaseBranch != pr.BaseBranch {
			return governance_model.ErrConflict
		}
	}
	return nil
}
