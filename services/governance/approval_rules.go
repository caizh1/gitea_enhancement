// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/json"

	"xorm.io/builder"
)

type RepositoryApprovalRules struct {
	SettingsRevision          int64                                       `json:"settings_revision"`
	FullPath                  string                                      `json:"full_path"`
	CanManage                 bool                                        `json:"can_manage"`
	Rules                     []*governance_model.ApprovalRule            `json:"rules"`
	Settings                  *governance_model.EffectiveApprovalSettings `json:"settings"`
	EnforcementReady          bool                                        `json:"enforcement_ready"`
	MergeEnforcementReady     bool                                        `json:"merge_enforcement_ready"`
	ReferenceEnforcementReady bool                                        `json:"reference_enforcement_ready"`
}

func checkApprovalRuleAccess(ctx context.Context, actorID, repoID int64, write bool) (*repo_model.Repository, bool, error) {
	repo, err := repo_model.GetRepositoryByID(ctx, repoID)
	if repo_model.IsErrRepoNotExist(err) {
		return nil, false, governance_model.ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	if actorID <= 0 {
		permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, nil)
		if err != nil {
			return nil, false, err
		}
		if write || !permission.CanRead(unit.TypePullRequests) {
			return nil, false, governance_model.ErrNotFound
		}
		return repo, false, nil
	}
	actor, err := activeActor(ctx, actorID)
	if err != nil {
		return nil, false, err
	}
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, actor)
	if err != nil {
		return nil, false, err
	}
	grants, err := governance_model.RepositoryGrants(ctx, repo.ID, repo.OwnerID, actor.ID, time.Now())
	if err != nil {
		return nil, false, err
	}
	manage := permission.IsAdmin() || governance_model.EffectiveAbilities(grants)[governance_model.ManageApprovals]
	if !permission.CanRead(unit.TypePullRequests) || write && !manage {
		return nil, false, governance_model.ErrNotFound
	}
	return repo, manage, nil
}

func ListRepositoryApprovalRules(ctx context.Context, actorID, repoID int64) (*RepositoryApprovalRules, error) {
	repo, manage, err := checkApprovalRuleAccess(ctx, actorID, repoID, false)
	if err != nil {
		return nil, err
	}
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		return nil, err
	}
	scopes := builder.Or(builder.Eq{"scope_type": "instance", "scope_id": 0}, builder.Eq{"scope_type": "repository", "scope_id": repo.ID})
	for _, ancestor := range chain {
		if ancestor.Kind == "group" {
			scopes = scopes.Or(builder.Eq{"scope_type": "group", "scope_id": ancestor.ID})
		}
	}
	installed, err := gitrepo.ReferenceTransactionHookInstalled(repo)
	if err != nil {
		return nil, err
	}
	result := &RepositoryApprovalRules{FullPath: repo.FullPath(), CanManage: manage, Rules: []*governance_model.ApprovalRule{}, ReferenceEnforcementReady: installed}
	if err := db.GetEngine(ctx).Where(scopes).Asc("id").Find(&result.Rules); err != nil {
		return nil, err
	}
	var localSettings governance_model.ApprovalSettings
	if _, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repo.ID).Get(&localSettings); err != nil {
		return nil, err
	}
	result.SettingsRevision = localSettings.Revision
	result.Settings, err = governance_model.ResolveApprovalSettings(ctx, repo.ID, repo.OwnerID)
	if err == nil {
		source := result.Settings.Sources["prevent_overrides"]
		if source.ScopeType == "instance" && source.Value {
			result.CanManage = false
		}
	}
	return result, err
}

