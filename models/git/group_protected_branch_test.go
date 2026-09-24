// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package git

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupProtectedBranchWildcardCrossesPath(t *testing.T) {
	require.True(t, ValidGroupProtectionPattern("*gitlab*"))
	rule := &GroupProtectedBranch{RuleName: "*gitlab*"}
	for _, branch := range []string{"gitlab", "gitlab/staging", "master/gitlab/production"} {
		require.True(t, rule.Match(branch), branch)
	}
	require.False(t, rule.Match("master/GitLab/production"))
}
