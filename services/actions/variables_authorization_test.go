// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"testing"

	actions_model "gitea.dev/models/actions"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/util"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestVariableWriteRejectsRevokedGroupOwner(t *testing.T) {
	for _, operation := range []string{"create", "update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			ctx := t.Context()
			require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
			owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
			group, err := governance_model.GetNamespace(ctx, 3)
			require.NoError(t, err)
			require.NoError(t, governance_service.SetGroupMember(ctx, owner, 3,
				governance_service.GroupMemberOption{UserID: 4, Role: governance_model.Owner, Revision: group.Revision}, false))
			doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
			request := governance_model.WithAuditActor(ctx, governance_service.RequestActor(doer, "", "web"))
			_, err = governance_service.CheckGroupAccess(request, doer.ID, 3, governance_model.ManageGroup)
			require.NoError(t, err)
			var existing *actions_model.ActionVariable
			if operation != "create" {
				existing, err = CreateVariable(request, 3, 0, "ACCESS_CHECK", "before", "initial")
				require.NoError(t, err)
			}
			group, err = governance_model.GetNamespace(ctx, 3)
			require.NoError(t, err)
			require.NoError(t, governance_service.SetGroupMember(ctx, owner, 3,
				governance_service.GroupMemberOption{UserID: 4, Revision: group.Revision}, true))

			switch operation {
			case "create":
				_, err = CreateVariable(request, 3, 0, "ACCESS_CHECK", "after", "revoked")
			case "update":
				existing.Data = "after"
				_, err = UpdateVariableNameData(request, existing)
			case "delete":
				err = DeleteVariableByID(request, existing.ID)
			}
			require.ErrorIs(t, err, util.ErrPermissionDenied)
			values, err := actions_model.FindVariables(ctx, actions_model.FindVariablesOpts{OwnerID: 3, Name: "ACCESS_CHECK"})
			require.NoError(t, err)
			if operation == "create" {
				require.Empty(t, values)
			} else {
				require.Len(t, values, 1)
				require.Equal(t, "before", values[0].Data)
			}
		})
	}
}
