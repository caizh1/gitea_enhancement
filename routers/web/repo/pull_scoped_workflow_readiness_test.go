// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScopedWorkflowReadinessKey(t *testing.T) {
	for _, sample := range []struct {
		name, message, key string
	}{
		{"缺少当前配置运行", "scoped workflow pr.yaml from source 3 needs a run for commit abc at configuration revision 2", "missing_run"},
		{"来源提交已更新", "scoped workflow pr.yaml from source 3 needs a run at current source commit def", "stale_source"},
		{"等待运行", "not ready to merge: required scoped run is pending", "pending"},
		{"运行失败", "not ready to merge: required scoped run failed", "failed"},
		{"运行取消", "not ready to merge: required scoped run was cancelled", "cancelled"},
		{"Runner 不可信", "scoped workflow pr.yaml needs a successful job on a runner managed by its required source scope", "runner"},
		{"必选任务失败", "scoped workflow pr.yaml from source 3 needs a successful run for commit abc", "job"},
		{"配置异常", "scoped workflow pr.yaml has an invalid required pattern", "configuration"},
		{"来源不可用", "scoped source 3 needs a verified reference hook", "source"},
		{"未知错误", "database temporarily unavailable", "unavailable"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			assert.Equal(t, "repo.pulls.required_scoped_workflow."+sample.key, scopedWorkflowReadinessKey(sample.message))
		})
	}
}
