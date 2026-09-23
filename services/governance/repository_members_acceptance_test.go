// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/secret"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 独立预期表来自已确认的角色规则，不调用生产能力函数生成断言。
var memberAcceptanceRoles = []struct {
	role          gm.Role
	read, write   uint8
	manage, owner bool
}{
	{gm.Guest, 2, 0, false, false},
	{gm.Planner, 6, 2, false, false},
	{gm.Reporter, 127, 0, false, false},
	{gm.Developer, 127, 127, false, false},
	{gm.Maintainer, 127, 127, true, false},
	{gm.Owner, 127, 127, true, true},
}
var memberAcceptanceUnits = []unit.Type{unit.TypeCode, unit.TypeIssues, unit.TypePullRequests, unit.TypeWiki, unit.TypeActions, unit.TypeReleases, unit.TypePackages}

func TestMemberAcceptanceInvitationOwner(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "dummy"})()
	defer test.MockVariableValue(&setting.SecretKey, "成员验收临时密钥")()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	repo := acceptanceRepo(t, 2, "invited-owner")
	require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, GroupMemberOption{UserID: 5, Role: gm.Maintainer, Revision: acceptanceRevision(t, 2)}, false))
	email := "owner-acceptance@example.invalid"
	require.NoError(t, db.Insert(ctx, &user_model.EmailAddress{UID: 4, Email: email, IsActivated: true}))
	option := InvitationOption{Email: email, Role: gm.Owner, Revision: acceptanceRevision(t, 2)}
	_, err := CreateInvitation(ctx, acceptanceActor(5), "repository", repo.ID, option)
	require.ErrorIs(t, err, gm.ErrNotFound, "AUTH-07：Maintainer 不能通过邮件邀请授予 Owner")
	invitation, err := CreateInvitation(ctx, acceptanceActor(2), "repository", repo.ID, option)
	require.NoError(t, err)
	_, err = ListRepositoryMembersView(ctx, 4, repo.ID, RepositoryMemberQuery{})
	require.ErrorIs(t, err, gm.ErrNotFound, "VIEW-07：待接受邀请不产生项目访问权")
	token, err := secret.DecryptSecret(setting.SecretKey, invitation.TokenEncrypted)
	require.NoError(t, err)
	require.NoError(t, AcceptInvitation(ctx, acceptanceActor(4), invitation.ID, token))
	view, err := ListRepositoryMembersView(ctx, 4, repo.ID, RepositoryMemberQuery{})
	require.NoError(t, err)
	require.True(t, view.CanOwn)
}

func TestMemberAcceptanceAuditRollback(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	repo := acceptanceRepo(t, 2, "owner-audit-rollback")
	revision := acceptanceRevision(t, 2)
	before, err := db.GetEngine(ctx).Count(&gm.AuditEvent{})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Exec("CREATE TRIGGER member_acceptance_fail BEFORE INSERT ON governance_audit_event BEGIN SELECT RAISE(ABORT, '成员审计故障'); END")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.GetEngine(context.Background()).Exec("DROP TRIGGER member_acceptance_fail")
		require.NoError(t, err)
	})
	err = SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, GroupMemberOption{UserID: 4, Role: gm.Owner, Revision: revision}, false)
	require.Error(t, err, "OWN-08：审计失败必须使授权整体回滚")
	unittest.AssertCount(t, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 4}, 0)
	require.Equal(t, revision, acceptanceRevision(t, 2))
	after, err := db.GetEngine(ctx).Count(&gm.AuditEvent{})
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestMemberAcceptanceLastPermanentOwner(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	repo := acceptanceRepo(t, 2, "last-permanent-owner")
	require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, GroupMemberOption{UserID: 4, Role: gm.Owner, Revision: acceptanceRevision(t, 2)}, false))
	// 该项目由直接 Owner 接替停用的个人归属者，没有组织及祖先来源。
	require.NoError(t, user_model.UpdateUserCols(ctx, &user_model.User{ID: 2, IsActive: false}, "is_active"))
	require.ErrorIs(t, gm.EnsurePermanentRepositoryOwner(ctx, repo.ID, 4), gm.ErrConflict)
	for _, sample := range []struct {
		name   string
		option GroupMemberOption
		remove bool
	}{
		{"移除", GroupMemberOption{UserID: 4}, true},
		{"降级", GroupMemberOption{UserID: 4, Role: gm.Developer}, false},
		{"改为临时", GroupMemberOption{UserID: 4, Role: gm.Owner, ExpiresUnix: time.Now().Add(time.Hour).Unix()}, false},
	} {
		t.Run("OWN-01_"+sample.name, func(t *testing.T) {
			revision := acceptanceRevision(t, 2)
			sample.option.Revision = revision
			require.ErrorIs(t, SetRepositoryMember(ctx, acceptanceActor(4), repo.ID, sample.option, sample.remove), gm.ErrConflict)
			require.Equal(t, revision, acceptanceRevision(t, 2))
			unittest.AssertExistsAndLoadBean(t, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 4, Role: gm.Owner, ExpiresUnix: 0})
		})
	}
	require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(4), repo.ID, GroupMemberOption{UserID: 5, Role: gm.Owner, ExpiresUnix: time.Now().Add(time.Hour).Unix(), Revision: acceptanceRevision(t, 2)}, false))
	require.ErrorIs(t, gm.EnsurePermanentRepositoryOwner(ctx, repo.ID, 4), gm.ErrConflict, "OWN-02：临时 Owner 不计入保障")
	require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(4), repo.ID, GroupMemberOption{UserID: 5, Role: gm.Owner, Revision: acceptanceRevision(t, 2)}, false))
	require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(4), repo.ID, GroupMemberOption{UserID: 4, Revision: acceptanceRevision(t, 2)}, true))
	view, err := ListRepositoryMembersView(ctx, 5, repo.ID, RepositoryMemberQuery{})
	require.NoError(t, err)
	require.True(t, view.CanOwn, "OWN-03：接替者必须有实际管理能力")
}

