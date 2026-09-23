// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"fmt"
	"testing"
	"time"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestUnifiedRepositoryOwnerSourcesAndPreview(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	actor := gm.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	repo := &repo_model.Repository{OwnerID: 2, OwnerName: "user2", OwnerNamespace: "user2", Name: "owner-view", LowerName: "owner-view", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}))
	revision := func() int64 { n, e := gm.GetNamespace(ctx, 2); require.NoError(t, e); return n.Revision }
	change := RepositoryMemberChange{Kind: "member", Member: GroupMemberOption{UserID: 4, Role: gm.Owner, Revision: revision()}}
	auditCount, err := db.GetEngine(ctx).Count(new(gm.AuditEvent))
	require.NoError(t, err)
	preview, err := PreviewRepositoryMemberChange(ctx, actor, repo.ID, change)
	require.NoError(t, err)
	require.NotEmpty(t, preview.Token)
	require.True(t, preview.RequiresConfirmation)
	afterPreview, err := db.GetEngine(ctx).Count(new(gm.AuditEvent))
	require.NoError(t, err)
	require.Equal(t, auditCount, afterPreview, "预览不能保存权限或影响审计")
	unittest.AssertCount(t, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 4}, 0)
	require.Equal(t, change.Member.Revision, revision(), "预览不能提交修订号")
	change.Member.PreviewToken = preview.Token
	require.NoError(t, ApplyRepositoryMemberChange(ctx, actor, repo.ID, change))
	var audit gm.AuditEvent
	hasImpact, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND object_id = ?", "repository", repo.ID, 4).And("details LIKE ?", "%impact_after%").Get(&audit)
	require.NoError(t, err)
	require.True(t, hasImpact, "提交保存实际权限变化")
	u := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	p, err := access_model.GetIndividualUserRepoPermission(ctx, repo, u)
	require.NoError(t, err)
	require.True(t, p.IsOwner())
	sibling := &repo_model.Repository{OwnerID: 2, OwnerName: "user2", OwnerNamespace: "user2", Name: "owner-sibling", LowerName: "owner-sibling", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, sibling))
	siblingPermission, e := access_model.GetIndividualUserRepoPermission(ctx, sibling, u)
	require.NoError(t, e)
	require.False(t, siblingPermission.IsOwner())

	require.NoError(t, SetRepositoryMember(ctx, actor, repo.ID, GroupMemberOption{UserID: 5, Role: gm.Maintainer, Revision: revision()}, false))
	maintainer := gm.Actor{ID: 5, Name: "user5", Kind: "user", Transport: "api"}
	for _, remove := range []bool{false, true} {
		require.ErrorIs(t, SetRepositoryMember(ctx, maintainer, repo.ID, GroupMemberOption{UserID: 4, Role: gm.Developer, Revision: revision()}, remove), gm.ErrNotFound)
	}
	require.ErrorIs(t, SetRepositoryMember(ctx, maintainer, repo.ID, GroupMemberOption{UserID: 8, Role: gm.Owner, Revision: revision()}, false), gm.ErrNotFound)
	require.ErrorIs(t, CheckRepositoryMemberMutation(ctx, maintainer, repo.ID, 4), gm.ErrNotFound, "原生协作者入口同样保护 Owner")
	view, err := ListRepositoryMembersView(ctx, 4, repo.ID, RepositoryMemberQuery{Role: gm.Owner})
	require.NoError(t, err)
	require.Len(t, view.Members, 2)
	_, err = ListRepositoryMembersView(ctx, 0, repo.ID, RepositoryMemberQuery{})
	require.ErrorIs(t, err, gm.ErrNotFound)
	change = RepositoryMemberChange{Kind: "member", Member: GroupMemberOption{UserID: 8, Role: gm.Developer, Revision: revision()}}
	preview, err = PreviewRepositoryMemberChange(ctx, actor, repo.ID, change)
	require.NoError(t, err)
	require.NoError(t, SetRepositoryMember(ctx, actor, repo.ID, GroupMemberOption{UserID: 10, Role: gm.Reporter, Revision: revision()}, false))
	change.Member.PreviewToken = preview.Token
	change.Member.Revision = revision()
	require.ErrorIs(t, ApplyRepositoryMemberChange(ctx, actor, repo.ID, change), gm.ErrConflict, "即使提交新修订号也不能绕过来源快照")
	require.NoError(t, gm.EnsurePermanentRepositoryOwner(ctx, repo.ID, 2))
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "repository", repo.ID, 4).Cols("expires_unix").Update(&gm.Membership{ExpiresUnix: time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	require.ErrorIs(t, gm.EnsurePermanentRepositoryOwner(ctx, repo.ID, 2), gm.ErrConflict, "临时 Owner 不能接替永久所有者")
}

func TestUnifiedRepositorySharedOwnerAndNativeAdmin(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	actor := gm.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	repo := &repo_model.Repository{OwnerID: 3, OwnerName: "org3", OwnerNamespace: "org3", Name: "shared-owner-view", LowerName: "shared-owner-view", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}))
	group, err := CreateGroup(ctx, actor, GroupOption{Path: "private-owner-source", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, group.ID, GroupMemberOption{UserID: 4, Role: gm.Owner, Revision: group.Revision}, false))
	revision := func() int64 { n, e := gm.GetNamespace(ctx, 3); require.NoError(t, e); return n.Revision }
	u := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	for _, role := range []gm.Role{gm.Owner, gm.Developer} {
		require.NoError(t, SetRepositoryShare(ctx, actor, repo.ID, GroupShareOption{GroupID: group.ID, MaxRole: role, Revision: revision()}, false))
		p, e := access_model.GetIndividualUserRepoPermission(ctx, repo, u)
		require.NoError(t, e)
		require.Equal(t, role == gm.Owner, p.IsOwner())
	}
	team := &organization.Team{OrgID: 3, Name: "ordinary-admin", LowerName: "ordinary-admin", AccessMode: perm.AccessModeAdmin}
	require.NoError(t, db.Insert(ctx, team))
	require.NoError(t, db.Insert(ctx, &organization.TeamUser{OrgID: 3, TeamID: team.ID, UID: 5}, &organization.TeamRepo{OrgID: 3, TeamID: team.ID, RepoID: repo.ID}))
	admin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	p, err := access_model.GetIndividualUserRepoPermission(ctx, repo, admin)
	require.NoError(t, err)
	require.True(t, p.IsAdmin())
	require.False(t, p.IsOwner())
	abilities, _ := gm.AbilitiesFor(gm.Owner, nil)
	require.False(t, gm.HasOwnerGrant([]gm.Grant{{Role: gm.Guest, Abilities: abilities}}), "自定义普通角色不能拼出 Owner")
	view, err := ListRepositoryMembersView(ctx, 4, repo.ID, RepositoryMemberQuery{})
	require.NoError(t, err)
	_, groupErr := CheckGroupAccess(ctx, 4, 3, gm.ManageGroupMembers)
	require.ErrorIs(t, groupErr, gm.ErrNotFound, "共享 Owner 不会成为目标所属组织的 Owner")
	require.False(t, view.CanOwn)
	for _, m := range view.Members {
		require.Nil(t, m.Direct)
		for _, source := range m.Sources {
			require.False(t, source.CanEdit)
		}
	}
}

