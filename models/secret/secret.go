// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secret

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	actions_module "gitea.dev/modules/actions"
	"gitea.dev/modules/actions/jobparser"
	"gitea.dev/modules/git"
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"
	secret_module "gitea.dev/modules/secret"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// Secret represents a secret
//
// It can be:
//  1. org/user level secret, OwnerID is org/user ID and RepoID is 0
//  2. repo level secret, OwnerID is 0 and RepoID is repo ID
//
// Please note that it's not acceptable to have both OwnerID and RepoID to be non-zero,
// or it will be complicated to find secrets belonging to a specific owner.
// For example, conditions like `OwnerID = 1` will also return secret {OwnerID: 1, RepoID: 1},
// but it's a repo level secret, not an org/user level secret.
// To avoid this, make it clear with {OwnerID: 0, RepoID: 1} for repo level secrets.
//
// Please note that it's not acceptable to have both OwnerID and RepoID to zero, global secrets are not supported.
// It's for security reasons, admin may be not aware of that the secrets could be stolen by any user when setting them as global.
type Secret struct {
	ID          int64
	OwnerID     int64              `xorm:"INDEX UNIQUE(owner_repo_name) NOT NULL"`
	RepoID      int64              `xorm:"INDEX UNIQUE(owner_repo_name) NOT NULL DEFAULT 0"`
	Name        string             `xorm:"UNIQUE(owner_repo_name) NOT NULL"`
	Data        string             `xorm:"LONGTEXT"` // encrypted data
	Description string             `xorm:"TEXT"`
	Protected   bool               `xorm:"NOT NULL DEFAULT false"`
	CreatedUnix timeutil.TimeStamp `xorm:"created NOT NULL"`
}

const (
	SecretDataMaxLength        = 65536
	SecretDescriptionMaxLength = 4096
)

// ErrSecretNotFound represents a "secret not found" error.
type ErrSecretNotFound struct {
	Name string
}

func (err ErrSecretNotFound) Error() string {
	return fmt.Sprintf("secret was not found [name: %s]", err.Name)
}

func (err ErrSecretNotFound) Unwrap() error {
	return util.ErrNotExist
}

// InsertEncryptedSecret Creates, encrypts, and validates a new secret with yet unencrypted data and insert into database
func InsertEncryptedSecret(ctx context.Context, ownerID, repoID int64, name, data, description string, protected ...bool) (*Secret, error) {
	if ownerID != 0 && repoID != 0 {
		// It's trying to create a secret that belongs to a repository, but OwnerID has been set accidentally.
		// Remove OwnerID to avoid confusion; it's not worth returning an error here.
		ownerID = 0
	}
	if ownerID == 0 && repoID == 0 {
		return nil, fmt.Errorf("%w: ownerID and repoID cannot be both zero, global secrets are not supported", util.ErrInvalidArgument)
	}

	if len(data) > SecretDataMaxLength {
		return nil, util.NewInvalidArgumentErrorf("data too long")
	}

	description = util.TruncateRunes(description, SecretDescriptionMaxLength)

	encrypted, err := secret_module.EncryptSecret(setting.SecretKey, data)
	if err != nil {
		return nil, err
	}

	secret := &Secret{
		OwnerID:     ownerID,
		RepoID:      repoID,
		Name:        strings.ToUpper(name),
		Data:        encrypted,
		Description: description,
	}
	if len(protected) > 0 {
		secret.Protected = protected[0]
	}
	if err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		if err := db.Insert(ctx, secret); err != nil {
			return err
		}
		return actions_model.AppendConfigurationAudit(ctx, secret.OwnerID, secret.RepoID, "actions.secret_created", "actions_secret", secret.ID, secret.Name, map[string]any{"name": secret.Name, "protected": secret.Protected, "value_configured": secret.Data != "", "description_configured": secret.Description != ""})
	}); err != nil {
		return nil, err
	}
	return secret, nil
}

func init() {
	db.RegisterModel(new(Secret))
}

type FindSecretsOptions struct {
	db.ListOptions
	RepoID   int64
	OwnerID  int64 // it will be ignored if RepoID is set
	SecretID int64
	Name     string
}

func (opts FindSecretsOptions) ToConds() builder.Cond {
	cond := builder.NewCond()

	cond = cond.And(builder.Eq{"repo_id": opts.RepoID})
	if opts.RepoID != 0 { // if RepoID is set
		// ignore OwnerID and treat it as 0
		cond = cond.And(builder.Eq{"owner_id": 0})
	} else {
		cond = cond.And(builder.Eq{"owner_id": opts.OwnerID})
	}

	if opts.SecretID != 0 {
		cond = cond.And(builder.Eq{"id": opts.SecretID})
	}
	if opts.Name != "" {
		cond = cond.And(builder.Eq{"name": strings.ToUpper(opts.Name)})
	}

	return cond
}

