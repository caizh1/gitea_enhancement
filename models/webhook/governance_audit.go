// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"slices"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/json"
	webhook_module "gitea.dev/modules/webhook"
)

func webhookAuditValues(hook *Webhook) map[string]any {
	if hook == nil {
		return nil
	}
	values := map[string]any{
		"name":                     hook.Name,
		"active":                   hook.IsActive,
		"content_type":             hook.ContentType.Name(),
		"http_method":              hook.HTTPMethod,
		"system":                   hook.IsSystemWebhook,
		"type":                     hook.Type,
		"secret_configured":        hook.Secret != "",
		"authorization_configured": hook.HeaderAuthorizationEncrypted != "",
	}
	if endpoint, err := url.Parse(hook.URL); err == nil {
		values["endpoint_scheme"] = endpoint.Scheme
		values["endpoint_host"] = endpoint.Hostname()
		values["endpoint_port"] = endpoint.Port()
	}
	events := webhookAuditEvents(hook)
	slices.Sort(events)
	values["events"] = events
	if hook.HookEvent != nil {
		values["branch_filter"] = hook.BranchFilter
	} else {
		parsed := new(webhook_module.HookEvent)
		if json.Unmarshal([]byte(hook.Events), parsed) == nil {
			values["branch_filter"] = parsed.BranchFilter
		}
	}
	return values
}

func webhookAuditEvents(hook *Webhook) []string {
	if hook.HookEvent != nil {
		return hook.EventsArray()
	}
	events := new(webhook_module.HookEvent)
	if json.Unmarshal([]byte(hook.Events), events) != nil {
		return nil
	}
	copy := *hook
	copy.HookEvent = events
	return copy.EventsArray()
}

func webhookAuditScope(ctx context.Context, hook *Webhook) (scopeType string, scopeID int64, ancestors []int64, resourcePath string, err error) {
	if hook.RepoID > 0 {
		repo, getErr := repo_model.GetRepositoryByID(ctx, hook.RepoID)
		if getErr != nil {
			return "", 0, nil, "", getErr
		}
		if getErr := repo.LoadOwner(ctx); getErr != nil {
			return "", 0, nil, "", getErr
		}
		chain, getErr := governance_model.Ancestors(ctx, repo.OwnerID)
		if getErr != nil {
			return "", 0, nil, "", getErr
		}
		for _, namespace := range chain {
			if namespace.Kind == "group" {
				ancestors = append(ancestors, namespace.ID)
			}
		}
		return "repository", hook.RepoID, ancestors, repo.FullPath(), nil
	}
	if hook.OwnerID > 0 {
		namespace, getErr := governance_model.GetNamespace(ctx, hook.OwnerID)
		if getErr != nil {
			return "", 0, nil, "", getErr
		}
		if namespace.Kind == "group" {
			chain, getErr := governance_model.Ancestors(ctx, hook.OwnerID)
			if getErr != nil {
				return "", 0, nil, "", getErr
			}
			for _, ancestor := range chain[1:] {
				if ancestor.Kind == "group" {
					ancestors = append(ancestors, ancestor.ID)
				}
			}
			return "group", hook.OwnerID, ancestors, namespace.FullPath, nil
		}
		return "user", hook.OwnerID, nil, namespace.FullPath, nil
	}
	return "instance", 0, nil, "instance", nil
}

func appendWebhookAudit(ctx context.Context, scopeHook, objectHook *Webhook, action string, before, after map[string]any, changedFields []string) error {
	scopeType, scopeID, ancestors, resourcePath, err := webhookAuditScope(ctx, scopeHook)
	if err != nil {
		return err
	}
	details, err := json.Marshal(map[string]any{"before": before, "after": after, "changed_fields": changedFields})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{
		Type: "webhook." + action, Actor: governance_model.AuditActor(ctx),
		ScopeType: scopeType, ScopeID: scopeID, AncestorIDs: ancestors,
		ObjectType: "webhook", ObjectID: objectHook.ID, ObjectPath: path.Join(resourcePath, fmt.Sprintf("webhook-%d-%s", objectHook.ID, objectHook.Name)),
		Result: "success", Details: details,
	})
}

// AppendRepositoryWebhookDeletionAudits 在项目行仍存在时保存批量删除的 Hook 快照。
func AppendRepositoryWebhookDeletionAudits(ctx context.Context, repoID int64) error {
	hooks, err := db.Find[Webhook](ctx, ListWebhookOptions{RepoID: repoID})
	if err != nil {
		return err
	}
	for _, hook := range hooks {
		if err := appendWebhookAudit(ctx, hook, hook, "deleted", webhookAuditValues(hook), nil, []string{"deleted"}); err != nil {
			return err
		}
	}
	return nil
}

// DeleteOwnerWebhooks 审计并删除用户或群组直属 Hook，供账号彻底删除复用。
func DeleteOwnerWebhooks(ctx context.Context, ownerID int64) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		hooks, err := db.Find[Webhook](ctx, ListWebhookOptions{OwnerID: ownerID})
		if err != nil {
			return err
		}
		for _, hook := range hooks {
			if err := appendWebhookAudit(ctx, hook, hook, "deleted", webhookAuditValues(hook), nil, []string{"deleted"}); err != nil {
				return err
			}
			if _, err := db.DeleteByBean(ctx, &HookTask{HookID: hook.ID}); err != nil {
				return err
			}
			if count, err := db.DeleteByID[Webhook](ctx, hook.ID); err != nil {
				return err
			} else if count != 1 {
				return ErrWebhookNotExist{ID: hook.ID}
			}
		}
		return nil
	})
}

func webhookChangedFields(before, after *Webhook) []string {
	fields := make([]string, 0, 12)
	for name, changed := range map[string]bool{
		"url": before.URL != after.URL, "name": before.Name != after.Name, "http_method": before.HTTPMethod != after.HTTPMethod,
		"content_type": before.ContentType != after.ContentType, "secret": before.Secret != after.Secret, "events": before.Events != after.Events,
		"active": before.IsActive != after.IsActive, "type": before.Type != after.Type, "meta": before.Meta != after.Meta,
		"authorization": before.HeaderAuthorizationEncrypted != after.HeaderAuthorizationEncrypted, "system": before.IsSystemWebhook != after.IsSystemWebhook,
	} {
		if changed {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	return fields
}
