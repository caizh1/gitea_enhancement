// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"strings"
	"unicode/utf8"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/log"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// ActionVariable represents a variable that can be used in actions
//
// It can be:
//  1. global variable, OwnerID is 0 and RepoID is 0
//  2. org/user level variable, OwnerID is org/user ID and RepoID is 0
//  3. repo level variable, OwnerID is 0 and RepoID is repo ID
//
// Please note that it's not acceptable to have both OwnerID and RepoID to be non-zero,
// or it will be complicated to find variables belonging to a specific owner.
// For example, conditions like `OwnerID = 1` will also return variable {OwnerID: 1, RepoID: 1},
// but it's a repo level variable, not an org/user level variable.
// To avoid this, make it clear with {OwnerID: 0, RepoID: 1} for repo level variables.
type ActionVariable struct {
	ID          int64              `xorm:"pk autoincr"`
	OwnerID     int64              `xorm:"UNIQUE(owner_repo_name)"`
	RepoID      int64              `xorm:"INDEX UNIQUE(owner_repo_name)"`
	Name        string             `xorm:"UNIQUE(owner_repo_name) NOT NULL"`
	Data        string             `xorm:"LONGTEXT NOT NULL"`
	Description string             `xorm:"TEXT"`
	CreatedUnix timeutil.TimeStamp `xorm:"created NOT NULL"`
	UpdatedUnix timeutil.TimeStamp `xorm:"updated"`
}

const (
	VariableDataMaxLength        = 65536
	VariableDescriptionMaxLength = 4096
)

func init() {
	db.RegisterModel(new(ActionVariable))
}

func InsertVariable(ctx context.Context, ownerID, repoID int64, name, data, description string) (*ActionVariable, error) {
	if ownerID != 0 && repoID != 0 {
		// It's trying to create a variable that belongs to a repository, but OwnerID has been set accidentally.
		// Remove OwnerID to avoid confusion; it's not worth returning an error here.
		ownerID = 0
	}

	if utf8.RuneCountInString(data) > VariableDataMaxLength {
		return nil, util.NewInvalidArgumentErrorf("data too long")
	}

	description = util.TruncateRunes(description, VariableDescriptionMaxLength)

	variable := &ActionVariable{
		OwnerID:     ownerID,
		RepoID:      repoID,
		Name:        strings.ToUpper(name),
		Data:        data,
		Description: description,
	}
	if err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		if err := db.Insert(ctx, variable); err != nil {
			return err
		}
		return AppendConfigurationAudit(ctx, variable.OwnerID, variable.RepoID, "actions.variable_created", "actions_variable", variable.ID, variable.Name, variableAuditDetails(variable, []string{"created"}))
	}); err != nil {
		return nil, err
	}
	return variable, nil
}

type FindVariablesOpts struct {
	db.ListOptions
	IDs     []int64
	RepoID  int64
	OwnerID int64 // it will be ignored if RepoID is set
	Name    string
}

func (opts FindVariablesOpts) ToConds() builder.Cond {
	cond := builder.NewCond()

	if len(opts.IDs) > 0 {
		if len(opts.IDs) == 1 {
			cond = cond.And(builder.Eq{"id": opts.IDs[0]})
		} else {
			cond = cond.And(builder.In("id", opts.IDs))
		}
	}

	// Since we now support instance-level variables,
	// there is no need to check for null values for `owner_id` and `repo_id`
	cond = cond.And(builder.Eq{"repo_id": opts.RepoID})
	if opts.RepoID != 0 { // if RepoID is set
		// ignore OwnerID and treat it as 0
		cond = cond.And(builder.Eq{"owner_id": 0})
	} else {
		cond = cond.And(builder.Eq{"owner_id": opts.OwnerID})
	}

	if opts.Name != "" {
		cond = cond.And(builder.Eq{"name": strings.ToUpper(opts.Name)})
	}
	return cond
}

func FindVariables(ctx context.Context, opts FindVariablesOpts) ([]*ActionVariable, error) {
	return db.Find[ActionVariable](ctx, opts)
}

