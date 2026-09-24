// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"

	"github.com/google/uuid"
)

type (
	writeContextKey       struct{}
	mergeAuthorizationKey struct{}
)

// WithMergeAuthorizationContext 仅供核对过持久化授权的内部 Git 操作继续其自身写入。
func WithMergeAuthorizationContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, mergeAuthorizationKey{}, id)
}

// WithReferenceTransactionWrite 允许持有该引用事务占用的恢复操作完成自己的数据库补写。
func WithReferenceTransactionWrite(ctx context.Context, id string, resources []string, f func(context.Context) error) error {
	if id == "" {
		return ErrInvalid
	}
	return WithWrite(context.WithValue(ctx, referenceTransactionKey{}, id), resources, f)
}

// WithWrite 排列授权、撤权和规则变更；回调内禁止网络请求和 Git 操作。
func WithWrite(ctx context.Context, resources []string, f func(context.Context) error) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		if ctx.Value(writeContextKey{}) == nil {
			// ponytail: 单应用节点使用短事务锁；容量验收不达标时按顶级组分片。
			n, err := db.GetEngine(ctx).ID(1).Incr("revision").Update(new(WriteLock))
			if err != nil {
				return err
			}
			if n != 1 {
				return fmt.Errorf("%w：治理写入锁尚未初始化", ErrConflict)
			}
			ctx = context.WithValue(ctx, writeContextKey{}, true)
		}
		if len(resources) > 0 {
			mergeQuery := db.GetEngine(ctx).In("resource", resources)
			if id, ok := ctx.Value(mergeAuthorizationKey{}).(string); ok {
				mergeQuery = mergeQuery.Where("authorization_id <> ?", id)
			}
			exists, err := mergeQuery.Exist(new(Reservation))
			if err != nil {
				return err
			}
			if exists {
				return ErrConflict
			}
			query := db.GetEngine(ctx).In("resource", resources)
			if operationID, ok := ctx.Value(referenceTransactionKey{}).(string); ok {
				query = query.Where("transaction_id <> ?", operationID)
			}
			exists, err = query.Exist(new(ReferenceReservation))
			if err != nil {
				return err
			}
			if exists {
				return ErrConflict
			}
		}
		return f(ctx)
	})
}

func Resource(kind string, id int64) string {
	return fmt.Sprintf("%s:%d", kind, id)
}

// AuthorizeMerge 在最终检查所在事务内创建持久化记录，不以超时释放资源。
func AuthorizeMerge(ctx context.Context, operation *MergeAuthorization, dependencies []string, check func(context.Context) error) error {
	if operation.PullID <= 0 || operation.RepoID <= 0 || operation.Head == "" || operation.OldTarget == "" || operation.NewTarget == "" || operation.OldTarget == operation.NewTarget || operation.Branch == "" || check == nil {
		return ErrInvalid
	}
	resources := append([]string{Resource("pull", operation.PullID), Resource("repository", operation.RepoID)}, dependencies...)
	return WithWrite(ctx, resources[:2], func(ctx context.Context) error {
		if err := check(ctx); err != nil {
			return err
		}
		operation.ID = uuid.NewString()
		operation.State = "authorized"
		operation.CreatedAt = time.Now().UTC()
		if err := db.Insert(ctx, operation); err != nil {
			return err
		}
		seen := make(map[string]bool)
		for _, resource := range resources {
			if seen[resource] {
				continue
			}
			seen[resource] = true
			if err := db.Insert(ctx, &Reservation{AuthorizationID: operation.ID, Resource: resource}); err != nil {
				return err
			}
		}
		return appendMergeAudit(ctx, operation, "merge.authorized", "pending")
	})
}

// ReconcileMerge 必须在对应 Git 写入者已结束且持有仓库操作锁时调用。
// 未知引用保留授权和资源占用，交由恢复工具核对，不能自动重复合并。
func ReconcileMerge(ctx context.Context, operationID, actualRef string) (string, error) {
	state := ""
	err := WithWrite(ctx, nil, func(ctx context.Context) error {
		var operation MergeAuthorization
		has, err := db.GetEngine(ctx).ID(operationID).Get(&operation)
		if err != nil {
			return err
		}
		if !has {
			return ErrNotFound
		}
		if operation.State == "succeeded" || operation.State == "failed" {
			state = operation.State
			return nil
		}
		switch actualRef {
		case operation.NewTarget:
			state = "succeeded"
		case operation.OldTarget:
			state = "failed"
		default:
			state = "unknown"
		}
		operation.State = state
		if _, err = db.GetEngine(ctx).ID(operation.ID).Cols("state").Update(&operation); err != nil {
			return err
		}
		if state != "unknown" {
			_, err = db.GetEngine(ctx).Where("authorization_id = ?", operation.ID).Delete(new(Reservation))
		}
		if err != nil {
			return err
		}
		result := map[string]string{"succeeded": "success", "failed": "failure", "unknown": "unknown"}[state]
		return appendMergeAudit(ctx, &operation, "merge.reconciled", result)
	})
	return state, err
}

func appendMergeAudit(ctx context.Context, operation *MergeAuthorization, eventType, result string) error {
	details, err := json.Marshal(map[string]string{
		"operation_id": operation.ID, "head": operation.Head,
		"old_target": operation.OldTarget, "new_target": operation.NewTarget, "branch": operation.Branch,
	})
	if err != nil {
		return err
	}
	return AppendAudit(ctx, &AuditEvent{
		Type: eventType, Actor: operation.Actor,
		ScopeType: "repository", ScopeID: operation.RepoID, AncestorIDs: operation.AncestorIDs,
		ObjectType: "pull", ObjectID: operation.PullID, ObjectPath: operation.ObjectPath, Result: result, Details: details,
	})
}

// WithStableRead 与治理变更互斥读取数据库快照，但不推进修订号；回调只允许数据库查询。
func WithStableRead(ctx context.Context, f func(context.Context) error) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		if setting.Database.Type.IsMySQL() {
			var lock WriteLock
			has, err := db.GetEngine(ctx).ID(1).ForUpdate().Get(&lock)
			if err != nil {
				return err
			}
			if !has {
				return ErrConflict
			}
			return f(ctx)
		}
		affected, err := db.GetEngine(ctx).ID(1).Incr("revision", 0).Update(new(WriteLock))
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrConflict
		}
		return f(ctx)
	})
}
