// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
)

type AccessRequestState struct {
	CanManageSettings bool                                   `json:"can_manage_settings"`
	Archived          bool                                   `json:"archived"`
	FullPath          string                                 `json:"full_path"`
	Revision          int64                                  `json:"revision"`
	CanRequest        bool                                   `json:"can_request"`
	CanManage         bool                                   `json:"can_manage"`
	Setting           *governance_model.AccessRequestSetting `json:"setting"`
	OwnRequest        *governance_model.AccessRequest        `json:"own_request,omitempty"`
	Requests          []*governance_model.AccessRequest      `json:"requests"`
	Roles             []*governance_model.CustomRole         `json:"roles"`
	NextID            int64                                  `json:"next_id,omitempty"`
}

func GetAccessRequestState(ctx context.Context, userID int64, scope string, id, afterID int64) (*AccessRequestState, error) {
	if afterID < 0 {
		return nil, governance_model.ErrInvalid
	}
	state, err := loadRequestScope(ctx, userID, scope, id, false)
	if err != nil {
		return nil, err
	}
	setting, err := accessRequestSetting(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	result := &AccessRequestState{FullPath: state.path, Revision: state.revision, Setting: setting, Requests: []*governance_model.AccessRequest{}}
	_, err = loadRequestScope(ctx, userID, scope, id, true)
	if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
		return nil, err
	}
	result.CanManage = err == nil
	result.Archived = state.archived
	if !state.archived {
		err = checkRequestSettingAccess(ctx, userID, scope, id)
		if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
			return nil, err
		}
		result.CanManageSettings = err == nil
	}
	var own governance_model.AccessRequest
	has, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", scope, id, userID).Get(&own)
	if err != nil {
		return nil, err
	}
	if has {
		result.OwnRequest = &own
	}
	result.CanRequest = !has && !setting.Disabled && !state.archived && !state.member
	if result.CanManage {
		chain, err := governance_model.Ancestors(ctx, state.ownerID)
		if err != nil {
			return nil, err
		}
		if err := db.GetEngine(ctx).Where("root_id = ?", chain[len(chain)-1].ID).Asc("id").Find(&result.Roles); err != nil {
			return nil, err
		}
		if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND id > ?", scope, id, afterID).Asc("id").Limit(101).Find(&result.Requests); err != nil {
			return nil, err
		}
		if len(result.Requests) > 100 {
			result.Requests = result.Requests[:100]
			result.NextID = result.Requests[99].ID
		}
	}
	visible := append([]*governance_model.AccessRequest{}, result.Requests...)
	if result.OwnRequest != nil {
		visible = append(visible, result.OwnRequest)
	}
	ids := make([]int64, 0, len(visible))
	for _, request := range visible {
		ids = append(ids, request.UserID)
		request.UserState = "unavailable"
	}
	if len(ids) > 0 {
		var users []*user_model.User
		if err := db.GetEngine(ctx).In("id", ids).Find(&users); err != nil {
			return nil, err
		}
		byID := make(map[int64]*user_model.User, len(users))
		for _, user := range users {
			byID[user.ID] = user
		}
		for _, request := range visible {
			if user := byID[request.UserID]; user != nil {
				request.CurrentUsername = user.Name
				if user.IsActive && !user.ProhibitLogin && !user.IsOrganization() && !user.IsGiteaActions() && !user.IsGhost() {
					request.UserState = "active"
				}
			}
		}
	}
	return result, nil
}

type AccessRequestSettingOption struct {
	Disabled bool  `json:"disabled"`
	Revision int64 `json:"revision"`
}

func checkRequestSettingAccess(ctx context.Context, userID int64, scope string, id int64) error {
	if scope == "group" {
		_, err := CheckGroupAccess(ctx, userID, id, governance_model.ManageGroup)
		return err
	}
	_, abilities, err := repositoryMemberManager(ctx, userID, id)
	if err != nil {
		return err
	}
	if !abilities[governance_model.ManageProject] {
		return governance_model.ErrNotFound
	}
	return nil
}

