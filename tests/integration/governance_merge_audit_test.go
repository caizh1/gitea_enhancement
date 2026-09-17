// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/git"
	"gitea.dev/services/forms"

	"github.com/stretchr/testify/require"
)

func TestGovernanceNativeMergePrecheckAudit(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, _ *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		pr, err := issues_model.GetPullRequestByIndex(t.Context(), 1, 3)
		require.NoError(t, err)
		repository, err := repo_model.GetRepositoryByID(t.Context(), 1)
		require.NoError(t, err)
		before, err := git.GetFullCommitID(t.Context(), repository.RepoPath(), "refs/heads/"+pr.BaseBranch)
		require.NoError(t, err)
		_, err = db.GetEngine(t.Context()).ID(pr.ID).Cols("status").Update(&issues_model.PullRequest{Status: issues_model.PullRequestStatusChecking})
		require.NoError(t, err)
		request := NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo1/pulls/3/merge", forms.MergePullRequestForm{Do: "merge"}).AddTokenAuth(getUserToken(t, "user1", auth_model.AccessTokenScopeAll))
		request.RemoteAddr = "192.0.2.5:45000"
		MakeRequest(t, request, http.StatusMethodNotAllowed)
		var events []governance_model.AuditEvent
		require.NoError(t, db.GetEngine(t.Context()).Where("type = ? AND object_id = ?", "merge.denied", pr.ID).Find(&events))
		require.Len(t, events, 1)
		require.Equal(t, "user1", events[0].Actor.Name)
		require.Equal(t, "api", events[0].Actor.Transport)
		require.Equal(t, "192.0.2.5", events[0].Actor.IP)
		require.Contains(t, string(events[0].Details), `"stage":"native_precheck"`)
		require.Contains(t, string(events[0].Details), "原生冲突检查尚未完成或未通过")
		after, err := git.GetFullCommitID(t.Context(), repository.RepoPath(), "refs/heads/"+pr.BaseBranch)
		require.NoError(t, err)
		require.Equal(t, before, after, "原生预检查拒绝不改变真实目标引用")
	})
}
