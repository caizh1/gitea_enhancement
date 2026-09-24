// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package private

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	asymkey_model "gitea.dev/models/asymkey"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	perm_model "gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/git"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"
	"gitea.dev/modules/private"
	"gitea.dev/modules/web"
	gitea_context "gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"

	"github.com/google/uuid"
)

func HookReferenceTransaction(ctx *gitea_context.PrivateContext) {
	options := web.GetForm(ctx).(*private.HookOptions)
	if writerID, err := strconv.ParseInt(options.ReferenceWriterID, 10, 64); err != nil || writerID <= 0 {
		ctx.PrivateError(http.StatusBadRequest, governance_model.ErrInvalid, "缺少引用写入进程标识")
		return
	}
	if len(options.RefFullNames) != len(options.OldCommitIDs) || len(options.RefFullNames) != len(options.NewCommitIDs) {
		ctx.PrivateError(http.StatusBadRequest, governance_model.ErrInvalid, "引用事务输入不完整")
		return
	}
	changes := make([]governance_model.ReferenceChange, len(options.RefFullNames))
	for i, ref := range options.RefFullNames {
		changes[i] = governance_model.ReferenceChange{Ref: string(ref), Old: options.OldCommitIDs[i], New: options.NewCommitIDs[i]}
	}
	fingerprint, err := governance_model.ReferenceFingerprint(changes)
	if err != nil {
		ctx.PrivateError(http.StatusBadRequest, err, "引用事务输入无效")
		return
	}
	repo := ctx.Repo.Repository
	referenceRepo := ctx.Repo.GitRepo
	var wikiRepo *git.Repository
	if options.IsWiki {
		wikiRepo, err = gitrepo.OpenRepository(ctx, repo.WikiStorageRepo())
		if err != nil {
			ctx.PrivateError(http.StatusInternalServerError, err, "无法打开 Wiki 引用仓库")
			return
		}
		defer wikiRepo.Close()
		referenceRepo = wikiRepo
	}
	actual := make(map[string]string, len(changes))
	for _, change := range changes {
		value, err := referenceRepo.GetRefCommitID(change.Ref)
		if git.IsErrNotExist(err) {
			value = strings.Repeat("0", len(change.Old))
		} else if err != nil {
			ctx.PrivateError(http.StatusInternalServerError, err, "无法核对真实引用")
			return
		}
		actual[change.Ref] = value
	}
	switch options.ReferenceState {
	case "prepared":
		actor, actorErr := governanceGitActor(ctx, options)
		if actorErr != nil {
			ctx.PrivateError(http.StatusForbidden, actorErr, "无法确认操作者")
			return
		}
		if options.UserID <= 0 && !(options.UserID == user_model.ActionsUserID && options.ActionsTaskID > 0) && options.DeployKeyID <= 0 && !options.IsInternal {
			ctx.PrivateError(http.StatusForbidden, governance_model.ErrNotFound, "缺少受信任的 Gitea 操作者")
			return
		}
		chain, ancestryErr := governance_model.Ancestors(ctx, repo.OwnerID)
		if ancestryErr != nil {
			ctx.PrivateError(http.StatusInternalServerError, ancestryErr, "无法确认治理范围")
			return
		}
		var ancestors []int64
		for _, n := range chain {
			if n.Kind == "group" {
				ancestors = append(ancestors, n.ID)
			}
		}
		for i := range changes {
			if strings.Trim(changes[i].Old, "0") != "" && changes[i].Old != actual[changes[i].Ref] {
				ctx.PrivateError(http.StatusConflict, governance_model.ErrConflict, "引用已变化")
				return
			}
			changes[i].Old = actual[changes[i].Ref]
		}
		if options.IsWiki && options.ContentEvent != "" && options.ContentEvent != "wiki.created" && options.ContentEvent != "wiki.updated" && options.ContentEvent != "wiki.deleted" {
			ctx.PrivateError(http.StatusBadRequest, governance_model.ErrInvalid, "Wiki 引用事务缺少内容事件")
			return
		}
		if options.IsWiki && options.ContentEvent != "" && (!strings.HasPrefix(options.ContentObjectPath, repo.FullPath()+"/wiki/") || len(options.ContentDetails) == 0 || !json.Valid(options.ContentDetails)) {
			ctx.PrivateError(http.StatusBadRequest, governance_model.ErrInvalid, "Wiki 内容审计元数据无效")
			return
		}
		if err := validateReferenceBusinessOperation(options, changes); err != nil {
			ctx.PrivateError(http.StatusBadRequest, err, "引用业务操作元数据无效")
			return
		}
		operation := &governance_model.ReferenceTransaction{RepoID: repo.ID, IsWiki: options.IsWiki, WriterID: options.ReferenceWriterID, InputFingerprint: fingerprint, Changes: changes, Actor: actor, ObjectPath: repo.FullPath(), AncestorIDs: ancestors, ContentEvent: options.ContentEvent, ContentObjectPath: options.ContentObjectPath, ContentDetails: options.ContentDetails, BusinessOperationID: options.ReferenceOperationID, BusinessOperation: options.ReferenceOperation, BusinessOldBranch: options.ReferenceOldBranch, BusinessNewBranch: options.ReferenceNewBranch, BusinessUpdateHEAD: options.ReferenceUpdateHEAD}
		operation.MergeAuthorizationID = options.MergeAuthorizationID
		if options.IsWiki {
			err = governance_model.PrepareReferenceTransaction(ctx, operation, []string{governance_model.Resource("wiki_repository", repo.ID)}, func(ctx context.Context) error {
				fresh, err := repo_model.GetRepositoryByID(ctx, repo.ID)
				if err != nil {
					return err
				}
				if fresh.IsArchived {
					return governance_model.ErrForbidden
				}
				if options.IsInternal && options.ReferenceActor == "mirror" {
					if !fresh.IsMirror {
						return governance_model.ErrForbidden
					}
					return nil
				}
				if options.DeployKeyID > 0 {
					key, err := asymkey_model.GetDeployKeyByID(ctx, options.DeployKeyID)
					if err != nil || key.RepoID != fresh.ID || key.Mode < perm_model.AccessModeWrite {
						return governance_model.ErrForbidden
					}
					return nil
				}
				user, err := user_model.GetUserByID(ctx, options.UserID)
				if err != nil || !user.IsActive || user.ProhibitLogin {
					return governance_model.ErrForbidden
				}
				permission, err := access_model.GetIndividualUserRepoPermission(ctx, fresh, user)
				if err != nil {
					return err
				}
				permission = permission.ForMutation()
				if !permission.CanWrite(unit.TypeWiki) {
					return governance_model.ErrForbidden
				}
				return nil
			})
		} else {
			err = governance_service.PrepareGitReferenceTransaction(ctx, operation, generateGitEnv(options))
		}
	case "committed", "aborted":
		var operation governance_model.ReferenceTransaction
		var found bool
		found, err = db.GetEngine(ctx).Where("repo_id = ? AND is_wiki = ? AND input_fingerprint = ? AND writer_id = ?", repo.ID, options.IsWiki, fingerprint, options.ReferenceWriterID).In("state", []string{"prepared", "unknown"}).Get(&operation)
		if err == nil && !found {
			err = governance_model.ErrNotFound
		}
		if err == nil {
			_, err = governance_model.ReconcileReferenceTransaction(ctx, operation.ID, actual, nil)
		}
	default:
		err = governance_model.ErrInvalid
	}
	if err != nil {
		log.Error("引用事务处理失败: repo=%d state=%s writer=%s error=%v", options.RepositoryID, options.ReferenceState, options.ReferenceWriterID, err)
		if options.ReferenceState == "prepared" && errors.Is(err, governance_model.ErrForbidden) {
			if auditErr := recordGovernanceGitDenial(ctx, options); auditErr != nil {
				err = auditErr
			}
		}
		ctx.PrivateError(http.StatusConflict, err, "引用事务需要核对，不能自动重试写入")
		return
	}
	ctx.PlainText(http.StatusOK, "ok")
}