func TestUnifiedMemberPrivacyExpiryAndNativeRemoval(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	actor := gm.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	repo := &repo_model.Repository{OwnerID: 2, OwnerName: "user2", OwnerNamespace: "user2", Name: "safe-members", LowerName: "safe-members"}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}))
	group, err := CreateGroup(ctx, actor, GroupOption{Path: "secret-source", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, actor, group.ID, GroupMemberOption{UserID: 4, Role: gm.Developer, Revision: group.Revision}, false))
	require.NoError(t, db.Insert(ctx, &gm.Share{ScopeType: "repository", ScopeID: repo.ID, GroupID: group.ID, MaxRole: gm.Developer}))
	public, err := ListRepositoryMembersView(ctx, 0, repo.ID, RepositoryMemberQuery{})
	require.NoError(t, err)
	require.False(t, public.CanManage)
	require.Nil(t, public.Roles)
	for _, m := range public.Members {
		require.Nil(t, m.Direct)
		for _, source := range m.Sources {
			require.NotContains(t, source.Name, "secret-source")
			require.Empty(t, source.ManageURL)
		}
	}
	require.Equal(t, "受限群组", public.Shares[0].Name)
	for _, member := range public.Members {
		require.NotEqual(t, int64(4), member.UserID, "匿名访客不能枚举仅来自私有邀请群组的成员")
	}
	revision := func() int64 { n, e := gm.GetNamespace(ctx, 2); require.NoError(t, e); return n.Revision }
	require.NoError(t, SetRepositoryMember(ctx, actor, repo.ID, GroupMemberOption{UserID: 4, Role: gm.Reporter, Revision: revision()}, false))
	change := RepositoryMemberChange{Kind: "share", Remove: true, Share: GroupShareOption{GroupID: group.ID, Revision: revision()}}
	preview, err := PreviewRepositoryMemberChange(ctx, actor, repo.ID, change)
	require.NoError(t, err)
	require.NotEmpty(t, preview.VisibilityNotice)
	found := false
	for _, impact := range preview.Impacts {
		if impact.Username == "user4" {
			found = true
			require.Contains(t, impact.After, "Reporter")
			require.NotEmpty(t, impact.Sources)
		}
	}
	require.True(t, found)
	change.Share.PreviewToken = preview.Token
	require.NoError(t, ApplyRepositoryMemberChange(ctx, actor, repo.ID, change))
	require.NoError(t, db.Insert(ctx, &repo_model.Collaboration{RepoID: repo.ID, UserID: 4, Mode: perm.AccessModeWrite}))
	change = RepositoryMemberChange{Kind: "native", Remove: true, Member: GroupMemberOption{UserID: 4, Revision: revision()}}
	preview, err = PreviewRepositoryMemberChange(ctx, actor, repo.ID, change)
	require.NoError(t, err)
	unittest.AssertCount(t, &repo_model.Collaboration{RepoID: repo.ID, UserID: 4}, 1)
	change.Member.PreviewToken = preview.Token
	require.NoError(t, ApplyRepositoryMemberChange(ctx, actor, repo.ID, change))
	unittest.AssertCount(t, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 4}, 1)
	change = RepositoryMemberChange{Kind: "member", Member: GroupMemberOption{UserID: 5, Role: gm.Developer, Revision: revision()}}
	preview, err = PreviewRepositoryMemberChange(ctx, actor, repo.ID, change)
	require.NoError(t, err)
	// 直接模拟到期时刻，避免依赖清理任务或 sleep。
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "repository", repo.ID, 4).Cols("expires_unix").Update(&gm.Membership{ExpiresUnix: time.Now().Unix() - 1})
	require.NoError(t, err)
	change.Member.PreviewToken = preview.Token
	require.ErrorIs(t, ApplyRepositoryMemberChange(ctx, actor, repo.ID, change), gm.ErrConflict, "授权到期后即使修订号不变也必须重新预览")
	view, err := ListRepositoryMembersView(ctx, 2, repo.ID, RepositoryMemberQuery{Q: "user4"})
	require.NoError(t, err)
	require.Len(t, view.Members, 1)
	require.Zero(t, view.Members[0].Role)
	require.True(t, view.Members[0].Sources[0].Expired)
}

