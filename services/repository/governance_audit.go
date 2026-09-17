// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"slices"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/json"

	"github.com/google/uuid"
)

type repositoryCreationContextKey struct{}
type repositoryCreationContext struct {
	origin   string
	metadata map[string]any
}

func withRepositoryCreation(ctx context.Context, origin string, metadata map[string]any) context.Context {
	return context.WithValue(ctx, repositoryCreationContextKey{}, repositoryCreationContext{origin: origin, metadata: metadata})
}

func prepareRepositoryCreation(ctx context.Context, repo *repo_model.Repository) error {
	creation, _ := ctx.Value(repositoryCreationContextKey{}).(repositoryCreationContext)
	if creation.origin == "" {
		return nil
	}
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
		return err
	}
	now := time.Now()
	operation := &governance_model.RepositoryCreation{ID: uuid.NewString(), RepoID: repo.ID, State: "prepared", Phase: "database_prepared", Origin: creation.origin, Actor: governance_model.AuditActor(ctx), ObjectPath: repo.FullPath(), ExpectedRelativePath: repo.RelativePath(), Metadata: creation.metadata, CreatedUnix: now.Unix(), NextAttemptUnix: now.Add(10 * time.Minute).Unix()}
	for _, ancestor := range chain {
		if ancestor.Kind == "group" {
			operation.AncestorIDs = append(operation.AncestorIDs, ancestor.ID)
		}
	}
	return db.Insert(ctx, operation)
}

func markRepositoryCreationStorageComplete(ctx context.Context, repoID int64) error {
	affected, err := db.GetEngine(ctx).Where("repo_id = ? AND state = ?", repoID, "prepared").Cols("phase", "next_attempt_unix").Update(&governance_model.RepositoryCreation{Phase: "storage_complete", NextAttemptUnix: time.Now().Add(10 * time.Minute).Unix()})
	if err != nil {
		return err
	}
	if affected != 1 {
		return governance_model.ErrConflict
	}
	return nil
}

func startRepositoryCreationHeartbeat(repoID int64) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, _ = db.GetEngine(ctx).Where("repo_id = ? AND state = ?", repoID, "prepared").Cols("next_attempt_unix").Update(&governance_model.RepositoryCreation{NextAttemptUnix: time.Now().Add(10 * time.Minute).Unix()})
				cancel()
			case <-stop:
				return
			}
		}
	}()
	return func() { close(stop) }
}

func completeRepositoryCreation(ctx context.Context, repo *repo_model.Repository) error {
	operation := new(governance_model.RepositoryCreation)
	has, err := db.GetEngine(ctx).Where("repo_id = ?", repo.ID).Get(operation)
	if err != nil {
		return err
	}
	if !has {
		return governance_model.ErrConflict
	}
	if operation.State == "succeeded" {
		if repo.Status == repo_model.RepositoryReady && operation.ExpectedRelativePath == repo.RelativePath() {
			return nil
		}
		return governance_model.ErrConflict
	}
	if operation.State != "prepared" && operation.State != "unknown" || operation.Phase != "storage_complete" || operation.ExpectedRelativePath != repo.RelativePath() {
		return governance_model.ErrConflict
	}
	details, err := operation.AuditDetails()
	if err != nil {
		return err
	}
	if err := governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "repository.created", Actor: operation.Actor, ScopeType: "repository", ScopeID: repo.ID, AncestorIDs: operation.AncestorIDs, ObjectType: "repository", ObjectID: repo.ID, ObjectPath: operation.ObjectPath, Result: "success", Details: details}); err != nil {
		return err
	}
	affected, err := db.GetEngine(ctx).ID(operation.ID).In("state", "prepared", "unknown").Cols("state", "last_reason_code", "next_attempt_unix").Update(&governance_model.RepositoryCreation{State: "succeeded", LastReasonCode: "", NextAttemptUnix: 0})
	if err != nil {
		return err
	}
	if affected != 1 {
		return governance_model.ErrConflict
	}
	return nil
}

func repositoryAuditValues(repo *repo_model.Repository) map[string]any {
	if repo == nil {
		return nil
	}
	return map[string]any{
		"name": repo.Name, "description": repo.Description, "website": safeRepositoryURL(repo.Website),
		"private": repo.IsPrivate, "template": repo.IsTemplate, "mirror": repo.IsMirror,
		"archived": repo.IsArchived, "default_branch": repo.DefaultBranch, "default_wiki_branch": repo.DefaultWikiBranch,
		"trust_model": repo.TrustModel, "fsck_enabled": repo.IsFsckEnabled,
		"close_issues_via_commit_in_any_branch": repo.CloseIssuesViaCommitInAnyBranch,
	}
}

func safeRepositoryURL(value string) string {
	u, err := url.Parse(value)
	if err != nil {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

func appendRepositoryAudit(ctx context.Context, before, after *repo_model.Repository, eventType string, extra map[string]any) error {
	object := after
	if object == nil {
		object = before
	}
	if err := object.LoadOwner(ctx); err != nil {
		return err
	}
	chain, err := governance_model.Ancestors(ctx, object.OwnerID)
	if err != nil {
		if !errors.Is(err, governance_model.ErrNotFound) {
			return err
		}
		chain = nil
	}
	ancestors := make([]int64, 0, len(chain))
	for _, namespace := range chain {
		if namespace.Kind == "group" {
			ancestors = append(ancestors, namespace.ID)
		}
	}
	beforeValues, afterValues := repositoryAuditValues(before), repositoryAuditValues(after)
	changed := make([]string, 0)
	fields := make(map[string]bool, len(beforeValues)+len(afterValues))
	for field := range beforeValues {
		fields[field] = true
	}
	for field := range afterValues {
		fields[field] = true
	}
	for field := range fields {
		if !reflect.DeepEqual(beforeValues[field], afterValues[field]) {
			changed = append(changed, field)
		}
	}
	slices.Sort(changed)
	details := map[string]any{"before": beforeValues, "after": afterValues, "changed_fields": changed}
	for key, value := range extra {
		details[key] = value
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: eventType, Actor: governance_model.AuditActor(ctx), ScopeType: "repository", ScopeID: object.ID, AncestorIDs: ancestors, ObjectType: "repository", ObjectID: object.ID, ObjectPath: object.FullPath(), Result: "success", Details: raw})
}

// AppendRepositoryAudit 供仓库子服务记录显式白名单配置。
func AppendRepositoryAudit(ctx context.Context, before, after *repo_model.Repository, eventType string, extra map[string]any) error {
	return appendRepositoryAudit(ctx, before, after, eventType, extra)
}
