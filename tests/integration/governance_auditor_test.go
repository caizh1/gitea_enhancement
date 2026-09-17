// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/optional"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	governance_api "gitea.dev/routers/api/v1/governance"
	governance_service "gitea.dev/services/governance"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceAuditorPermissions(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	admin := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
	token := getUserToken(t, "user4", auth_model.AccessTokenScopeAll)
	setAuditor := func(enabled bool) {
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user4", api.EditUserOption{Auditor: &enabled}).AddTokenAuth(admin), http.StatusOK)
	}
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/user2/repo2").AddTokenAuth(token), http.StatusNotFound)
	setAuditor(true)
	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	require.True(t, actor.IsAuditor)
	event := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "user.auditor_changed", ObjectID: 4})
	require.EqualValues(t, 1, event.Actor.ID)
	for _, endpoint := range []string{"/api/v1/repos/user2/repo2", "/api/v1/repos/user2/repo2/issues", "/api/v1/repos/org3/repo3", "/api/v1/governance/audit-events?scope_type=instance", "/api/v1/governance/repositories/2/members?include_inherited=true", "/api/v1/governance/groups/3/members"} {
		MakeRequest(t, NewRequest(t, "GET", endpoint).AddTokenAuth(token), http.StatusOK)
	}
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/admin/users").AddTokenAuth(token), http.StatusForbidden)
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/audit-streams?scope_type=instance").AddTokenAuth(token), http.StatusNotFound)
	MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo2/issues", api.CreateIssueOption{Title: "审计员不得创建"}).AddTokenAuth(token), http.StatusForbidden)
	MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo1/issues", api.CreateIssueOption{Title: "公开项目也不额外授予写入"}).AddTokenAuth(token), http.StatusForbidden)
	MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo2/issues/1/comments", api.CreateIssueCommentOption{Body: "禁止只读身份评论"}).AddTokenAuth(token), http.StatusForbidden)
	MakeRequest(t, NewRequest(t, "DELETE", "/api/v1/repos/user2/repo2").AddTokenAuth(token), http.StatusForbidden)
	member := &governance_model.Membership{ScopeType: "repository", ScopeID: 2, UserID: 4, Role: governance_model.Developer}
	require.NoError(t, db.Insert(ctx, member))
	MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo2/issues", api.CreateIssueOption{Title: "独立开发者授权允许创建"}).AddTokenAuth(token), http.StatusCreated)
	custom := &governance_model.CustomRole{RootID: 2, Name: "仅代码读取", BaseRole: governance_model.MinimalAccess, Abilities: []string{governance_model.ReadCode}}
	require.NoError(t, db.Insert(ctx, custom))
	member.Role, member.CustomRoleID = governance_model.MinimalAccess, custom.ID
	_, err := db.GetEngine(ctx).ID(member.ID).Cols("role", "custom_role_id").Update(member)
	require.NoError(t, err)
	// 原生单元门禁隐藏无权使用的 Issue 单元，返回 404。
	MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo2/issues", api.CreateIssueOption{Title: "代码单元授权不能借审计员扩充 Issue 写入"}).AddTokenAuth(token), http.StatusNotFound)
	_, err = db.GetEngine(ctx).ID(member.ID).Delete(new(governance_model.Membership))
	require.NoError(t, err)
	MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo2/issues", api.CreateIssueOption{Title: "撤销独立授权立即拒绝"}).AddTokenAuth(token), http.StatusForbidden)
	session := loginUser(t, "user4")
	session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo2"), http.StatusOK)
	session.MakeRequest(t, NewRequest(t, "GET", "/governance/groups/3"), http.StatusOK)
	session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo2/issues/new"), http.StatusForbidden)
	listResponse := session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo2/issues"), http.StatusOK)
	require.NotContains(t, listResponse.Body.String(), "issue-list-new", "只读审计员页面不显示新建 Issue 按钮")
	session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo2/settings"), http.StatusNotFound)
	session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/repo2/issues/new", map[string]string{"title": "网页创建拒绝"}), http.StatusForbidden)
	response := session.MakeRequest(t, NewRequest(t, "GET", "/governance/repositories/2/members"), http.StatusOK)
	require.Contains(t, response.Body.String(), "全站审计员只读查看成员来源")
	require.NotContains(t, response.Body.String(), "保存直接授权")
	options := governance_api.AuditExportOption{Filter: governance_model.AuditFilter{ScopeType: "instance", From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Second)}, Format: "json"}
	response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/governance/audit-exports", options).AddTokenAuth(token), http.StatusAccepted)
	job := DecodeJSON(t, response, &governance_model.AuditExport{})
	require.NoError(t, governance_service.RunAuditExports(ctx))
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/audit-exports/"+job.ID+"/download").AddTokenAuth(token), http.StatusOK)
	setAuditor(false)
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/audit-exports/"+job.ID+"/download").AddTokenAuth(token), http.StatusNotFound)
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/user2/repo2").AddTokenAuth(token), http.StatusNotFound)
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/governance/audit-events?scope_type=instance").AddTokenAuth(token), http.StatusNotFound)
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	permission, err := access_model.GetIndividualUserRepoPermission(ctx, repo, actor)
	require.NoError(t, err)
	require.False(t, permission.CanRead(unit.TypeCode), "旧账号快照不能延续审计员读取权限")
}

