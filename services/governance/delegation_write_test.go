// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestRevokedDelegationCannotChangeMembers(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	// 请求已完成管理员 sudo 认证，但等待治理写锁时实际管理员先被撤销资格。
	actor := governance_model.Actor{ID: 1, Name: "user1", ActingAsID: 2, ActingAsName: "user2", Kind: "user", Transport: "api"}
	_, err := db.GetEngine(ctx).ID(1).Cols("is_admin").Update(&user_model.User{IsAdmin: false})
	require.NoError(t, err)
	group, err := governance_model.GetNamespace(ctx, 3)
	require.NoError(t, err)
	require.ErrorIs(t, SetGroupMember(ctx, actor, 3, GroupMemberOption{UserID: 4, Role: governance_model.Developer, Revision: group.Revision}, false), governance_model.ErrNotFound)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: 3, UserID: 4}, 0)
	for name, change := range map[string]func() error{
		"创建群组":  func() error { _, err := CreateGroup(ctx, actor, GroupOption{Path: "delegation-check"}); return err },
		"群组共享":  func() error { return SetGroupShare(ctx, actor, 3, GroupShareOption{}, false) },
		"项目共享":  func() error { return SetRepositoryShare(ctx, actor, 1, GroupShareOption{}, false) },
		"项目成员":  func() error { return SetRepositoryMember(ctx, actor, 1, GroupMemberOption{UserID: 4}, false) },
		"自定义角色": func() error { _, err := SaveGroupRole(ctx, actor, 3, 0, GroupRoleOption{}, false); return err },
		"项目审批设置": func() error {
			_, err := SaveRepositoryApprovalSettings(ctx, actor, 1, ApprovalSettingsOption{})
			return err
		},
		"项目审批规则": func() error {
			_, err := SaveRepositoryApprovalRule(ctx, actor, 1, 0, 0, governance_model.ApprovalRule{}, false)
			return err
		},
		"强制审批策略": func() error {
			_, err := SaveApprovalPolicy(ctx, actor, "group", 3, 0, 0, governance_model.ApprovalRule{}, false)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) { require.ErrorIs(t, change(), governance_model.ErrNotFound) })
	}
	_, err = db.GetEngine(ctx).ID(1).Cols("is_admin").Update(&user_model.User{IsAdmin: true})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, 3, GroupMemberOption{UserID: 4, Role: governance_model.Reporter, Revision: group.Revision}, false), "仍有效的管理员委托正常工作")
}
