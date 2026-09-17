// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestPrepareGitReferenceTransactionForRepositoryWithoutPulls(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	repo := &repo_model.Repository{OwnerID: 1, OwnerName: "user1", OwnerNamespace: "user1", Name: "reference-no-pulls", LowerName: "reference-no-pulls"}
	require.NoError(t, db.Insert(ctx, repo))
	operation := &governance_model.ReferenceTransaction{RepoID: repo.ID, WriterID: "test-writer", Actor: governance_model.Actor{ID: 1, Name: "user1", Kind: "user", Transport: "git_http"}, Changes: []governance_model.ReferenceChange{{Ref: "refs/heads/feature", Old: "0000000000000000000000000000000000000000", New: "517023983a7d8e3504479c2ded96e3a9146358c1"}}}
	require.NoError(t, PrepareGitReferenceTransaction(ctx, operation))
	require.NotEmpty(t, operation.ID)
}
