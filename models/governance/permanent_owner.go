// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"fmt"

	"gitea.dev/models/db"
)

type permanentOwnerUser struct {
	ID            int64 `xorm:"pk"`
	IsActive      bool
	ProhibitLogin bool
}

func (*permanentOwnerUser) TableName() string { return "user" }

// EnsurePermanentGroupOwner 确认当前组仍可从自身或祖先获得至少一个有效永久 Owner。
func EnsurePermanentGroupOwner(ctx context.Context, groupID, excludingUserID int64) error {
	chain, err := Ancestors(ctx, groupID)
	if err == ErrNotFound {
		query := db.GetEngine(ctx).Table("team_user").Alias("tu").Join("INNER", []string{"team", "t"}, "t.id = tu.team_id").Join("INNER", []string{"user", "u"}, "u.id = tu.uid").Where("t.org_id = ? AND t.lower_name = ? AND u.is_active = ? AND u.prohibit_login = ? AND u.type = ?", groupID, "owners", true, false, 0)
		if excludingUserID > 0 {
			query = query.And("u.id <> ?", excludingUserID)
		}
		has, queryErr := query.Exist(new(struct{ ID int64 }))
		if queryErr != nil {
			return queryErr
		}
		if has {
			return nil
		}
		return fmt.Errorf("%w：必须保留至少一个有效且不过期的 Owner", ErrConflict)
	}
	if err != nil {
		return err
	}
	ids, teams := make([]int64, 0, len(chain)), make([]int64, 0, len(chain))
	for _, namespace := range chain {
		ids = append(ids, namespace.ID)
		if namespace.NativeOwnerTeamID != 0 {
			teams = append(teams, namespace.NativeOwnerTeamID)
		}
	}
	var candidates []int64
	if err := db.GetEngine(ctx).Table(new(Membership)).Cols("user_id").Where("scope_type = ? AND role = ? AND expires_unix = ?", "group", Owner, 0).In("scope_id", ids).Find(&candidates); err != nil {
		return err
	}
	if len(teams) > 0 {
		var native []int64
		if err := db.GetEngine(ctx).Table("team_user").Cols("uid").In("team_id", teams).Find(&native); err != nil {
			return err
		}
		candidates = append(candidates, native...)
	}
	if len(candidates) > 0 {
		query := db.GetEngine(ctx).Where("is_active = ? AND prohibit_login = ? AND type = ?", true, false, 0).In("id", candidates)
		if excludingUserID > 0 {
			query = query.And("id <> ?", excludingUserID)
		}
		has, err := query.Exist(new(permanentOwnerUser))
		if err != nil {
			return err
		}
		if has {
			return nil
		}
	}
	return fmt.Errorf("%w：必须保留至少一个有效且不过期的 Owner", ErrConflict)
}

// EnsureUserCanLoseOwnerAccess 检查停用、禁登或删除用户不会使任何后代组失去有效永久 Owner。
func EnsureUserCanLoseOwnerAccess(ctx context.Context, userID int64) error {
	groups, err := GroupsRequiringOwnerReplacement(ctx, userID)
	if err != nil {
		return err
	}
	if len(groups) > 0 {
		return fmt.Errorf("%w：必须先为群组 %d 指派其他有效且不过期的 Owner", ErrConflict, groups[0])
	}
	var repositories []int64
	if err := db.GetEngine(ctx).Table("repository").Cols("id").Where("owner_id = ?", userID).Find(&repositories); err != nil {
		return err
	}
	var direct []int64
	if err := db.GetEngine(ctx).Table(new(Membership)).Cols("scope_id").Where("scope_type = ? AND user_id = ? AND role = ? AND expires_unix = 0", "repository", userID, Owner).Find(&direct); err != nil {
		return err
	}
	for _, id := range append(repositories, direct...) {
		if err := EnsurePermanentRepositoryOwner(ctx, id, userID); err != nil {
			return err
		}
	}
	return nil
}

