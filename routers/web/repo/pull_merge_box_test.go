// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package repo

import (
	"testing"

	governance_model "gitea.dev/models/governance"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/assert"
)

func TestApprovalStateBlocksImmediateMerge(t *testing.T) {
	assert.True(t, approvalStateBlocksMerge(true, nil, "状态不可用"))
	assert.True(t, approvalStateBlocksMerge(true, &governance_service.PullApprovalResult{}, ""))
	assert.True(t, approvalStateBlocksMerge(true, &governance_service.PullApprovalResult{VersionReady: true, State: governance_model.ApprovalState{Satisfied: false}}, ""))
	assert.False(t, approvalStateBlocksMerge(true, &governance_service.PullApprovalResult{VersionReady: true, State: governance_model.ApprovalState{Satisfied: true}}, ""))
	assert.False(t, approvalStateBlocksMerge(false, &governance_service.PullApprovalResult{}, ""), "仅可选或不匹配分支的规则不能阻止原生合并")
}