func TestMemberAcceptanceExpiryBoundary(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	repo := acceptanceRepo(t, 2, "member-expiry-boundary")
	deadline := time.Now().Add(time.Hour).Truncate(time.Second)
	require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, GroupMemberOption{UserID: 4, Role: gm.Owner, ExpiresUnix: deadline.Unix(), Revision: acceptanceRevision(t, 2)}, false))
	for _, sample := range []struct {
		offset time.Duration
		count  int
	}{{-time.Second, 1}, {0, 0}, {time.Second, 0}} {
		t.Run(fmt.Sprintf("SRC-10_截止时刻偏移%s", sample.offset), func(t *testing.T) {
			grants, err := gm.RepositoryGrants(ctx, repo.ID, repo.OwnerID, 4, deadline.Add(sample.offset))
			require.NoError(t, err)
			require.Len(t, grants, sample.count, "不运行清理任务也必须即时到期")
		})
	}
}

func acceptanceRepo(t *testing.T, ownerID int64, name string) *repo_model.Repository {
	t.Helper()
	owner, err := user_model.GetUserByID(t.Context(), ownerID)
	require.NoError(t, err)
	repo := &repo_model.Repository{OwnerID: ownerID, OwnerName: owner.Name, OwnerNamespace: owner.Name, Name: name, LowerName: name, IsPrivate: true}
	require.NoError(t, db.Insert(t.Context(), repo))
	for _, kind := range memberAcceptanceUnits {
		require.NoError(t, db.Insert(t.Context(), &repo_model.RepoUnit{RepoID: repo.ID, Type: kind}))
	}
	return repo
}

func acceptanceRevision(t *testing.T, ownerID int64) int64 {
	t.Helper()
	n, err := gm.GetNamespace(t.Context(), ownerID)
	require.NoError(t, err)
	return n.Revision
}

func acceptanceActor(id int64) gm.Actor {
	return gm.Actor{ID: id, Name: fmt.Sprintf("user%d", id), Kind: "user", Transport: "api"}
}