func validateReferenceBusinessOperation(options *private.HookOptions, changes []governance_model.ReferenceChange) error {
	if options.ReferenceOperationID == "" && options.ReferenceOperation == "" && options.ReferenceOldBranch == "" && options.ReferenceNewBranch == "" {
		return nil
	}
	if _, err := uuid.Parse(options.ReferenceOperationID); err != nil || !git.IsValidRefPattern(options.ReferenceOldBranch) {
		return governance_model.ErrInvalid
	}
	if options.ReferenceOperation != "branch_delete" && options.ReferenceOperation != "branch_rename" && options.ReferenceOperation != "wiki_default_branch_rename" && options.ReferenceOperation != "release_tag_create" && options.ReferenceOperation != "release_tag_delete" {
		return governance_model.ErrInvalid
	}
	if options.ReferenceOperation == "release_tag_create" || options.ReferenceOperation == "release_tag_delete" {
		if options.IsWiki || len(changes) != 1 || changes[0].Ref != git.TagPrefix+options.ReferenceOldBranch {
			return governance_model.ErrInvalid
		}
		zero := func(value string) bool { return strings.Trim(value, "0") == "" }
		if options.ReferenceOperation == "release_tag_create" && zero(changes[0].Old) && !zero(changes[0].New) || options.ReferenceOperation == "release_tag_delete" && !zero(changes[0].Old) && zero(changes[0].New) {
			return nil
		}
		return governance_model.ErrInvalid
	}
	if options.ReferenceOperation == "wiki_default_branch_rename" && !options.IsWiki || options.ReferenceOperation != "wiki_default_branch_rename" && options.IsWiki {
		return governance_model.ErrInvalid
	}
	if options.ReferenceOperation != "branch_delete" && !git.IsValidRefPattern(options.ReferenceNewBranch) {
		return governance_model.ErrInvalid
	}
	oldRef, newRef := git.BranchPrefix+options.ReferenceOldBranch, git.BranchPrefix+options.ReferenceNewBranch
	zero := func(value string) bool { return strings.Trim(value, "0") == "" }
	if options.ReferenceOperation == "branch_delete" {
		if len(changes) == 1 && changes[0].Ref == oldRef && !zero(changes[0].Old) && zero(changes[0].New) {
			return nil
		}
		return governance_model.ErrInvalid
	}
	if len(changes) != 2 {
		return governance_model.ErrInvalid
	}
	validOld, validNew := false, false
	for _, change := range changes {
		validOld = validOld || change.Ref == oldRef && !zero(change.Old) && zero(change.New)
		validNew = validNew || change.Ref == newRef && zero(change.Old) && !zero(change.New)
	}
	if !validOld || !validNew {
		return governance_model.ErrInvalid
	}
	return nil
}

