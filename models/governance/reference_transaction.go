// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/json"

	"github.com/google/uuid"
)

var referenceBusinessAppliers sync.Map
var activeReferenceBusinessOperations sync.Map

// RegisterReferenceBusinessApplier 注册跨存储业务补写器；注册发生在服务初始化阶段。
func RegisterReferenceBusinessApplier(operation string, apply func(context.Context, *ReferenceTransaction) error) {
	referenceBusinessAppliers.Store(operation, apply)
}

func ApplyRegisteredReferenceBusinessOperation(ctx context.Context, operation *ReferenceTransaction) (bool, error) {
	apply, ok := referenceBusinessAppliers.Load(operation.BusinessOperation)
	if !ok {
		return false, nil
	}
	return true, apply.(func(context.Context, *ReferenceTransaction) error)(ctx, operation)
}

// CompleteRegisteredReferenceBusinessOperation 在所属占用内幂等补写业务数据库并推进终态。
func CompleteRegisteredReferenceBusinessOperation(ctx context.Context, id string) error {
	op, err := GetReferenceBusinessOperation(ctx, id)
	if err != nil {
		return err
	}
	return WithReferenceTransactionWrite(ctx, op.ID, []string{Resource("repository", op.RepoID)}, func(ctx context.Context) error {
		fresh, err := GetReferenceBusinessOperation(ctx, id)
		if err != nil {
			return err
		}
		if fresh.BusinessApplied {
			return nil
		}
		if fresh.State != "committed" {
			return ErrConflict
		}
		return db.WithTx(ctx, func(ctx context.Context) error {
			ok, err := ApplyRegisteredReferenceBusinessOperation(WithAuditActor(ctx, fresh.Actor), fresh)
			if err != nil {
				return err
			}
			if !ok {
				return ErrInvalid
			}
			return MarkReferenceBusinessOperationApplied(ctx, id)
		})
	})
}

// BeginReferenceBusinessOperation 仅避免同进程恢复任务干扰正在执行的 Git 命令。
func BeginReferenceBusinessOperation(id string) func() {
	activeReferenceBusinessOperations.Store(id, struct{}{})
	return func() { activeReferenceBusinessOperations.Delete(id) }
}

func ReferenceBusinessOperationActive(id string) bool {
	_, ok := activeReferenceBusinessOperations.Load(id)
	return ok
}

type ReferenceChange struct {
	Ref string `json:"ref"`
	Old string `json:"old"`
	New string `json:"new"`
}

// ReferencePullChange 保存写入前后的差异，恢复时不能重新猜测中间代码版本。
type ReferencePullChange struct {
	AuditScope    *PullVersionAuditScope `json:"audit_scope,omitempty"`
	Before        PullVersion            `json:"before"`
	After         PullVersion            `json:"after"`
	ResetOnChange bool                   `json:"reset_on_change"`
}

