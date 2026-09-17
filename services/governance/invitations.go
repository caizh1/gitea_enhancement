// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/secret"
	"gitea.dev/modules/setting"
)

type InvitationOption struct {
	Email        string                `json:"email"`
	Role         governance_model.Role `json:"role"`
	CustomRoleID int64                 `json:"custom_role_id"`
	ExpiresUnix  int64                 `json:"expires_unix"`
	Revision     int64                 `json:"revision"`
}

func invitationAuthority(ctx context.Context, actor governance_model.Actor, scope string, id int64, option GroupMemberOption) (*requestScope, error) {
	if err := checkDelegation(ctx, actor); err != nil {
		return nil, err
	}
	state, err := loadRequestScope(ctx, actor.EffectiveUserID(), scope, id, true)
	if err != nil {
		return nil, err
	}
	if state.archived {
		return nil, governance_model.ErrConflict
	}
	var ceiling governance_model.Abilities
	if scope == "group" {
		group, err := CheckGroupAccess(ctx, actor.EffectiveUserID(), id, governance_model.ManageGroupMembers)
		if err != nil {
			return nil, err
		}
		ceiling = group.Abilities
	} else {
		_, abilities, err := repositoryMemberManager(ctx, actor.EffectiveUserID(), id)
		if err != nil {
			return nil, err
		}
		ceiling = abilities
	}
	if err := checkMemberRole(ctx, scope, state.ownerID, option, ceiling); err != nil {
		return nil, err
	}
	return state, nil
}

func invitationAudit(ctx context.Context, actor governance_model.Actor, state *requestScope, invitation *governance_model.Invitation, kind string, userID int64) error {
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
	details, err := json.Marshal(map[string]any{"invitation": invitation, "scope_path": state.path})
	if err != nil {
		return err
	}
	objectType, objectID, objectPath := "invitation", invitation.ID, invitation.Email
	if userID > 0 {
		objectType, objectID = "user", userID
		user, err := user_model.GetUserByID(ctx, userID)
		if err != nil {
			return err
		}
		objectPath = user.Name
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: kind, Actor: actor, ScopeType: invitation.ScopeType, ScopeID: invitation.ScopeID, AncestorIDs: ancestors, ObjectType: objectType, ObjectID: objectID, ObjectPath: objectPath, Result: "success", Details: details})
}

func CreateInvitation(ctx context.Context, actor governance_model.Actor, scope string, id int64, option InvitationOption) (*governance_model.Invitation, error) {
	email := strings.TrimSpace(option.Email)
	if len(email) > 254 || user_model.ValidateEmail(email) != nil {
		return nil, governance_model.ErrInvalid
	}
	if setting.MailService == nil || setting.SecretKey == "" {
		return nil, fmt.Errorf("%w：邮件服务或实例密钥尚未配置", governance_model.ErrConflict)
	}
	member := GroupMemberOption{Role: option.Role, CustomRoleID: option.CustomRoleID, ExpiresUnix: option.ExpiresUnix}
	var result *governance_model.Invitation
	err := withActorWrite(ctx, actor, []string{governance_model.Resource(scope, id)}, func(ctx context.Context) error {
		state, err := invitationAuthority(ctx, actor, scope, id, member)
		if err != nil {
			return err
		}
		if state.revision != option.Revision {
			return governance_model.ErrConflict
		}
		exists, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND lower_email = ?", scope, id, strings.ToLower(email)).Exist(new(governance_model.Invitation))
		if err != nil {
			return err
		}
		if exists {
			return governance_model.ErrConflict
		}
		token := make([]byte, 32)
		if _, err := rand.Read(token); err != nil {
			return err
		}
		plaintext := hex.EncodeToString(token)
		hash := sha256.Sum256([]byte(plaintext))
		encrypted, err := secret.EncryptSecret(setting.SecretKey, plaintext)
		if err != nil {
			return err
		}
		now := time.Now()
		result = &governance_model.Invitation{ScopeType: scope, ScopeID: id, Email: email, LowerEmail: strings.ToLower(email), ScopePath: state.path, Inviter: actor, Role: option.Role, CustomRoleID: option.CustomRoleID, MembershipExpiresUnix: option.ExpiresUnix, CreatedUnix: now.Unix(), ExpiresUnix: now.Add(90 * 24 * time.Hour).Unix(), TokenHash: hex.EncodeToString(hash[:]), TokenEncrypted: encrypted, NextDeliveryUnix: now.Unix(), DeliveryState: "pending"}
		if err := db.Insert(ctx, result); err != nil {
			return err
		}
		return invitationAudit(ctx, actor, state, result, "invitation.created", 0)
	})
	return result, err
}