// SaveRepositoryApprovalRule 仅修改项目本级规则；实例和祖先策略不能通过项目接口降级。
func SaveRepositoryApprovalRule(ctx context.Context, actor governance_model.Actor, repoID, ruleID, revision int64, input governance_model.ApprovalRule, remove bool) (*governance_model.ApprovalRule, error) {
	var result *governance_model.ApprovalRule
	if !remove && ctx.Value(approvalBatchContextKey{}) == nil {
		// 先核对权限、版本及主体，无效请求不能触发 Hook 安装。
		if err := checkDelegation(ctx, actor); err != nil {
			return nil, err
		}
		repo, _, err := checkApprovalRuleAccess(ctx, actor.EffectiveUserID(), repoID, true)
		if err != nil {
			return nil, err
		}
		state, err := ListRepositoryApprovalRules(ctx, actor.EffectiveUserID(), repoID)
		if err != nil {
			return nil, err
		}
		if !state.CanManage {
			return nil, governance_model.ErrForbidden
		}
		if repo.IsArchived {
			return nil, governance_model.ErrConflict
		}
		var previous *governance_model.ApprovalRule
		if ruleID > 0 {
			var has bool
			previous, has, err = db.GetByID[governance_model.ApprovalRule](ctx, ruleID)
			if err != nil {
				return nil, err
			}
			if !has || previous.ScopeType != "repository" || previous.ScopeID != repoID {
				return nil, governance_model.ErrNotFound
			}
			if previous.Revision != revision {
				return nil, governance_model.ErrConflict
			}
		}
		check := input
		check.ScopeType, check.ScopeID = "repository", repoID
		if err := normalizeProtectionIDs(ctx, repo, &check, previous); err != nil {
			return nil, err
		}
		if err := validateApprovalRuleSubjects(ctx, actor, repo, &check, previous); err != nil {
			return nil, err
		}
		if err := ensureRequiredRuleReferenceHooks(ctx, actor, repoID, &input); err != nil {
			return nil, err
		}
	}
	err := withActorWrite(ctx, actor, []string{governance_model.Resource("repository", repoID)}, func(ctx context.Context) error {
		repo, _, err := checkApprovalRuleAccess(ctx, actor.EffectiveUserID(), repoID, true)
		if err != nil {
			return err
		}
		var instance governance_model.ApprovalSettings
		if _, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "instance", 0).Get(&instance); err != nil {
			return err
		}
		if instance.PreventOverrides {
			return governance_model.ErrForbidden
		}
		chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
		if err != nil {
			return err
		}
		if repo.IsArchived {
			return governance_model.ErrConflict
		}
		var ancestors []int64
		for _, ancestor := range chain {
			if ancestor.Archived || ancestor.DeleteAfter != 0 {
				return governance_model.ErrConflict
			}
			if ancestor.Kind == "group" {
				ancestors = append(ancestors, ancestor.ID)
			}
		}
		var previous *governance_model.ApprovalRule
		if ruleID != 0 {
			var has bool
			previous, has, err = db.GetByID[governance_model.ApprovalRule](ctx, ruleID)
			if err != nil {
				return err
			}
			if !has || previous.ScopeType != "repository" || previous.ScopeID != repoID {
				return governance_model.ErrNotFound
			}
			if previous.Revision != revision {
				return governance_model.ErrConflict
			}
		} else if revision != 0 || remove {
			return governance_model.ErrInvalid
		}
		if err := snapshotRepositoryPullRules(ctx, repoID, false); err != nil {
			return err
		}
		if remove && previous.NativeProtectionID > 0 {
			return fmt.Errorf("%w：总人数要求请改为 0，不能删除兼容关联", governance_model.ErrInvalid)
		}
		if remove {
			if _, err := db.GetEngine(ctx).ID(ruleID).Delete(new(governance_model.ApprovalRule)); err != nil {
				return err
			}
		} else {
			next := input
			next.ID, next.ScopeType, next.ScopeID, next.Locked, next.Revision = ruleID, "repository", repoID, false, revision+1
			if err := normalizeProtectionIDs(ctx, repo, &next, previous); err != nil {
				return err
			}
			if err := preserveHiddenApprovalSubjects(ctx, actor.EffectiveUserID(), repo, &next, previous); err != nil {
				return err
			}
			if err := validateApprovalRuleSubjects(ctx, actor, repo, &next, previous); err != nil {
				return err
			}
			duplicate, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND name = ? AND id <> ?", "repository", repoID, next.Name, ruleID).Exist(new(governance_model.ApprovalRule))
			if err != nil {
				return err
			}
			if duplicate {
				return governance_model.ErrConflict
			}
			if ruleID == 0 {
				err = db.Insert(ctx, &next)
			} else {
				_, err = db.GetEngine(ctx).ID(ruleID).AllCols().Update(&next)
			}
			if err != nil {
				return err
			}
			if err := governance_model.WriteNativeApprovalProjection(ctx, &next); err != nil {
				return err
			}
			result = &next
		}
		settings, err := governance_model.ResolveApprovalSettings(ctx, repo.ID, repo.OwnerID)
		if err != nil {
			return err
		}
		if settings.Settings.PreventOverrides {
			if err := snapshotRepositoryPullRules(ctx, repo.ID, true); err != nil {
				return err
			}
		}
		details, err := json.Marshal(map[string]any{"before": previous, "after": result})
		if err != nil {
			return err
		}
		event := "approval.rule_changed"
		objectID := ruleID
		if result != nil {
			objectID = result.ID
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: event, Actor: actor, ScopeType: "repository", ScopeID: repoID, AncestorIDs: ancestors, ObjectType: "approval_rule", ObjectID: objectID, ObjectPath: repo.FullPath(), Result: "success", Details: details})
	})
	return result, err
}

