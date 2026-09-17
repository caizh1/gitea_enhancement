// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
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
	api "gitea.dev/modules/structs"
	repository_service "gitea.dev/services/repository"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceTagProtectionAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	token := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
	endpoint := "/api/v1/repos/org3/repo3/tag_protections"
	response := MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, api.CreateTagProtectionOption{NamePattern: "audit-*", WhitelistUsernames: []string{"user2"}}).AddTokenAuth(token), http.StatusCreated)
	tag := DecodeJSON(t, response, &api.TagProtection{})
	created := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "repository.tag_protection_created", ObjectID: tag.ID})
	require.EqualValues(t, 1, created.Actor.ID)
	require.Equal(t, "api", created.Actor.Transport)
	require.Equal(t, "org3/repo3:audit-*", created.ObjectPath)
	require.Contains(t, created.AncestorIDs, int64(3))
	endpoint += fmt.Sprintf("/%d", tag.ID)
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", endpoint, map[string]any{"name_pattern": "release-*", "whitelist_usernames": []string{}}).AddTokenAuth(token), http.StatusOK)
	updated := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "repository.tag_protection_updated", ObjectID: tag.ID})
	var details map[string]any
	require.NoError(t, json.Unmarshal(updated.Details, &details))
	require.Equal(t, "audit-*", details["before"].(map[string]any)["name_pattern"])
	require.Equal(t, "release-*", details["after"].(map[string]any)["name_pattern"])
	require.Empty(t, details["after"].(map[string]any)["allowlist_user_ids"])
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", endpoint, map[string]any{"name_pattern": "release-*"}).AddTokenAuth(token), http.StatusOK)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "repository.tag_protection_updated", ObjectID: tag.ID}, 1)
	MakeRequest(t, NewRequest(t, "DELETE", endpoint).AddTokenAuth(token), http.StatusNoContent)
	unittest.AssertCount(t, &git_model.ProtectedTag{ID: tag.ID}, 0)
	unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "repository.tag_protection_deleted", ObjectID: tag.ID})
}

func TestGovernanceTagProtectionAuditRollback(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	tag := &git_model.ProtectedTag{RepoID: 3, NamePattern: "rollback-*", AllowlistUserIDs: []int64{2}}
	require.NoError(t, db.Insert(ctx, tag))
	create := "CREATE TRIGGER governance_tag_protection_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type LIKE 'repository.tag_protection_%' BEGIN SELECT RAISE(ABORT, '标签保护审计故障'); END"
	drop := "DROP TRIGGER IF EXISTS governance_tag_protection_fail"
	if setting.Database.Type.IsPostgreSQL() {
		_, err := db.GetEngine(ctx).Exec("CREATE FUNCTION governance_tag_protection_fail_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type LIKE 'repository.tag_protection_%' THEN RAISE EXCEPTION '标签保护审计故障'; END IF; RETURN NEW; END $$")
		require.NoError(t, err)
		defer func() {
			_, err := db.GetEngine(context.WithoutCancel(ctx)).Exec("DROP FUNCTION IF EXISTS governance_tag_protection_fail_fn() CASCADE")
			require.NoError(t, err)
		}()
		create = "CREATE TRIGGER governance_tag_protection_fail BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION governance_tag_protection_fail_fn()"
		drop += " ON governance_audit_event"
	}
	_, err := db.GetEngine(ctx).Exec(create)
	require.NoError(t, err)
	defer func() { _, err := db.GetEngine(context.WithoutCancel(ctx)).Exec(drop); require.NoError(t, err) }()
	fresh := &git_model.ProtectedTag{RepoID: 3, NamePattern: "failed-*"}
	require.Error(t, git_model.InsertProtectedTag(ctx, fresh))
	require.Zero(t, fresh.ID)
	candidate := *tag
	candidate.NamePattern = "changed-*"
	candidate.AllowlistUserIDs = nil
	require.Error(t, git_model.UpdateProtectedTag(ctx, &candidate))
	require.Error(t, git_model.DeleteProtectedTag(ctx, tag))
	branch := &git_model.ProtectedBranch{RepoID: 3, RuleName: "cleanup-rollback"}
	require.NoError(t, db.Insert(ctx, branch))
	require.Error(t, repository_service.DeleteRepositoryDirectly(ctx, 3))
	unittest.AssertCount(t, &repo_model.Repository{ID: 3}, 1)
	unittest.AssertCount(t, &git_model.ProtectedBranch{ID: branch.ID}, 1)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "repository.branch_protection_deleted", ObjectID: branch.ID}, 0)
	retained := unittest.AssertExistsAndLoadBean(t, &git_model.ProtectedTag{ID: tag.ID})
	require.Equal(t, "rollback-*", retained.NamePattern)
	require.Equal(t, []int64{2}, retained.AllowlistUserIDs)
	unittest.AssertCount(t, &git_model.ProtectedTag{RepoID: 3, NamePattern: "failed-*"}, 0)
}

func TestGovernanceTagProtectionFreshAuthorization(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	tag := &git_model.ProtectedTag{RepoID: 3, NamePattern: "boundary-*"}
	require.NoError(t, db.Insert(ctx, tag))
	actor := governance_model.Actor{ID: 1, Name: "user1", Kind: "user", Transport: "api"}
	authorized := governance_model.WithAuditActor(ctx, actor)
	foreign := *tag
	foreign.RepoID = 1
	require.ErrorIs(t, git_model.UpdateProtectedTag(authorized, &foreign), governance_model.ErrNotFound)
	require.ErrorIs(t, git_model.DeleteProtectedTag(authorized, &foreign), governance_model.ErrNotFound)
	_, err := db.GetEngine(ctx).ID(1).Cols("is_admin").Update(&user_model.User{IsAdmin: false})
	require.NoError(t, err)
	require.ErrorIs(t, git_model.UpdateProtectedTag(authorized, tag), governance_model.ErrForbidden)
	require.ErrorIs(t, git_model.DeleteProtectedTag(authorized, tag), governance_model.ErrForbidden)
	require.ErrorIs(t, git_model.InsertProtectedTag(authorized, &git_model.ProtectedTag{RepoID: 3, NamePattern: "denied-*"}), governance_model.ErrForbidden)
	_, err = db.GetEngine(ctx).ID(3).Cols("is_archived").Update(&repo_model.Repository{IsArchived: true})
	require.NoError(t, err)
	require.Error(t, git_model.UpdateProtectedTag(ctx, tag))
	require.Error(t, git_model.DeleteProtectedTag(ctx, tag))
	unittest.AssertCount(t, &git_model.ProtectedTag{ID: tag.ID}, 1)
}

func TestGovernanceRepositoryDeletionProtectionAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	branch := &git_model.ProtectedBranch{RepoID: 3, RuleName: "deleted-project-branch"}
	tag := &git_model.ProtectedTag{RepoID: 3, NamePattern: "deleted-project-*"}
	require.NoError(t, db.Insert(ctx, branch, tag))
	require.NoError(t, repository_service.DeleteRepositoryDirectly(ctx, 3))
	unittest.AssertCount(t, &repo_model.Repository{ID: 3}, 0)
	branchEvent := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "repository.branch_protection_deleted", ObjectID: branch.ID})
	tagEvent := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "repository.tag_protection_deleted", ObjectID: tag.ID})
	require.Equal(t, "org3/repo3:deleted-project-branch", branchEvent.ObjectPath)
	require.Equal(t, "org3/repo3:deleted-project-*", tagEvent.ObjectPath)
	require.Contains(t, tagEvent.AncestorIDs, int64(3))
}
