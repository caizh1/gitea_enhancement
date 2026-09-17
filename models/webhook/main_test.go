// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"testing"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m, &unittest.TestOptions{
		FixtureFiles: []string{
			"user.yml",
			"repository.yml",
			"governance_write_lock.yml",
			"webhook.yml",
			"hook_task.yml",
		},
		SetUp: func() error {
			if err := prepareWebhookTestData(); err != nil {
				return err
			}
			return governance_model.InitializeLegacyNamespaces(context.Background())
		},
	})
}