func TestMemberAcceptanceMinimalAccess(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	group, err := CreateGroup(ctx, acceptanceActor(2), GroupOption{Path: "minimal-members", Visibility: 2})
	require.NoError(t, err)
	require.NoError(t, SetGroupMember(ctx, acceptanceActor(2), group.ID, GroupMemberOption{UserID: 4, Role: gm.MinimalAccess, Revision: acceptanceRevision(t, group.ID)}, false))
	repo := acceptanceRepo(t, group.ID, "minimal-private")
	t.Run("NAV-06_仅群组最低权限不能枚举私有成员", func(t *testing.T) {
		_, err := ListRepositoryMembersView(ctx, 4, repo.ID, RepositoryMemberQuery{})
		assert.ErrorIs(t, err, gm.ErrNotFound)
	})
	t.Run("NAV-06_独立Guest授权后可读撤销后拒绝", func(t *testing.T) {
		require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, GroupMemberOption{UserID: 4, Role: gm.Guest, Revision: acceptanceRevision(t, group.ID)}, false))
		view, err := ListRepositoryMembersView(ctx, 4, repo.ID, RepositoryMemberQuery{})
		require.NoError(t, err)
		assert.False(t, view.CanManage)
		require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, GroupMemberOption{UserID: 4, Revision: acceptanceRevision(t, group.ID)}, true))
		_, err = ListRepositoryMembersView(ctx, 4, repo.ID, RepositoryMemberQuery{})
		assert.ErrorIs(t, err, gm.ErrNotFound)
	})
	t.Run("AUTH-11_不能直接添加项目MinimalAccess", func(t *testing.T) {
		err := SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, GroupMemberOption{UserID: 5, Role: gm.MinimalAccess, Revision: acceptanceRevision(t, group.ID)}, false)
		assert.ErrorIs(t, err, gm.ErrInvalid)
		unittest.AssertCount(t, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 5}, 0)
	})
	t.Run("NAV-07_最低权限不阻止公开基础访问", func(t *testing.T) {
		personal := acceptanceRepo(t, 2, "public-baseline")
		_, err := db.GetEngine(ctx).ID(personal.ID).Cols("is_private").Update(&repo_model.Repository{IsPrivate: false})
		require.NoError(t, err)
		view, err := ListRepositoryMembersView(ctx, 4, personal.ID, RepositoryMemberQuery{})
		require.NoError(t, err)
		assert.False(t, view.CanManage)
		assert.False(t, view.CanOwn)
	})
}

func TestMemberAcceptanceRoleMatrix(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	repo := acceptanceRepo(t, 2, "role-matrix")
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	for _, sample := range memberAcceptanceRoles {
		t.Run(fmt.Sprintf("AUTH-10_角色%d", sample.role), func(t *testing.T) {
			require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, GroupMemberOption{UserID: 4, Role: sample.role, Revision: acceptanceRevision(t, 2)}, false))
			view, err := ListRepositoryMembersView(ctx, 4, repo.ID, RepositoryMemberQuery{})
			require.NoError(t, err)
			assert.Equal(t, sample.manage, view.CanManage)
			assert.Equal(t, sample.owner, view.CanOwn)
			p, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
			require.NoError(t, err)
			for i, kind := range memberAcceptanceUnits {
				assert.Equal(t, sample.read&(1<<i) != 0, p.CanRead(kind), "单元 %v 的读取权限", kind)
				assert.Equal(t, sample.write&(1<<i) != 0, p.CanWrite(kind), "单元 %v 的写入权限", kind)
			}
			_, err = CheckRepositoryOwnerMutation(ctx, 4, repo.ID)
			if sample.owner {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, gm.ErrNotFound)
			}
			for _, targetRole := range []gm.Role{gm.Developer, gm.Owner} {
				err = SetRepositoryMember(ctx, acceptanceActor(4), repo.ID, GroupMemberOption{UserID: 5, Role: targetRole, Revision: acceptanceRevision(t, 2)}, false)
				allowed := sample.manage && (targetRole != gm.Owner || sample.owner)
				if allowed {
					require.NoError(t, err)
					require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, GroupMemberOption{UserID: 5, Revision: acceptanceRevision(t, 2)}, true))
				} else {
					assert.ErrorIs(t, err, gm.ErrNotFound)
				}
			}
		})
	}
}