type ReferenceTransaction struct {
	BusinessOperationID  string                `xorm:"VARCHAR(36) INDEX" json:"business_operation_id,omitempty"`
	BusinessOperation    string                `xorm:"VARCHAR(40) INDEX" json:"business_operation,omitempty"`
	BusinessOldBranch    string                `xorm:"VARCHAR(255)" json:"business_old_branch,omitempty"`
	BusinessNewBranch    string                `xorm:"VARCHAR(255)" json:"business_new_branch,omitempty"`
	BusinessUpdateHEAD   bool                  `xorm:"NOT NULL DEFAULT false" json:"business_update_head,omitempty"`
	BusinessPayload      json.Value            `xorm:"JSON TEXT" json:"-"`
	BusinessApplied      bool                  `xorm:"NOT NULL DEFAULT false INDEX" json:"business_applied"`
	IsWiki               bool                  `xorm:"NOT NULL DEFAULT false" json:"is_wiki"`
	ContentEvent         string                `xorm:"VARCHAR(100)" json:"content_event,omitempty"`
	ContentObjectPath    string                `xorm:"VARCHAR(2048)" json:"content_object_path,omitempty"`
	ContentDetails       AuditDetails          `xorm:"JSON TEXT" json:"content_details,omitempty"`
	MergeAuthorizationID string                `xorm:"VARCHAR(36) INDEX" json:"merge_authorization_id,omitempty"`
	WriterID             string                `xorm:"VARCHAR(64) INDEX NOT NULL" json:"writer_id"`
	InputFingerprint     string                `xorm:"VARCHAR(64) INDEX NOT NULL" json:"input_fingerprint"`
	ID                   string                `xorm:"VARCHAR(36) pk" json:"id"`
	RepoID               int64                 `xorm:"INDEX(pending) NOT NULL" json:"repository_id"`
	Fingerprint          string                `xorm:"VARCHAR(64) INDEX NOT NULL" json:"fingerprint"`
	State                string                `xorm:"VARCHAR(20) INDEX(pending) NOT NULL" json:"state"`
	Changes              []ReferenceChange     `xorm:"JSON TEXT" json:"changes"`
	PullChanges          []ReferencePullChange `xorm:"JSON TEXT" json:"pull_changes"`
	Actor                Actor                 `xorm:"JSON TEXT" json:"actor"`
	ObjectPath           string                `xorm:"VARCHAR(2048)" json:"path"`
	AncestorIDs          []int64               `xorm:"JSON TEXT" json:"ancestor_ids"`
	CreatedAt            time.Time             `xorm:"NOT NULL" json:"created_at"`
}

// PendingReferenceBusinessOperations 返回 Git 已提交、但业务数据库尚未完成的操作。
func PendingReferenceBusinessOperations(ctx context.Context, limit int) ([]*ReferenceTransaction, error) {
	operations := make([]*ReferenceTransaction, 0, limit)
	err := db.GetEngine(ctx).Where("business_operation_id <> ? AND business_applied = ?", "", false).In("state", []string{"planned", "prepared", "unknown", "committed"}).Asc("created_at").Limit(limit).Find(&operations)
	return operations, err
}

func GetReferenceBusinessOperation(ctx context.Context, id string) (*ReferenceTransaction, error) {
	operation := new(ReferenceTransaction)
	has, err := db.GetEngine(ctx).Where("business_operation_id = ?", id).Get(operation)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrNotFound
	}
	return operation, nil
}

// MarkReferenceBusinessOperationApplied 必须与业务数据库变更处于同一事务。
func MarkReferenceBusinessOperationApplied(ctx context.Context, id string) error {
	count, err := db.GetEngine(ctx).Where("business_operation_id = ? AND state = ? AND business_applied = ?", id, "committed", false).Cols("business_applied").Update(&ReferenceTransaction{BusinessApplied: true})
	if err != nil {
		return err
	}
	if count == 0 {
		op, getErr := GetReferenceBusinessOperation(ctx, id)
		if getErr != nil {
			return getErr
		}
		if op.BusinessApplied {
			return nil
		}
		return ErrConflict
	}
	applied, getErr := GetReferenceBusinessOperation(ctx, id)
	if getErr != nil {
		return getErr
	}
	_, err = db.GetEngine(ctx).Where("transaction_id = ?", applied.ID).Delete(new(ReferenceReservation))
	return err
}

func (*ReferenceTransaction) TableName() string { return "governance_ref_transaction" }

type ReferenceReservation struct {
	Resource      string `xorm:"VARCHAR(300) pk"`
	TransactionID string `xorm:"VARCHAR(36) INDEX NOT NULL"`
}

func (*ReferenceReservation) TableName() string { return "governance_ref_reservation" }

