// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"net/http"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceBranchProtectionAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	token := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
	endpoint := "/api/v1/repos/org3/repo3/branch_protections"
	MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, map[string]any{"rule_name": "audit-rule", "enable_push": true, "enable_bypass_allowlist": true, "bypass_allowlist_usernames": []string{"user2"}}).AddTokenAuth(token), http.StatusCreated)
	rule := unittest.AssertExistsAndLoadBean(t, &git_model.ProtectedBranch{RepoID: 3, RuleName: "audit-rule"})
	event := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "repository.branch_protection_created", ObjectID: rule.ID})
	require.EqualValues(t, 1, event.Actor.ID)
	require.Equal(t, "api", event.Actor.Transport)
	require.Equal(t, "org3/repo3:audit-rule", event.ObjectPath)
	require.Contains(t, event.AncestorIDs, int64(3))
	var details map[string]any
	require.NoError(t, json.Unmarshal(event.Details, &details))
	require.Nil(t, details["before"])
	require.Equal(t, true, details["after"].(map[string]any)["enable_bypass_allowlist"])
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", endpoint+"/audit-rule", map[string]any{"required_approvals": 2, "enable_force_push": true}).AddTokenAuth(token), http.StatusOK)
	updated := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "repository.branch_protection_updated", ObjectID: rule.ID})
	require.NoError(t, json.Unmarshal(updated.Details, &details))
	require.EqualValues(t, 2, details["after"].(map[string]any)["required_approvals"])
	MakeRequest(t, NewRequest(t, "DELETE", endpoint+"/audit-rule").AddTokenAuth(token), http.StatusNoContent)
	unittest.AssertCount(t, &git_model.ProtectedBranch{ID: rule.ID}, 0)
	unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "repository.branch_protection_deleted", ObjectID: rule.ID})
}

func TestGovernanceBranchProtectionStaleCleanup(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	rule := &git_model.ProtectedBranch{RepoID: 3, RuleName: "cleanup-rule", EnableBypassAllowlist: true, BypassAllowlistUserIDs: []int64{2}, BypassAllowlistTeamIDs: []int64{1}}
	require.NoError(t, db.Insert(ctx, rule))
	stale := *rule
	rule.BypassAllowlistUserIDs = []int64{2, 1}
	_, err := db.GetEngine(ctx).ID(rule.ID).Cols("bypass_allowlist_user_i_ds").Update(rule)
	require.NoError(t, err)
	require.NoError(t, git_model.RemoveUserIDFromProtectedBranch(ctx, &stale, 2))
	after := unittest.AssertExistsAndLoadBean(t, &git_model.ProtectedBranch{ID: rule.ID})
	require.Equal(t, []int64{1}, after.BypassAllowlistUserIDs, "清理旧快照不能丢失其后新增的绕过授权")
	require.NoError(t, git_model.RemoveTeamIDFromProtectedBranch(ctx, &stale, 1))
	after = unittest.AssertExistsAndLoadBean(t, &git_model.ProtectedBranch{ID: rule.ID})
	require.Empty(t, after.BypassAllowlistTeamIDs)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "repository.branch_protection_updated", ObjectID: rule.ID}, 2)
}

func TestGovernanceBranchProtectionAuditRollback(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	rule := &git_model.ProtectedBranch{RepoID: 3, RuleName: "rollback-rule", Priority: 5, BypassAllowlistUserIDs: []int64{2}}
	require.NoError(t, db.Insert(ctx, rule))
	create := "CREATE TRIGGER governance_protection_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type LIKE 'repository.branch_protection_%' BEGIN SELECT RAISE(ABORT, '保护分支审计故障'); END"
	drop := "DROP TRIGGER IF EXISTS governance_protection_fail"
	if setting.Database.Type.IsPostgreSQL() {
		_, err := db.GetEngine(ctx).Exec("CREATE FUNCTION governance_protection_fail_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type LIKE 'repository.branch_protection_%' THEN RAISE EXCEPTION '保护分支审计故障'; END IF; RETURN NEW; END $$")
		require.NoError(t, err)
		defer func() {
			_, err := db.GetEngine(context.WithoutCancel(ctx)).Exec("DROP FUNCTION IF EXISTS governance_protection_fail_fn() CASCADE")
			require.NoError(t, err)
		}()
		create = "CREATE TRIGGER governance_protection_fail BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION governance_protection_fail_fn()"
		drop += " ON governance_audit_event"
	}
	_, err := db.GetEngine(ctx).Exec(create)
	require.NoError(t, err)
	defer func() { _, err := db.GetEngine(context.WithoutCancel(ctx)).Exec(drop); require.NoError(t, err) }()
	created := &git_model.ProtectedBranch{RepoID: 3, RuleName: "failed-create"}
	require.Error(t, git_model.UpdateProtectBranch(ctx, repo, created, git_model.WhitelistOptions{}))
	require.Zero(t, created.ID)
	changed := *rule
	changed.RequiredApprovals = 3
	require.Error(t, git_model.UpdateProtectBranch(ctx, repo, &changed, git_model.WhitelistOptions{BypassUserIDs: []int64{2}}))
	require.Error(t, git_model.UpdateProtectBranchPriorities(ctx, repo, []int64{rule.ID}))
	require.Error(t, git_model.RemoveUserIDFromProtectedBranch(ctx, rule, 2))
	require.Error(t, git_model.DeleteProtectedBranch(ctx, repo, rule.ID))
	after := unittest.AssertExistsAndLoadBean(t, &git_model.ProtectedBranch{ID: rule.ID})
	require.EqualValues(t, 5, after.Priority)
	require.Zero(t, after.RequiredApprovals)
	require.Equal(t, []int64{2}, after.BypassAllowlistUserIDs)
	unittest.AssertCount(t, &git_model.ProtectedBranch{RepoID: 3, RuleName: "failed-create"}, 0)
}