// UpdateSecret changes org or user reop secret.
func UpdateSecret(ctx context.Context, secretID int64, data, description string, protected ...*bool) error {
	if len(data) > SecretDataMaxLength {
		return util.NewInvalidArgumentErrorf("data too long")
	}

	description = util.TruncateRunes(description, SecretDescriptionMaxLength)

	encrypted, err := secret_module.EncryptSecret(setting.SecretKey, data)
	if err != nil {
		return err
	}

	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		s, has, err := db.GetByID[Secret](ctx, secretID)
		if err != nil {
			return err
		}
		if !has {
			return ErrSecretNotFound{}
		}
		beforeData, beforeDescription, beforeProtected := s.Data, s.Description, s.Protected
		s.Data, s.Description = encrypted, description
		columns := []string{"data", "description"}
		if len(protected) > 0 && protected[0] != nil {
			s.Protected = *protected[0]
			columns = append(columns, "protected")
		}
		affected, err := db.GetEngine(ctx).ID(secretID).Cols(columns...).Update(s)
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrSecretNotFound{}
		}
		changed := make([]string, 0, 2)
		if beforeData != s.Data {
			changed = append(changed, "value")
		}
		if beforeDescription != s.Description {
			changed = append(changed, "description")
		}
		if beforeProtected != s.Protected {
			changed = append(changed, "protected")
		}
		return actions_model.AppendConfigurationAudit(ctx, s.OwnerID, s.RepoID, "actions.secret_updated", "actions_secret", s.ID, s.Name, map[string]any{"name": s.Name, "changed_fields": changed, "protected": s.Protected, "value_configured": s.Data != "", "description_configured": s.Description != ""})
	})
}

// ProtectedRefTrusted currently accepts only direct trusted events on a protected branch.
func ProtectedRefTrusted(ctx context.Context, run *actions_model.ActionRun) (bool, error) {
	if run == nil || run.IsForkPullRequest || run.CommitSHA == "" {
		return false, nil
	}
	switch run.TriggerEvent {
	case actions_module.GithubEventPush, actions_module.GithubEventSchedule, "workflow_dispatch":
	default:
		return false, nil
	}
	ref := git.RefName(run.Ref)
	if ref.IsBranch() && ref.BranchName() != "" {
		protected, err := git_model.IsBranchProtected(ctx, run.RepoID, ref.BranchName())
		if err != nil || !protected {
			return false, err
		}
		branch, err := git_model.GetBranch(ctx, run.RepoID, ref.BranchName())
		if errors.Is(err, util.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return !branch.IsDeleted && branch.CommitID == run.CommitSHA, nil
	}
	// Tag protection has no durable tag-ref snapshot here; a rule added after an
	// untrusted tag run was queued must not make its old commit trusted.
	return false, nil
}

func GetSecretsOfTask(ctx context.Context, task *actions_model.ActionTask) (map[string]string, error) {
	if task.Job == nil || task.Job.Run == nil || task.Job.Run.Repo == nil || task.OwnerID != task.Job.Run.Repo.OwnerID || task.Job.OwnerID != task.OwnerID || task.Job.Run.OwnerID != task.OwnerID {
		return nil, fmt.Errorf("%w：Actions 任务的归属已变化", governance_model.ErrConflict)
	}
	baseSecrets := map[string]string{}

	baseSecrets["GITHUB_TOKEN"] = task.Token
	baseSecrets["GITEA_TOKEN"] = task.Token

	if task.Job.Run.IsForkPullRequest && task.Job.Run.TriggerEvent != actions_module.GithubEventPullRequestTarget {
		// ignore secrets for fork pull request, except GITHUB_TOKEN and GITEA_TOKEN which are automatically generated.
		// for the tasks triggered by pull_request_target event, they could access the secrets because they will run in the context of the base branch
		// see the documentation: https://docs.github.com/en/actions/using-workflows/events-that-trigger-workflows#pull_request_target
		return baseSecrets, nil
	}

	chain, err := governance_model.Ancestors(ctx, task.OwnerID)
	if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
		return nil, err
	}
	ownerIDs := []int64{task.OwnerID}
	if len(chain) > 0 {
		ownerIDs = ownerIDs[:0]
		for _, ancestor := range slices.Backward(chain) {
			ownerIDs = append(ownerIDs, ancestor.ID)
		}
	}
	var ownerSecrets []*Secret
	for _, ownerID := range ownerIDs {
		secrets, err := db.Find[Secret](ctx, FindSecretsOptions{OwnerID: ownerID})
		if err != nil {
			log.Error("find secrets of owner %v: %v", ownerID, err)
			return nil, err
		}
		ownerSecrets = append(ownerSecrets, secrets...)
	}
	repoSecrets, err := db.Find[Secret](ctx, FindSecretsOptions{RepoID: task.Job.Run.RepoID})
	if err != nil {
		log.Error("find secrets of repo %v: %v", task.Job.Run.RepoID, err)
		return nil, err
	}

	selected := make(map[string]*Secret, len(ownerSecrets)+len(repoSecrets))
	for _, secret := range append(ownerSecrets, repoSecrets...) {
		selected[secret.Name] = secret
	}
	trustedProtectedRef, checkedProtectedRef := false, false
	for _, secret := range selected {
		if secret.Protected {
			// Check after choosing the nearest source; an ineligible override cannot expose a parent value.
			if !checkedProtectedRef {
				trustedProtectedRef, err = ProtectedRefTrusted(ctx, task.Job.Run)
				if err != nil {
					return nil, err
				}
				checkedProtectedRef = true
			}
			if !trustedProtectedRef {
				continue
			}
		}
		v, err := secret_module.DecryptSecret(setting.SecretKey, secret.Data)
		if err != nil {
			log.Error("Unable to decrypt Actions secret %v %q, maybe SECRET_KEY is wrong: %v", secret.ID, secret.Name, err)
			return nil, fmt.Errorf("unable to decrypt Actions secret %d: %w", secret.ID, err)
		}
		baseSecrets[secret.Name] = v
	}

	return getScopedSecretsForJob(ctx, task.Job, baseSecrets)
}

