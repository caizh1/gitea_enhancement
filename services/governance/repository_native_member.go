// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"

	"xorm.io/builder"
)

// setNativeRepositoryMember 只调整已有原生来源，新成员统一使用治理授权。
func setNativeRepositoryMember(ctx context.Context, actor gm.Actor, repoID int64, option GroupMemberOption, remove bool) error {
	err := updateNativeRepositoryMember(ctx, actor, repoID, option, remove)
	auditRepositoryMemberFailure(ctx, actor, repoID, "member.updated", err)
	return err
}

func updateNativeRepositoryMember(ctx context.Context, actor gm.Actor, repoID int64, option GroupMemberOption, remove bool) error {
	return withActorWrite(ctx, actor, []string{gm.Resource("repository", repoID), gm.Resource("user", option.UserID)}, func(ctx context.Context) error {
		repo, ceiling, err := repositoryMemberManager(ctx, actor.EffectiveUserID(), repoID)
		if err != nil {
			return err
		}
		if err := checkRepositoryMemberTarget(ctx, actor.EffectiveUserID(), repo, option.UserID); err != nil {
			return err
		}
		if err := checkRepositoryPreview(ctx, actor.EffectiveUserID(), repoID, option.PreviewToken, RepositoryMemberChange{Kind: "native", Member: option, Remove: remove}); err != nil {
			return err
		}
		namespace, err := gm.GetNamespace(ctx, repo.OwnerID)
		if err != nil {
			return err
		}
		if namespace.Revision != option.Revision {
			return gm.ErrConflict
		}
		previous, has, err := db.Get[repo_model.Collaboration](ctx, builder.Eq{"repo_id": repoID, "user_id": option.UserID})
		if err != nil {
			return err
		}
		if !has {
			return gm.ErrNotFound
		}
		impactBefore, err := repositoryAuditAccessFor(ctx, repo, option.UserID)
		if err != nil {
			return err
		}
		if !remove {
			if repo.IsArchived {
				return gm.ErrConflict
			}
			if err := checkMemberRole(ctx, "repository", repo.OwnerID, option, ceiling); err != nil {
				return err
			}
			mode, ok := map[gm.Role]perm.AccessMode{gm.Reporter: perm.AccessModeRead, gm.Developer: perm.AccessModeWrite, gm.Maintainer: perm.AccessModeAdmin}[option.Role]
			if !ok || option.CustomRoleID != 0 || option.ExpiresUnix != 0 {
				return gm.ErrInvalid
			}
			if _, err := db.GetEngine(ctx).ID(previous.ID).Cols("mode").Update(&repo_model.Collaboration{Mode: mode}); err != nil {
				return err
			}
		} else if _, err := db.GetEngine(ctx).ID(previous.ID).Delete(new(repo_model.Collaboration)); err != nil {
			return err
		}
		if err := access_model.RecalculateUserAccess(ctx, repo, option.UserID); err != nil {
			return err
		}
		user, err := user_model.GetUserByID(ctx, option.UserID)
		if err != nil {
			return err
		}
		canRead, err := access_model.HasAnyUnitAccess(ctx, user.ID, repo)
		if err != nil {
			return err
		}
		if !canRead {
			if err := repo_model.WatchRepo(ctx, user, repo, false); err != nil {
				return err
			}
			if err := issues_model.RemoveStopwatchesByRepoID(ctx, user.ID, repoID); err != nil {
				return err
			}
			if err := issues_model.RemoveIssueWatchersByRepoID(ctx, user.ID, repoID); err != nil {
				return err
			}
		}
		canAssign, err := access_model.CanBeAssigned(ctx, user, repo)
		if err != nil {
			return err
		}
		if !canAssign {
			if _, err := db.GetEngine(ctx).Where(builder.Eq{"assignee_id": user.ID}).In("issue_id", builder.Select("id").From("issue").Where(builder.Eq{"repo_id": repoID})).Delete(&issues_model.IssueAssignees{}); err != nil {
				return err
			}
		}
		if _, err := db.GetEngine(ctx).ID(repo.OwnerID).Incr("revision").Update(new(gm.Namespace)); err != nil {
			return err
		}
		if err := auditRepositoryAccessImpact(ctx, actor, repo, "native_collaborator", map[int64]*repositoryAuditAccess{option.UserID: impactBefore}); err != nil {
			return err
		}
		details, err := json.Marshal(map[string]any{"before": previous, "after": option, "removed": remove, "source": "native_collaborator"})
		if err != nil {
			return err
		}
		return gm.AppendAudit(ctx, &gm.AuditEvent{Type: "member.updated", Actor: actor, ScopeType: "repository", ScopeID: repoID, ObjectType: "user", ObjectID: user.ID, Result: "success", Details: details})
	})
}
