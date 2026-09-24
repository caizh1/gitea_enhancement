// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	governance_service "gitea.dev/services/governance"
	org_service "gitea.dev/services/org"
	repo_service "gitea.dev/services/repository"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestTeamMemberAndRepositoryConcurrentRevocation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	if !setting.Database.Type.IsPostgreSQL() {
		t.Skip("固定行锁交错仅在 PostgreSQL 验收")
	}
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	target := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 5})
	require.EqualValues(t, 3, repo.OwnerID)

	for _, sample := range []struct {
		name   string
		slug   string
		direct bool
	}{
		{name: "唯一Team来源", slug: "only"},
		{name: "保留治理直接Reporter", slug: "direct", direct: true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			team := &organization.Team{
				OrgID: repo.OwnerID, Name: "t02-" + sample.slug, AccessMode: perm.AccessModeRead,
				Units: []*organization.TeamUnit{
					{OrgID: repo.OwnerID, Type: unit.TypeCode, AccessMode: perm.AccessModeRead},
					{OrgID: repo.OwnerID, Type: unit.TypeIssues, AccessMode: perm.AccessModeRead},
				},
			}
			require.NoError(t, org_service.NewTeamAsOwner(ctx, actor, team))
			require.NoError(t, repo_service.ChangeTeamRepositoryAsOwner(ctx, actor, team, repo.ID, true))
			require.NoError(t, org_service.AddTeamMemberAsOwner(ctx, actor, team, target))
			if sample.direct {
				namespace, err := governance_model.GetNamespace(ctx, repo.OwnerID)
				require.NoError(t, err)
				owner := governance_model.Actor{ID: actor.ID, Name: actor.Name, Kind: "user", Transport: "api"}
				require.NoError(t, governance_service.SetRepositoryMember(ctx, owner, repo.ID,
					governance_service.GroupMemberOption{UserID: target.ID, Role: governance_model.Reporter, Revision: namespace.Revision}, false))
			}
			hasAccess, err := access_model.HasAnyUnitAccess(ctx, target.ID, repo)
			require.NoError(t, err)
			require.True(t, hasAccess)

			entered := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseLock := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseLock()
			holder := make(chan error, 1)
			go func() {
				holder <- governance_model.WithWrite(ctx, nil, func(context.Context) error {
					close(entered)
					<-release
					return nil
				})
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				require.FailNow(t, "未取得治理写入行锁")
			}

			ready := make(chan struct{}, 2)
			start := make(chan struct{})
			memberDone := make(chan error, 1)
			repoDone := make(chan error, 1)
			go func() {
				ready <- struct{}{}
				<-start
				memberDone <- org_service.RemoveTeamMemberAsOwner(ctx, actor, team, target)
			}()
			go func() {
				ready <- struct{}{}
				<-start
				repoDone <- repo_service.ChangeTeamRepositoryAsOwner(ctx, actor, team, repo.ID, false)
			}()
			<-ready
			<-ready
			close(start)
			require.Eventually(t, func() bool {
				var count struct {
					N int64 `xorm:"n"`
				}
				_, err := db.GetEngine(ctx).SQL("SELECT count(*) AS n FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND wait_event_type = 'Lock' AND query LIKE '%governance_write_lock%'").Get(&count)
				return err == nil && count.N >= 2
			}, 10*time.Second, 20*time.Millisecond, "两项服务撤权均须等待同一 PostgreSQL 治理写锁")
			releaseLock()
			require.NoError(t, <-holder)
			require.NoError(t, <-memberDone)
			require.NoError(t, <-repoDone)

			unittest.AssertNotExistsBean(t, &organization.TeamUser{TeamID: team.ID, UID: target.ID})
			unittest.AssertNotExistsBean(t, &organization.TeamRepo{TeamID: team.ID, RepoID: repo.ID})
			hasAccess, err = access_model.HasAnyUnitAccess(ctx, target.ID, repo)
			require.NoError(t, err)
			require.Equal(t, sample.direct, hasAccess)
			if sample.direct {
				unittest.AssertExistsAndLoadBean(t, &governance_model.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: target.ID, Role: governance_model.Reporter})
			}
		})
	}
}
