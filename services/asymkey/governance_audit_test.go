// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package asymkey

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	asymkey_model "gitea.dev/models/asymkey"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/require"
)

const governanceTestPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICV0MGX/W9IvLA4FXpIuUcdDcbj5KX4syHgsTy7soVgf"

func TestGovernancePublicKeyAuditRollbackBeforeFile(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"})
	defer test.MockVariableValue(&setting.SSH.StartBuiltinServer, false)()
	defer test.MockVariableValue(&setting.SSH.CreateAuthorizedKeysFile, true)()
	defer test.MockVariableValue(&setting.SSH.RootPath, t.TempDir())()
	path := filepath.Join(setting.SSH.RootPath, "authorized_keys")
	original := []byte("# 验收保留内容\n")
	require.NoError(t, os.WriteFile(path, original, 0o600))
	_, err := db.GetEngine(ctx).Exec("CREATE TRIGGER governance_key_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'credential.ssh_key_created' BEGIN SELECT RAISE(ABORT, 'SSH审计故障'); END")
	require.NoError(t, err)
	_, err = AddPublicKey(ctx, 2, "审计创建", governanceTestPublicKey, 0, false)
	require.Error(t, err)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, body)
	unittest.AssertCount(t, &asymkey_model.PublicKey{Name: "审计创建"}, 0)
	_, err = db.GetEngine(ctx).Exec("DROP TRIGGER governance_key_fail")
	require.NoError(t, err)
	key, err := AddPublicKey(ctx, 2, "审计创建", governanceTestPublicKey, 0, false)
	require.NoError(t, err)
	event := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.ssh_key_created", ObjectID: key.ID})
	require.EqualValues(t, 2, event.Actor.ID)
	require.NotContains(t, string(event.Details), governanceTestPublicKey)
	body, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(body), governanceTestPublicKey)
	_, err = db.GetEngine(ctx).Exec("CREATE TRIGGER governance_key_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'credential.ssh_key_revoked' BEGIN SELECT RAISE(ABORT, 'SSH撤销审计故障'); END")
	require.NoError(t, err)
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	require.Error(t, DeletePublicKey(ctx, user, key.ID))
	unittest.AssertCount(t, &asymkey_model.PublicKey{ID: key.ID}, 1)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, body, after)
	_, err = db.GetEngine(ctx).Exec("DROP TRIGGER governance_key_fail")
	require.NoError(t, err)
	require.NoError(t, DeletePublicKey(ctx, user, key.ID))
	after, err = os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(after), governanceTestPublicKey)
	require.Contains(t, string(after), string(original))
}

func TestGovernanceDeployKeyAuditSharedReferences(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"})
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	defer test.MockVariableValue(&setting.SSH.StartBuiltinServer, false)()
	defer test.MockVariableValue(&setting.SSH.CreateAuthorizedKeysFile, true)()
	defer test.MockVariableValue(&setting.SSH.RootPath, t.TempDir())()
	_, err := db.GetEngine(ctx).Exec("CREATE TRIGGER governance_deploy_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'credential.deploy_key_created' BEGIN SELECT RAISE(ABORT, '部署密钥审计故障'); END")
	require.NoError(t, err)
	_, err = AddDeployKey(ctx, 1, "部署甲", governanceTestPublicKey, true)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(setting.SSH.RootPath, "authorized_keys"))
	require.True(t, os.IsNotExist(err))
	unittest.AssertCount(t, &asymkey_model.DeployKey{Name: "部署甲"}, 0)
	_, err = db.GetEngine(ctx).Exec("DROP TRIGGER governance_deploy_fail")
	require.NoError(t, err)
	first, err := AddDeployKey(ctx, 1, "部署甲", governanceTestPublicKey, true)
	require.NoError(t, err)
	second, err := AddDeployKey(ctx, 2, "部署乙", governanceTestPublicKey, false)
	require.NoError(t, err)
	require.Equal(t, first.KeyID, second.KeyID)
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.NoError(t, DeleteDeployKey(ctx, repo, first.ID))
	unittest.AssertCount(t, &asymkey_model.PublicKey{ID: first.KeyID}, 1)
	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	require.NoError(t, DeleteDeployKey(ctx, repo, second.ID))
	unittest.AssertCount(t, &asymkey_model.PublicKey{ID: first.KeyID}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "credential.deploy_key_created"}, 2)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "credential.deploy_key_revoked"}, 2)
}

