// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package setting

import (
	"net/http"
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/json"
	"gitea.dev/services/context"
	"gitea.dev/services/contexttest"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/require"
)

func TestActionsRepositoryConfigurationRejectsRevokedManager(t *testing.T) {
	for _, operation := range []struct {
		name    string
		handler func(*context.Context)
		status  int
	}{
		{"令牌默认与上限", UpdateTokenPermissions, http.StatusSeeOther},
		{"新增跨仓协作方", AddCollaborativeOwner, http.StatusOK},
		{"移除跨仓协作方", DeleteCollaborativeOwner, http.StatusOK},
	} {
		t.Run(operation.name, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			ctx := t.Context()
			require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
			repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
			initial := &repo_model.ActionsConfig{TokenPermissionMode: repo_model.ActionsTokenPermissionModeRestricted, CollaborativeOwnerIDs: []int64{5}}
			actionsUnit, err := repo.GetUnit(ctx, unit.TypeActions)
			if repo_model.IsErrUnitTypeNotExist(err) {
				actionsUnit = &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeActions, Config: initial}
				require.NoError(t, db.Insert(ctx, actionsUnit))
			} else {
				require.NoError(t, err)
				actionsUnit.Config = initial
				require.NoError(t, repo_model.UpdateRepoUnitConfig(ctx, actionsUnit))
			}
			before, err := json.Marshal(initial)
			require.NoError(t, err)
			owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "web"}
			namespace, err := governance_model.GetNamespace(ctx, repo.OwnerID)
			require.NoError(t, err)
			require.NoError(t, governance_service.SetRepositoryMember(ctx, owner, repo.ID, governance_service.GroupMemberOption{UserID: 4, Role: governance_model.Maintainer, Revision: namespace.Revision}, false))
			request, response := contexttest.MockContext(t, "POST /user2/repo1/settings/actions/general")
			contexttest.LoadUser(t, request, 4)
			contexttest.LoadRepo(t, request, repo.ID)
			require.True(t, request.Repo.Permission.IsAdmin())
			request.Req.Form.Set("override_owner_config", "true")
			request.Req.Form.Set("token_permission_mode", "permissive")
			request.Req.Form.Set("enable_max_permissions", "true")
			request.Req.Form.Set("max_unit_access_mode_1", "write")
			request.Req.Form.Set("collaborative_owner", "user1")
			request.Req.Form.Set("id", "5")
			namespace, err = governance_model.GetNamespace(ctx, repo.OwnerID)
			require.NoError(t, err)
			require.NoError(t, governance_service.SetRepositoryMember(ctx, owner, repo.ID, governance_service.GroupMemberOption{UserID: 4, Revision: namespace.Revision}, true))
			operation.handler(request)
			require.Equal(t, http.StatusForbidden, response.Code)
			stored := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{ID: actionsUnit.ID})
			after, err := json.Marshal(stored.ActionsConfig())
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))

			legal, legalResponse := contexttest.MockContext(t, "POST /user2/repo1/settings/actions/general")
			contexttest.LoadUser(t, legal, owner.ID)
			contexttest.LoadRepo(t, legal, repo.ID)
			legal.Req.Form = request.Req.Form
			operation.handler(legal)
			require.Equal(t, operation.status, legalResponse.Code)
			stored = unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{ID: actionsUnit.ID})
			after, err = json.Marshal(stored.ActionsConfig())
			require.NoError(t, err)
			require.NotEqual(t, string(before), string(after))
		})
	}
}
