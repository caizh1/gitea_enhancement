// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package auth_test

import (
	"strings"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/json"

	"github.com/stretchr/testify/require"
)

func TestAccessTokenAuditAtomicity(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx := t.Context()
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api", IP: "192.0.2.5", RequestID: "凭据事务验收"}
	valid := governance_model.WithAuditActor(ctx, actor)
	actor.Transport = ""
	invalid := governance_model.WithAuditActor(ctx, actor)
	token := &auth_model.AccessToken{UID: 2, Name: "个人凭据验收", Scope: auth_model.AccessTokenScopeReadRepository}
	require.Error(t, auth_model.NewAccessToken(invalid, token))
	require.Empty(t, token.Token, "审计失败不能返回已创建的令牌")
	unittest.AssertNotExistsBean(t, &auth_model.AccessToken{UID: 2, Name: token.Name})
	require.NoError(t, auth_model.NewAccessToken(valid, token))
	require.Error(t, auth_model.DeleteAccessTokenByID(invalid, token.ID, token.UID))
	_, err := auth_model.GetAccessTokenBySHA(ctx, token.Token)
	require.NoError(t, err, "撤销审计失败必须保留凭据")
	require.NoError(t, auth_model.DeleteAccessTokenByID(valid, token.ID, token.UID))
	_, err = auth_model.GetAccessTokenBySHA(ctx, token.Token)
	require.Error(t, err, "撤销成功后缓存不能继续接受凭据")
	var events []*governance_model.AuditEvent
	err = db.GetEngine(ctx).Where("object_type = ? AND object_id = ?", "access_token", token.ID).Asc("id").Find(&events)
	require.NoError(t, err)
	require.Len(t, events, 2)
	for _, event := range events {
		require.Equal(t, int64(2), event.Actor.ID)
		require.Equal(t, "192.0.2.5", event.Actor.IP)
		require.Equal(t, "凭据事务验收", event.RequestID)
		require.Equal(t, token.ID, event.ObjectID)
		raw, err := json.Marshal(event)
		require.NoError(t, err)
		for _, secret := range []string{token.Token, token.TokenHash, token.TokenSalt, token.TokenLastEight} {
			require.False(t, strings.Contains(string(raw), secret), "审计中不能出现任何令牌秘密字段") //nolint:testifylint // 失败日志不能打印秘密值。
		}
	}
}