func TestMemberAcceptanceShareMatrix(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	group, err := CreateGroup(ctx, acceptanceActor(2), GroupOption{Path: "share-matrix", Visibility: 2})
	require.NoError(t, err)
	repo := acceptanceRepo(t, 2, "share-matrix")
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	for _, source := range memberAcceptanceRoles {
		require.NoError(t, SetGroupMember(ctx, acceptanceActor(2), group.ID, GroupMemberOption{UserID: 4, Role: source.role, Revision: acceptanceRevision(t, group.ID)}, false))
		for _, ceiling := range memberAcceptanceRoles {
			t.Run(fmt.Sprintf("SRC-01_来源%d_上限%d", source.role, ceiling.role), func(t *testing.T) {
				require.NoError(t, SetRepositoryShare(ctx, acceptanceActor(2), repo.ID, GroupShareOption{GroupID: group.ID, MaxRole: ceiling.role, Revision: acceptanceRevision(t, 2)}, false))
				p, err := access_model.GetIndividualUserRepoPermission(ctx, repo, user)
				require.NoError(t, err)
				for i, kind := range memberAcceptanceUnits {
					assert.Equal(t, (source.read&ceiling.read)&(1<<i) != 0, p.CanRead(kind), "单元 %v 读取交集", kind)
					assert.Equal(t, (source.write&ceiling.write)&(1<<i) != 0, p.CanWrite(kind), "单元 %v 写入交集", kind)
				}
				assert.Equal(t, source.owner && ceiling.owner, p.IsOwner())
				view, err := ListRepositoryMembersView(ctx, 4, repo.ID, RepositoryMemberQuery{})
				require.NoError(t, err)
				assert.Equal(t, source.owner && ceiling.owner, view.CanOwn)
			})
		}
	}
}

func TestMemberAcceptanceOwnerTargetProtection(t *testing.T) {
	for _, source := range []string{"直接", "继承", "共享"} {
		t.Run("AUTH-03_AUTH-04_"+source, func(t *testing.T) {
			unittest.PrepareTestEnv(t)
			ctx := t.Context()
			require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
			group, err := CreateGroup(ctx, acceptanceActor(2), GroupOption{Path: "owner-target", Visibility: 2})
			require.NoError(t, err)
			ownerID := int64(2)
			if source == "继承" {
				ownerID = group.ID
			}
			repo := acceptanceRepo(t, ownerID, "owner-target")
			require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, GroupMemberOption{UserID: 5, Role: gm.Maintainer, Revision: acceptanceRevision(t, ownerID)}, false))
			if source == "直接" {
				require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, GroupMemberOption{UserID: 4, Role: gm.Owner, Revision: acceptanceRevision(t, ownerID)}, false))
			} else {
				require.NoError(t, SetGroupMember(ctx, acceptanceActor(2), group.ID, GroupMemberOption{UserID: 4, Role: gm.Owner, Revision: acceptanceRevision(t, group.ID)}, false))
				if source == "共享" {
					require.NoError(t, SetRepositoryShare(ctx, acceptanceActor(2), repo.ID, GroupShareOption{GroupID: group.ID, MaxRole: gm.Owner, Revision: acceptanceRevision(t, ownerID)}, false))
				}
				require.NoError(t, SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, GroupMemberOption{UserID: 4, Role: gm.Developer, Revision: acceptanceRevision(t, ownerID)}, false))
			}
			for _, operation := range []string{"角色", "期限", "移除"} {
				t.Run(operation, func(t *testing.T) {
					option := GroupMemberOption{UserID: 4, Role: gm.Developer, Revision: acceptanceRevision(t, ownerID)}
					if operation == "期限" {
						option.ExpiresUnix = time.Now().Add(time.Hour).Unix()
					}
					err := SetRepositoryMember(ctx, acceptanceActor(5), repo.ID, option, operation == "移除")
					assert.ErrorIs(t, err, gm.ErrNotFound)
					view, e := ListRepositoryMembersView(ctx, 4, repo.ID, RepositoryMemberQuery{})
					require.NoError(t, e)
					assert.True(t, view.CanOwn)
				})
			}
		})
	}
}