func TestGovernanceAuditorAuditRollback(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	admin := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
	create := "CREATE TRIGGER governance_auditor_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'user.auditor_changed' BEGIN SELECT RAISE(ABORT, '审计员变更审计故障'); END"
	drop := "DROP TRIGGER IF EXISTS governance_auditor_fail"
	if setting.Database.Type.IsPostgreSQL() {
		_, err := db.GetEngine(ctx).Exec("CREATE FUNCTION governance_auditor_fail_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type = 'user.auditor_changed' THEN RAISE EXCEPTION '审计员变更审计故障'; END IF; RETURN NEW; END $$")
		require.NoError(t, err)
		defer func() {
			_, err := db.GetEngine(context.WithoutCancel(ctx)).Exec("DROP FUNCTION IF EXISTS governance_auditor_fail_fn() CASCADE")
			require.NoError(t, err)
		}()
		create = "CREATE TRIGGER governance_auditor_fail BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION governance_auditor_fail_fn()"
		drop += " ON governance_audit_event"
	}
	_, err := db.GetEngine(ctx).Exec(create)
	require.NoError(t, err)
	defer func() { _, err := db.GetEngine(context.WithoutCancel(ctx)).Exec(drop); require.NoError(t, err) }()
	enabled := true
	before := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	require.NoError(t, auth_model.InsertAuthToken(ctx, &auth_model.AuthToken{ID: "auditor-atomic-update", UserID: 4, TokenHash: "验收占位摘要"}))
	email := "atomic-auditor@example.invalid"
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user4", api.EditUserOption{Auditor: &enabled, Password: "Atomic-test-Password!42", Email: &email}).AddTokenAuth(admin), http.StatusInternalServerError)
	after := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	require.Equal(t, before.Passwd, after.Passwd, "角色审计失败必须回滚同次保存的密码")
	require.Equal(t, before.Email, after.Email, "角色审计失败必须回滚同次保存的邮箱")
	unittest.AssertExistsAndLoadBean(t, &auth_model.AuthToken{ID: "auditor-atomic-update"})
	require.False(t, unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4}).IsAuditor)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "user.auditor_changed"}, 0)
	_, err = db.GetEngine(ctx).ID(4).Cols("is_auditor").Update(&user_model.User{IsAuditor: true})
	require.NoError(t, err)
	disabled := false
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user4", api.EditUserOption{Auditor: &disabled}).AddTokenAuth(admin), http.StatusInternalServerError)
	require.True(t, unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4}).IsAuditor, "审计失败不能虚假报告已撤销身份")
	MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/admin/users", api.CreateUserOption{Username: "audit-failed-create", Email: "audit-failed@example.invalid", Password: "Audit-test-Password!42", Auditor: &enabled}).AddTokenAuth(admin), http.StatusInternalServerError)
	unittest.AssertCount(t, &user_model.User{Name: "audit-failed-create"}, 0)
}

func TestGovernanceAuditorRejectsRevokedAdministrator(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 1, Name: "user1", Kind: "user", Transport: "api"}
	ctx = governance_model.WithAuditActor(ctx, actor)
	_, err := db.GetEngine(ctx).ID(1).Cols("is_admin").Update(&user_model.User{IsAdmin: false})
	require.NoError(t, err)
	target := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	target.IsAuditor = true
	require.ErrorIs(t, user_model.UpdateUserCols(ctx, target, "is_auditor"), governance_model.ErrForbidden)
	require.False(t, unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4}).IsAuditor)
	created := &user_model.User{Name: "revoked-admin-auditor", Email: "revoked-auditor@example.invalid"}
	created.IsAuditor = true
	require.ErrorIs(t, user_model.AdminCreateUser(ctx, created, &user_model.Meta{}), governance_model.ErrForbidden)
	unittest.AssertCount(t, &user_model.User{Name: created.Name}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "user.auditor_changed"}, 0)
}

func TestGovernanceAuditorPrivateOrganization(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	_, err := db.GetEngine(ctx).ID(3).Cols("visibility").Update(&user_model.User{Visibility: api.VisibleTypePrivate})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(3).Cols("visibility").Update(&governance_model.Namespace{Visibility: int(api.VisibleTypePrivate)})
	require.NoError(t, err)
	admin := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
	enabled := true
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user5", api.EditUserOption{Auditor: &enabled}).AddTokenAuth(admin), http.StatusOK)
	session := loginUser(t, "user5")
	token := getUserToken(t, "user5", auth_model.AccessTokenScopeAll)
	for _, path := range []string{"/api/v1/orgs/org3", "/api/v1/orgs/org3/teams", "/api/v1/teams/1", "/api/v1/teams/1/members", "/api/v1/teams/1/repos"} {
		MakeRequest(t, NewRequest(t, "GET", path).AddTokenAuth(token), http.StatusOK)
	}
	for _, path := range []string{"/org3", "/org/org3/members", "/org/org3/teams", "/org/org3/teams/owners"} {
		session.MakeRequest(t, NewRequest(t, "GET", path), http.StatusOK)
	}
	session.MakeRequest(t, NewRequest(t, "GET", "/org/org3/settings"), http.StatusNotFound)
	session.MakeRequest(t, NewRequestWithValues(t, "POST", "/org/org3/teams/owners/action/join", nil), http.StatusNotFound)
	unittest.AssertCount(t, &organization.OrgUser{OrgID: 3, UID: 5}, 0)
	unittest.AssertCount(t, &organization.TeamUser{TeamID: 1, UID: 5}, 0)
	actor := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	options := &issues_model.IssuesOptions{Doer: actor, RepoIDs: []int64{2}, IsPull: optional.Some(false)}
	issues, err := issues_model.Issues(ctx, options)
	require.NoError(t, err)
	require.NotEmpty(t, issues, "聚合查询必须包含可审计的私有项目")
	disabled := false
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/admin/users/user5", api.EditUserOption{Auditor: &disabled}).AddTokenAuth(admin), http.StatusOK)
	issues, err = issues_model.Issues(ctx, options)
	require.NoError(t, err)
	require.Empty(t, issues, "撤销后旧账号快照不能继续查询私有项目")
}
