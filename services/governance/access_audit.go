// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"maps"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"
)

// BeginCodeAccessAudit 在返回内容前保存开始事件；完成事件不代表客户端已完整收到内容。
func BeginCodeAccessAudit(ctx context.Context, actor governance_model.Actor, repo *repo_model.Repository, path, commit string) (func(int), error) {
	return BeginRepositoryAccessAudit(ctx, actor, repo, "access.code", map[string]any{"path": path, "commit": commit})
}

// BeginRepositoryAccessAudit 只接受服务端确定的事件类型和最小资源元数据。
func BeginRepositoryAccessAudit(ctx context.Context, actor governance_model.Actor, repo *repo_model.Repository, eventType string, metadata map[string]any) (func(int), error) {
	event, err := StartRepositoryAccessAudit(ctx, actor, repo, eventType, metadata)
	if err != nil || event == nil {
		return nil, err
	}
	return func(status int) {
		result := "success"
		if status == 0 {
			result = "unknown"
		} else if status >= 400 || ctx.Err() != nil {
			result = "failure"
		}
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := FinishRepositoryAccessAudit(recoveryCtx, event, result, map[string]any{"http_status": status}); err != nil {
			log.Error("资源访问完成审计保存失败，开始事件 %s 待核对：%v", event.EventID, err)
		}
	}, nil
}

// StartRepositoryAccessAudit 在任何内容传输之前持久化开始证据。
func StartRepositoryAccessAudit(ctx context.Context, actor governance_model.Actor, repo *repo_model.Repository, eventType string, metadata map[string]any) (*governance_model.AuditEvent, error) {
	if actor.ID <= 0 && !(actor.Kind == "deploy_key" && actor.CredentialID > 0) {
		return nil, nil //nolint:nilnil // 匿名访问不在本次已认证访问审计范围内。
	}
	configured, err := db.GetEngine(ctx).Exist(new(governance_model.AuditStream))
	if err != nil || !configured {
		return nil, err
	}
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		return nil, err
	}
	var ancestors []int64
	for _, group := range chain {
		if group.Kind == "group" {
			ancestors = append(ancestors, group.ID)
		}
	}
	metadata = maps.Clone(metadata)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	metadata["phase"] = "response_started"
	details, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	event := governance_model.AuditEvent{Type: eventType, Actor: actor, ScopeType: "repository", ScopeID: repo.ID, AncestorIDs: ancestors, ObjectType: "repository", ObjectID: repo.ID, ObjectPath: repo.FullPath(), Result: "pending", Details: details}
	recorded, err := governance_model.AppendConfiguredAccessAudit(ctx, &event)
	if err != nil || !recorded {
		return nil, err
	}
	return &event, nil
}

// FinishRepositoryAccessAudit 使用开始时的身份和范围快照，保留删除或撤权前的来源。
func FinishRepositoryAccessAudit(ctx context.Context, event *governance_model.AuditEvent, result string, metadata map[string]any) error {
	if result != "success" && result != "failure" && result != "unknown" {
		return errors.New("无效访问结果")
	}
	details := make(map[string]any)
	if err := json.Unmarshal(event.Details, &details); err != nil {
		return err
	}
	maps.Copy(details, metadata)
	details["phase"], details["started_event_id"] = "response_finished", event.EventID
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	completed := *event
	completed.Result, completed.Details = result, encoded
	recorded, err := governance_model.AppendConfiguredAccessAudit(ctx, &completed)
	if err != nil {
		return err
	}
	if !recorded {
		return errors.New("访问审计接收目标已移除，开始记录待核对")
	}
	return nil
}