func SaveAccessRequestSetting(ctx context.Context, actor governance_model.Actor, scope string, id int64, option AccessRequestSettingOption) (*governance_model.AccessRequestSetting, error) {
	var result *governance_model.AccessRequestSetting
	err := withActorWrite(ctx, actor, []string{governance_model.Resource(scope, id)}, func(ctx context.Context) error {
		state, err := loadRequestScope(ctx, actor.EffectiveUserID(), scope, id, true)
		if err != nil {
			return err
		}
		if err := checkRequestSettingAccess(ctx, actor.EffectiveUserID(), scope, id); err != nil {
			return err
		}
		if state.archived {
			return governance_model.ErrConflict
		}
		previous, err := accessRequestSetting(ctx, scope, id)
		if err != nil {
			return err
		}
		if previous.Revision != option.Revision {
			return governance_model.ErrConflict
		}
		result = &governance_model.AccessRequestSetting{ScopeType: scope, ScopeID: id, Disabled: option.Disabled, Revision: previous.Revision + 1}
		if previous.Revision == 0 {
			if err := db.Insert(ctx, result); err != nil {
				return err
			}
		} else if _, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", scope, id).Cols("disabled", "revision").Update(result); err != nil {
			return err
		}
		chain, err := governance_model.Ancestors(ctx, state.ownerID)
		if err != nil {
			return err
		}
		ancestors := make([]int64, 0, len(chain))
		for _, n := range chain {
			if n.Kind == "group" {
				ancestors = append(ancestors, n.ID)
			}
		}
		details, err := json.Marshal(map[string]any{"before": previous, "after": result})
		if err != nil {
			return err
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "access_request.setting_changed", Actor: actor, ScopeType: scope, ScopeID: id, AncestorIDs: ancestors, ObjectType: scope, ObjectID: id, ObjectPath: state.path, Result: "success", Details: details})
	})
	return result, err
}

type requestScope struct {
	ownerID, revision int64
	path              string
	archived, member  bool
}

func loadRequestScope(ctx context.Context, userID int64, scope string, id int64, manage bool) (*requestScope, error) {
	user, err := activeActor(ctx, userID)
	if err != nil {
		return nil, err
	}
	state := &requestScope{}
	switch scope {
	case "group":
		ability := governance_model.ReadGroup
		if manage {
			ability = governance_model.ManageGroupMembers
		}
		group, err := CheckGroupAccess(ctx, userID, id, ability)
		if err != nil {
			return nil, err
		}
		nativeMember, err := organization.IsOrganizationMember(ctx, id, userID)
		if err != nil {
			return nil, err
		}
		state.ownerID, state.path = id, group.FullPath
		state.member = user.IsAdmin || nativeMember || len(group.Grants) > 0
	case "repository":
		repo, err := repo_model.GetRepositoryByID(ctx, id)
		if repo_model.IsErrRepoNotExist(err) {
			return nil, governance_model.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
		if err != nil {
			return nil, err
		}
		if !permission.HasAnyUnitAccessOrPublicAccess() {
			return nil, governance_model.ErrNotFound
		}
		if manage {
			if _, _, err := repositoryMemberManager(ctx, userID, id); err != nil {
				return nil, err
			}
		}
		grants, err := governance_model.RepositoryGrants(ctx, id, repo.OwnerID, userID, time.Now())
		if err != nil {
			return nil, err
		}
		nativeMember, err := db.GetEngine(ctx).Where("repo_id = ? AND user_id = ?", id, userID).Exist(new(access_model.Access))
		if err != nil {
			return nil, err
		}
		collaborator, err := repo_model.IsCollaborator(ctx, id, userID)
		if err != nil {
			return nil, err
		}
		state.ownerID, state.path, state.archived = repo.OwnerID, repo.FullPath(), repo.IsArchived
		state.member = user.IsAdmin || user.ID == repo.OwnerID || nativeMember || collaborator || len(grants) > 0
	default:
		return nil, governance_model.ErrInvalid
	}
	chain, err := governance_model.Ancestors(ctx, state.ownerID)
	if err != nil {
		return nil, err
	}
	state.revision = chain[0].Revision
	for _, n := range chain {
		state.archived = state.archived || n.Archived || n.DeleteAfter != 0
	}
	return state, nil
}

func accessRequestSetting(ctx context.Context, scope string, id int64) (*governance_model.AccessRequestSetting, error) {
	setting := &governance_model.AccessRequestSetting{ScopeType: scope, ScopeID: id}
	_, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", scope, id).Get(setting)
	return setting, err
}

func requestAudit(ctx context.Context, actor governance_model.Actor, state *requestScope, request *governance_model.AccessRequest, kind string) error {
	chain, err := governance_model.Ancestors(ctx, state.ownerID)
	if err != nil {
		return err
	}
	ancestors := make([]int64, 0, len(chain))
	for _, n := range chain {
		if n.Kind == "group" {
			ancestors = append(ancestors, n.ID)
		}
	}
	objectPath := request.Username
	user, has, err := db.GetByID[user_model.User](ctx, request.UserID)
	if err != nil {
		return err
	}
	if has {
		objectPath = user.Name
	}
	details, err := json.Marshal(map[string]any{"request": request, "scope_path": state.path})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: kind, Actor: actor, ScopeType: request.ScopeType, ScopeID: request.ScopeID, AncestorIDs: ancestors, ObjectType: "user", ObjectID: request.UserID, ObjectPath: objectPath, Result: "success", Details: details})
}

