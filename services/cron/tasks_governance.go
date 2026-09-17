// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package cron

import (
	"context"

	user_model "gitea.dev/models/user"
	asymkey_service "gitea.dev/services/asymkey"
	"gitea.dev/services/governance"
	"gitea.dev/services/mirror"
	org_service "gitea.dev/services/org"
	repo_service "gitea.dev/services/repository"
)

func registerAuditExports() {
	RegisterTaskFatal("governance_ssh_key_files", &BaseConfig{Enabled: true, RunAtStart: true, Schedule: "@every 10s"}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return asymkey_service.SyncSSHKeyFiles(ctx)
	})
	RegisterTaskFatal("governance_invitations", &BaseConfig{Enabled: true, RunAtStart: true, Schedule: "@every 1m"}, func(ctx context.Context, _ *user_model.User, _ Config) error { return governance.RunInvitations(ctx) })
	RegisterTaskFatal("governance_expired_grants", &BaseConfig{Enabled: true, RunAtStart: true, Schedule: "@every 1m"}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return governance.RunExpiredGrants(ctx)
	})
	RegisterTaskFatal("governance_repository_deletion", &BaseConfig{Enabled: true, RunAtStart: true, Schedule: "@every 1m"}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return repo_service.RunScheduledRepositoryDeletions(ctx)
	})
	RegisterTaskFatal("governance_group_deletion", &BaseConfig{Enabled: true, RunAtStart: true, Schedule: "@every 1m"}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return org_service.RunScheduledGroupDeletions(ctx)
	})
	RegisterTaskFatal("governance_resource_cleanup", &BaseConfig{Enabled: true, RunAtStart: true, Schedule: "@every 10s"}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return repo_service.RunResourceCleanups(ctx)
	})
	RegisterTaskFatal("governance_repository_creation_recovery", &BaseConfig{Enabled: true, RunAtStart: true, Schedule: "@every 1m"}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return repo_service.RunRepositoryCreationRecovery(ctx)
	})
	RegisterTaskFatal("governance_mirror_operation_recovery", &BaseConfig{Enabled: true, RunAtStart: true, Schedule: "@every 1m"}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return mirror.RunMirrorOperationRecovery(ctx)
	})
	RegisterTaskFatal("governance_reference_business_recovery", &BaseConfig{Enabled: true, RunAtStart: true, Schedule: "@every 10s"}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return repo_service.RunReferenceBusinessOperationRecovery(ctx)
	})
	RegisterTaskFatal("governance_audit_exports", &BaseConfig{Enabled: true, RunAtStart: true, Schedule: "@every 5s"}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return governance.RunAuditExports(ctx)
	})
	RegisterTaskFatal("governance_audit_streams", &BaseConfig{Enabled: true, RunAtStart: true, Schedule: "@every 5s"}, func(ctx context.Context, _ *user_model.User, _ Config) error {
		return governance.RunAuditStreams(ctx)
	})
}