func UpdateVariableCols(ctx context.Context, variable *ActionVariable, cols ...string) (bool, error) {
	if utf8.RuneCountInString(variable.Data) > VariableDataMaxLength {
		return false, util.NewInvalidArgumentErrorf("data too long")
	}

	variable.Description = util.TruncateRunes(variable.Description, VariableDescriptionMaxLength)

	variable.Name = strings.ToUpper(variable.Name)
	var updated bool
	err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		fresh, has, err := db.GetByID[ActionVariable](ctx, variable.ID)
		if err != nil {
			return err
		}
		if !has {
			return util.NewNotExistErrorf("variable not found")
		}
		before := variableAuditValues(fresh)
		count, err := db.GetEngine(ctx).ID(variable.ID).Cols(cols...).Update(variable)
		if err != nil || count == 0 {
			return err
		}
		updated = true
		after := variableAuditValues(variable)
		changed := make([]string, 0, 3)
		for _, field := range []string{"name", "value_configured", "description_configured"} {
			if before[field] != after[field] || (field == "value_configured" && fresh.Data != variable.Data) || (field == "description_configured" && fresh.Description != variable.Description) {
				changed = append(changed, field)
			}
		}
		return AppendConfigurationAudit(ctx, fresh.OwnerID, fresh.RepoID, "actions.variable_updated", "actions_variable", fresh.ID, variable.Name, map[string]any{"before": before, "after": after, "changed_fields": changed})
	})
	return updated, err
}

func DeleteVariable(ctx context.Context, id int64) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		variable, has, err := db.GetByID[ActionVariable](ctx, id)
		if err != nil {
			return err
		}
		if !has {
			return util.NewNotExistErrorf("variable not found")
		}
		if count, err := db.DeleteByID[ActionVariable](ctx, id); err != nil {
			return err
		} else if count != 1 {
			return util.NewNotExistErrorf("variable not found")
		}
		return AppendConfigurationAudit(ctx, variable.OwnerID, variable.RepoID, "actions.variable_deleted", "actions_variable", variable.ID, variable.Name, variableAuditDetails(variable, []string{"deleted"}))
	})
}

func variableAuditDetails(variable *ActionVariable, changed []string) map[string]any {
	return map[string]any{"name": variable.Name, "value_configured": variable.Data != "", "description_configured": variable.Description != "", "changed_fields": changed}
}

func variableAuditValues(variable *ActionVariable) map[string]any {
	return map[string]any{"name": variable.Name, "value_configured": variable.Data != "", "description_configured": variable.Description != ""}
}

func GetVariablesOfRun(ctx context.Context, run *ActionRun) (map[string]string, error) {
	variables := map[string]string{}

	if err := run.LoadRepo(ctx); err != nil {
		log.Error("LoadRepo: %v", err)
		return nil, err
	}

	// Global
	globalVariables, err := db.Find[ActionVariable](ctx, FindVariablesOpts{})
	if err != nil {
		log.Error("find global variables: %v", err)
		return nil, err
	}

	// Org / User level
	ownerVariables, err := db.Find[ActionVariable](ctx, FindVariablesOpts{OwnerID: run.Repo.OwnerID})
	if err != nil {
		log.Error("find variables of org: %d, error: %v", run.Repo.OwnerID, err)
		return nil, err
	}

	// Repo level
	repoVariables, err := db.Find[ActionVariable](ctx, FindVariablesOpts{RepoID: run.RepoID})
	if err != nil {
		log.Error("find variables of repo: %d, error: %v", run.RepoID, err)
		return nil, err
	}

	// Level precedence: Repo > Org / User > Global
	for _, v := range append(globalVariables, append(ownerVariables, repoVariables...)...) {
		variables[v.Name] = v.Data
	}

	return variables, nil
}

func CountWrongRepoLevelVariables(ctx context.Context) (int64, error) {
	var result int64
	_, err := db.GetEngine(ctx).SQL("SELECT count(`id`) FROM `action_variable` WHERE `repo_id` > 0 AND `owner_id` > 0").Get(&result)
	return result, err
}

func UpdateWrongRepoLevelVariables(ctx context.Context) (int64, error) {
	result, err := db.GetEngine(ctx).Exec("UPDATE `action_variable` SET `owner_id` = 0 WHERE `repo_id` > 0 AND `owner_id` > 0")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