func TestUnifiedOwnerConcurrentDemotion(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	repo := &repo_model.Repository{OwnerID: 2, OwnerName: "user2", OwnerNamespace: "user2", Name: "owner-race", LowerName: "owner-race", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 4, Role: gm.Owner}, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 5, Role: gm.Owner}))
	// 样本由两个直接 Owner 接替已经停用的命名空间归属者。
	_, err := db.GetEngine(ctx).ID(2).Cols("is_active").Update(&user_model.User{IsActive: false})
	require.NoError(t, err)
	n, err := gm.GetNamespace(ctx, 2)
	require.NoError(t, err)
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, id := range []int64{4, 5} {
		go func() {
			<-start
			results <- SetRepositoryMember(ctx, gm.Actor{ID: id, Name: "并发所有者", Kind: "user", Transport: "api"}, repo.ID, GroupMemberOption{UserID: id, Role: gm.Developer, Revision: n.Revision}, false)
		}()
	}
	close(start)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, gm.ErrConflict)
		}
	}
	require.Equal(t, 1, successes)
	require.NoError(t, gm.EnsurePermanentRepositoryOwner(ctx, repo.ID, 0))
	owners, err := ListRepositoryMembersView(ctx, 4, repo.ID, RepositoryMemberQuery{Role: gm.Owner})
	require.NoError(t, err)
	require.Len(t, owners.Members, 1)
	remaining := owners.Members[0].UserID
	n, err = gm.GetNamespace(ctx, 2)
	require.NoError(t, err)
	require.ErrorIs(t, SetRepositoryMember(ctx, gm.Actor{ID: remaining, Name: "最后所有者", Kind: "user", Transport: "api"}, repo.ID, GroupMemberOption{UserID: remaining, Role: gm.Developer, Revision: n.Revision}, false), gm.ErrConflict)
}

func TestUnifiedMembersFiltersAcrossPages(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	repo := &repo_model.Repository{OwnerID: 2, OwnerName: "user2", OwnerNamespace: "user2", Name: "member-filter-page", LowerName: "member-filter-page", IsPrivate: true}
	require.NoError(t, db.Insert(ctx, repo))
	for i := range 205 {
		user := &user_model.User{Name: fmt.Sprintf("unified-page-%03d", i), LowerName: fmt.Sprintf("unified-page-%03d", i), Email: fmt.Sprintf("unified-%d@example.invalid", i), IsActive: true}
		require.NoError(t, db.Insert(ctx, user))
		role := gm.Reporter
		if i >= 102 {
			role = gm.Owner
		}
		require.NoError(t, db.Insert(ctx, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: user.ID, Role: role}))
	}
	start := time.Now()
	first, err := ListRepositoryMembersView(ctx, 2, repo.ID, RepositoryMemberQuery{Q: "unified-page", Role: gm.Owner})
	require.NoError(t, err)
	require.Len(t, first.Members, 100)
	require.NotZero(t, first.NextID)
	second, err := ListRepositoryMembersView(ctx, 2, repo.ID, RepositoryMemberQuery{Q: "unified-page", Role: gm.Owner, AfterID: first.NextID})
	require.NoError(t, err)
	require.Len(t, second.Members, 3)
	require.Zero(t, second.NextID)
	require.Greater(t, second.Members[0].UserID, first.Members[99].UserID)
	t.Logf("205 个授权样本，筛选后 103 位 Owner 分两页完整返回，耗时 %s", time.Since(start))
}
