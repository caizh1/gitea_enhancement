// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package password

import (
	"context"

	user_model "gitea.dev/models/user"
)

// Authenticator 是原生登录和批准再次认证共用的密码认证契约。
type Authenticator interface {
	Authenticate(ctx context.Context, user *user_model.User, login, password string) (*user_model.User, error)
}