func TestMemberAcceptancePreviewIsolation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	repo := acceptanceRepo(t, 2, "preview-isolation")
	other := acceptanceRepo(t, 2, "preview-other")
	owner := acceptanceActor(2)
	change := RepositoryMemberChange{Kind: "member", Member: GroupMemberOption{UserID: 4, Role: gm.Developer, Revision: acceptanceRevision(t, 2)}}
	before, err := db.GetEngine(ctx).Count(new(gm.AuditEvent))
	require.NoError(t, err)
	preview, err := PreviewRepositoryMemberChange(ctx, owner, repo.ID, change)
	require.NoError(t, err)
	t.Run("PRE-01_预览不提交权限修订审计", func(t *testing.T) {
		unittest.AssertCount(t, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 4}, 0)
		assert.Equal(t, change.Member.Revision, acceptanceRevision(t, 2))
		after, e := db.GetEngine(ctx).Count(new(gm.AuditEvent))
		require.NoError(t, e)
		assert.Equal(t, before, after)
	})
	change.Member.PreviewToken = preview.Token
	t.Run("PRE-04_跨项目令牌拒绝", func(t *testing.T) {
		assert.ErrorIs(t, ApplyRepositoryMemberChange(ctx, owner, other.ID, change), gm.ErrConflict)
	})
	t.Run("PRE-04_无权操作者不能借令牌写入", func(t *testing.T) {
		assert.ErrorIs(t, ApplyRepositoryMemberChange(ctx, acceptanceActor(5), repo.ID, change), gm.ErrNotFound)
	})
	require.NoError(t, ApplyRepositoryMemberChange(ctx, owner, repo.ID, change))
	t.Run("PRE-04_已提交预览不可重放", func(t *testing.T) {
		assert.ErrorIs(t, ApplyRepositoryMemberChange(ctx, owner, repo.ID, change), gm.ErrConflict)
	})
	t.Run("PRE-03_操作者降权后拒绝提交", func(t *testing.T) {
		require.NoError(t, SetRepositoryMember(ctx, owner, repo.ID, GroupMemberOption{UserID: 5, Role: gm.Maintainer, Revision: acceptanceRevision(t, 2)}, false))
		next := RepositoryMemberChange{Kind: "member", Member: GroupMemberOption{UserID: 8, Role: gm.Reporter, Revision: acceptanceRevision(t, 2)}}
		p, e := PreviewRepositoryMemberChange(ctx, acceptanceActor(5), repo.ID, next)
		require.NoError(t, e)
		require.NoError(t, SetRepositoryMember(ctx, owner, repo.ID, GroupMemberOption{UserID: 5, Role: gm.Developer, Revision: acceptanceRevision(t, 2)}, false))
		next.Member.PreviewToken = p.Token
		assert.ErrorIs(t, ApplyRepositoryMemberChange(ctx, acceptanceActor(5), repo.ID, next), gm.ErrNotFound)
		unittest.AssertCount(t, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 8}, 0)
	})
}

func TestMemberAcceptancePreviewPayload(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	for _, sample := range []struct {
		name   string
		userID int64
		role   gm.Role
		expiry int64
	}{
		{"角色", 4, gm.Owner, 0}, {"用户", 5, gm.Developer, 0}, {"期限", 4, gm.Developer, time.Now().Add(time.Hour).Unix()},
	} {
		t.Run("PRE-02_替换"+sample.name, func(t *testing.T) {
			repo := acceptanceRepo(t, 2, fmt.Sprintf("preview-payload-%d-%d-%d", sample.userID, sample.role, sample.expiry))
			change := RepositoryMemberChange{Kind: "member", Member: GroupMemberOption{UserID: 4, Role: gm.Developer, Revision: acceptanceRevision(t, 2)}}
			preview, err := PreviewRepositoryMemberChange(ctx, acceptanceActor(2), repo.ID, change)
			require.NoError(t, err)
			option := GroupMemberOption{UserID: sample.userID, Role: sample.role, ExpiresUnix: sample.expiry, Revision: change.Member.Revision, PreviewToken: preview.Token}
			require.ErrorIs(t, SetRepositoryMember(ctx, acceptanceActor(2), repo.ID, option, false), gm.ErrConflict)
		})
	}
}

