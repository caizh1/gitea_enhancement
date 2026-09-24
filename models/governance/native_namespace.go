// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"strings"

	"gitea.dev/models/db"
	"gitea.dev/modules/json"
)

// RegisterNativeNamespace 接入原生创建事务；旧版合法名称继续保留，新子组另行校验。
func RegisterNativeNamespace(ctx context.Context, n *Namespace) error {
	if n.ID <= 0 || n.ParentID != 0 || n.Slug == "" || len(n.Slug) > 100 || strings.ContainsAny(n.Slug, "/\\") || (n.Kind != "user" && n.Kind != "group") {
		return ErrInvalid
	}
	return WithWrite(ctx, nil, func(ctx context.Context) error {
		n.FullPath, n.LowerPath, n.LowerSlug, n.Revision = n.Slug, strings.ToLower(n.Slug), strings.ToLower(n.Slug), 1
		if err := reservePath(ctx, n.FullPath, n.Kind, n.ID); err != nil {
			return err
		}
		return db.Insert(ctx, n)
	})
}

// RegisterNativeRepository 与仓库记录共用事务，群组和项目共享同一地址空间。
func RegisterNativeRepository(ctx context.Context, id, ownerID int64, name string) (string, error) {
	var ownerPath string
	err := WithWrite(ctx, []string{Resource("group", ownerID)}, func(ctx context.Context) error {
		n, err := GetNamespace(ctx, ownerID)
		if err == ErrNotFound {
			n, err = registerExistingNativeOwner(ctx, ownerID)
		}
		if err != nil {
			return err
		}
		if n.Archived || n.DeleteAfter != 0 {
			return ErrConflict
		}
		ownerPath = n.FullPath
		if err := reservePath(ctx, n.FullPath+"/"+name, "repository", id); err != nil {
			return err
		}
		var owner struct{ Name string }
		if _, err := db.GetEngine(ctx).Table("user").Where("id = ?", ownerID).Get(&owner); err != nil {
			return err
		}
		if !strings.EqualFold(owner.Name, n.FullPath) {
			return ReserveNativeAlias(ctx, owner.Name+"/"+name, "repository", id)
		}
		return nil
	})
	return ownerPath, err
}

func registerExistingNativeOwner(ctx context.Context, id int64) (*Namespace, error) {
	var user struct {
		ID         int64
		Name       string
		Type       int
		Visibility int
	}
	has, err := db.GetEngine(ctx).Table("user").Where("id = ?", id).Get(&user)
	if err != nil {
		return nil, err
	}
	if !has || (user.Type != 0 && user.Type != 1 && user.Type != 4) {
		return nil, ErrNotFound
	}
	n := &Namespace{ID: id, Slug: user.Name, Kind: "user", Visibility: user.Visibility}
	if user.Type == 1 {
		n.Kind = "group"
		var team struct{ ID int64 }
		if _, err := db.GetEngine(ctx).Table("team").Where("org_id = ? AND lower_name = ?", id, "owners").Get(&team); err != nil {
			return nil, err
		}
		n.NativeOwnerTeamID = team.ID
	}
	if err := RegisterNativeNamespace(ctx, n); err != nil {
		return nil, err
	}
	return n, SyncNativeNamespacePath(ctx, n)
}

// 迁移视图只增加公开路径列，不改动原生用户名称和仓库物理位置。
type nativeNamespaceUser struct {
	ID            int64  `xorm:"pk"`
	NamespacePath string `xorm:"VARCHAR(2048) NOT NULL DEFAULT ''"`
}

func (*nativeNamespaceUser) TableName() string { return "user" }

type nativeNamespaceRepo struct {
	ID             int64  `xorm:"pk"`
	OwnerNamespace string `xorm:"VARCHAR(2048) NOT NULL DEFAULT ''"`
}

func (*nativeNamespaceRepo) TableName() string { return "repository" }

func AddNativeNamespacePaths(engine db.EngineMigration) error {
	if err := AddPathHashes(engine); err != nil {
		return err
	}
	if err := engine.Sync(new(nativeNamespaceUser), new(nativeNamespaceRepo)); err != nil {
		return err
	}
	var owners []struct {
		ID    int64
		OrgID int64
	}
	if err := engine.Table("team").Select("id, org_id").Where("lower_name = ?", "owners").Find(&owners); err != nil {
		return err
	}
	for _, owner := range owners {
		if _, err := engine.ID(owner.OrgID).Cols("native_owner_team_id").Update(&Namespace{NativeOwnerTeamID: owner.ID}); err != nil {
			return err
		}
	}
	var cursor int64
	for {
		var namespaces []*Namespace
		if err := engine.Where("id > ?", cursor).Asc("id").Limit(1000).Find(&namespaces); err != nil {
			return err
		}
		for _, namespace := range namespaces {
			cursor = namespace.ID
			if _, err := engine.Table("user").Where("id = ?", namespace.ID).Update(map[string]any{"namespace_path": namespace.FullPath}); err != nil {
				return err
			}
			if _, err := engine.Table("repository").Where("owner_id = ?", namespace.ID).Update(map[string]any{"owner_namespace": namespace.FullPath}); err != nil {
				return err
			}
		}
		if len(namespaces) < 1000 {
			return nil
		}
	}
}

