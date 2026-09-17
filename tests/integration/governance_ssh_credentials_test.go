// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	asymkey_model "gitea.dev/models/asymkey"
	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	asymkey_service "gitea.dev/services/asymkey"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

const governanceCredentialPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICV0MGX/W9IvLA4FXpIuUcdDcbj5KX4syHgsTy7soVgf"

func TestGovernanceSSHCredentialHTTP(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteUser, auth_model.AccessTokenScopeWriteRepository)
	response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/user/keys", api.CreateKeyOption{Title: "原生公钥审计", Key: governanceCredentialPublicKey}).AddTokenAuth(token), http.StatusCreated)
	key := DecodeJSON(t, response, &api.PublicKey{})
	event := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.ssh_key_created", ObjectID: key.ID})
	require.EqualValues(t, 2, event.Actor.ID)
	require.Equal(t, "api", event.Actor.Transport)
	require.NotContains(t, string(event.Details), governanceCredentialPublicKey)
	MakeRequest(t, NewRequest(t, "DELETE", fmt.Sprintf("/api/v1/user/keys/%d", key.ID)).AddTokenAuth(token), http.StatusNoContent)
	event = unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.ssh_key_revoked", ObjectID: key.ID})
	require.EqualValues(t, 2, event.Actor.ID)
	response = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo1/keys", api.CreateKeyOption{Title: "原生部署审计", Key: governanceCredentialPublicKey, ReadOnly: true}).AddTokenAuth(token), http.StatusCreated)
	deploy := DecodeJSON(t, response, &api.DeployKey{})
	event = unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.deploy_key_created", ObjectID: deploy.ID})
	require.EqualValues(t, 2, event.Actor.ID)
	require.Equal(t, "repository", event.ScopeType)
	require.EqualValues(t, 1, event.ScopeID)
	require.Contains(t, string(event.Details), "user2/repo1")
	MakeRequest(t, NewRequest(t, "DELETE", fmt.Sprintf("/api/v1/repos/user2/repo1/keys/%d", deploy.ID)).AddTokenAuth(token), http.StatusNoContent)
	unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.deploy_key_revoked", ObjectID: deploy.ID})
}

func TestGovernanceSSHCredentialAuditRollback(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteUser, auth_model.AccessTokenScopeWriteRepository)
	create := "CREATE TRIGGER governance_ssh_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type IN ('credential.ssh_key_created', 'credential.deploy_key_created') BEGIN SELECT RAISE(ABORT, '密钥审计故障'); END"
	drop := "DROP TRIGGER IF EXISTS governance_ssh_fail"
	if setting.Database.Type.IsPostgreSQL() {
		_, err := db.GetEngine(ctx).Exec("CREATE FUNCTION governance_ssh_fail_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type IN ('credential.ssh_key_created', 'credential.deploy_key_created') THEN RAISE EXCEPTION '密钥审计故障'; END IF; RETURN NEW; END $$")
		require.NoError(t, err)
		defer func() {
			_, err := db.GetEngine(context.WithoutCancel(ctx)).Exec("DROP FUNCTION IF EXISTS governance_ssh_fail_fn() CASCADE")
			require.NoError(t, err)
		}()
		create = "CREATE TRIGGER governance_ssh_fail BEFORE INSERT ON governance_audit_event FOR EACH ROW EXECUTE FUNCTION governance_ssh_fail_fn()"
		drop += " ON governance_audit_event"
	}
	_, err := db.GetEngine(ctx).Exec(create)
	require.NoError(t, err)
	defer func() { _, err := db.GetEngine(context.WithoutCancel(ctx)).Exec(drop); require.NoError(t, err) }()
	for _, endpoint := range []string{"/api/v1/user/keys", "/api/v1/repos/user2/repo1/keys"} {
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, api.CreateKeyOption{Title: "失败密钥", Key: governanceCredentialPublicKey}).AddTokenAuth(token), http.StatusInternalServerError)
	}
	unittest.AssertCount(t, &asymkey_model.PublicKey{Content: governanceCredentialPublicKey}, 0)
	unittest.AssertCount(t, &asymkey_model.DeployKey{Name: "失败密钥"}, 0)
	unittest.AssertCount(t, &governance_model.SSHKeyFileSync{}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "credential.ssh_key_created"}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "credential.deploy_key_created"}, 0)
}