// EnsurePermanentRepositoryOwner 排除共享、临时授权和站点管理员的兜底身份。
func EnsurePermanentRepositoryOwner(ctx context.Context, repoID, excludingUserID int64) error {
	var repo struct {
		OwnerID int64
		Name    string
	}
	has, err := db.GetEngine(ctx).Table("repository").Where("id = ?", repoID).Get(&repo)
	if err != nil || !has {
		return err
	}
	var owner struct {
		Type          int
		IsActive      bool
		ProhibitLogin bool
	}
	has, err = db.GetEngine(ctx).Table("user").Where("id = ?", repo.OwnerID).Get(&owner)
	if err != nil {
		return err
	}
	if has && owner.Type == 0 && repo.OwnerID != excludingUserID && owner.IsActive && !owner.ProhibitLogin {
		return nil
	}
	if has && owner.Type == 1 {
		if err := EnsurePermanentGroupOwner(ctx, repo.OwnerID, excludingUserID); err == nil {
			return nil
		} else if !errors.Is(err, ErrConflict) {
			return err
		}
	}
	query := db.GetEngine(ctx).Table(new(Membership)).Alias("m").Join("INNER", []string{"user", "u"}, "u.id = m.user_id").Where("m.scope_type = ? AND m.scope_id = ? AND m.role = ? AND m.expires_unix = 0 AND u.is_active = ? AND u.prohibit_login = ? AND u.type = 0", "repository", repoID, Owner, true, false)
	if excludingUserID > 0 {
		query = query.And("u.id <> ?", excludingUserID)
	}
	has, err = query.Exist(new(struct{ ID int64 }))
	if err != nil || has {
		return err
	}
	return fmt.Errorf("%w：项目 %s（#%d）必须保留至少一位有效、永久且非共享来源的 Owner，请先添加接替所有者", ErrConflict, repo.Name, repoID)
}

// GroupsRequiringOwnerReplacement 返回移除指定用户后会失去有效永久 Owner 的群组。
func GroupsRequiringOwnerReplacement(ctx context.Context, userID int64) ([]int64, error) {
	var roots []int64
	if err := db.GetEngine(ctx).Table(new(Membership)).Cols("scope_id").Where("scope_type = ? AND user_id = ? AND role = ? AND expires_unix = ?", "group", userID, Owner, 0).Find(&roots); err != nil {
		return nil, err
	}
	var nativeRoots []int64
	if err := db.GetEngine(ctx).Table("team").Alias("t").Cols("t.org_id").Join("INNER", []string{"team_user", "tu"}, "tu.team_id = t.id").Where("tu.uid = ? AND t.lower_name = ?", userID, "owners").Find(&nativeRoots); err != nil {
		return nil, err
	}
	roots = append(roots, nativeRoots...)
	seen := make(map[int64]bool)
	affectedIDs := make([]int64, 0)
	for _, rootID := range roots {
		root, err := GetNamespace(ctx, rootID)
		if errors.Is(err, ErrNotFound) {
			if !seen[rootID] {
				seen[rootID] = true
				if ownerErr := EnsurePermanentGroupOwner(ctx, rootID, userID); ownerErr != nil {
					if errors.Is(ownerErr, ErrConflict) {
						affectedIDs = append(affectedIDs, rootID)
					} else {
						return nil, ownerErr
					}
				}
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		var affected []Namespace
		if err := db.GetEngine(ctx).Where("kind = ? AND (lower_path = ? OR lower_path LIKE ?)", "group", root.LowerPath, root.LowerPath+"/%").Find(&affected); err != nil {
			return nil, err
		}
		for _, group := range affected {
			if seen[group.ID] {
				continue
			}
			seen[group.ID] = true
			if ownerErr := EnsurePermanentGroupOwner(ctx, group.ID, userID); ownerErr != nil {
				if errors.Is(ownerErr, ErrConflict) {
					affectedIDs = append(affectedIDs, group.ID)
					continue
				}
				return nil, ownerErr
			}
		}
	}
	return affectedIDs, nil
}
