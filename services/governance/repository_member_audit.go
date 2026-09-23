// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"time"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"
)

func auditRepositoryMemberFailure(ctx context.Context, actor gm.Actor, repoID int64, kind string, failure error) {
	if failure == nil || actor.EffectiveUserID() <= 0 || actor.Kind == "" || actor.Transport == "" {
		return
	}
	reason := "内部错误"
	switch {
	case errors.Is(failure, gm.ErrNotFound), errors.Is(failure, gm.ErrForbidden):
		reason = "权限不足或目标不可见"
	case errors.Is(failure, gm.ErrInvalid):
		reason = "授权参数无效"
	case errors.Is(failure, gm.ErrConflict):
		reason = "来源变更、资源忙或永久所有者保护冲突"
	}
	details, _ := json.Marshal(map[string]string{"reason": reason})
	if err := gm.AppendAudit(ctx, &gm.AuditEvent{Type: kind, Actor: actor, ScopeType: "repository", ScopeID: repoID, ObjectType: "repository", ObjectID: repoID, Result: "denied", Details: details}); err != nil {
		log.Error("成员变更失败审计保存失败，项目 %d：%v", repoID, err)
	}
}

// 权限影响按成员逐条保存，避免大群组超过单条审计大小上限。
type repositoryAuditAccess struct {
	Active    bool                 `json:"active"`
	Owner     bool                 `json:"owner"`
	Access    string               `json:"access"`
	Abilities gm.Abilities         `json:"abilities"`
	Units     map[unit.Type]string `json:"units"`
}

func repositoryAuditAccessFor(ctx context.Context, repo *repo_model.Repository, userID int64) (*repositoryAuditAccess, error) {
	user, err := user_model.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
	if err != nil {
		return nil, err
	}
	permission = permission.ForMutation()
	grants, err := gm.RepositoryGrants(ctx, repo.ID, repo.OwnerID, userID, time.Now())
	if err != nil {
		return nil, err
	}
	result := &repositoryAuditAccess{Active: user.IsActive && !user.ProhibitLogin, Owner: permission.IsOwner(), Access: permission.AccessMode.ToString(), Abilities: gm.EffectiveAbilities(grants), Units: map[unit.Type]string{}}
	if !result.Active {
		result.Abilities = gm.Abilities{}
	}
	for _, kind := range unit.AllRepoUnitTypes {
		result.Units[kind] = permission.UnitAccessMode(kind).ToString()
	}
	return result, nil
}

func repositoryAuditMembers(ctx context.Context, repo *repo_model.Repository, invitedGroupID int64) (map[int64]*repositoryAuditAccess, error) {
	result := map[int64]*repositoryAuditAccess{}
	if err := repo.LoadOwner(ctx); err != nil {
		return nil, err
	}
	if invitedGroupID > 0 {
		groups, err := repositoryMemberGroups(ctx, repo.ID, invitedGroupID, time.Now())
		if err != nil {
			return nil, err
		}
		var teams []int64
		if len(groups) > 0 {
			if err := db.GetEngine(ctx).Table(new(gm.Namespace)).Cols("native_owner_team_id").In("id", groups).Where("native_owner_team_id > 0").Find(&teams); err != nil {
				return nil, err
			}
		}
		for cursor := int64(0); ; {
			ids, err := repositoryMemberCandidates(ctx, repo, groups, teams, cursor, time.Now())
			if err != nil {
				return nil, err
			}
			for _, userID := range ids {
				result[userID], err = repositoryAuditAccessFor(ctx, repo, userID)
				if err != nil {
					return nil, err
				}
			}
			if len(ids) < 101 {
				break
			}
			cursor = ids[len(ids)-1]
		}
	}
	for cursor := int64(0); ; {
		page, err := listRepositoryAllMembers(ctx, repo, true, cursor)
		if err != nil {
			return nil, err
		}
		for _, member := range page.Members {
			result[member.UserID], err = repositoryAuditAccessFor(ctx, repo, member.UserID)
			if err != nil {
				return nil, err
			}
		}
		if page.NextID == 0 {
			return result, nil
		}
		cursor = page.NextID
	}
}

func auditRepositoryAccessImpact(ctx context.Context, actor gm.Actor, repo *repo_model.Repository, source string, before map[int64]*repositoryAuditAccess) error {
	ids := slices.Sorted(maps.Keys(before))
	for _, userID := range ids {
		after, err := repositoryAuditAccessFor(ctx, repo, userID)
		if err != nil {
			return err
		}
		if reflect.DeepEqual(before[userID], after) {
			continue
		}
		details, err := json.Marshal(map[string]any{"source": source, "impact_before": before[userID], "impact_after": after})
		if err != nil {
			return err
		}
		if err := gm.AppendAudit(ctx, &gm.AuditEvent{Type: "member.updated", Actor: actor, ScopeType: "repository", ScopeID: repo.ID, ObjectType: "user", ObjectID: userID, ObjectPath: repo.FullPath(), Result: "success", Details: details}); err != nil {
			return err
		}
	}
	return nil
}
