// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package asymkey

import (
	"context"
	"errors"
	"fmt"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
)

// AppendPublicKeyAudit 不保存密钥正文，仅保存稳定标识、指纹与配置。
func AppendPublicKeyAudit(ctx context.Context, key *PublicKey, kind string) error {
	details, err := json.Marshal(map[string]any{"owner_id": key.OwnerID, "name": key.Name, "fingerprint": key.Fingerprint, "verified": key.Verified, "authentication_source_id": key.LoginSourceID, "key_type": key.Type})
	if err != nil {
		return err
	}
	if err := governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: kind, Actor: governance_model.AuditActor(ctx), ScopeType: "user", ScopeID: key.OwnerID, ObjectType: "ssh_key", ObjectID: key.ID, ObjectPath: key.Name, Result: "success", Details: details}); err != nil {
		return err
	}
	if kind == "credential.ssh_key_verified" {
		return nil
	}
	return governance_model.QueueSSHKeyFileSync(ctx, key.Type == KeyTypePrincipal)
}

func AppendDeployKeyAudit(ctx context.Context, key *DeployKey, kind string) error {
	var repo struct {
		OwnerID int64
		Name    string
	}
	has, err := db.GetEngine(ctx).Table("repository").Where("id = ?", key.RepoID).Get(&repo)
	if err != nil {
		return err
	}
	if !has {
		return fmt.Errorf("部署密钥所属项目不存在：%d", key.RepoID)
	}
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
		return err
	}
	ancestors := []int64{}
	ownerPath := ""
	for _, group := range chain {
		if ownerPath == "" {
			ownerPath = group.FullPath
		}
		if group.Kind == "group" {
			ancestors = append(ancestors, group.ID)
		}
	}
	if ownerPath == "" {
		var owner struct {
			Name string
			Type int
		}
		has, ownerErr := db.GetEngine(ctx).Table("user").Where("id = ?", repo.OwnerID).Get(&owner)
		if ownerErr != nil || !has {
			return governance_model.ErrNotFound
		}
		ownerPath = owner.Name
		if owner.Type == 1 {
			ancestors = append(ancestors, repo.OwnerID)
		}
	}
	details, err := json.Marshal(map[string]any{"key_id": key.KeyID, "name": key.Name, "fingerprint": key.Fingerprint, "access": key.Mode.ToString(), "repository_path": ownerPath + "/" + repo.Name})
	if err != nil {
		return err
	}
	if err := governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: kind, Actor: governance_model.AuditActor(ctx), ScopeType: "repository", ScopeID: key.RepoID, AncestorIDs: ancestors, ObjectType: "deploy_key", ObjectID: key.ID, ObjectPath: key.Name, Result: "success", Details: details}); err != nil {
		return err
	}
	return governance_model.QueueSSHKeyFileSync(ctx, false)
}

// WithKeyWrite 在读取共享引用前取得事务锁，避免最后一次撤销与新关联相互越过。
func WithKeyWrite[T any](ctx context.Context, f func(context.Context) (T, error)) (T, error) {
	var result T
	err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		var err error
		result, err = f(ctx)
		return err
	})
	return result, err
}

func sourceKeyContext(ctx context.Context) context.Context {
	actor := governance_model.AuditActor(ctx)
	if actor.Kind == "anonymous" || actor.Kind == "system" {
		actor.Kind, actor.Name, actor.Transport = "system", "认证来源密钥同步", "authentication_source"
		return governance_model.WithAuditActor(ctx, actor)
	}
	return ctx
}
