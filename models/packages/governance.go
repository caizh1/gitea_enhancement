// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package packages

import (
	"context"
	"errors"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
)

// withPackageOwnerWrite 与群组归档、删除共用事务锁，拒绝旧请求在所有者消失后新增软件包引用。
func withPackageOwnerWrite(ctx context.Context, ownerID int64, apply func(context.Context) error) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("group", ownerID)}, func(ctx context.Context) error {
		var owner struct{ ID int64 }
		has, err := db.GetEngine(ctx).Table("user").ID(ownerID).Get(&owner)
		if err != nil {
			return err
		}
		if !has {
			return governance_model.ErrNotFound
		}
		chain, err := governance_model.Ancestors(ctx, ownerID)
		if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
			return err
		}
		for _, n := range chain {
			if n.Archived || n.DeleteAfter != 0 {
				return governance_model.ErrConflict
			}
		}
		return apply(ctx)
	})
}

func withPackageWrite(ctx context.Context, packageID int64, apply func(context.Context) error) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		pkg, err := GetPackageByID(ctx, packageID)
		if err != nil {
			return err
		}
		return withPackageOwnerWrite(ctx, pkg.OwnerID, apply)
	})
}

func withPackageVersionWrite(ctx context.Context, versionID int64, apply func(context.Context) error) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		version, err := GetVersionByID(ctx, versionID)
		if err != nil {
			return err
		}
		return withPackageWrite(ctx, version.PackageID, apply)
	})
}

func withPackageReferenceWrite(ctx context.Context, refType PropertyType, refID int64, apply func(context.Context) error) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		switch refType {
		case PropertyTypePackage:
			return withPackageWrite(ctx, refID, apply)
		case PropertyTypeVersion:
			return withPackageVersionWrite(ctx, refID, apply)
		case PropertyTypeFile:
			file, has, err := db.GetByID[PackageFile](ctx, refID)
			if err != nil {
				return err
			}
			if !has {
				return ErrPackageFileNotExist
			}
			return withPackageVersionWrite(ctx, file.VersionID, apply)
		default:
			return governance_model.ErrInvalid
		}
	})
}
