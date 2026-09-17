// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"strings"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/git"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/json"

	"github.com/google/uuid"
)

// RecoverGovernanceOperation 只用于有数据库管理权的主机命令；只核对，不重新执行 Git 写入。
func RecoverGovernanceOperation(ctx context.Context, kind, id string, actor governance_model.Actor) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", governance_model.ErrInvalid
	}
	var repoID int64
	var refs []string
	var ancestors []int64
	var path string
	mergeID := ""
	var references []*governance_model.ReferenceTransaction
	switch kind {
	case "reference":
		operation := new(governance_model.ReferenceTransaction)
		has, err := db.GetEngine(ctx).ID(id).Get(operation)
		if err != nil {
			return "", err
		}
		if !has {
			return "", governance_model.ErrNotFound
		}
		repoID, ancestors, path, mergeID = operation.RepoID, operation.AncestorIDs, operation.ObjectPath, operation.MergeAuthorizationID
		references = append(references, operation)
		for _, change := range operation.Changes {
			refs = append(refs, change.Ref)
		}
	case "merge":
		operation := new(governance_model.MergeAuthorization)
		has, err := db.GetEngine(ctx).ID(id).Get(operation)
		if err != nil {
			return "", err
		}
		if !has {
			return "", governance_model.ErrNotFound
		}
		repoID, ancestors, mergeID = operation.RepoID, operation.AncestorIDs, operation.ID
		path = operation.ObjectPath
		refs = append(refs, "refs/heads/"+operation.Branch)
		if err := db.GetEngine(ctx).Where("merge_authorization_id = ?", id).Find(&references); err != nil {
			return "", err
		}
		for _, operation := range references {
			for _, change := range operation.Changes {
				refs = append(refs, change.Ref)
			}
		}
	default:
		return "", governance_model.ErrInvalid
	}
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if err != nil {
		return "", err
	}
	if path == "" {
		path = repo.FullPath()
	}
	referenceRepo := gitrepo.Repository(repo)
	if len(references) > 0 && references[0].IsWiki {
		referenceRepo = repo.WikiStorageRepo()
	}
	gitRepo, err := gitrepo.OpenRepository(ctx, referenceRepo)
	if err != nil {
		return "", err
	}
	defer gitRepo.Close()
	actual := make(map[string]string)
	for _, ref := range refs {
		value, err := gitRepo.GetRefCommitID(ref)
		if git.IsErrNotExist(err) {
			value = strings.Repeat("0", git.ObjectFormatFromName(repo.ObjectFormatName).FullLength())
		} else if err != nil {
			return "", err
		}
		actual[ref] = value
	}
	state := ""
	err = gitrepo.WithVerifiedReferenceLocks(ctx, referenceRepo, actual, func(ctx context.Context) error {
		return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
			for _, operation := range references {
				var err error
				state, err = governance_model.ReconcileReferenceTransaction(ctx, operation.ID, actual, nil)
				if err != nil {
					return err
				}
				if state == "unknown" {
					break
				}
			}
			if mergeID != "" && state != "unknown" {
				operation := new(governance_model.MergeAuthorization)
				has, err := db.GetEngine(ctx).ID(mergeID).Get(operation)
				if err != nil {
					return err
				}
				if !has {
					return governance_model.ErrNotFound
				}
				state, err = governance_model.ReconcileMerge(ctx, mergeID, actual["refs/heads/"+operation.Branch])
				if err != nil {
					return err
				}
			}
			details, err := json.Marshal(map[string]any{"operation_kind": kind, "operation_id": id, "state": state, "actual_refs": actual})
			if err != nil {
				return err
			}
			result := "success"
			if state == "unknown" {
				result = "unknown"
			}
			return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "governance.operation_recovered", Actor: actor, ScopeType: "repository", ScopeID: repoID, AncestorIDs: ancestors, ObjectType: "repository", ObjectID: repoID, ObjectPath: path, Result: result, Details: details})
		})
	})
	return state, err
}