// getScopedSecretsForJob walks up the caller chain (ParentJobID) and applies
// each caller's secrets policy:
//   - "secrets: inherit" passes the parent scope's secrets through unchanged.
//   - explicit mapping {alias: SOURCE} only forwards the named secrets, plus the auto-generated tokens.
//
// For top-level jobs (ParentJobID == 0) the base secrets are returned as-is.
func getScopedSecretsForJob(ctx context.Context, job *actions_model.ActionRunJob, baseSecrets map[string]string) (map[string]string, error) {
	if job.ParentJobID == 0 {
		return baseSecrets, nil
	}

	caller, err := actions_model.GetRunJobByRunAndID(ctx, job.RunID, job.ParentJobID)
	if err != nil {
		return nil, fmt.Errorf("load caller job %d: %w", job.ParentJobID, err)
	}

	parentScope, err := getScopedSecretsForJob(ctx, caller, baseSecrets)
	if err != nil {
		return nil, err
	}

	if caller.CallSecrets == jobparser.SecretsInherit {
		return parentScope, nil
	}

	// Empty or explicit-mapping path: only auto-tokens + (any) mapped aliases are exposed.
	scoped := map[string]string{
		"GITHUB_TOKEN": baseSecrets["GITHUB_TOKEN"],
		"GITEA_TOKEN":  baseSecrets["GITEA_TOKEN"],
	}
	if caller.CallSecrets == "" {
		return scoped, nil
	}
	var mapping map[string]string
	if err := json.Unmarshal([]byte(caller.CallSecrets), &mapping); err != nil {
		return nil, fmt.Errorf("decode caller %d secret map: %w", caller.ID, err)
	}
	for alias, source := range mapping {
		if v, ok := parentScope[source]; ok {
			scoped[alias] = v
			continue
		}
		// Secret names are case-insensitive in storage (uppercased).
		if v, ok := parentScope[strings.ToUpper(source)]; ok {
			scoped[alias] = v
		}
	}
	return scoped, nil
}

func CountWrongRepoLevelSecrets(ctx context.Context) (int64, error) {
	var result int64
	_, err := db.GetEngine(ctx).SQL("SELECT count(`id`) FROM `secret` WHERE `repo_id` > 0 AND `owner_id` > 0").Get(&result)
	return result, err
}

func UpdateWrongRepoLevelSecrets(ctx context.Context) (int64, error) {
	result, err := db.GetEngine(ctx).Exec("UPDATE `secret` SET `owner_id` = 0 WHERE `repo_id` > 0 AND `owner_id` > 0")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
