// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/git"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/log"
	webhook_module "gitea.dev/modules/webhook"
	actions_service "gitea.dev/services/actions"
)

// ApplyReferenceBusinessOperation 把已由 Git 确认提交的跨存储操作幂等写回数据库。
func ApplyReferenceBusinessOperation(ctx context.Context, operationID string) error {
	operation, err := governance_model.GetReferenceBusinessOperation(ctx, operationID)
	if err != nil {
		return err
	}
	if operation.State != "committed" {
		return governance_model.ErrConflict
	}
	return governance_model.WithReferenceTransactionWrite(ctx, operation.ID, []string{governance_model.Resource("repository", operation.RepoID)}, func(ctx context.Context) error {
		operation, err := governance_model.GetReferenceBusinessOperation(ctx, operationID)
		if err != nil {
			return err
		}
		if operation.State != "committed" {
			return governance_model.ErrConflict
		}
		if operation.BusinessApplied {
			return nil
		}
		repo, err := repo_model.GetRepositoryByID(ctx, operation.RepoID)
		if err != nil {
			return err
		}
		if operation.BusinessUpdateHEAD {
			referenceRepo := gitrepo.Repository(repo)
			if operation.IsWiki {
				referenceRepo = repo.WikiStorageRepo()
			}
			if err := gitrepo.SetDefaultBranch(ctx, referenceRepo, operation.BusinessNewBranch); err != nil {
				return err
			}
		}
		return db.WithTx(ctx, func(ctx context.Context) error {
			switch operation.BusinessOperation {
			case "branch_rename":
				if !gitrepo.IsBranchExist(ctx, repo, operation.BusinessNewBranch) || gitrepo.IsBranchExist(ctx, repo, operation.BusinessOldBranch) {
					return governance_model.ErrConflict
				}
				oldExists, err := git_model.IsBranchExist(ctx, repo.ID, operation.BusinessOldBranch)
				if err != nil {
					return err
				}
				newExists, err := git_model.IsBranchExist(ctx, repo.ID, operation.BusinessNewBranch)
				if err != nil {
					return err
				}
				if oldExists {
					if err := git_model.RenameBranch(ctx, repo, operation.BusinessOldBranch, operation.BusinessNewBranch, func(context.Context, bool) error { return nil }); err != nil {
						return err
					}
				} else if !newExists {
					return governance_model.ErrConflict
				}
				if repo.DefaultBranch == operation.BusinessNewBranch {
					if err := actions_model.DeleteScheduleTaskByRepo(ctx, repo.ID); err != nil {
						return err
					}
					if err := actions_service.CancelPreviousJobs(ctx, repo.ID, "", "", webhook_module.HookEventSchedule); err != nil {
						return err
					}
				}
			case "branch_delete":
				if gitrepo.IsBranchExist(ctx, repo, operation.BusinessOldBranch) {
					return governance_model.ErrConflict
				}
				exists, err := git_model.IsBranchExist(ctx, repo.ID, operation.BusinessOldBranch)
				if err != nil {
					return err
				}
				if exists {
					if err := git_model.MarkBranchAsDeleted(ctx, repo.ID, operation.BusinessOldBranch, operation.Actor.EffectiveUserID()); err != nil {
						return err
					}
				}
			case "wiki_default_branch_rename":
				branch, err := gitrepo.GetDefaultBranch(ctx, repo.WikiStorageRepo())
				if err != nil || branch != operation.BusinessNewBranch {
					return governance_model.ErrConflict
				}
				if repo.DefaultWikiBranch != operation.BusinessNewBranch {
					repo.DefaultWikiBranch = operation.BusinessNewBranch
					if err := repo_model.UpdateRepositoryColsNoAutoTime(ctx, repo, "default_wiki_branch"); err != nil {
						return err
					}
				}
			default:
				ok, err := governance_model.ApplyRegisteredReferenceBusinessOperation(governance_model.WithAuditActor(ctx, operation.Actor), operation)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("%w：未知引用业务操作", governance_model.ErrInvalid)
				}
			}
			return governance_model.MarkReferenceBusinessOperationApplied(ctx, operationID)
		})
	})
}

func RunReferenceBusinessOperationRecovery(ctx context.Context) error {
	operations, err := governance_model.PendingReferenceBusinessOperations(ctx, 100)
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if operation.State == "planned" {
			if governance_model.ReferenceBusinessOperationActive(operation.BusinessOperationID) || time.Since(operation.CreatedAt) < 10*time.Minute {
				continue
			}
		}
		if operation.State != "committed" {
			repo, repoErr := repo_model.GetRepositoryByID(ctx, operation.RepoID)
			if repoErr != nil {
				log.Error("读取引用业务操作项目失败: %v", repoErr)
				continue
			}
			referenceRepo := gitrepo.Repository(repo)
			if operation.IsWiki {
				referenceRepo = repo.WikiStorageRepo()
			}
			gitRepo, openErr := gitrepo.OpenRepository(ctx, referenceRepo)
			if openErr != nil {
				log.Error("打开引用业务操作仓库失败: %v", openErr)
				continue
			}
			actual := make(map[string]string, len(operation.Changes))
			for _, change := range operation.Changes {
				value, refErr := gitRepo.GetRefCommitID(change.Ref)
				if git.IsErrNotExist(refErr) {
					value = strings.Repeat("0", len(change.Old))
				} else if refErr != nil {
					openErr = refErr
					break
				}
				actual[change.Ref] = value
			}
			_ = gitRepo.Close()
			if openErr != nil {
				log.Error("读取引用业务操作引用失败: %v", openErr)
				continue
			}
			state := ""
			lockErr := gitrepo.WithVerifiedReferenceLocks(ctx, referenceRepo, actual, func(ctx context.Context) error {
				var err error
				state, err = governance_model.ReconcileReferenceTransaction(ctx, operation.ID, actual, nil)
				return err
			})
			if lockErr != nil {
				log.Error("核对引用业务操作失败: %v", lockErr)
				continue
			}
			if state != "committed" {
				continue
			}
		}
		if err := ApplyReferenceBusinessOperation(ctx, operation.BusinessOperationID); err != nil {
			log.Error("恢复引用业务操作失败: repo=%d operation=%s err=%v", operation.RepoID, operation.BusinessOperationID, err)
		}
	}
	return nil
}
