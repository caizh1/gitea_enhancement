// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
)

func oauthApplicationAuditValues(app *OAuth2Application) map[string]any {
	if app == nil {
		return nil
	}
	redirects := make([]string, 0, len(app.RedirectURIs))
	for _, raw := range app.RedirectURIs {
		if parsed, err := url.Parse(raw); err == nil {
			redirects = append(redirects, parsed.Scheme+"://"+parsed.Host)
		}
	}
	slices.Sort(redirects)
	return map[string]any{"name": app.Name, "confidential_client": app.ConfidentialClient, "skip_secondary_authorization": app.SkipSecondaryAuthorization, "redirect_origins": redirects}
}

func appendOAuthApplicationAudit(ctx context.Context, before, after *OAuth2Application, action string) error {
	object := after
	if object == nil {
		object = before
	}
	beforeValues, afterValues := oauthApplicationAuditValues(before), oauthApplicationAuditValues(after)
	changed := make([]string, 0)
	for _, field := range []string{"name", "confidential_client", "skip_secondary_authorization", "redirect_origins"} {
		if !reflect.DeepEqual(beforeValues[field], afterValues[field]) {
			changed = append(changed, field)
		}
	}
	if before != nil && after != nil && !reflect.DeepEqual(before.RedirectURIs, after.RedirectURIs) && !slices.Contains(changed, "redirect_uris") {
		changed = append(changed, "redirect_uris")
	}
	details, err := json.Marshal(map[string]any{"before": beforeValues, "after": afterValues, "changed_fields": changed})
	if err != nil {
		return err
	}
	event := &governance_model.AuditEvent{Type: "oauth.application_" + action, Actor: governance_model.AuditActor(ctx), ScopeType: "user", ScopeID: object.UID, ObjectType: "oauth_application", ObjectID: object.ID, ObjectPath: object.Name, Result: "success", Details: details}
	if object.UID == 0 {
		event.ScopeType = "instance"
	} else {
		// 组织类型固定为 1；直接投影避免 auth 与 user 模型循环依赖。
		organization, err := db.GetEngine(ctx).Table("user").Where("id = ? AND type = ?", object.UID, 1).Exist()
		if err != nil {
			return err
		}
		if organization {
			event.ScopeType = "group"
			ancestors, err := governance_model.Ancestors(ctx, object.UID)
			if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
				return err
			}
			for _, ancestor := range ancestors {
				event.AncestorIDs = append(event.AncestorIDs, ancestor.ID)
			}
		}
	}
	return governance_model.AppendAudit(ctx, event)
}

func appendOAuthGrantAudit(ctx context.Context, grant *OAuth2Grant, eventType string) error {
	scopes := strings.Fields(grant.Scope)
	slices.Sort(scopes)
	details, err := json.Marshal(map[string]any{"application_id": grant.ApplicationID, "scopes": scopes})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: eventType, Actor: governance_model.AuditActor(ctx), ScopeType: "user", ScopeID: grant.UserID, ObjectType: "oauth_authorization", ObjectID: grant.ID, ObjectPath: "OAuth authorization", Result: "success", Details: details})
}

// AppendOAuthTokenAudit 在令牌已成功签名后记录签发结果，不记录令牌正文。
func AppendOAuthTokenAudit(ctx context.Context, grant *OAuth2Grant, refreshed bool) error {
	eventType := "oauth.token_issued"
	if refreshed {
		eventType = "oauth.token_refreshed"
	}
	return appendOAuthGrantAudit(ctx, grant, eventType)
}
