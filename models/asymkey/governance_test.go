// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package asymkey

import (
	"strings"
	"testing"

	"gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/42wim/sshsig"
	"github.com/stretchr/testify/require"
)

func TestGovernanceSourceKeysAtomicAudit(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{Kind: "anonymous", Transport: "web", RequestID: "来源同步验收"})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	source := &auth.Source{ID: 12, Name: "验收来源"}
	old, err := AddPublicKey(ctx, 2, "来源旧密钥", ed25519PublicKey, source.ID, true)
	require.NoError(t, err)
	// 原生手工密钥必须保留；来源删除的审计失败则新增也回滚。
	_, err = db.GetEngine(ctx).Exec("CREATE TRIGGER governance_source_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'credential.ssh_key_revoked' BEGIN SELECT RAISE(ABORT, '来源撤销审计故障'); END")
	require.NoError(t, err)
	changed, err := SynchronizePublicKeys(ctx, user, source, []string{sshPublicKey}, false)
	require.Error(t, err)
	require.False(t, changed)
	unittest.AssertCount(t, &PublicKey{ID: old.ID}, 1)
	unittest.AssertCount(t, &PublicKey{LoginSourceID: source.ID}, 1)
	unittest.AssertCount(t, &PublicKey{ID: 1}, 1)
	_, err = db.GetEngine(ctx).Exec("DROP TRIGGER governance_source_fail")
	require.NoError(t, err)
	changed, err = SynchronizePublicKeys(ctx, user, source, []string{sshPublicKey}, false)
	require.NoError(t, err)
	require.True(t, changed)
	unittest.AssertCount(t, &PublicKey{ID: old.ID}, 0)
	unittest.AssertCount(t, &PublicKey{ID: 1}, 1)
	event := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.ssh_key_revoked", ObjectID: old.ID})
	require.Equal(t, "system", event.Actor.Kind)
	require.Equal(t, "authentication_source", event.Actor.Transport)
	require.Equal(t, "来源同步验收", event.Actor.RequestID)
	require.Contains(t, string(event.Details), `"authentication_source_id":12`)
	require.NotContains(t, string(event.Details), old.Content)
}

func TestGovernanceKeyVerificationAuditRollback(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"})
	key, err := AddPublicKey(ctx, 2, "验证密钥", ed25519PublicKey, 0, false)
	require.NoError(t, err)
	signature, err := sshsig.Sign([]byte(ed25519PrivateKey), strings.NewReader("仅本地验收令牌"), "gitea")
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Exec("CREATE TRIGGER governance_verify_fail BEFORE INSERT ON governance_audit_event WHEN NEW.type = 'credential.ssh_key_verified' BEGIN SELECT RAISE(ABORT, '验证审计故障'); END")
	require.NoError(t, err)
	_, err = VerifySSHKey(ctx, 2, key.Fingerprint, "仅本地验收令牌", string(signature))
	require.Error(t, err)
	current, err := GetPublicKeyByID(ctx, key.ID)
	require.NoError(t, err)
	require.False(t, current.Verified)
	_, err = db.GetEngine(ctx).Exec("DROP TRIGGER governance_verify_fail")
	require.NoError(t, err)
	_, err = VerifySSHKey(ctx, 2, key.Fingerprint, "仅本地验收令牌", string(signature))
	require.NoError(t, err)
	event := unittest.AssertExistsAndLoadBean(t, &governance_model.AuditEvent{Type: "credential.ssh_key_verified", ObjectID: key.ID})
	require.EqualValues(t, 2, event.Actor.ID)
	require.Contains(t, string(event.Details), `"verified":true`)
	require.NotContains(t, string(event.Details), "仅本地验收令牌")
	require.NotContains(t, string(event.Details), string(signature))
}
