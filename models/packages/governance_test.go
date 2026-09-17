// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package packages_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	packages_model "gitea.dev/models/packages"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestPackageReferencesRespectNamespaceLifecycle(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	pkg, err := packages_model.TryInsertPackage(ctx, &packages_model.Package{OwnerID: 3, Type: packages_model.TypeGeneric, Name: "lifecycle", LowerName: "lifecycle"})
	require.NoError(t, err)
	version, err := packages_model.GetOrInsertVersion(ctx, &packages_model.PackageVersion{PackageID: pkg.ID, Version: "1", LowerVersion: "1"})
	require.NoError(t, err)
	blob, _, err := packages_model.GetOrInsertBlob(ctx, &packages_model.PackageBlob{HashSHA256: strings.Repeat("b", 64)})
	require.NoError(t, err)
	file, err := packages_model.TryInsertFile(ctx, &packages_model.PackageFile{VersionID: version.ID, BlobID: blob.ID, Name: "sample", LowerName: "sample"})
	require.NoError(t, err)
	property, err := packages_model.InsertProperty(ctx, packages_model.PropertyTypeFile, file.ID, "kind", "sample")
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(3).Cols("archived").Update(&governance_model.Namespace{Archived: true})
	require.NoError(t, err)
	_, err = packages_model.TryInsertPackage(ctx, &packages_model.Package{OwnerID: 3, Name: "blocked", LowerName: "blocked"})
	require.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = packages_model.GetOrInsertVersion(ctx, &packages_model.PackageVersion{PackageID: pkg.ID, LowerVersion: "2"})
	require.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = packages_model.TryInsertFile(ctx, &packages_model.PackageFile{VersionID: version.ID, LowerName: "blocked"})
	require.ErrorIs(t, err, governance_model.ErrConflict)
	_, err = packages_model.InsertProperty(ctx, packages_model.PropertyTypePackage, pkg.ID, "blocked", "sample")
	require.ErrorIs(t, err, governance_model.ErrConflict)
	require.ErrorIs(t, packages_model.InsertOrUpdateProperty(ctx, packages_model.PropertyTypeVersion, version.ID, "blocked", "sample"), governance_model.ErrConflict)
	require.ErrorIs(t, packages_model.UpdateProperty(ctx, property), governance_model.ErrConflict)
	require.ErrorIs(t, packages_model.UpdateVersion(ctx, version), governance_model.ErrConflict)
	require.ErrorIs(t, packages_model.UpdateFile(ctx, file, []string{"is_lead"}), governance_model.ErrConflict)
	require.NoError(t, packages_model.DeletePackageByID(ctx, pkg.ID))
	_, err = packages_model.InsertProperty(ctx, packages_model.PropertyTypeFile, file.ID, "orphan", "sample")
	require.Error(t, err, "旧请求不能在目标软件包消失后留下悬空属性")
	_, err = packages_model.TryInsertPackage(ctx, &packages_model.Package{OwnerID: 999999, Name: "orphan", LowerName: "orphan"})
	require.ErrorIs(t, err, governance_model.ErrNotFound)
}

func TestDuplicatePackageResultKeepsOuterTransaction(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	pkg, err := packages_model.TryInsertPackage(ctx, &packages_model.Package{OwnerID: 2, Type: packages_model.TypeGeneric, Name: "duplicate-transaction", LowerName: "duplicate-transaction"})
	require.NoError(t, err)
	version, err := packages_model.GetOrInsertVersion(ctx, &packages_model.PackageVersion{PackageID: pkg.ID, Version: "1", LowerVersion: "1"})
	require.NoError(t, err)
	err = db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := packages_model.InsertProperty(ctx, packages_model.PropertyTypePackage, pkg.ID, "before", "事务内"); err != nil {
			return err
		}
		_, duplicate := packages_model.GetOrInsertVersion(ctx, &packages_model.PackageVersion{PackageID: pkg.ID, Version: version.Version, LowerVersion: version.LowerVersion})
		if !errors.Is(duplicate, packages_model.ErrDuplicatePackageVersion) {
			return fmt.Errorf("预期已存在版本，实际为 %v", duplicate)
		}
		_, err := packages_model.InsertProperty(ctx, packages_model.PropertyTypePackage, pkg.ID, "after", "同一事务")
		return err
	})
	require.NoError(t, err)
	properties, err := packages_model.GetProperties(ctx, packages_model.PropertyTypePackage, pkg.ID)
	require.NoError(t, err)
	require.Len(t, properties, 2, "已存在回执不能回滚同一事务中先前的修改")
}