func TestGovernanceSSHFileRecovery(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"})
	defer test.MockVariableValue(&setting.SSH.StartBuiltinServer, false)()
	defer test.MockVariableValue(&setting.SSH.CreateAuthorizedKeysFile, true)()
	defer test.MockVariableValue(&setting.SSH.RootPath, t.TempDir())()
	path := filepath.Join(setting.SSH.RootPath, "authorized_keys")
	original := []byte("# 保留人工授权\n")
	require.NoError(t, os.WriteFile(path, original, 0o600))
	// 嵌套事务不得落盘；外层失败时密钥、审计和待同步记录一起消失。
	rollback := errors.New("外层事务回滚")
	require.ErrorIs(t, db.WithTx(ctx, func(tx context.Context) error {
		_, err := AddPublicKey(tx, 2, "回滚密钥", governanceTestPublicKey, 0, false)
		require.NoError(t, err)
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, original, body)
		return rollback
	}), rollback)
	unittest.AssertCount(t, &asymkey_model.PublicKey{Name: "回滚密钥"}, 0)
	unittest.AssertCount(t, &governance_model.SSHKeyFileSync{}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "credential.ssh_key_created"}, 0)
	// 模拟文件不可写：数据库结果已经提交，不能重复创建或假报文件同步成功。
	require.NoError(t, os.Mkdir(path+".tmp", 0o700))
	key, err := AddPublicKey(ctx, 2, "可恢复密钥", governanceTestPublicKey, 0, false)
	require.ErrorContains(t, err, "同步待恢复")
	require.NotNil(t, key)
	unittest.AssertCount(t, &asymkey_model.PublicKey{ID: key.ID}, 1)
	unittest.AssertCount(t, &governance_model.SSHKeyFileSync{}, 1)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, body)
	require.NoError(t, os.Remove(path+".tmp"))
	// 模拟替换文件后、清理回执前失败，恢复重复执行但不重复业务审计。
	_, err = db.GetEngine(ctx).Exec("CREATE TRIGGER governance_sync_fail BEFORE DELETE ON governance_ssh_key_file_sync BEGIN SELECT RAISE(ABORT, '同步回执故障'); END")
	require.NoError(t, err)
	require.Error(t, SyncSSHKeyFiles(ctx))
	body, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(body), governanceTestPublicKey)
	unittest.AssertCount(t, &governance_model.SSHKeyFileSync{}, 1)
	_, err = db.GetEngine(ctx).Exec("DROP TRIGGER governance_sync_fail")
	require.NoError(t, err)
	require.NoError(t, SyncSSHKeyFiles(ctx))
	unittest.AssertCount(t, &governance_model.SSHKeyFileSync{}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "credential.ssh_key_created", ObjectID: key.ID}, 1)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, body, after)
	// 撤销已提交而文件尚未恢复时，数据库已拒绝该稳定密钥 ID。
	require.NoError(t, os.Mkdir(path+".tmp", 0o700))
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	require.ErrorContains(t, DeletePublicKey(ctx, user, key.ID), "同步待恢复")
	_, err = asymkey_model.GetPublicKeyByID(ctx, key.ID)
	require.True(t, asymkey_model.IsErrKeyNotExist(err))
	unittest.AssertCount(t, &governance_model.SSHKeyFileSync{}, 1)
	require.NoError(t, os.Remove(path+".tmp"))
	require.NoError(t, SyncSSHKeyFiles(ctx))
	after, err = os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(after), governanceTestPublicKey)
	require.Contains(t, string(after), string(original))
}

func TestGovernancePrincipalFileRecovery(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"})
	defer test.MockVariableValue(&setting.SSH.StartBuiltinServer, false)()
	defer test.MockVariableValue(&setting.SSH.CreateAuthorizedPrincipalsFile, true)()
	defer test.MockVariableValue(&setting.SSH.RootPath, t.TempDir())()
	path := filepath.Join(setting.SSH.RootPath, "authorized_principals")
	require.NoError(t, os.WriteFile(path, []byte("# 保留人工配置\n"), 0o600))
	require.NoError(t, os.Mkdir(path+".tmp", 0o700))
	key, err := AddPrincipalKey(ctx, 2, "验收-principal", 0)
	require.ErrorContains(t, err, "同步待恢复")
	require.NotNil(t, key)
	unittest.AssertCount(t, &governance_model.SSHKeyFileSync{Principal: true}, 1)
	require.NoError(t, os.Remove(path+".tmp"))
	require.NoError(t, SyncSSHKeyFiles(ctx))
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(body), "验收-principal")
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	require.NoError(t, DeletePublicKey(ctx, user, key.ID))
	body, err = os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(body), "验收-principal")
	require.Contains(t, string(body), "# 保留人工配置")
	unittest.AssertCount(t, &governance_model.SSHKeyFileSync{}, 0)
}
