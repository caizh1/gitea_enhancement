// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package packages

import (
	"context"
	"errors"
	"fmt"
	"path"

	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
)

func packageAuditScope(ctx context.Context, ownerID int64) (string, int64, []int64, string, error) {
	namespace, err := governance_model.GetNamespace(ctx, ownerID)
	if err != nil {
		if !errors.Is(err, governance_model.ErrNotFound) {
			return "", 0, nil, "", err
		}
		owner, userErr := user_model.GetUserByID(ctx, ownerID)
		if userErr != nil {
			return "", 0, nil, "", userErr
		}
		return "user", ownerID, nil, owner.Name, nil
	}
	if namespace.Kind != "group" {
		return "user", ownerID, nil, namespace.FullPath, nil
	}
	chain, err := governance_model.Ancestors(ctx, ownerID)
	if err != nil {
		return "", 0, nil, "", err
	}
	ancestors := make([]int64, 0, len(chain))
	for _, ancestor := range chain[1:] {
		if ancestor.Kind == "group" {
			ancestors = append(ancestors, ancestor.ID)
		}
	}
	return "group", ownerID, ancestors, namespace.FullPath, nil
}

// AppendVersionAudit 记录软件包版本生命周期，不包含元数据或文件内容。
func AppendVersionAudit(ctx context.Context, version *PackageVersion, eventType string) error {
	pkg, err := GetPackageByID(ctx, version.PackageID)
	if err != nil {
		return err
	}
	return appendPackageAudit(ctx, pkg, eventType, "package_version", version.ID, version.Version, map[string]any{"package_type": pkg.Type, "package_name": pkg.Name, "version": version.Version, "internal": version.IsInternal})
}

// AppendVersionTagAudit 记录公开分发标签与版本的关联，不包含包正文。
func AppendVersionTagAudit(ctx context.Context, version *PackageVersion, tag, action string) error {
	pkg, err := GetPackageByID(ctx, version.PackageID)
	if err != nil {
		return err
	}
	return appendPackageAudit(ctx, pkg, "package.tag_changed", "package_version", version.ID, version.Version, map[string]any{"package_type": pkg.Type, "package_name": pkg.Name, "version": version.Version, "tag": tag, "action": action})
}

// AppendFileAudit 记录软件包文件删除，不包含文件正文、摘要或元数据属性。
func AppendFileAudit(ctx context.Context, file *PackageFile, eventType string) error {
	version, err := GetVersionByID(ctx, file.VersionID)
	if err != nil {
		return err
	}
	pkg, err := GetPackageByID(ctx, version.PackageID)
	if err != nil {
		return err
	}
	return appendPackageAudit(ctx, pkg, eventType, "package_file", file.ID, file.Name, map[string]any{"package_type": pkg.Type, "package_name": pkg.Name, "version": version.Version, "file_name": file.Name})
}

// AppendDownloadAccessAudit 在存储已成功打开后记录已认证的软件包读取；不记录下载 URL、查询参数或摘要。
func AppendDownloadAccessAudit(ctx context.Context, file *PackageFile) error {
	actor := governance_model.AuditActor(ctx)
	if actor.ID <= 0 && actor.CredentialID <= 0 {
		return nil
	}
	version, err := GetVersionByID(ctx, file.VersionID)
	if err != nil {
		return err
	}
	pkg, err := GetPackageByID(ctx, version.PackageID)
	if err != nil {
		return err
	}
	scopeType, scopeID, ancestors, ownerPath, err := packageAuditScope(ctx, pkg.OwnerID)
	if err != nil {
		return err
	}
	details, err := json.Marshal(map[string]any{"phase": "content_opened", "package_type": pkg.Type, "package_name": pkg.Name, "version": version.Version, "file_name": file.Name})
	if err != nil {
		return err
	}
	_, err = governance_model.AppendConfiguredAccessAudit(ctx, &governance_model.AuditEvent{Type: "access.package_download", Actor: actor, ScopeType: scopeType, ScopeID: scopeID, AncestorIDs: ancestors, ObjectType: "package_file", ObjectID: file.ID, ObjectPath: path.Join(ownerPath, "packages", fmt.Sprintf("package_file-%d-%s", file.ID, file.Name)), Result: "success", Details: details})
	return err
}

func appendPackageAudit(ctx context.Context, pkg *Package, eventType, objectType string, objectID int64, objectName string, details map[string]any) error {
	scopeType, scopeID, ancestors, ownerPath, err := packageAuditScope(ctx, pkg.OwnerID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: eventType, Actor: governance_model.AuditActor(ctx), ScopeType: scopeType, ScopeID: scopeID, AncestorIDs: ancestors, ObjectType: objectType, ObjectID: objectID, ObjectPath: path.Join(ownerPath, "packages", fmt.Sprintf("%s-%d-%s", objectType, objectID, objectName)), Result: "success", Details: raw})
}

func appendCleanupRuleAudit(ctx context.Context, rule *PackageCleanupRule, eventType string, changed []string) error {
	pkg := &Package{OwnerID: rule.OwnerID}
	return appendPackageAudit(ctx, pkg, eventType, "package_cleanup_rule", rule.ID, string(rule.Type), map[string]any{"package_type": rule.Type, "enabled": rule.Enabled, "keep_count": rule.KeepCount, "remove_days": rule.RemoveDays, "match_full_name": rule.MatchFullName, "keep_pattern": rule.KeepPattern, "remove_pattern": rule.RemovePattern, "changed_fields": changed})
}