func snapshotRepositoryPullRules(ctx context.Context, repoID int64, replace bool) error {
	var pulls []struct{ ID int64 }
	query := db.GetEngine(ctx).Table("pull_request").Where("base_repo_id = ? AND has_merged = ?", repoID, false)
	if replace {
		query = query.Join("INNER", "issue", "issue.id = pull_request.issue_id").And("issue.is_closed = ?", false)
	}
	if err := query.Select("pull_request.id").Find(&pulls); err != nil {
		return err
	}
	for _, pr := range pulls {
		if _, err := governance_model.SnapshotProjectPullRules(ctx, pr.ID, repoID, replace); err != nil {
			return err
		}
	}
	return nil
}

// validateApprovalRuleSubjects 共用项目与 PR 的可见性和真实访问资格检查。
func validateApprovalRuleSubjects(ctx context.Context, actor governance_model.Actor, repo *repo_model.Repository, next, previous *governance_model.ApprovalRule) error {
	next.Name = strings.TrimSpace(next.Name)
	if err := governance_model.ValidateApprovalRule(next); err != nil {
		return err
	}
	if len(next.UserIDs)+len(next.GroupIDs)+len(next.TeamIDs) > 1000 || len(next.Branches) > 1000 {
		return governance_model.ErrInvalid
	}
	next.UserIDs, next.GroupIDs, next.TeamIDs, next.Branches = slices.Clone(next.UserIDs), slices.Clone(next.GroupIDs), slices.Clone(next.TeamIDs), slices.Clone(next.Branches)
	for _, ids := range [][]int64{next.UserIDs, next.GroupIDs, next.TeamIDs} {
		slices.Sort(ids)
	}
	next.UserIDs, next.GroupIDs, next.TeamIDs = slices.Compact(next.UserIDs), slices.Compact(next.GroupIDs), slices.Compact(next.TeamIDs)
	for _, id := range next.UserIDs {
		if previous != nil && slices.Contains(previous.UserIDs, id) {
			continue
		}
		user, err := user_model.GetUserByID(ctx, id)
		if err != nil {
			return governance_model.ErrInvalid
		}
		if repo == nil {
			viewer, err := activeActor(ctx, actor.EffectiveUserID())
			if err != nil {
				return err
			}
			if !user.IsActive || user.ProhibitLogin || user.IsOrganization() || !user_model.IsUserVisibleToViewer(ctx, user, viewer) {
				return governance_model.ErrNotFound
			}
		} else {
			permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
			if err != nil {
				return err
			}
			if !permission.CanRead(unit.TypePullRequests) {
				return governance_model.ErrInvalid
			}
		}
	}
	for _, id := range next.GroupIDs {
		if previous != nil && slices.Contains(previous.GroupIDs, id) {
			continue
		}
		if _, err := CheckGroupAccess(ctx, actor.EffectiveUserID(), id, governance_model.ReadGroup); err != nil {
			return err
		}
	}
	for _, id := range next.TeamIDs {
		if previous != nil && slices.Contains(previous.TeamIDs, id) {
			continue
		}
		team, err := organization.GetTeamByID(ctx, id)
		if err != nil || repo != nil && team.OrgID != repo.OwnerID {
			return governance_model.ErrInvalid
		}
		visible, err := canReadApprovalTeam(ctx, actor.EffectiveUserID(), team)
		if err != nil {
			return err
		}
		if !visible {
			return governance_model.ErrNotFound
		}
	}
	return nil
}
