// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package pull

import (
	"context"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/timeutil"
	governance_service "gitea.dev/services/governance"
)

// FinalizeAuthorizedMerge 只修复已经核对成功的原生 PR 状态，不重复 Git 写入。
func FinalizeAuthorizedMerge(ctx context.Context, authorizationID string) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		operation := new(governance_model.MergeAuthorization)
		has, err := db.GetEngine(ctx).ID(authorizationID).Get(operation)
		if err != nil {
			return err
		}
		if !has {
			return governance_model.ErrNotFound
		}
		if operation.State != "succeeded" {
			return governance_model.ErrConflict
		}
		pr, err := issues_model.GetPullRequestByID(ctx, operation.PullID)
		if err != nil {
			return err
		}
		if pr.HasMerged {
			if pr.MergedCommitID != operation.NewTarget || pr.MergerID != operation.Actor.EffectiveUserID() {
				return governance_model.ErrConflict
			}
			return nil
		}
		if pr.BaseRepoID != operation.RepoID || pr.BaseBranch != operation.Branch {
			return governance_model.ErrConflict
		}
		return governance_model.WithWrite(ctx, []string{governance_model.Resource("pull", pr.ID)}, func(ctx context.Context) error {
			merger, err := user_model.GetUserByID(ctx, operation.Actor.EffectiveUserID())
			if err != nil {
				return err
			}
			if _, err := SetMerged(ctx, pr, operation.NewTarget, timeutil.TimeStamp(operation.CreatedAt.Unix()), merger, pr.Status); err != nil {
				return err
			}
			details, err := json.Marshal(map[string]string{"authorization_id": operation.ID, "commit": operation.NewTarget, "branch": operation.Branch})
			if err != nil {
				return err
			}
			return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "merge.native_state_recovered", Actor: operation.Actor, ScopeType: "repository", ScopeID: operation.RepoID, AncestorIDs: operation.AncestorIDs, ObjectType: "pull", ObjectID: operation.PullID, ObjectPath: operation.ObjectPath, Result: "success", Details: details})
		})
	})
}

// RecoverGovernanceOperation 核对引用后补齐原生状态；中断后可以继续，不能再次合并。
func RecoverGovernanceOperation(ctx context.Context, kind, id string, actor governance_model.Actor) (string, error) {
	state, err := governance_service.RecoverGovernanceOperation(ctx, kind, id, actor)
	if err != nil || state != "succeeded" {
		return state, err
	}
	if kind == "reference" {
		operation := new(governance_model.ReferenceTransaction)
		has, err := db.GetEngine(ctx).ID(id).Get(operation)
		if err != nil {
			return state, err
		}
		if !has {
			return state, governance_model.ErrNotFound
		}
		id = operation.MergeAuthorizationID
	}
	return state, FinalizeAuthorizedMerge(ctx, id)
}
