// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/util"
	governance_service "gitea.dev/services/governance"
	secret_service "gitea.dev/services/secrets"
)

func CreateVariable(ctx context.Context, ownerID, repoID int64, name, data, description string) (*actions_model.ActionVariable, error) {
	if err := secret_service.ValidateName(name); err != nil {
		return nil, err
	}

	var variable *actions_model.ActionVariable
	err := governance_service.WithConfigurationWrite(ctx, governance_model.AuditActor(ctx), ownerID, repoID, func(tx context.Context) error {
		var err error
		variable, err = actions_model.InsertVariable(tx, ownerID, repoID, name, util.NormalizeStringEOL(data), description)
		return err
	})
	return variable, err
}

func UpdateVariableNameData(ctx context.Context, variable *actions_model.ActionVariable) (bool, error) {
	if err := secret_service.ValidateName(variable.Name); err != nil {
		return false, err
	}

	variable.Data = util.NormalizeStringEOL(variable.Data)

	var updated bool
	err := governance_service.WithConfigurationWrite(ctx, governance_model.AuditActor(ctx), variable.OwnerID, variable.RepoID, func(tx context.Context) error {
		fresh, has, err := db.GetByID[actions_model.ActionVariable](tx, variable.ID)
		if err != nil {
			return err
		}
		if !has || fresh.OwnerID != variable.OwnerID || fresh.RepoID != variable.RepoID {
			return util.ErrPermissionDenied
		}
		updated, err = actions_model.UpdateVariableCols(tx, variable, "name", "data", "description")
		return err
	})
	return updated, err
}

func DeleteVariableByID(ctx context.Context, variableID int64) error {
	variable, has, err := db.GetByID[actions_model.ActionVariable](ctx, variableID)
	if err != nil {
		return err
	}
	if !has {
		return util.NewNotExistErrorf("variable not found")
	}
	return governance_service.WithConfigurationWrite(ctx, governance_model.AuditActor(ctx), variable.OwnerID, variable.RepoID, func(tx context.Context) error {
		fresh, has, err := db.GetByID[actions_model.ActionVariable](tx, variableID)
		if err != nil {
			return err
		}
		if !has || fresh.OwnerID != variable.OwnerID || fresh.RepoID != variable.RepoID {
			return util.ErrPermissionDenied
		}
		return actions_model.DeleteVariable(tx, variableID)
	})
}

func DeleteVariableByName(ctx context.Context, ownerID, repoID int64, name string) error {
	v, err := GetVariable(ctx, actions_model.FindVariablesOpts{
		OwnerID: ownerID,
		RepoID:  repoID,
		Name:    name,
	})
	if err != nil {
		return err
	}

	return governance_service.WithConfigurationWrite(ctx, governance_model.AuditActor(ctx), ownerID, repoID, func(tx context.Context) error {
		fresh, err := GetVariable(tx, actions_model.FindVariablesOpts{OwnerID: ownerID, RepoID: repoID, Name: name})
		if err != nil {
			return err
		}
		if fresh.ID != v.ID {
			return util.ErrPermissionDenied
		}
		return actions_model.DeleteVariable(tx, fresh.ID)
	})
}

func GetVariable(ctx context.Context, opts actions_model.FindVariablesOpts) (*actions_model.ActionVariable, error) {
	vars, err := actions_model.FindVariables(ctx, opts)
	if err != nil {
		return nil, err
	}
	if len(vars) != 1 {
		return nil, util.NewNotExistErrorf("variable not found")
	}
	return vars[0], nil
}