// recipientInvitation 同时要求已登录账号、已验证邮箱和邮件令牌，不能仅凭链接获取权限。
func recipientInvitation(ctx context.Context, userID, id int64, token string) (*governance_model.Invitation, error) {
	if _, err := activeActor(ctx, userID); err != nil {
		return nil, err
	}
	if len(token) != 64 {
		return nil, governance_model.ErrNotFound
	}
	invitation, has, err := db.GetByID[governance_model.Invitation](ctx, id)
	if err != nil {
		return nil, err
	}
	if !has || invitation.ExpiresUnix <= time.Now().Unix() {
		return nil, governance_model.ErrNotFound
	}
	digest := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare([]byte(invitation.TokenHash), []byte(hex.EncodeToString(digest[:]))) != 1 {
		return nil, governance_model.ErrNotFound
	}
	verified, err := db.GetEngine(ctx).Where("uid = ? AND lower_email = ? AND is_activated = ?", userID, invitation.LowerEmail, true).Exist(new(user_model.EmailAddress))
	if err != nil {
		return nil, err
	}
	if !verified {
		return nil, governance_model.ErrNotFound
	}
	return invitation, nil
}

func AcceptInvitation(ctx context.Context, actor governance_model.Actor, id int64, token string) error {
	// 邮件是个人接受动作，管理员代办不能替代邮箱持有者的决定。
	if actor.ActingAsID > 0 {
		return governance_model.ErrNotFound
	}
	invitation, err := recipientInvitation(ctx, actor.ID, id, token)
	if err != nil {
		return err
	}
	return withActorWrite(ctx, actor, []string{governance_model.Resource(invitation.ScopeType, invitation.ScopeID), governance_model.Resource("user", actor.ID)}, func(ctx context.Context) error {
		invitation, err := recipientInvitation(ctx, actor.ID, id, token)
		if err != nil {
			return err
		}

		// 条件更新同时重新核对邮箱并持有行锁，原生删除或撤销验证必须等本事务结束。
		matched, err := db.GetEngine(ctx).Where("uid = ? AND lower_email = ? AND is_activated = ?", actor.ID, invitation.LowerEmail, true).Cols("is_activated").Update(&user_model.EmailAddress{IsActivated: true})
		if err != nil {
			return err
		}
		if matched != 1 {
			return governance_model.ErrNotFound
		}
		option := GroupMemberOption{UserID: actor.ID, Role: invitation.Role, CustomRoleID: invitation.CustomRoleID, ExpiresUnix: invitation.MembershipExpiresUnix}
		state, err := invitationAuthority(ctx, invitation.Inviter, invitation.ScopeType, invitation.ScopeID, option)
		if err != nil {
			return err
		}
		exists, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", invitation.ScopeType, invitation.ScopeID, actor.ID).Exist(new(governance_model.Membership))
		if err != nil {
			return err
		}
		if exists {
			return governance_model.ErrConflict
		}
		option.Revision = state.revision
		if invitation.ScopeType == "group" {
			err = setGroupMember(ctx, invitation.Inviter, actor, invitation.ScopeID, option, false)
		} else {
			err = setRepositoryMember(ctx, invitation.Inviter, actor, invitation.ScopeID, option, false)
		}
		if err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).ID(invitation.ID).Delete(new(governance_model.Invitation)); err != nil {
			return err
		}

		var pending governance_model.AccessRequest
		has, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", invitation.ScopeType, invitation.ScopeID, actor.ID).Get(&pending)
		if err != nil {
			return err
		}
		if has {
			if err := requestAudit(ctx, actor, state, &pending, "access_request.fulfilled"); err != nil {
				return err
			}
			if _, err := db.GetEngine(ctx).ID(pending.ID).Delete(new(governance_model.AccessRequest)); err != nil {
				return err
			}
		}
		return invitationAudit(ctx, actor, state, invitation, "invitation.accepted", actor.ID)
	})
}

type InvitationPage struct {
	Roles       []*governance_model.CustomRole `json:"roles"`
	FullPath    string                         `json:"full_path"`
	Revision    int64                          `json:"revision"`
	Archived    bool                           `json:"archived"`
	Invitations []*governance_model.Invitation `json:"invitations"`
	NextID      int64                          `json:"next_id,omitempty"`
}