// SyncNativeNamespacePath 与路径变更共用事务，只同步公开路径，禁止搬迁仓库目录。
func SyncNativeNamespacePath(ctx context.Context, namespace *Namespace) error {
	if _, err := db.GetEngine(ctx).Table("user").Where("id = ?", namespace.ID).Update(map[string]any{"namespace_path": namespace.FullPath}); err != nil {
		return err
	}
	_, err := db.GetEngine(ctx).Table("repository").Where("owner_id = ?", namespace.ID).Update(map[string]any{"owner_namespace": namespace.FullPath})
	return err
}

// DeleteNativeNamespace 仅用于已完成原生永久删除检查的同一事务，审计和旧别名继续保留。
func DeleteNativeNamespace(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrInvalid
	}
	return WithWrite(ctx, []string{Resource("group", id), Resource("user", id)}, func(ctx context.Context) error {
		children, err := db.GetEngine(ctx).Where("parent_id = ?", id).Exist(new(Namespace))
		if err != nil {
			return err
		}
		if children {
			return ErrConflict
		}
		pending, err := db.GetEngine(ctx).Where("kind = ? AND resource_id = ?", "group", id).Exist(new(ResourceCleanup))
		if err != nil {
			return err
		}
		if !pending {
			if _, err := db.GetEngine(ctx).Where("resource_id = ? AND kind IN (?, ?) AND alias = ?", id, "user", "group", false).Delete(new(ResourcePath)); err != nil {
				return err
			}
		}
		if err := db.DeleteBeans(ctx, &Invitation{ScopeType: "group", ScopeID: id}, &AccessRequest{ScopeType: "group", ScopeID: id}, &AccessRequestSetting{ScopeType: "group", ScopeID: id}, &GroupDeletion{GroupID: id}, &Membership{ScopeType: "group", ScopeID: id}, &Share{ScopeType: "group", ScopeID: id}, &Share{GroupID: id}, &CustomRole{RootID: id}, &ApprovalRule{ScopeType: "group", ScopeID: id}, &ApprovalSettings{ScopeType: "group", ScopeID: id}); err != nil {
			return err
		}
		_, err = db.GetEngine(ctx).ID(id).Delete(new(Namespace))
		return err
	})
}

// PrepareNativeRepositoryPath 在文件系统改名前先占用目标地址，防止子组抢占同名路径。
// 中断留下的地址仍指向原仓库，不允许误指向另一个对象。
func PrepareNativeRepositoryPath(ctx context.Context, id, ownerID int64, name string) error {
	return WithWrite(ctx, []string{Resource("repository", id), Resource("group", ownerID)}, func(ctx context.Context) error {
		if err := checkNativeRepositoryPathSource(ctx, id); err != nil {
			return err
		}
		n, err := GetNamespace(ctx, ownerID)
		if err == ErrNotFound {
			n, err = registerExistingNativeOwner(ctx, ownerID)
		}
		if err != nil {
			return err
		}
		if n.Archived || n.DeleteAfter != 0 {
			return ErrConflict
		}
		path := strings.ToLower(n.FullPath + "/" + name)
		if err := reservePath(ctx, path, "repository", id); err != nil {
			return err
		}
		_, err = db.GetEngine(ctx).Where("path_hash = ? AND path = ?", pathHash(path), path).Cols("alias").Update(&ResourcePath{Alias: true})
		return err
	})
}

// ChangeNativeRepositoryPath 与原生名称或归属更新共用事务。
func ChangeNativeRepositoryPath(ctx context.Context, id, ownerID int64, name string) (string, error) {
	var path string
	err := WithWrite(ctx, []string{Resource("repository", id), Resource("group", ownerID)}, func(ctx context.Context) error {
		if err := checkNativeRepositoryPathSource(ctx, id); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).Where("kind = ? AND resource_id = ?", "repository", id).Cols("alias").Update(&ResourcePath{Alias: true}); err != nil {
			return err
		}
		var err error
		path, err = RegisterNativeRepository(ctx, id, ownerID, name)
		return err
	})
	return path, err
}

