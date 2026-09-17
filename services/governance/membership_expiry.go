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
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
)

// auditGrantExpiry 由持有治理写锁的续期、撤销和到期清理共用，避免后台尚未运行时漏记旧授权。
func auditGrantExpiry(ctx context.Context, scope string, scopeID, expires int64, kind string, before any, now time.Time) error {
	if expires == 0 || expires > now.Unix() {
		return nil
	}
	ownerID, path := scopeID, ""
	if scope == "repository" {
		repo, err := repo_model.GetRepositoryByID(ctx, scopeID)
		if err != nil {
			return err
		}
		ownerID, path = repo.OwnerID, repo.FullPath()
	} else if scope != "group" {
		return governance_model.ErrInvalid
	}
	chain, err := governance_model.Ancestors(ctx, ownerID)
	if err != nil {
		return err
	}
	if path == "" {
		path = chain[0].FullPath
	}
	objectType, objectID, objectPath := scope, scopeID, path
	if member, ok := before.(*governance_model.Membership); ok {
		objectType, objectID, objectPath = "user", member.UserID, ""
		user, err := user_model.GetUserByID(ctx, member.UserID)
		if err != nil && !user_model.IsErrUserNotExist(err) {
			return err
		}
		if user != nil {
			objectPath = user.Name
		}
	}
	ancestors := make([]int64, 0, len(chain))
	for _, n := range chain {
		if n.Kind == "group" {
			ancestors = append(ancestors, n.ID)
		}
	}
	details, err := json.Marshal(map[string]any{"before": before, "scope_path": path, "effective_at": time.Unix(expires, 0).UTC(), "reason": "authorization_expired"})
	if err != nil {
		return err
	}
	if _, err := db.GetEngine(ctx).ID(ownerID).Incr("revision").Update(new(governance_model.Namespace)); err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: kind, Actor: governance_model.Actor{Kind: "system", Name: "授权到期任务", Transport: "background"}, ScopeType: scope, ScopeID: scopeID, AncestorIDs: ancestors, ObjectType: objectType, ObjectID: objectID, ObjectPath: objectPath, Result: "success", Details: details})
}

// RunExpiredGrants 权限已按时间即时失效；逐条短事务仅清理到期来源并保存证据。
func RunExpiredGrants(ctx context.Context) error {
	now := time.Now()
	var members []*governance_model.Membership
	var shares []*governance_model.Share
	if err := db.GetEngine(ctx).Where("expires_unix > 0 AND expires_unix <= ?", now.Unix()).Asc("id").Limit(100).Find(&members); err != nil {
		return err
	}
	if err := db.GetEngine(ctx).Where("expires_unix > 0 AND expires_unix <= ?", now.Unix()).Asc("id").Limit(100).Find(&shares); err != nil {
		return err
	}
	var failures []error
	for _, candidate := range members {
		err := governance_model.WithWrite(ctx, []string{governance_model.Resource(candidate.ScopeType, candidate.ScopeID), governance_model.Resource("user", candidate.UserID)}, func(ctx context.Context) error {
			member, has, err := db.GetByID[governance_model.Membership](ctx, candidate.ID)
			if err != nil || !has || member.ExpiresUnix == 0 || member.ExpiresUnix > now.Unix() {
				return err
			}
			if err := auditGrantExpiry(ctx, member.ScopeType, member.ScopeID, member.ExpiresUnix, "member.expired", member, now); err != nil {
				return err
			}
			_, err = db.GetEngine(ctx).ID(member.ID).Delete(new(governance_model.Membership))
			return err
		})
		if err != nil {
			failures = append(failures, err)
		}
	}
	for _, candidate := range shares {
		err := governance_model.WithWrite(ctx, []string{governance_model.Resource(candidate.ScopeType, candidate.ScopeID), governance_model.Resource("group", candidate.GroupID)}, func(ctx context.Context) error {
			share, has, err := db.GetByID[governance_model.Share](ctx, candidate.ID)
			if err != nil || !has || share.ExpiresUnix == 0 || share.ExpiresUnix > now.Unix() {
				return err
			}
			if err := auditGrantExpiry(ctx, share.ScopeType, share.ScopeID, share.ExpiresUnix, "share.expired", share, now); err != nil {
				return err
			}
			_, err = db.GetEngine(ctx).ID(share.ID).Delete(new(governance_model.Share))
			return err
		})
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