func RequestAccess(ctx context.Context, actor governance_model.Actor, scope string, id int64) (*governance_model.AccessRequest, error) {
	var result *governance_model.AccessRequest
	err := withActorWrite(ctx, actor, []string{governance_model.Resource(scope, id), governance_model.Resource("user", actor.EffectiveUserID())}, func(ctx context.Context) error {
		state, err := loadRequestScope(ctx, actor.EffectiveUserID(), scope, id, false)
		if err != nil {
			return err
		}
		setting, err := accessRequestSetting(ctx, scope, id)
		if err != nil {
			return err
		}
		if setting.Disabled || state.archived || state.member {
			return governance_model.ErrConflict
		}
		exists, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", scope, id, actor.EffectiveUserID()).Exist(new(governance_model.AccessRequest))
		if err != nil {
			return err
		}
		if exists {
			return governance_model.ErrConflict
		}
		user, err := activeActor(ctx, actor.EffectiveUserID())
		if err != nil {
			return err
		}
		result = &governance_model.AccessRequest{ScopeType: scope, ScopeID: id, UserID: user.ID, Username: user.Name, ScopePath: state.path, CreatedUnix: time.Now().Unix()}
		if err := db.Insert(ctx, result); err != nil {
			return err
		}
		return requestAudit(ctx, actor, state, result, "access_request.created")
	})
	return result, err
}

// DecideAccessRequest 使用申请稳定 ID，撤回后重建的申请不能被旧页面误批准。
func DecideAccessRequest(ctx context.Context, actor governance_model.Actor, scope string, id, requestID int64, option GroupMemberOption, approve bool) error {
	return withActorWrite(ctx, actor, []string{governance_model.Resource(scope, id)}, func(ctx context.Context) error {
		if _, err := activeActor(ctx, actor.EffectiveUserID()); err != nil {
			return err
		}
		request, has, err := db.GetByID[governance_model.AccessRequest](ctx, requestID)
		if err != nil {
			return err
		}
		if !has || request.ScopeType != scope || request.ScopeID != id {
			return governance_model.ErrNotFound
		}
		selfCancel := !approve && actor.EffectiveUserID() == request.UserID
		state, err := loadRequestScope(ctx, actor.EffectiveUserID(), scope, id, !selfCancel)
		if selfCancel && errors.Is(err, governance_model.ErrNotFound) {
			// 资源改为私有后仍允许撤回自己已知的申请，审计不向申请人透露新的私有路径。
			state = &requestScope{ownerID: id, path: request.ScopePath}
			if scope == "repository" {
				var repo *repo_model.Repository
				repo, err = repo_model.GetRepositoryByID(ctx, id)
				if err == nil {
					state.ownerID = repo.OwnerID
				}
			} else {
				_, err = governance_model.GetNamespace(ctx, id)
			}
		}
		if err != nil {
			return err
		}
		kind := "access_request.denied"
		if selfCancel {
			kind = "access_request.withdrawn"
		}
		if approve {
			if state.archived || state.revision != option.Revision {
				return governance_model.ErrConflict
			}
			exists, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", scope, id, request.UserID).Exist(new(governance_model.Membership))
			if err != nil {
				return err
			}
			if exists {
				return governance_model.ErrConflict
			}
			option.UserID = request.UserID
			if scope == "group" {
				err = SetGroupMember(ctx, actor, id, option, false)
			} else {
				err = SetRepositoryMember(ctx, actor, id, option, false)
			}
			if err != nil {
				return err
			}
			kind = "access_request.approved"
		}
		if _, err := db.GetEngine(ctx).ID(request.ID).Delete(new(governance_model.AccessRequest)); err != nil {
			return err
		}
		return requestAudit(ctx, actor, state, request, kind)
	})
}

// OwnAccessRequests 只返回当前账号的申请时快照，不泄漏失去权限后的资源新名称。
func OwnAccessRequests(ctx context.Context, userID, afterID int64) ([]*governance_model.AccessRequest, error) {
	if _, err := activeActor(ctx, userID); err != nil {
		return nil, err
	}
	if afterID < 0 {
		return nil, governance_model.ErrInvalid
	}
	requests := []*governance_model.AccessRequest{}
	err := db.GetEngine(ctx).Where("user_id = ? AND id > ?", userID, afterID).Asc("id").Limit(100).Find(&requests)
	return requests, err
}

func WithdrawOwnAccessRequest(ctx context.Context, actor governance_model.Actor, requestID int64) error {
	request, has, err := db.GetByID[governance_model.AccessRequest](ctx, requestID)
	if err != nil {
		return err
	}
	if !has || request.UserID != actor.EffectiveUserID() {
		return governance_model.ErrNotFound
	}
	return DecideAccessRequest(ctx, actor, request.ScopeType, request.ScopeID, request.ID, GroupMemberOption{}, false)
}