// SyncNativeVisibility 在原生可见性更新事务中维护同一层级约束。
func SyncNativeVisibility(ctx context.Context, id int64, visibility int) error {
	return WithWrite(ctx, []string{Resource("group", id)}, func(ctx context.Context) error {
		n, err := GetNamespace(ctx, id)
		if err == ErrNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		if visibility < 0 || visibility > 2 {
			return ErrInvalid
		}
		if n.ParentID != 0 {
			parent, err := GetNamespace(ctx, n.ParentID)
			if err != nil {
				return err
			}
			if visibility < parent.Visibility {
				return ErrConflict
			}
		}
		children, err := db.GetEngine(ctx).Where("parent_id = ? AND visibility < ?", id, visibility).Exist(new(Namespace))
		if err != nil {
			return err
		}
		if children {
			return ErrConflict
		}
		if n.Visibility == visibility {
			return nil
		}
		n.Visibility, n.Revision = visibility, n.Revision+1
		_, err = db.GetEngine(ctx).ID(id).Cols("visibility", "revision").Update(n)
		return err
	})
}

// RenameNativePersonalNamespace 与原生个人账号改名共用事务，组织改名使用 MoveNamespace。
func RenameNativePersonalNamespace(ctx context.Context, id int64, name string, actor Actor) error {
	return WithWrite(ctx, []string{Resource("user", id), Resource("group", id)}, func(ctx context.Context) error {
		n, err := GetNamespace(ctx, id)
		if err == ErrNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		if n.Kind != "user" || n.ParentID != 0 || name == "" || len(name) > 100 || strings.ContainsAny(name, "/\\") {
			return ErrInvalid
		}
		old := n.FullPath
		escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(n.LowerPath) + "/%"
		var paths []*ResourcePath
		if err := db.GetEngine(ctx).Where("alias = ? AND (path = ? OR path LIKE ? ESCAPE '!')", false, n.LowerPath, escaped).Find(&paths); err != nil {
			return err
		}
		resources := make([]string, 0, len(paths))
		for _, path := range paths {
			resources = append(resources, Resource(path.Kind, path.ResourceID))
		}
		return WithWrite(ctx, resources, func(ctx context.Context) error {
			for _, path := range paths {
				if _, err := db.GetEngine(ctx).Where("path_hash = ? AND path = ?", pathHash(path.Path), path.Path).Cols("alias").Update(&ResourcePath{Alias: true}); err != nil {
					return err
				}
			}
			for _, path := range paths {
				if err := reservePath(ctx, strings.ToLower(name)+path.Path[len(n.LowerPath):], path.Kind, path.ResourceID); err != nil {
					return err
				}
			}
			n.Slug, n.FullPath, n.LowerSlug, n.LowerPath, n.Revision = name, name, strings.ToLower(name), strings.ToLower(name), n.Revision+1
			n.LowerPathHash = pathHash(n.LowerPath)
			if _, err := db.GetEngine(ctx).ID(id).Cols("slug", "full_path", "lower_slug", "lower_path", "lower_path_hash", "revision").Update(n); err != nil {
				return err
			}
			if err := SyncNativeNamespacePath(ctx, n); err != nil {
				return err
			}
			data, err := json.Marshal(map[string]string{"before": old, "after": name})
			if err != nil {
				return err
			}
			return AppendAudit(ctx, &AuditEvent{Type: "user.renamed", Actor: actor, ScopeType: "user", ScopeID: id, ObjectType: "user", ObjectID: id, ObjectPath: name, Result: "success", Details: data})
		})
	})
}

// ReserveNativeAlias 保留兼容地址的身份，防止内部名称日后被重新分配。
func ReserveNativeAlias(ctx context.Context, path, kind string, id int64) error {
	return WithWrite(ctx, nil, func(ctx context.Context) error {
		if err := reservePath(ctx, path, kind, id); err != nil {
			return err
		}
		canonical := strings.ToLower(path)
		_, err := db.GetEngine(ctx).Where("path_hash = ? AND path = ?", pathHash(canonical), canonical).Cols("alias").Update(&ResourcePath{Alias: true})
		return err
	})
}

func checkNativeRepositoryPathSource(ctx context.Context, id int64) error {
	var source struct {
		OwnerID    int64
		IsArchived bool
	}
	has, err := db.GetEngine(ctx).Table("repository").Where("id = ?", id).Get(&source)
	if err != nil {
		return err
	}
	if !has {
		return ErrNotFound
	}
	if source.IsArchived {
		return ErrConflict
	}
	owner, err := GetNamespace(ctx, source.OwnerID)
	if err == ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if owner.Archived || owner.DeleteAfter != 0 {
		return ErrConflict
	}
	return nil
}
