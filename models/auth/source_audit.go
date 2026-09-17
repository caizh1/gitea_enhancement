// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"reflect"
	"slices"
	"strings"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
)

func sourceAuditValues(source *Source) map[string]any {
	if source == nil {
		return nil
	}
	values := map[string]any{
		"name": source.Name, "type": source.Type.String(), "active": source.IsActive,
		"sync_enabled": source.IsSyncEnabled, "two_factor_policy": source.TwoFactorPolicy,
		"tls": source.UseTLS(), "skip_tls_verify": source.SkipVerify(),
	}
	if config, ok := source.Cfg.(AuditableSourceConfig); ok {
		values["config"] = config.AuditSourceConfig()
	}
	return values
}

// AppendSourceAudit 只记录认证源的非秘密配置摘要。
func AppendSourceAudit(ctx context.Context, before, after *Source, action string) error {
	object := after
	if object == nil {
		object = before
	}
	beforeValues, afterValues := sourceAuditValues(before), sourceAuditValues(after)
	keys := make(map[string]bool)
	for key := range beforeValues {
		keys[key] = true
	}
	for key := range afterValues {
		keys[key] = true
	}
	changedFields := make([]string, 0, len(keys))
	for key := range keys {
		if !reflect.DeepEqual(beforeValues[key], afterValues[key]) {
			changedFields = append(changedFields, key)
		}
	}
	if before != nil && after != nil && before.Cfg != nil && after.Cfg != nil && reflect.TypeOf(before.Cfg) == reflect.TypeOf(after.Cfg) {
		changedFields = append(changedFields, rawConfigChangedFields(reflect.ValueOf(before.Cfg), reflect.ValueOf(after.Cfg), "config")...)
	}
	slices.Sort(changedFields)
	changedFields = slices.Compact(changedFields)
	details, err := json.Marshal(map[string]any{"before": beforeValues, "after": afterValues, "changed_fields": changedFields})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{
		Type: "authentication.source_" + action, Actor: governance_model.AuditActor(ctx), ScopeType: "instance", ScopeID: 0,
		ObjectType: "authentication_source", ObjectID: object.ID, ObjectPath: object.Name, Result: "success", Details: details,
	})
}

func rawConfigChangedFields(before, after reflect.Value, prefix string) []string {
	for before.Kind() == reflect.Pointer {
		if before.IsNil() || after.IsNil() {
			if before.IsNil() != after.IsNil() {
				return []string{prefix}
			}
			return nil
		}
		before, after = before.Elem(), after.Elem()
	}
	if before.Kind() != reflect.Struct {
		if !reflect.DeepEqual(before.Interface(), after.Interface()) {
			return []string{prefix}
		}
		return nil
	}
	fields := make([]string, 0)
	for i := 0; i < before.NumField(); i++ {
		field := before.Type().Field(i)
		if field.PkgPath != "" || field.Name == "ConfigBase" || field.Name == "AuthSource" {
			continue
		}
		name := prefix + "." + strings.ToLower(field.Name)
		if !reflect.DeepEqual(before.Field(i).Interface(), after.Field(i).Interface()) {
			fields = append(fields, name)
		}
	}
	return fields
}
