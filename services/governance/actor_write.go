// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"

	governance_model "gitea.dev/models/governance"
)

// withActorWrite 取得治理锁后重新核对实际委托者，不能沿用请求进入时的管理员资格。
func withActorWrite(ctx context.Context, actor governance_model.Actor, resources []string, write func(context.Context) error) error {
	return governance_model.WithWrite(ctx, resources, func(ctx context.Context) error {
		if err := checkDelegation(ctx, actor); err != nil {
			return err
		}
		return write(ctx)
	})
}

// WithActorWrite 供原生项目生命周期在同一治理锁内复核实际操作者。
func WithActorWrite(ctx context.Context, actor governance_model.Actor, resources []string, write func(context.Context) error) error {
	return withActorWrite(ctx, actor, resources, write)
}