func ListInvitations(ctx context.Context, userID int64, scope string, id, afterID int64) (*InvitationPage, error) {
	if afterID < 0 {
		return nil, governance_model.ErrInvalid
	}
	state, err := loadRequestScope(ctx, userID, scope, id, true)
	if err != nil {
		return nil, err
	}
	result := &InvitationPage{FullPath: state.path, Revision: state.revision, Archived: state.archived, Invitations: []*governance_model.Invitation{}}
	if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND id > ?", scope, id, afterID).Asc("id").Limit(101).Find(&result.Invitations); err != nil {
		return nil, err
	}
	if len(result.Invitations) > 100 {
		result.Invitations = result.Invitations[:100]
		result.NextID = result.Invitations[99].ID
	}
	for _, invitation := range result.Invitations {
		invitation.Inviter.IP = ""
		invitation.Inviter.RequestID = ""
		invitation.Inviter.CredentialID = 0
	}
	chain, err := governance_model.Ancestors(ctx, state.ownerID)
	if err != nil {
		return nil, err
	}
	if err := db.GetEngine(ctx).Where("root_id = ?", chain[len(chain)-1].ID).Asc("id").Find(&result.Roles); err != nil {
		return nil, err
	}
	return result, nil
}

type InvitationPreview struct {
	ID                    int64                 `json:"id"`
	ScopePath             string                `json:"scope_path"`
	Email                 string                `json:"email"`
	Role                  governance_model.Role `json:"role"`
	CustomRoleID          int64                 `json:"custom_role_id"`
	ExpiresUnix           int64                 `json:"expires_unix"`
	MembershipExpiresUnix int64                 `json:"membership_expires_unix"`
	InviterName           string                `json:"inviter_name"`
}

func PreviewInvitation(ctx context.Context, userID, id int64, token string) (*InvitationPreview, error) {
	invitation, err := recipientInvitation(ctx, userID, id, token)
	if err != nil {
		return nil, err
	}
	name := invitation.Inviter.Name
	if invitation.Inviter.ActingAsID > 0 {
		name = invitation.Inviter.ActingAsName
	}
	return &InvitationPreview{ID: id, ScopePath: invitation.ScopePath, Email: invitation.Email, Role: invitation.Role, CustomRoleID: invitation.CustomRoleID, ExpiresUnix: invitation.ExpiresUnix, MembershipExpiresUnix: invitation.MembershipExpiresUnix, InviterName: name}, nil
}

func invitationResource(ctx context.Context, invitation *governance_model.Invitation) (*requestScope, error) {
	state := &requestScope{ownerID: invitation.ScopeID}
	if invitation.ScopeType == "repository" {
		repo, err := repo_model.GetRepositoryByID(ctx, invitation.ScopeID)
		if err != nil {
			return nil, err
		}
		state.ownerID, state.path = repo.OwnerID, repo.FullPath()
	} else {
		group, err := governance_model.GetNamespace(ctx, invitation.ScopeID)
		if err != nil {
			return nil, err
		}
		state.path = group.FullPath
	}
	return state, nil
}

func RevokeInvitation(ctx context.Context, actor governance_model.Actor, scope string, scopeID, id int64) error {
	return withActorWrite(ctx, actor, []string{governance_model.Resource(scope, scopeID)}, func(ctx context.Context) error {
		state, err := loadRequestScope(ctx, actor.EffectiveUserID(), scope, scopeID, true)
		if err != nil {
			return err
		}
		invitation, has, err := db.GetByID[governance_model.Invitation](ctx, id)
		if err != nil {
			return err
		}
		if !has || invitation.ScopeType != scope || invitation.ScopeID != scopeID {
			return governance_model.ErrNotFound
		}
		if _, err := db.GetEngine(ctx).ID(id).Delete(new(governance_model.Invitation)); err != nil {
			return err
		}
		return invitationAudit(ctx, actor, state, invitation, "invitation.revoked", 0)
	})
}

func DeclineInvitation(ctx context.Context, actor governance_model.Actor, id int64, token string) error {
	if actor.ActingAsID > 0 {
		return governance_model.ErrNotFound
	}
	invitation, err := recipientInvitation(ctx, actor.ID, id, token)
	if err != nil {
		return err
	}
	return withActorWrite(ctx, actor, []string{governance_model.Resource(invitation.ScopeType, invitation.ScopeID)}, func(ctx context.Context) error {
		invitation, err := recipientInvitation(ctx, actor.ID, id, token)
		if err != nil {
			return err
		}
		state, err := invitationResource(ctx, invitation)
		if err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).ID(id).Delete(new(governance_model.Invitation)); err != nil {
			return err
		}
		return invitationAudit(ctx, actor, state, invitation, "invitation.declined", actor.ID)
	})
}

// InvitationTokenOption 仅用于请求正文，不能通过查询参数或请求路径传递邀请凭据。
type InvitationTokenOption struct {
	Token string `json:"token"`
}
