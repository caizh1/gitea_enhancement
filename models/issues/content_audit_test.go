// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package issues_test

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/require"
)

func TestIssueAndCommentAuditExcludeContent(t *testing.T) {
	unittest.PrepareTestEnv(t)
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	require.NoError(t, issue.LoadRepo(t.Context()))
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	ctx := governance_model.WithAuditActor(t.Context(), governance_model.Actor{ID: doer.ID, Name: doer.Name, Kind: "user", Transport: "api"})

	oldTitle := issue.Title
	issue.Title = "审计后的公开标题"
	require.NoError(t, issues_model.ChangeIssueTitle(ctx, issue, doer, oldTitle))
	comment, err := issues_model.CreateComment(ctx, &issues_model.CreateCommentOptions{Type: issues_model.CommentTypeComment, Doer: doer, Repo: issue.Repo, Issue: issue, Content: "绝不能进入审计的评论秘密"})
	require.NoError(t, err)
	comment.Content = "修改后仍不能进入审计的正文"
	require.NoError(t, issues_model.UpdateComment(ctx, comment, comment.ContentVersion, doer))
	require.NoError(t, issues_model.DeleteComment(ctx, comment))
	require.ErrorIs(t, issues_model.DeleteComment(ctx, comment), issues_model.ErrCommentNotExist{ID: comment.ID, IssueID: comment.IssueID})
	_, err = issues_model.CloseIssue(ctx, issue, doer)
	require.NoError(t, err)
	_, err = issues_model.ReopenIssue(ctx, issue, doer)
	require.NoError(t, err)

	var events []governance_model.AuditEvent
	require.NoError(t, db.GetEngine(ctx).In("type", []string{"issue.updated", "issue.closed", "issue.reopened", "comment.created", "comment.updated", "comment.deleted"}).OrderBy("id").Find(&events))
	require.Len(t, events, 6)
	for _, event := range events {
		require.Equal(t, issue.RepoID, event.ScopeID)
		require.NotContains(t, string(event.Details), "绝不能进入审计")
		require.NotContains(t, string(event.Details), "修改后仍不能进入审计")
	}
}

func TestIssueAuditFailureRollsBackBusinessChange(t *testing.T) {
	unittest.PrepareTestEnv(t)
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	originalTitle := issue.Title
	issue.Title = "不应提交的标题"

	definition := governance_model.EventCatalog["issue.updated"]
	delete(governance_model.EventCatalog, "issue.updated")
	t.Cleanup(func() { governance_model.EventCatalog["issue.updated"] = definition })
	// 注入未登记事件故障，业务更新必须随同 AppendAudit 失败回滚。
	require.Error(t, issues_model.ChangeIssueTitle(t.Context(), issue, doer, originalTitle))
	reloaded := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issue.ID})
	require.Equal(t, originalTitle, reloaded.Title)
}
