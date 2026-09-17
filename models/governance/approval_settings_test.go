// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/db"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApprovalSettingsExplainAndLockInheritedFields(t *testing.T) {
	ctx := testDatabase(t)
	testGroup(ctx, t, 1, 0, "parent")
	testGroup(ctx, t, 2, 1, "child")
	current, err := ResolveApprovalSettings(ctx, 100, 2)
	require.NoError(t, err)
	assert.True(t, current.Settings.PreventAuthor)
	assert.True(t, current.Settings.ResetOnChange)
	assert.False(t, current.Settings.PreventCommitter)
	assert.False(t, current.Settings.PreventOverrides, "默认允许逐 PR 覆盖，与 GitLab 设置语义一致")
	require.NoError(t, db.Insert(ctx,
		&ApprovalSettings{ScopeType: "group", ScopeID: 1, PreventCommitter: true, Revision: 7},
		&ApprovalSettings{ScopeType: "repository", ScopeID: 100, PreventAuthor: true, Revision: 3},
	))
	current, err = ResolveApprovalSettings(ctx, 100, 2)
	require.NoError(t, err)
	assert.True(t, current.Settings.PreventCommitter, "项目不能关闭父组开启的限制")
	assert.Equal(t, ApprovalSettingSource{Value: true, Locked: true, ScopeType: "group", ScopeID: 1, Revision: 7}, current.Sources["prevent_committer"])
	assert.True(t, current.Settings.PreventAuthor, "父组关闭时，项目仍可开启")
	assert.Equal(t, "repository", current.Sources["prevent_author"].ScopeType)
	assert.False(t, current.Sources["prevent_author"].Locked)
	require.NoError(t, db.Insert(ctx, &ApprovalSettings{ScopeType: "instance", Revision: 9}))
	current, err = ResolveApprovalSettings(ctx, 100, 2)
	require.NoError(t, err)
	assert.False(t, current.Settings.PreventCommitter, "实例明确配置的关闭值同样锁定后代")
	assert.Equal(t, "instance", current.Sources["prevent_committer"].ScopeType)
	assert.True(t, current.Sources["prevent_committer"].Locked)
}

func TestApprovalDefaultsWithoutGovernanceConfiguration(t *testing.T) {
	ctx := testDatabase(t)
	settings, err := ResolveApprovalSettings(ctx, 100, 200)
	require.NoError(t, err)
	require.True(t, settings.Settings.PreventAuthor)
	require.True(t, settings.Settings.ResetOnChange)
	require.False(t, settings.Settings.RequireReauthentication)
	require.NoError(t, db.Insert(ctx, &ApprovalSettings{ScopeType: "instance", RequireReauthentication: true}))
	_, err = ResolveApprovalSettings(ctx, 100, 200)
	require.ErrorIs(t, err, ErrNotFound, "存在治理设置时，缺失命名空间必须拒绝，不能绕过实例限制")
}