func TestMemberAcceptanceCustomRolesAndInvalidTargets(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	owner := acceptanceActor(2)
	repo := acceptanceRepo(t, 2, "custom-acceptance")
	for _, sample := range []struct {
		name                  string
		extra                 []string
		readCode, writeIssues bool
	}{
		{"代码读取", []string{gm.ReadCode}, true, false},
		{"议题管理", []string{gm.WriteIssues}, false, true},
	} {
		t.Run("VIEW-05_AUTH-10_"+sample.name, func(t *testing.T) {
			role, e := SaveRepositoryRootRole(ctx, owner, repo.ID, 0, GroupRoleOption{Name: sample.name, BaseRole: gm.Guest, Abilities: sample.extra, Revision: acceptanceRevision(t, 2)}, false)
			require.NoError(t, e)
			require.NoError(t, SetRepositoryMember(ctx, owner, repo.ID, GroupMemberOption{UserID: 4, Role: gm.Guest, CustomRoleID: role.ID, Revision: acceptanceRevision(t, 2)}, false))
			u := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
			p, e := access_model.GetIndividualUserRepoPermission(ctx, repo, u)
			require.NoError(t, e)
			assert.Equal(t, sample.readCode, p.CanRead(unit.TypeCode))
			assert.Equal(t, sample.writeIssues, p.CanWrite(unit.TypeIssues))
			assert.False(t, p.IsOwner())
			view, e := ListRepositoryMembersView(ctx, 4, repo.ID, RepositoryMemberQuery{Q: "user4"})
			require.NoError(t, e)
			require.Len(t, view.Members, 1)
			assert.NotEqual(t, gm.Owner, view.Members[0].Role)
		})
	}
	for _, sample := range []struct {
		name    string
		userID  int64
		role    gm.Role
		custom  int64
		expires int64
	}{
		{"未知角色", 5, 777, 0, 0}, {"组织账号", 3, gm.Developer, 0, 0}, {"未知账号", 999999, gm.Developer, 0, 0}, {"过期时间", 5, gm.Developer, 0, time.Now().Unix() - 1}, {"不存在自定义角色", 5, gm.Guest, 999999, 0},
	} {
		t.Run("AUTH-11_"+sample.name, func(t *testing.T) {
			rev := acceptanceRevision(t, 2)
			e := SetRepositoryMember(ctx, owner, repo.ID, GroupMemberOption{UserID: sample.userID, Role: sample.role, CustomRoleID: sample.custom, ExpiresUnix: sample.expires, Revision: rev}, false)
			assert.Error(t, e)
			assert.Equal(t, rev, acceptanceRevision(t, 2))
			unittest.AssertCount(t, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: sample.userID}, 0)
		})
	}
	t.Run("VIEW-11_站点管理员不自动成为项目成员", func(t *testing.T) {
		view, e := ListRepositoryMembersView(ctx, 1, repo.ID, RepositoryMemberQuery{})
		require.NoError(t, e)
		assert.True(t, view.CanOwn)
		for _, m := range view.Members {
			assert.NotEqual(t, int64(1), m.UserID)
		}
	})
}

func TestMemberAcceptancePaginationBoundaries(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, gm.InitializeLegacyNamespaces(ctx))
	repo := acceptanceRepo(t, 2, "pagination-boundaries")
	for i := range 1001 {
		u := &user_model.User{Name: fmt.Sprintf("accept-page-%04d", i), LowerName: fmt.Sprintf("accept-page-%04d", i), Email: fmt.Sprintf("accept-page-%d@example.invalid", i), IsActive: true}
		require.NoError(t, db.Insert(ctx, u))
		require.NoError(t, db.Insert(ctx, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: u.ID, Role: gm.Reporter}))
	}
	for _, limit := range []int{0, 1, 99, 100, 101, 205, 1001} {
		t.Run(fmt.Sprintf("VIEW-09_VIEW-10_人数%d", limit), func(t *testing.T) {
			// 使用明确的用户 ID 边界构造样本，查询预期不依赖生产分页代码。
			var users []*user_model.User
			require.NoError(t, db.GetEngine(ctx).Where("lower_name LIKE ?", "accept-page-%").Asc("id").Find(&users))
			require.NoError(t, func() error {
				_, e := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repo.ID).Delete(new(gm.Membership))
				return e
			}())
			for _, u := range users[:limit] {
				require.NoError(t, db.Insert(ctx, &gm.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: u.ID, Role: gm.Reporter}))
			}
			found := map[int64]bool{}
			cursor := int64(0)
			for range 12 {
				view, e := ListRepositoryMembersView(ctx, 2, repo.ID, RepositoryMemberQuery{Q: "accept-page-", Role: gm.Reporter, Source: "direct", AfterID: cursor})
				require.NoError(t, e)
				for _, m := range view.Members {
					assert.False(t, found[m.UserID], "不能重复计数")
					found[m.UserID] = true
					assert.Equal(t, gm.Reporter, m.Role)
				}
				if view.NextID == 0 {
					break
				}
				require.Greater(t, view.NextID, cursor)
				cursor = view.NextID
			}
			assert.Len(t, found, limit)
		})
	}
}