func gitHookTransport(value string) string {
	switch value {
	case "git_http", "git_ssh", "internal_git":
		return value
	}
	return "git_unknown"
}

func recordGovernanceGitDenial(ctx *gitea_context.PrivateContext, options *private.HookOptions) error {
	actor, err := governanceGitActor(ctx, options)
	if err != nil {
		return err
	}
	return governance_model.WithWrite(ctx, nil, func(tx context.Context) error {
		repo := ctx.Repo.Repository
		chain, err := governance_model.Ancestors(tx, repo.OwnerID)
		if err != nil {
			return err
		}
		var ancestors []int64
		for _, group := range chain {
			if group.Kind == "group" {
				ancestors = append(ancestors, group.ID)
			}
		}
		details, err := json.Marshal(map[string]any{"refs": options.RefFullNames, "old": options.OldCommitIDs, "new": options.NewCommitIDs, "reason": "项目已归档，或缺少有效最终审批授权及引用事务入口"})
		if err != nil {
			return err
		}
		return governance_model.AppendAudit(tx, &governance_model.AuditEvent{Type: "git.push_denied", Actor: actor, ScopeType: "repository", ScopeID: repo.ID, AncestorIDs: ancestors, ObjectType: "repository", ObjectID: repo.ID, ObjectPath: repo.FullPath(), Result: "denied", Details: details})
	})
}

// governanceGitActor 不把原生 Hook 为兼容权限传递的仓库所有者当作部署密钥本人。
func governanceGitActor(ctx *gitea_context.PrivateContext, options *private.HookOptions) (governance_model.Actor, error) {
	if options.IsInternal && options.ReferenceActor == "mirror" {
		if !ctx.Repo.Repository.IsMirror || options.UserID != 0 || options.DeployKeyID != 0 {
			return governance_model.Actor{}, governance_model.ErrForbidden
		}
		return governance_model.Actor{Kind: "system", Name: "镜像后台同步", Transport: "internal_git"}, nil
	}
	if options.DeployKeyID > 0 {
		deploy, err := asymkey_model.GetDeployKeyByID(ctx, options.DeployKeyID)
		if err != nil {
			return governance_model.Actor{}, err
		}
		if deploy.RepoID != ctx.Repo.Repository.ID {
			return governance_model.Actor{}, governance_model.ErrForbidden
		}
		key, err := asymkey_model.GetPublicKeyByID(ctx, deploy.KeyID)
		if err != nil {
			return governance_model.Actor{}, err
		}
		if key.Type != asymkey_model.KeyTypeDeploy {
			return governance_model.Actor{}, governance_model.ErrForbidden
		}
		actor := governance_service.RequestActor(&user_model.User{Name: deploy.Name}, options.PusherRemoteAddr, gitHookTransport(options.PusherTransport))
		actor.Kind, actor.CredentialID = "deploy_key", key.ID
		return actor, nil
	}
	if options.UserID > 0 {
		user, err := user_model.GetUserByID(ctx, options.UserID)
		if err != nil {
			return governance_model.Actor{}, err
		}
		return governance_service.RequestActor(user, options.PusherRemoteAddr, gitHookTransport(options.PusherTransport)), nil
	}
	if options.UserID == user_model.ActionsUserID {
		if options.ActionsTaskID <= 0 {
			return governance_model.Actor{}, governance_model.ErrForbidden
		}
		user := user_model.NewActionsUserWithTaskID(options.ActionsTaskID)
		permission, err := access_model.GetActionsUserRepoPermission(ctx, ctx.Repo.Repository, user, options.ActionsTaskID)
		if err != nil {
			return governance_model.Actor{}, err
		}
		if !permission.CanWrite(unit.TypeCode) {
			return governance_model.Actor{}, governance_model.ErrForbidden
		}
		actor := governance_service.RequestActor(user, options.PusherRemoteAddr, gitHookTransport(options.PusherTransport))
		actor.CredentialID = options.ActionsTaskID
		return actor, nil
	}
	return governance_model.Actor{Kind: "system", Name: "Gitea 内部 Git 操作", Transport: "internal_git"}, nil
}
