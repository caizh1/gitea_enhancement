// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package issue_test

import (
	"testing"

	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	issue_service "gitea.dev/services/issue"

	"github.com/stretchr/testify/require"
)

func TestGovernanceReviewRequestLoadsRepository(t *testing.T) {
	unittest.PrepareTestEnv(t)
	// 直接按 ID 读取，不预填网页上下文中的 Issue.Repo，覆盖规则 API 的同步路径。
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})
	require.Nil(t, issue.Repo)
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	reviewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 8})

	require.NoError(t, issue_service.GovernanceReviewRequest(t.Context(), issue, doer, reviewer))
	require.NotNil(t, issue.Repo)
}
