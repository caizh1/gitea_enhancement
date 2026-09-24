// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gitea.dev/models/db"
	"gitea.dev/modules/json"

	"xorm.io/builder"
)

const MaxDepth = 20

var validSlug = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,99}$`)

func ValidateSlug(slug string) error {
	if !validSlug.MatchString(slug) || strings.HasSuffix(strings.ToLower(slug), ".git") || strings.HasSuffix(slug, ".") {
		return fmt.Errorf("%w：路径段必须为 1—100 个字母、数字、点、下划线或连字符，不能以点或 .git 结尾", ErrInvalid)
	}
	return nil
}

func GetNamespace(ctx context.Context, id int64) (*Namespace, error) {
	n, has, err := db.GetByID[Namespace](ctx, id)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrNotFound
	}
	return n, nil
}

// Ancestors 返回当前命名空间到根的有序链，只读取当前数据库，不使用权限缓存。
func Ancestors(ctx context.Context, id int64) ([]*Namespace, error) {
	n, err := GetNamespace(ctx, id)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(n.LowerPath, "/")
	if len(parts) > MaxDepth || len(parts) == 0 {
		return nil, ErrInvalid
	}
	paths := make([]string, len(parts))
	for i := range parts {
		paths[i] = strings.Join(parts[:i+1], "/")
	}
	var rows []*Namespace
	if err := db.GetEngine(ctx).In("lower_path", paths).Find(&rows); err != nil {
		return nil, err
	}
	byPath := make(map[string]*Namespace, len(rows))
	for _, row := range rows {
		byPath[row.LowerPath] = row
	}
	result := make([]*Namespace, 0, len(parts))
	parent := int64(0)
	for _, path := range paths {
		row := byPath[path]
		if row == nil || row.ParentID != parent || (parent != 0 && row.Kind != "group") {
			return nil, fmt.Errorf("%w：命名空间路径与父子关系不一致", ErrConflict)
		}
		parent = row.ID
		result = append(result, row)
	}
	if parent != id {
		return nil, ErrConflict
	}
	slices.Reverse(result)
	return result, nil
}

func reservePath(ctx context.Context, path, kind string, id int64) error {
	var old ResourcePath
	path = strings.ToLower(path)
	has, err := db.GetEngine(ctx).Where("path_hash = ?", pathHash(path)).Get(&old)
	if err != nil {
		return err
	}
	if has {
		if old.Path != path || old.Kind != kind || old.ResourceID != id {
			return fmt.Errorf("%w：路径或历史别名已被占用", ErrConflict)
		}
		_, err = db.GetEngine(ctx).Where("path_hash = ? AND path = ?", pathHash(path), path).Cols("alias").Update(&ResourcePath{Alias: false})
		return err
	}
	return db.Insert(ctx, &ResourcePath{Path: path, Kind: kind, ResourceID: id})
}

// InsertNamespace 由创建用户或组织的同一事务调用，不能独立产生无主命名空间。
func InsertNamespace(ctx context.Context, n *Namespace) error {
	if n.ID <= 0 || (n.Kind != "user" && n.Kind != "group") || n.Visibility < 0 || n.Visibility > 2 {
		return ErrInvalid
	}
	if err := ValidateSlug(n.Slug); err != nil {
		return err
	}
	return WithWrite(ctx, []string{Resource("group", n.ParentID)}, func(ctx context.Context) error {
		n.FullPath = n.Slug
		if n.ParentID != 0 {
			parents, err := Ancestors(ctx, n.ParentID)
			if err != nil {
				return err
			}
			if n.Kind != "group" || parents[0].Kind != "group" || len(parents) >= MaxDepth {
				return ErrInvalid
			}
			for _, parent := range parents {
				if parent.Archived || parent.DeleteAfter != 0 || parent.ID == n.ID || n.Visibility < parent.Visibility {
					return ErrConflict
				}
			}
			n.FullPath = parents[0].FullPath + "/" + n.Slug
		}
		n.LowerSlug, n.LowerPath = strings.ToLower(n.Slug), strings.ToLower(n.FullPath)
		n.Revision = 1
		if err := reservePath(ctx, n.FullPath, n.Kind, n.ID); err != nil {
			return err
		}
		return db.Insert(ctx, n)
	})
}

func ResolvePath(ctx context.Context, path string) (*ResourcePath, error) {
	if path == "" || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return nil, ErrInvalid
	}
	var result ResourcePath
	canonical := strings.ToLower(path)
	has, err := db.GetEngine(ctx).Where("path_hash = ?", pathHash(canonical)).Get(&result)
	if err != nil {
		return nil, err
	}
	if !has || result.Path != canonical {
		return nil, ErrNotFound
	}
	return &result, nil
}

// MoveNamespace 原子切换整棵树的规范路径；旧地址作为受权限检查的别名保留。
// authorize 在同一事务内校验来源与目标权限，调用方不能在事务外提前授权。
func MoveNamespace(ctx context.Context, id, parentID, revision int64, slug string, actor Actor, authorize func(context.Context, *Namespace, *Namespace) error) error {
	return moveNamespace(ctx, id, parentID, revision, slug, actor, authorize, false)
}

// RenameNamespaceForDeletion 仅在删除生命周期事务中复用路径切换，不允许改变父级。
func RenameNamespaceForDeletion(ctx context.Context, id, revision int64, slug string, actor Actor, authorize func(context.Context, *Namespace, *Namespace) error) error {
	return WithWrite(ctx, nil, func(ctx context.Context) error {
		n, err := GetNamespace(ctx, id)
		if err != nil {
			return err
		}
		return moveNamespace(ctx, id, n.ParentID, revision, slug, actor, authorize, true)
	})
}

func moveNamespace(ctx context.Context, id, parentID, revision int64, slug string, actor Actor, authorize func(context.Context, *Namespace, *Namespace) error, lifecycle bool) error {
	if err := ValidateSlug(slug); err != nil {
		return err
	}
	if authorize == nil || id <= 0 || parentID < 0 {
		return ErrInvalid
	}
	return WithWrite(ctx, nil, func(ctx context.Context) error {
		root, err := GetNamespace(ctx, id)
		if err != nil {
			return err
		}
		if root.Kind != "group" || root.Revision != revision || !lifecycle && (root.DeleteAfter != 0 || root.Archived) {
			return ErrConflict
		}
		var parent *Namespace
		newPath := slug
		if parentID != 0 {
			chain, err := Ancestors(ctx, parentID)
			if err != nil {
				return err
			}
			parent = chain[0]
			for _, item := range chain {
				if item.ID == id || item.Kind != "group" || !lifecycle && (item.Archived || item.DeleteAfter != 0) || root.Visibility < item.Visibility {
					return ErrConflict
				}
			}
			newPath = parent.FullPath + "/" + slug
		}
		if err := authorize(ctx, root, parent); err != nil {
			return err
		}
		prefix := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(root.LowerPath) + "/%"
		var descendants []*Namespace
		if err := db.GetEngine(ctx).Where("lower_path = ? OR lower_path LIKE ? ESCAPE '!'", root.LowerPath, prefix).Find(&descendants); err != nil {
			return err
		}
		var paths []*ResourcePath
		if err := db.GetEngine(ctx).Where("alias = ?", false).
			And(builder.Expr("path = ? OR path LIKE ? ESCAPE '!'", root.LowerPath, prefix)).Find(&paths); err != nil {
			return err
		}
		resources := []string{Resource("group", parentID)}
		for _, path := range paths {
			resources = append(resources, Resource(path.Kind, path.ResourceID))
		}
		return WithWrite(ctx, resources, func(ctx context.Context) error {
			oldPath := root.FullPath
			for _, child := range descendants {
				path := newPath + child.FullPath[len(oldPath):]
				if len(path) > 2048 || len(strings.Split(path, "/")) > MaxDepth {
					return fmt.Errorf("%w：移动后超出二十层限制", ErrInvalid)
				}
				child.FullPath, child.LowerPath = path, strings.ToLower(path)
				child.LowerPathHash = pathHash(child.LowerPath)
				child.Revision++
				if child.ID == id {
					child.ParentID, child.Slug, child.LowerSlug = parentID, slug, strings.ToLower(slug)
				}
				if _, err := db.GetEngine(ctx).ID(child.ID).Cols("parent_id", "slug", "lower_slug", "full_path", "lower_path", "lower_path_hash", "revision").Update(child); err != nil {
					return err
				}
			}
			for _, path := range paths {
				if _, err := db.GetEngine(ctx).Where("path_hash = ? AND path = ?", pathHash(path.Path), path.Path).Cols("alias").Update(&ResourcePath{Alias: true}); err != nil {
					return err
				}
			}
			for _, path := range paths {
				if err := reservePath(ctx, strings.ToLower(newPath)+path.Path[len(root.LowerPath):], path.Kind, path.ResourceID); err != nil {
					return err
				}
			}
			details, err := json.Marshal(map[string]any{
				"before": map[string]any{"full_path": oldPath, "parent_id": root.ParentID},
				"after":  map[string]any{"full_path": newPath, "parent_id": parentID}, "namespace_count": len(descendants),
			})
			if err != nil {
				return err
			}
			// 来源与目标分别记录，避免来源范围读者取得目标私有群组信息。
			oldChain, err := namespacePathIDs(ctx, oldPath, id)
			if err != nil {
				return err
			}
			if err := AppendAudit(ctx, &AuditEvent{
				Type: "group.transferred", Actor: actor, ScopeType: "group", ScopeID: id,
				AncestorIDs: oldChain, ObjectType: "group", ObjectID: id, ObjectPath: oldPath, Result: "success",
				Details: []byte(`{"direction":"out"}`),
			}); err != nil {
				return err
			}
			chain, err := Ancestors(ctx, id)
			if err != nil {
				return err
			}
			ids := make([]int64, 0, len(chain))
			for _, item := range chain {
				ids = append(ids, item.ID)
			}
			return AppendAudit(ctx, &AuditEvent{
				Type: "group.transferred", Actor: actor, ScopeType: "group", ScopeID: id,
				AncestorIDs: ids, ObjectType: "group", ObjectID: id, ObjectPath: newPath, Result: "success", Details: details,
			})
		})
	})
}

func namespacePathIDs(ctx context.Context, path string, id int64) ([]int64, error) {
	parts := strings.Split(strings.ToLower(path), "/")
	paths := make([]string, 0, len(parts))
	for i := 1; i < len(parts); i++ {
		paths = append(paths, strings.Join(parts[:i], "/"))
	}
	ids := []int64{id}
	if len(paths) == 0 {
		return ids, nil
	}
	var aliases []ResourcePath
	if err := db.GetEngine(ctx).Where("kind = ?", "group").In("path", paths).Find(&aliases); err != nil {
		return nil, err
	}
	for _, alias := range aliases {
		ids = append(ids, alias.ResourceID)
	}
	return ids, nil
}