// PlanReferenceBusinessOperation 在 Git 执行前保存恢复载荷并占用项目写入顺序。
func PlanReferenceBusinessOperation(ctx context.Context, operation *ReferenceTransaction) error {
	if operation == nil || operation.RepoID <= 0 || operation.BusinessOperation == "" || len(operation.BusinessPayload) == 0 || !json.Valid(operation.BusinessPayload) || len(operation.Changes) == 0 {
		return ErrInvalid
	}
	if operation.BusinessOperationID == "" {
		operation.BusinessOperationID = uuid.NewString()
	}
	if _, err := uuid.Parse(operation.BusinessOperationID); err != nil {
		return ErrInvalid
	}
	operation.ID = uuid.NewString()
	operation.State = "planned"
	operation.CreatedAt = time.Now().UTC()
	return WithWrite(ctx, []string{Resource("repository", operation.RepoID)}, func(ctx context.Context) error {
		if err := db.Insert(ctx, operation); err != nil {
			return err
		}
		if err := advanceReferenceRevision(ctx, operation.RepoID); err != nil {
			return err
		}
		return db.Insert(ctx, &ReferenceReservation{Resource: Resource("repository", operation.RepoID), TransactionID: operation.ID})
	})
}

type referenceTransactionKey struct{}

// ReferenceFingerprint 绑定完整事务；Git 已持有引用锁，重名引用与不完整输入必须拒绝。
func ReferenceFingerprint(changes []ReferenceChange) (string, error) {
	if len(changes) == 0 || len(changes) > 10000 {
		return "", ErrInvalid
	}
	sorted := slices.Clone(changes)
	slices.SortFunc(sorted, func(a, b ReferenceChange) int { return strings.Compare(a.Ref, b.Ref) })
	hash := sha256.New()
	for i, change := range sorted {
		if !strings.HasPrefix(change.Ref, "refs/") || strings.ContainsAny(change.Ref, "\x00\r\n ") || i > 0 && sorted[i-1].Ref == change.Ref {
			return "", ErrInvalid
		}
		if len(change.Old) != len(change.New) || len(change.Old) != 40 && len(change.Old) != 64 {
			return "", ErrInvalid
		}
		for _, oid := range []string{change.Old, change.New} {
			if _, err := hex.DecodeString(oid); err != nil {
				return "", ErrInvalid
			}
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\n", change.Ref, change.Old, change.New)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// PrepareReferenceTransaction 在 Git prepared 阶段落盘；不会因超时或回执丢失释放占用。
func PrepareReferenceTransaction(ctx context.Context, operation *ReferenceTransaction, dependencies []string, check func(context.Context) error) error {
	if operation == nil || operation.RepoID <= 0 || check == nil {
		return ErrInvalid
	}
	fingerprint, err := ReferenceFingerprint(operation.Changes)
	if err != nil {
		return err
	}
	if operation.InputFingerprint == "" {
		operation.InputFingerprint = fingerprint
	}
	if len(operation.InputFingerprint) != 64 {
		return ErrInvalid
	}
	if _, err := hex.DecodeString(operation.InputFingerprint); err != nil {
		return ErrInvalid
	}
	resources := append([]string{Resource("repository", operation.RepoID)}, dependencies...)
	seenPulls := make(map[int64]bool)
	for _, change := range operation.PullChanges {
		if change.Before.PullID <= 0 || change.Before.PullID != change.After.PullID || seenPulls[change.Before.PullID] {
			return ErrInvalid
		}
		seenPulls[change.Before.PullID] = true
		resources = append(resources, Resource("pull", change.Before.PullID))
	}
	var planned *ReferenceTransaction
	if operation.BusinessOperationID != "" {
		planned, err = GetReferenceBusinessOperation(ctx, operation.BusinessOperationID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if planned != nil {
			ctx = context.WithValue(ctx, referenceTransactionKey{}, planned.ID)
		}
	}
	return WithWrite(ctx, resources, func(ctx context.Context) error {
		if err := check(ctx); err != nil {
			return err
		}
		if operation.BusinessOperationID != "" && planned == nil {
			exists, err := db.GetEngine(ctx).Where("business_operation_id = ?", operation.BusinessOperationID).Exist(new(ReferenceTransaction))
			if err != nil {
				return err
			}
			if exists {
				return ErrConflict
			}
		}
		for _, change := range operation.PullChanges {
			previous, has, err := db.GetByID[PullVersion](ctx, change.Before.PullID)
			if err != nil {
				return err
			}
			if !has {
				if _, err := AdvancePullVersion(ctx, change.Before, "", change.ResetOnChange); err != nil {
					return err
				}
			} else if previous.Head != change.Before.Head || previous.PatchID != change.Before.PatchID || previous.BaseBranch != change.Before.BaseBranch {
				return fmt.Errorf("%w：引用写入前的审批版本不一致", ErrConflict)
			}
			if change.After.Head == "" || change.After.PatchID == "" || change.After.BaseBranch == "" {
				return ErrInvalid
			}
		}
		operation.Fingerprint, operation.State, operation.CreatedAt = fingerprint, "prepared", time.Now().UTC()
		if planned != nil {
			freshPlanned := new(ReferenceTransaction)
			has, err := db.GetEngine(ctx).ID(planned.ID).Get(freshPlanned)
			if err != nil {
				return err
			}
			if !has {
				return ErrNotFound
			}
			owned, err := db.GetEngine(ctx).Where("resource = ? AND transaction_id = ?", Resource("repository", operation.RepoID), freshPlanned.ID).Exist(new(ReferenceReservation))
			if err != nil {
				return err
			}
			if !owned {
				return ErrConflict
			}
			planned = freshPlanned
			if planned.State != "planned" || planned.RepoID != operation.RepoID || planned.IsWiki != operation.IsWiki || planned.BusinessOperation != operation.BusinessOperation || planned.BusinessOldBranch != operation.BusinessOldBranch || planned.BusinessNewBranch != operation.BusinessNewBranch || planned.Actor.EffectiveUserID() != operation.Actor.EffectiveUserID() || len(planned.Changes) != len(operation.Changes) {
				return ErrConflict
			}
			for i := range planned.Changes {
				if planned.Changes[i].Ref != operation.Changes[i].Ref || planned.Changes[i].Old != operation.Changes[i].Old || strings.Trim(planned.Changes[i].New, "0") != "" && planned.Changes[i].New != operation.Changes[i].New {
					return ErrConflict
				}
			}
			// Hook 只证明当前 Git 写入身份一致；审计仍保留网页/API 规划阶段的真实请求上下文。
			operation.Actor = planned.Actor
			operation.ObjectPath = planned.ObjectPath
			operation.AncestorIDs = slices.Clone(planned.AncestorIDs)
			operation.ID = planned.ID
			operation.BusinessPayload = planned.BusinessPayload
			affected, err := db.GetEngine(ctx).ID(planned.ID).Where("state = ?", "planned").Cols("writer_id", "input_fingerprint", "fingerprint", "state", "changes", "pull_changes", "actor", "object_path", "ancestor_ids", "created_at").Update(operation)
			if err != nil {
				return err
			}
			if affected != 1 {
				return ErrConflict
			}
		} else {
			operation.ID = uuid.NewString()
			if err := db.Insert(ctx, operation); err != nil {
				return err
			}
		}
		if err := advanceReferenceRevision(ctx, operation.RepoID); err != nil {
			return err
		}
		seen := make(map[string]bool)
		for _, resource := range resources {
			if seen[resource] {
				continue
			}
			seen[resource] = true
			if planned != nil && resource == Resource("repository", operation.RepoID) {
				continue
			}
			if err := db.Insert(ctx, &ReferenceReservation{Resource: resource, TransactionID: operation.ID}); err != nil {
				return err
			}
		}
		return appendReferenceAudit(ctx, operation, "pending")
	})
}

// ReconcileReferenceTransaction 的 actual 必须在原写入者结束并持有仓库操作锁时读取。
// 不能使用经过时间、客户端超时或同名进程不存在作为引用结果。
func ReconcileReferenceTransaction(ctx context.Context, id string, actual map[string]string, apply func(context.Context, *ReferenceTransaction) error) (string, error) {
	state := ""
	err := WithWrite(ctx, nil, func(ctx context.Context) error {
		operation := new(ReferenceTransaction)
		has, err := db.GetEngine(ctx).ID(id).Get(operation)
		if err != nil {
			return err
		}
		if !has {
			return ErrNotFound
		}
		if operation.State == "committed" || operation.State == "aborted" {
			state = operation.State
			return nil
		}
		oldMatch, newMatch := true, true
		for _, change := range operation.Changes {
			value, exists := actual[change.Ref]
			oldMatch = oldMatch && exists && value == change.Old
			newMatch = newMatch && exists && value == change.New
		}
		state = "unknown"
		if operation.InputFingerprint == "" {
			if oldMatch {
				state = "aborted"
			}
		} else if newMatch {
			state = "committed"
		} else if oldMatch {
			state = "aborted"
		}
		operation.State = state
		if state == "committed" {
			applyCtx := WithAuditActor(context.WithValue(ctx, referenceTransactionKey{}, id), operation.Actor)
			if operation.MergeAuthorizationID != "" {
				applyCtx = WithMergeAuthorizationContext(applyCtx, operation.MergeAuthorizationID)
			}
			for _, change := range operation.PullChanges {
				if _, err := AdvancePullVersion(applyCtx, change.After, change.Before.Head, change.ResetOnChange, change.AuditScope); err != nil {
					return err
				}
			}
			if operation.ContentEvent != "" {
				if err := AppendAudit(applyCtx, &AuditEvent{Type: operation.ContentEvent, Actor: operation.Actor, ScopeType: "repository", ScopeID: operation.RepoID, AncestorIDs: operation.AncestorIDs, ObjectType: "wiki", ObjectID: operation.RepoID, ObjectPath: operation.ContentObjectPath, Result: "success", Details: operation.ContentDetails}); err != nil {
					return err
				}
			}
		}
		if state == "committed" && apply != nil {
			if err := apply(context.WithValue(ctx, referenceTransactionKey{}, id), operation); err != nil {
				return err
			}
		}
		if _, err := db.GetEngine(ctx).ID(id).Cols("state").Update(operation); err != nil {
			return err
		}
		if err := advanceReferenceRevision(ctx, operation.RepoID); err != nil {
			return err
		}
		if state != "unknown" && (state == "aborted" || operation.BusinessOperationID == "") {
			if _, err := db.GetEngine(ctx).Where("transaction_id = ?", id).Delete(new(ReferenceReservation)); err != nil {
				return err
			}
		}
		return appendReferenceAudit(ctx, operation, map[string]string{"committed": "success", "aborted": "failure", "unknown": "unknown"}[state])
	})
	return state, err
}

func appendReferenceAudit(ctx context.Context, operation *ReferenceTransaction, result string) error {
	objectType := "repository"
	if operation.IsWiki {
		objectType = "wiki"
	}
	for _, change := range operation.Changes {
		details, err := json.Marshal(map[string]any{"transaction_id": operation.ID, "merge_authorization_id": operation.MergeAuthorizationID, "state": operation.State, "ref": change.Ref, "old": change.Old, "new": change.New, "repository_kind": objectType})
		if err != nil {
			return err
		}
		if err := AppendAudit(ctx, &AuditEvent{Type: "git.reference_transaction", Actor: operation.Actor, ScopeType: "repository", ScopeID: operation.RepoID, AncestorIDs: operation.AncestorIDs, ObjectType: objectType, ObjectID: operation.RepoID, ObjectPath: operation.ObjectPath, Result: result, Details: details}); err != nil {
			return err
		}
	}
	return nil
}
