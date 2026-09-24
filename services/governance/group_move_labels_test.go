// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestGroupMovePreservesAncestorLabelHistory(t *testing.T) {
	for _, mode := range []string{"current", "history", "internal"} {
		t.Run(mode, func(t *testing.T) {
			unittest.PrepareTestEnv(t)
			ctx := t.Context()
			require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
			actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
			root, err := CreateGroup(ctx, actor, GroupOption{Path: "label-root", Visibility: 2})
			require.NoError(t, err)
			child, err := CreateGroup(ctx, actor, GroupOption{Path: "child", ParentID: root.ID, Visibility: 2})
			require.NoError(t, err)
			target, err := CreateGroup(ctx, actor, GroupOption{Path: "target", Visibility: 2})
			require.NoError(t, err)
			repo := &repo_model.Repository{OwnerID: child.ID, OwnerName: child.InternalName, OwnerNamespace: child.FullPath, Name: "work", LowerName: "work", IsPrivate: true}
			require.NoError(t, db.Insert(ctx, repo))
			_, err = governance_model.RegisterNativeRepository(ctx, repo.ID, child.ID, repo.Name)
			require.NoError(t, err)
			issue := &issues_model.Issue{RepoID: repo.ID, PosterID: 2, Index: 1, Title: "保留标签历史"}
			require.NoError(t, db.Insert(ctx, issue))
			label := &issues_model.Label{OrgID: root.ID, Name: "ancestor", Color: "000000"}
			if mode == "internal" {
				label.OrgID = child.ID
			}
			require.NoError(t, db.Insert(ctx, label))
			comment := &issues_model.Comment{IssueID: issue.ID, LabelID: label.ID, PosterID: 2, Type: issues_model.CommentTypeLabel, Content: "1"}
			require.NoError(t, db.Insert(ctx, comment))
			if mode != "history" {
				require.NoError(t, db.Insert(ctx, &issues_model.IssueLabel{IssueID: issue.ID, LabelID: label.ID}))
			}
			moved, err := MoveGroup(ctx, actor, child.ID, GroupOption{Path: "child", ParentID: target.ID, Revision: child.Revision})
			if mode == "internal" {
				require.NoError(t, err, "随子树移动的标签来源仍然可用")
				require.Equal(t, target.ID, moved.ParentID)
			} else {
				require.ErrorIs(t, err, governance_model.ErrConflict)
				unchanged, readErr := governance_model.GetNamespace(ctx, child.ID)
				require.NoError(t, readErr)
				require.Equal(t, root.ID, unchanged.ParentID)
				require.Equal(t, child.Revision, unchanged.Revision)
				require.Zero(t, unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID}).ActionsScopeRevision, "失败时 Actions 失效变更一起回滚")
			}
			unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID})
			if mode != "history" {
				unittest.AssertExistsAndLoadBean(t, &issues_model.IssueLabel{IssueID: issue.ID, LabelID: label.ID})
			}
		})
	}
}