func TestGovernanceSSHCredentialSudoAndWeb(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	token := getUserToken(t, "user1", auth_model.AccessTokenScopeWriteUser)
	request := NewRequestWithJSON(t, "POST", "/api/v1/user/keys", api.CreateKeyOption{Title: "管理员代办密钥", Key: governanceCredentialPublicKey}).AddTokenAuth(token)
	request.Header.Set("Sudo", "user2")
	response := MakeRequest(t, request, http.StatusCreated)
	key := DecodeJSON(t, response, &api.PublicKey{})
	event := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.ssh_key_created", ObjectID: key.ID})
	require.EqualValues(t, 1, event.Actor.ID)
	require.EqualValues(t, 2, event.Actor.ActingAsID)
	require.EqualValues(t, 2, event.ScopeID)
	session := loginUser(t, "user2")
	session.MakeRequest(t, NewRequest(t, "GET", "/user/settings/keys"), http.StatusOK)
	session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user/settings/keys/delete", map[string]string{"type": "ssh", "id": strconv.FormatInt(key.ID, 10)}), http.StatusOK)
	event = unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.ssh_key_revoked", ObjectID: key.ID})
	require.EqualValues(t, 2, event.Actor.ID)
	require.Zero(t, event.Actor.ActingAsID)
	require.Equal(t, "web", event.Actor.Transport)
	session.MakeRequest(t, NewRequest(t, "GET", "/user/settings/keys"), http.StatusOK)
	session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user/settings/keys", map[string]string{"type": "ssh", "title": "网页密钥", "content": governanceCredentialPublicKey}), http.StatusSeeOther)
	created := unittest.AssertExistsAndLoadBean(t, &asymkey_model.PublicKey{Name: "网页密钥"})
	event = unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.ssh_key_created", ObjectID: created.ID})
	require.EqualValues(t, 2, event.Actor.ID)
	require.Equal(t, "web", event.Actor.Transport)
}

func TestGovernanceSSHSharedKeyConcurrent(t *testing.T) {
	if !setting.Database.Type.IsPostgreSQL() {
		t.Skip("共享引用锁的阻塞顺序使用目标 PostgreSQL 验证")
	}
	defer tests.PrepareTestEnv(t)()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	first, err := asymkey_service.AddDeployKey(ctx, 1, "最后一个关联", governanceCredentialPublicKey, true)
	require.NoError(t, err)
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	deleted := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var workers sync.WaitGroup
	defer func() { unblock(); workers.Wait() }()
	deleteResult := make(chan error, 1)
	workers.Go(func() {
		deleteResult <- governance_model.WithWrite(ctx, nil, func(tx context.Context) error {
			if err := asymkey_service.DeleteDeployKey(tx, repo, first.ID); err != nil {
				return err
			}
			close(deleted)
			<-release
			return nil
		})
	})
	select {
	case <-deleted:
	case err := <-deleteResult:
		t.Fatalf("删除事务未就绪：%v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("删除事务未就绪")
	}
	type keyResult struct {
		key *asymkey_model.DeployKey
		err error
	}
	added := make(chan keyResult, 1)
	workers.Go(func() {
		key, err := asymkey_service.AddDeployKey(ctx, 2, "并发新关联", governanceCredentialPublicKey, true)
		added <- keyResult{key, err}
	})
	require.Eventually(t, func() bool {
		var rows []struct{ PID int64 }
		err := db.GetEngine(ctx).SQL("SELECT pid FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE '%governance_write_lock%'").Find(&rows)
		return err == nil && len(rows) > 0
	}, 5*time.Second, 10*time.Millisecond, "新增关联必须在读取旧公钥前等待删除事务")
	unblock()
	require.NoError(t, <-deleteResult)
	result := <-added
	require.NoError(t, result.err)
	require.NotEqual(t, first.KeyID, result.key.KeyID)
	key, err := asymkey_model.GetPublicKeyByID(ctx, result.key.KeyID)
	require.NoError(t, err)
	require.Equal(t, governanceCredentialPublicKey, key.Content)
	unittest.AssertCount(t, &asymkey_model.DeployKey{KeyID: result.key.KeyID}, 1)
}