func TestGovernanceBranchProtectionFreshAuthorization(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	rule := &git_model.ProtectedBranch{RepoID: 3, RuleName: "fresh-authorization", Priority: 5}
	require.NoError(t, db.Insert(ctx, rule))
	actor := governance_model.Actor{ID: 1, Name: "user1", Kind: "user", Transport: "api"}
	authorized := governance_model.WithAuditActor(ctx, actor)
	require.NoError(t, git_model.UpdateProtectBranchPriorities(authorized, repo, []int64{rule.ID}))
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "repository.branch_protection_updated", ObjectID: rule.ID}, 1)
	require.NoError(t, git_model.UpdateProtectBranchPriorities(authorized, repo, []int64{rule.ID}))
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "repository.branch_protection_updated", ObjectID: rule.ID}, 1)
	_, err := db.GetEngine(ctx).ID(1).Cols("is_admin").Update(&user_model.User{IsAdmin: false})
	require.NoError(t, err)
	// 使用撤权之前建立的请求身份，最终写入仍须重新核对权限。
	require.ErrorIs(t, git_model.UpdateProtectBranch(authorized, repo, rule, git_model.WhitelistOptions{}), governance_model.ErrForbidden)
	require.ErrorIs(t, git_model.UpdateProtectBranchPriorities(authorized, repo, []int64{rule.ID}), governance_model.ErrForbidden)
	require.ErrorIs(t, git_model.DeleteProtectedBranch(authorized, repo, rule.ID), governance_model.ErrForbidden)
	actor.ActingAsID = 2
	require.ErrorIs(t, git_model.DeleteProtectedBranch(governance_model.WithAuditActor(ctx, actor), repo, rule.ID), governance_model.ErrForbidden)
	unittest.AssertCount(t, &git_model.ProtectedBranch{ID: rule.ID}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "repository.branch_protection_deleted", ObjectID: rule.ID}, 0)
}

func TestGovernanceBranchProtectionArchivedAndForeignRule(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	rule := &git_model.ProtectedBranch{RepoID: 3, RuleName: "archive-test", Priority: 5}
	foreign := &git_model.ProtectedBranch{RepoID: 1, RuleName: "foreign-test", Priority: 9}
	require.NoError(t, db.Insert(ctx, rule, foreign))
	require.ErrorIs(t, git_model.UpdateProtectBranchPriorities(ctx, repo, []int64{rule.ID, foreign.ID}), governance_model.ErrNotFound)
	require.EqualValues(t, 5, unittest.AssertExistsAndLoadBean(t, &git_model.ProtectedBranch{ID: rule.ID}).Priority)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "repository.branch_protection_updated", ObjectID: rule.ID}, 0)
	require.ErrorIs(t, git_model.DeleteProtectedBranch(ctx, repo, foreign.ID), governance_model.ErrNotFound)
	candidate := *foreign
	candidate.RepoID = repo.ID
	require.ErrorIs(t, git_model.UpdateProtectBranch(ctx, repo, &candidate, git_model.WhitelistOptions{}), governance_model.ErrNotFound)
	_, err := db.GetEngine(ctx).ID(repo.ID).Cols("is_archived").Update(&repo_model.Repository{IsArchived: true})
	require.NoError(t, err)
	require.False(t, repo.IsArchived, "请求仍持有归档前的仓库快照")
	require.Error(t, git_model.UpdateProtectBranch(ctx, repo, rule, git_model.WhitelistOptions{}))
	require.Error(t, git_model.UpdateProtectBranchPriorities(ctx, repo, []int64{rule.ID}))
	require.Error(t, git_model.DeleteProtectedBranch(ctx, repo, rule.ID))
	unittest.AssertCount(t, &git_model.ProtectedBranch{ID: rule.ID}, 1)
}
