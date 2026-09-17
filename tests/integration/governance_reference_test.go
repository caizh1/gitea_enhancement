// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/git"
	"gitea.dev/modules/git/gitcmd"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	governance_api "gitea.dev/routers/api/v1/governance"
	"gitea.dev/services/forms"
	governance_service "gitea.dev/services/governance"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGovernanceReferenceTransactionRealGit(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		resp := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/user/repos", api.CreateRepoOption{Name: "reference-journal", Private: true, AutoInit: true}).AddTokenAuth(token), http.StatusCreated)
		created := DecodeJSON(t, resp, &api.Repository{})
		repo, err := repo_model.GetRepositoryByID(t.Context(), created.ID)
		require.NoError(t, err)
		installed, err := gitrepo.ReferenceTransactionHookInstalled(repo)
		require.NoError(t, err)
		require.True(t, installed, "新建代码仓库应自动接入引用事务入口")
		cloneURL := *baseURL
		cloneURL.User, cloneURL.Path = url.UserPassword("user2", "password"), "/user2/reference-journal.git"
		local := filepath.Join(t.TempDir(), "引用事务仓库")
		doGitClone(local, &cloneURL)(t)
		old, err := git.GetFullCommitID(t.Context(), local, "HEAD")
		require.NoError(t, err)
		doGitCheckoutWriteFileCommit(localGitAddCommitOptions{LocalRepoPath: local, CheckoutBranch: created.DefaultBranch, TreeFilePath: "审批版本.txt", TreeFileContent: "第二个差异版本\n"})(t)
		next, err := git.GetFullCommitID(t.Context(), local, "HEAD")
		require.NoError(t, err)
		doGitPushTestRepository(local, "origin", created.DefaultBranch)(t)
		var transactions []*governance_model.ReferenceTransaction
		require.NoError(t, db.GetEngine(t.Context()).Where("repo_id = ?", repo.ID).Find(&transactions))
		require.Len(t, transactions, 1)
		assert.Equal(t, "committed", transactions[0].State)
		require.Len(t, transactions[0].Changes, 1)
		assert.Equal(t, old, transactions[0].Changes[0].Old)
		assert.Equal(t, next, transactions[0].Changes[0].New)
		firstID := transactions[0].ID
		doGitPushTestRepository(local, "--force", "origin", old+":refs/heads/"+created.DefaultBranch)(t)
		transactions = nil
		require.NoError(t, db.GetEngine(t.Context()).Where("repo_id = ?", repo.ID).Find(&transactions))
		require.Len(t, transactions, 2, "A→B→A 的两次实际引用更新均留下独立记录")
		var restored bool
		for _, operation := range transactions {
			assert.Equal(t, "committed", operation.State)
			if operation.ID != firstID {
				restored = operation.Changes[0].Old == next && operation.Changes[0].New == old
			}
		}
		assert.True(t, restored)
		count, err := db.GetEngine(t.Context()).Where("resource = ?", governance_model.Resource("repository", repo.ID)).Count(new(governance_model.ReferenceReservation))
		require.NoError(t, err)
		assert.Zero(t, count, "真实结果核对后释放占用")
		hookPath := filepath.Join(repo.RepoPath(), "hooks", "reference-transaction")
		original, err := os.ReadFile(hookPath)
		require.NoError(t, err)
		interrupted := "#!/bin/sh\nif [ \"$1\" = aborted ]; then exit 0; fi\n" + strings.Replace(string(original), "\nexec ", "\n", 1) + "\nif [ \"$1\" = prepared ]; then exit 42; fi\n"
		require.NoError(t, os.WriteFile(hookPath, []byte(interrupted), 0o755))
		t.Cleanup(func() { _ = os.WriteFile(hookPath, original, 0o755) })
		doGitPushTestRepositoryFail(local, "origin", created.DefaultBranch)(t)
		gitRepo, err := gitrepo.OpenRepository(t.Context(), repo)
		require.NoError(t, err)
		actual, err := gitRepo.GetRefCommitID("refs/heads/" + created.DefaultBranch)
		gitRepo.Close()
		require.NoError(t, err)
		assert.Equal(t, old, actual, "prepared 回调被拒绝时真实 Git 引用不能变化")
		var pending governance_model.ReferenceTransaction
		found, err := db.GetEngine(t.Context()).Where("repo_id = ? AND state = ?", repo.ID, "prepared").Get(&pending)
		require.NoError(t, err)
		require.True(t, found, "落库后未取得提交回执的记录必须保留")
		require.NoError(t, os.WriteFile(hookPath, original, 0o755))
		_, _, pushErr := gitcmd.NewCommand("push").AddDynamicArguments("origin", created.DefaultBranch).WithDir(local).RunStdString(t.Context())
		require.Error(t, pushErr, "待核对引用必须拒绝下一次写入")
		gitRepo, err = gitrepo.OpenRepository(t.Context(), repo)
		require.NoError(t, err)
		actual, err = gitRepo.GetRefCommitID("refs/heads/" + created.DefaultBranch)
		gitRepo.Close()
		require.NoError(t, err)
		require.Equal(t, old, actual)
		state, err := governance_service.RecoverGovernanceOperation(t.Context(), "reference", pending.ID, governance_model.Actor{Kind: "system", Name: "隔离恢复验收", Transport: "cli"})
		require.NoError(t, err)
		assert.Equal(t, "aborted", state, "原 Git 进程已退出，依据实际引用恢复失败状态")
		doGitPushTestRepository(local, "origin", created.DefaultBranch)(t)
		// 实际写入成功但回执丢失时，恢复必须确认成功，不能重复推送。
		lostReceipt := "#!/bin/sh\nif [ \"$1\" = committed ]; then exit 0; fi\n" + string(original)
		require.NoError(t, os.WriteFile(hookPath, []byte(lostReceipt), 0o755))
		doGitCheckoutWriteFileCommit(localGitAddCommitOptions{LocalRepoPath: local, CheckoutBranch: created.DefaultBranch, TreeFilePath: "回执丢失.txt", TreeFileContent: "实际成功，回执丢失\n"})(t)
		doGitPushTestRepository(local, "origin", created.DefaultBranch)(t)
		pending = governance_model.ReferenceTransaction{}
		found, err = db.GetEngine(t.Context()).Where("repo_id = ? AND state = ?", repo.ID, "prepared").Get(&pending)
		require.NoError(t, err)
		require.True(t, found)
		state, err = governance_service.RecoverGovernanceOperation(t.Context(), "reference", pending.ID, governance_model.Actor{Kind: "system", Name: "隔离恢复验收", Transport: "cli"})
		require.NoError(t, err)
		require.Equal(t, "committed", state)
		gitRepo, err = gitrepo.OpenRepository(t.Context(), repo)
		require.NoError(t, err)
		actual, err = gitRepo.GetRefCommitID("refs/heads/" + created.DefaultBranch)
		gitRepo.Close()
		require.NoError(t, err)
		require.Equal(t, pending.Changes[0].New, actual)
		state, err = governance_service.RecoverGovernanceOperation(t.Context(), "reference", pending.ID, governance_model.Actor{Kind: "system", Name: "重复核对验收", Transport: "cli"})
		require.NoError(t, err)
		require.Equal(t, "committed", state)
	})
}

func TestGovernanceReferencePullGenerationsRealGit(t *testing.T) {
	testGovernanceReferencePullMerge(t, repo_model.MergeStyleMerge, governanceMergeTestOptions{})
}

func TestGovernanceFinalAuthorizationMergeStyles(t *testing.T) {
	for _, style := range []repo_model.MergeStyle{repo_model.MergeStyleRebase, repo_model.MergeStyleRebaseMerge, repo_model.MergeStyleSquash, repo_model.MergeStyleFastForwardOnly} {
		t.Run(string(style), func(t *testing.T) { testGovernanceReferencePullMerge(t, style, governanceMergeTestOptions{}) })
	}
}

func TestGovernanceRecoveryRejectsPausedMerge(t *testing.T) {
	testGovernanceReferencePullMerge(t, repo_model.MergeStyleMerge, governanceMergeTestOptions{recoverDuringPause: true})
}

func TestGovernanceMergeRepairsLostPostReceive(t *testing.T) {
	testGovernanceReferencePullMerge(t, repo_model.MergeStyleMerge, governanceMergeTestOptions{losePostReceive: true})
}

func TestGovernanceInstancePolicyRealGit(t *testing.T) {
	testGovernanceReferencePullMerge(t, repo_model.MergeStyleMerge, governanceMergeTestOptions{instancePolicy: true})
}

type governanceMergeTestOptions struct {
	approvalGate       bool
	recoverDuringPause bool
	losePostReceive    bool
	instancePolicy     bool
	preventCommitter   bool
	reauthenticate     bool
}

func TestGovernanceProtectedBranchDirectPushRealGit(t *testing.T) {
	testGovernanceReferencePullMerge(t, repo_model.MergeStyleMerge, governanceMergeTestOptions{approvalGate: true})
}

func TestGovernanceCommitterApprovalRealGit(t *testing.T) {
	testGovernanceReferencePullMerge(t, repo_model.MergeStyleMerge, governanceMergeTestOptions{preventCommitter: true})
}

func TestGovernanceReauthenticationRealGit(t *testing.T) {
	testGovernanceReferencePullMerge(t, repo_model.MergeStyleMerge, governanceMergeTestOptions{reauthenticate: true})
}

func testGovernanceReferencePullMerge(t *testing.T, style repo_model.MergeStyle, options governanceMergeTestOptions) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		token := getUserToken(t, "user2", auth_model.AccessTokenScopeAll)
		resp := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/user/repos", api.CreateRepoOption{Name: "reference-pull", Private: true, AutoInit: true}).AddTokenAuth(token), http.StatusCreated)
		created := DecodeJSON(t, resp, &api.Repository{})
		repo, err := repo_model.GetRepositoryByID(t.Context(), created.ID)
		require.NoError(t, err)
		cloneURL := *baseURL
		cloneURL.User, cloneURL.Path = url.UserPassword("user2", "password"), "/user2/reference-pull.git"
		ruleEndpoint := fmt.Sprintf("/api/v1/governance/repositories/%d/approval-rules", repo.ID)
		ruleToken := token
		if options.instancePolicy {
			ruleEndpoint = "/api/v1/governance/approval-policies/instance/0"
			ruleToken = getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
		}
		ruleOption := governance_api.ApprovalRuleOption{Rule: governance_model.ApprovalRule{Name: "管理员本人必批", Required: 1, UserIDs: []int64{1}, BranchMode: "all", Enabled: true}}
		MakeRequest(t, NewRequestWithJSON(t, "POST", ruleEndpoint, ruleOption).AddTokenAuth(ruleToken), http.StatusCreated)

		local := filepath.Join(t.TempDir(), "审批版本仓库")
		doGitClone(local, &cloneURL)(t)
		doGitPushTestRepository(local, "origin", "HEAD:refs/heads/alternate")(t)
		doGitCreateBranch(local, "feature")(t)
		doGitCheckoutWriteFileCommit(localGitAddCommitOptions{LocalRepoPath: local, CheckoutBranch: "feature", TreeFilePath: "审批.txt", TreeFileContent: "版本甲\n"})(t)
		if options.preventCommitter {
			require.NoError(t, gitcmd.NewCommand("-c", "user.name=user1", "-c", "user.email=user1@example.com", "commit", "--amend", "--no-edit").WithDir(local).RunWithStderr(t.Context()))
		}
		doGitPushTestRepository(local, "origin", "feature")(t)
		first, err := git.GetFullCommitID(t.Context(), local, "HEAD")
		require.NoError(t, err)
		resp = MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/reference-pull/pulls", api.CreatePullRequestOption{Title: "引用事务审批版本", Head: "feature", Base: created.DefaultBranch}).AddTokenAuth(token), http.StatusCreated)
		pull := DecodeJSON(t, resp, &api.PullRequest{})
		nativePull, err := issues_model.GetPullRequestByIndex(t.Context(), repo.ID, pull.Index)
		require.NoError(t, err)
		protectionEndpoint := "/api/v1/repos/user2/reference-pull/branch_protections"
		protection := api.CreateBranchProtectionOption{RuleName: "alternate", EnablePush: true, EnableForcePush: true, EnableBypassAllowlist: true, BypassAllowlistUsernames: []string{"user2", "user1"}, RequireGovernanceApproval: true}
		if options.approvalGate {
			// 新仓库已自动安装；明确模拟入口丢失，保留原有拒绝验收。
			require.NoError(t, os.Remove(filepath.Join(repo.RepoPath(), "hooks", "reference-transaction")))
			MakeRequest(t, NewRequestWithJSON(t, "POST", protectionEndpoint, protection).AddTokenAuth(token), http.StatusConflict)
			command := exec.CommandContext(t.Context(), setting.AppPath, "admin", "governance", "install-reference-hook", "--repository-id", strconv.FormatInt(repo.ID, 10), "--config", setting.CustomConf)
			output, err := command.CombinedOutput()
			require.NoError(t, err, "接入命令执行失败：%s", output)
			require.Contains(t, string(output), "引用事务入口已核对")
		} else {
			require.NoError(t, gitrepo.InstallReferenceTransactionHook(t.Context(), repo))
		}
		if options.approvalGate {
			response := MakeRequest(t, NewRequestWithJSON(t, "POST", protectionEndpoint, protection).AddTokenAuth(token), http.StatusCreated)
			require.True(t, DecodeJSON(t, response, &api.BranchProtection{}).RequireGovernanceApproval)
			before, err := git.GetFullCommitID(t.Context(), repo.RepoPath(), "refs/heads/alternate")
			require.NoError(t, err)
			for _, name := range []string{"user2", "user1"} {
				pushURL := cloneURL
				pushURL.User = url.UserPassword(name, "password")
				for _, spec := range []string{"feature:refs/heads/alternate", ":refs/heads/alternate"} {
					pushErr := gitcmd.NewCommand("push", "--force").AddDynamicArguments(pushURL.String(), spec).WithDir(local).RunWithStderr(t.Context())
					require.Error(t, pushErr, "管理员、绕过名单和强推均不能绕过最终审批授权")
					after, err := git.GetFullCommitID(t.Context(), repo.RepoPath(), "refs/heads/alternate")
					require.NoError(t, err)
					require.Equal(t, before, after)
				}
			}
			hookPath := filepath.Join(repo.RepoPath(), "hooks", "reference-transaction")
			hookBody, err := os.ReadFile(hookPath)
			require.NoError(t, err)
			require.NoError(t, os.Remove(hookPath))
			t.Cleanup(func() { _ = os.WriteFile(hookPath, hookBody, 0o755) })
			pushErr := gitcmd.NewCommand("push", "--force").AddDynamicArguments(cloneURL.String(), "feature:refs/heads/alternate").WithDir(local).RunWithStderr(t.Context())
			require.Error(t, pushErr, "引用事务入口丢失后普通接收入口仍须拒绝直接写入")
			after, err := git.GetFullCommitID(t.Context(), repo.RepoPath(), "refs/heads/alternate")
			require.NoError(t, err)
			require.Equal(t, before, after)
			require.NoError(t, os.WriteFile(hookPath, hookBody, 0o755))
			var denials []governance_model.AuditEvent
			require.NoError(t, db.GetEngine(t.Context()).Where("type = ? AND object_id = ?", "git.push_denied", repo.ID).Find(&denials))
			require.Len(t, denials, 5, "每次真实拒绝均须留下审计")
			for _, event := range denials {
				require.Equal(t, "denied", event.Result)
				require.Equal(t, "git_http", event.Actor.Transport)
				require.Equal(t, "127.0.0.1", event.Actor.IP)
				require.Contains(t, []string{"user1", "user2"}, event.Actor.Name)
			}
			withKeyFile(t, "治理验收密钥", func(keyFile string) {
				keyContext := NewAPITestContext(t, "user2", "reference-pull", auth_model.AccessTokenScopeAll)
				doAPICreateUserKey(keyContext, "治理拒绝审计验收", keyFile)(t)
				sshURL := createSSHUrl("user2/reference-pull.git", baseURL)
				pushErr := gitcmd.NewCommand("push", "--force").AddDynamicArguments(sshURL.String(), "feature:refs/heads/alternate").WithDir(local).RunWithStderr(t.Context())
				require.Error(t, pushErr, "SSH 接收入口必须执行相同门禁")
				after, err := git.GetFullCommitID(t.Context(), repo.RepoPath(), "refs/heads/alternate")
				require.NoError(t, err)
				require.Equal(t, before, after)
				var event governance_model.AuditEvent
				found, err := db.GetEngine(t.Context()).Where("type = ? AND object_id = ?", "git.push_denied", repo.ID).Desc("id").Get(&event)
				require.NoError(t, err)
				require.True(t, found)
				require.Equal(t, "git_ssh", event.Actor.Transport)
				require.Equal(t, "127.0.0.1", event.Actor.IP)
				require.Equal(t, "user2", event.Actor.Name)
			})
		}
		approvalPassword := ""
		if options.reauthenticate {
			settings := governance_api.ApprovalSettingsOption{PreventAuthor: true, ResetOnChange: true, RequireReauthentication: true}
			MakeRequest(t, NewRequestWithJSON(t, "PUT", fmt.Sprintf("/api/v1/governance/repositories/%d/approval-settings", repo.ID), settings).AddTokenAuth(token), http.StatusOK)
			approvalPassword = "password"
		}
		approverToken := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
		endpoint := fmt.Sprintf("/api/v1/repos/user2/reference-pull/pulls/%d/reviews", pull.Index)
		if options.preventCommitter {
			settingsPath := fmt.Sprintf("/api/v1/governance/repositories/%d/approval-settings", repo.ID)
			settings := governance_api.ApprovalSettingsOption{PreventAuthor: true, PreventCommitter: true, ResetOnChange: true}
			response := MakeRequest(t, NewRequestWithJSON(t, "PUT", settingsPath, settings).AddTokenAuth(token), http.StatusOK)
			saved := DecodeJSON(t, response, &governance_model.ApprovalSettings{})
			MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, api.CreatePullReviewOptions{Event: api.ReviewStateApproved, CommitID: first}).AddTokenAuth(approverToken), http.StatusForbidden)
			page := loginUser(t, "user1").MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/user2/reference-pull/pulls/%d/files", pull.Index)), http.StatusOK)
			button := NewHTMLParser(t, page.Body).Find(`button[name="type"][value="approve"]`)
			require.Equal(t, 1, button.Length())
			_, disabled := button.Attr("disabled")
			require.True(t, disabled, "被禁止的提交者不能使用原生批准按钮")
			settings.Revision, settings.PreventCommitter = saved.Revision, false
			MakeRequest(t, NewRequestWithJSON(t, "PUT", settingsPath, settings).AddTokenAuth(token), http.StatusOK)
		}
		if options.reauthenticate {
			MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, api.CreatePullReviewOptions{Event: api.ReviewStateApproved, CommitID: first}).AddTokenAuth(approverToken), http.StatusForbidden)
			MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, api.CreatePullReviewOptions{Event: api.ReviewStateApproved, CommitID: first, ApprovalPassword: "错误验收口令"}).AddTokenAuth(approverToken), http.StatusForbidden)
			count, err := db.GetEngine(t.Context()).Where("issue_id = ? AND type = ?", nativePull.IssueID, issues_model.ReviewTypeApprove).Count(new(issues_model.Review))
			require.NoError(t, err)
			require.Zero(t, count, "没有完成再次认证时不能产生有效批准")
			page := loginUser(t, "user1").MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/user2/reference-pull/pulls/%d/files", pull.Index)), http.StatusOK)
			require.Contains(t, page.Body.String(), `name="approval_password"`)
		}
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, api.CreatePullReviewOptions{Event: api.ReviewStateApproved, CommitID: first, ApprovalPassword: approvalPassword}).AddTokenAuth(approverToken), http.StatusOK)
		initialVersion, initialExists, initialErr := db.GetByID[governance_model.PullVersion](t.Context(), nativePull.ID)
		require.NoError(t, initialErr)
		require.True(t, initialExists)
		require.EqualValues(t, 1, initialVersion.Generation, "首次原生批准初始化代次")
		var approvalEvent governance_model.AuditEvent
		found, err := db.GetEngine(t.Context()).Where("scope_type = ? AND scope_id = ? AND type = ?", "repository", repo.ID, "approval.approved").Desc("id").Get(&approvalEvent)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "user1", approvalEvent.Actor.Name)
		require.Equal(t, "api", approvalEvent.Actor.Transport)
		require.Contains(t, string(approvalEvent.Details), "approval_generation")
		if options.reauthenticate {
			require.Contains(t, string(approvalEvent.Details), `"reauthenticated":true`, "批准审计直接保留再次认证的真实结果")
			count, err := db.GetEngine(t.Context()).Where("request_id = ? AND type = ? AND result = ?", approvalEvent.RequestID, "approval.reauthenticated", "success").Count(new(governance_model.AuditEvent))
			require.NoError(t, err)
			require.EqualValues(t, 1, count, "认证和批准使用同一个真实请求关联号")
		}
		doGitCheckoutWriteFileCommit(localGitAddCommitOptions{LocalRepoPath: local, CheckoutBranch: "feature", TreeFilePath: "审批.txt", TreeFileContent: "版本乙\n"})(t)
		doGitPushTestRepository(local, "origin", "feature")(t)
		version, has, err := db.GetByID[governance_model.PullVersion](t.Context(), nativePull.ID)
		require.NoError(t, err)
		require.True(t, has)
		require.EqualValues(t, 2, version.Generation)
		MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, api.CreatePullReviewOptions{Event: api.ReviewStateApproved, CommitID: version.Head, ApprovalPassword: approvalPassword}).AddTokenAuth(approverToken), http.StatusOK)
		evidence, err := issues_model.FindGovernanceApprovalEvidence(t.Context(), nativePull.ID)
		require.NoError(t, err)
		require.Len(t, evidence, 1)
		require.Equal(t, options.reauthenticate, evidence[0].Reauthenticated)
		require.EqualValues(t, 2, evidence[0].Generation)
		stateEndpoint := fmt.Sprintf("/api/v1/governance/pulls/%d/approval-state", nativePull.ID)
		stateResponse := MakeRequest(t, NewRequest(t, "GET", stateEndpoint).AddTokenAuth(approverToken), http.StatusOK)
		approvalResult := DecodeJSON(t, stateResponse, &governance_service.PullApprovalResult{})
		require.True(t, approvalResult.VersionReady)
		require.True(t, approvalResult.State.Satisfied)
		require.Len(t, approvalResult.State.Rules, 1)

		doGitPushTestRepository(local, "--force", "origin", first+":refs/heads/feature")(t)
		version, has, err = db.GetByID[governance_model.PullVersion](t.Context(), nativePull.ID)
		require.NoError(t, err)
		require.True(t, has)
		require.Equal(t, first, version.Head)
		require.EqualValues(t, 3, version.Generation, "真实 PR 回到原代码后，旧批准代次仍然失效")
		evidence, err = issues_model.FindGovernanceApprovalEvidence(t.Context(), nativePull.ID)
		require.NoError(t, err)
		require.Len(t, evidence, 1)
		require.EqualValues(t, 2, evidence[0].Generation, "原生批准证据保持旧代次")
		rule := governance_model.RuleCandidates{Rule: governance_model.ApprovalRule{Name: "指定人必批", ScopeType: "repository", ScopeID: repo.ID, BranchMode: "all", Enabled: true, Required: 1}, UserIDs: []int64{1}}
		state, err := governance_model.CountApprovalRules(*version, []governance_model.RuleCandidates{rule}, evidence, false)
		require.NoError(t, err)
		require.False(t, state.Satisfied, "真实回退后不可把旧批准重新计票")
		stateResponse = MakeRequest(t, NewRequest(t, "GET", stateEndpoint).AddTokenAuth(approverToken), http.StatusOK)
		approvalResult = DecodeJSON(t, stateResponse, &governance_service.PullApprovalResult{})
		require.True(t, approvalResult.VersionReady)
		require.False(t, approvalResult.State.Satisfied)
		require.Equal(t, 1, approvalResult.State.Rules[0].Missing)

		MakeRequest(t, NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/user2/reference-pull/pulls/%d", pull.Index), api.EditPullRequestOption{Base: "alternate"}).AddTokenAuth(token), http.StatusCreated)
		version, has, err = db.GetByID[governance_model.PullVersion](t.Context(), nativePull.ID)
		require.NoError(t, err)
		require.True(t, has)
		require.Equal(t, "alternate", version.BaseBranch)
		require.EqualValues(t, 4, version.Generation, "即使差异相同，目标分支变化也必须失效")
		var invalidations []governance_model.AuditEvent
		require.NoError(t, db.GetEngine(t.Context()).Where("type = ? AND object_type = ? AND object_id = ?", "approval.invalidated", "pull", nativePull.ID).OrderBy("id").Find(&invalidations))
		require.Len(t, invalidations, 3, "两次真实代码变化和一次修改目标分别记录失效")
		for i, event := range invalidations {
			require.Equal(t, repo.ID, event.ScopeID)
			require.Equal(t, "user2", event.Actor.Name)
			if i < 2 {
				require.Equal(t, "git_http", event.Actor.Transport)
			} else {
				require.Equal(t, "api", event.Actor.Transport)
			}
		}
		mergeEndpoint := fmt.Sprintf("/api/v1/repos/user2/reference-pull/pulls/%d/merge", pull.Index)
		mergeOption := forms.MergePullRequestForm{Do: string(style), HeadCommitID: version.Head}
		baseGit, err := gitrepo.OpenRepository(t.Context(), repo)
		require.NoError(t, err)
		beforeMerge, err := baseGit.GetBranchCommitID("alternate")
		baseGit.Close()
		require.NoError(t, err)
		MakeRequest(t, NewRequestWithJSON(t, "POST", mergeEndpoint, mergeOption).AddTokenAuth(token), http.StatusForbidden)
		unchanged, err := git.GetFullCommitID(t.Context(), repo.RepoPath(), "refs/heads/alternate")
		require.NoError(t, err)
		require.Equal(t, beforeMerge, unchanged, "审批不足时目标引用必须保持不变")
		approvedResponse := MakeRequest(t, NewRequestWithJSON(t, "POST", endpoint, api.CreatePullReviewOptions{Event: api.ReviewStateApproved, CommitID: version.Head, ApprovalPassword: approvalPassword}).AddTokenAuth(approverToken), http.StatusOK)
		approvedReview := DecodeJSON(t, approvedResponse, &api.PullReview{})
		if options.losePostReceive {
			path := filepath.Join(repo.RepoPath(), "hooks", "post-receive")
			original, err := os.ReadFile(path)
			require.NoError(t, err)
			script := "#!/bin/sh\n# 隔离验收：模拟合并写入后丢失原生后处理\n[ -z \"$GITEA_MERGE_AUTHORIZATION_ID\" ] || exit 0\n" + string(original)
			require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
			defer os.WriteFile(path, original, 0o755)
		}
		ownerSession := loginUser(t, "user2")
		pauseDir := t.TempDir()
		entered, resume := filepath.Join(pauseDir, "已取得最终授权"), filepath.Join(pauseDir, "允许写入")
		pauseHook := filepath.Join(repo.RepoPath(), "hooks", "pre-receive.d", "governance-test-pause")
		require.NoError(t, os.MkdirAll(filepath.Dir(pauseHook), 0o755))
		script := fmt.Sprintf("#!/bin/sh\n# 隔离验收：授权后暂停，验证撤回及写入不能越过授权\n[ -n \"$GITEA_MERGE_AUTHORIZATION_ID\" ] || exit 0\nprintf '已取得授权' > '%s'\ni=0\nwhile [ ! -f '%s' ]; do\n i=$((i+1))\n [ \"$i\" -lt 200 ] || exit 42\n sleep 0.05\ndone\n", entered, resume)
		require.NoError(t, os.WriteFile(pauseHook, []byte(script), 0o755))
		defer os.Remove(pauseHook)
		defer os.WriteFile(resume, []byte("清理释放"), 0o600)
		mergeRequest := NewRequestWithJSON(t, "POST", mergeEndpoint, mergeOption).AddTokenAuth(token)
		mergeDone := make(chan int, 1)
		go func() { mergeDone <- MakeRequest(t, mergeRequest, NoExpectedStatus).Code }()
		require.Eventually(t, func() bool { _, err := os.Stat(entered); return err == nil }, 10*time.Second, 10*time.Millisecond)
		MakeRequest(t, NewRequest(t, "DELETE", endpoint+"/"+strconv.FormatInt(approvedReview.ID, 10)).AddTokenAuth(approverToken), http.StatusConflict)
		MakeRequest(t, NewRequestWithJSON(t, "POST", ruleEndpoint, governance_api.ApprovalRuleOption{Rule: governance_model.ApprovalRule{Name: "授权后修改", Required: 0, BranchMode: "all", Enabled: true}}).AddTokenAuth(ruleToken), http.StatusConflict)
		archiveResponse := ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", "/user2/reference-pull/settings", map[string]string{"repo_name": "reference-pull", "action": "archive"}), http.StatusSeeOther)
		require.Equal(t, "/user2/reference-pull/settings", archiveResponse.Header().Get("Location"))
		archiveFlash := ownerSession.GetCookieFlashMessage()
		require.NotEmpty(t, archiveFlash.ErrorMsg, "网页必须展示归档失败")
		require.Empty(t, archiveFlash.SuccessMsg, "网页不能报告归档成功")
		currentRepository, err := repo_model.GetRepositoryByID(t.Context(), repo.ID)
		require.NoError(t, err)
		require.False(t, currentRepository.IsArchived, "原生网页归档不能越过最终合并授权")
		pushErr := gitcmd.NewCommand("push", "origin", "feature").WithDir(local).RunWithStderr(t.Context())
		require.Error(t, pushErr, "授权完成后源分支新代码不能越过占用")
		if options.recoverDuringPause {
			var pendingAuthorization governance_model.MergeAuthorization
			found, err := db.GetEngine(t.Context()).Where("pull_id = ? AND state = ?", nativePull.ID, "authorized").Get(&pendingAuthorization)
			require.NoError(t, err)
			require.True(t, found)
			state, err := governance_service.RecoverGovernanceOperation(t.Context(), "merge", pendingAuthorization.ID, governance_model.Actor{Kind: "system", Name: "隔离恢复验收", Transport: "cli"})
			require.NoError(t, err)
			require.Equal(t, "failed", state)
		}
		require.NoError(t, os.WriteFile(resume, []byte("并发检查完成"), 0o600))
		select {
		case status := <-mergeDone:
			if options.recoverDuringPause {
				require.Equal(t, http.StatusConflict, status, "恢复后原写入进程不能使用失效授权")
			} else {
				require.Equal(t, http.StatusOK, status)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("授权后合并未在限定时间内结束")
		}
		baseGit, err = gitrepo.OpenRepository(t.Context(), repo)
		require.NoError(t, err)
		mergedRef, err := baseGit.GetBranchCommitID("alternate")
		baseGit.Close()
		require.NoError(t, err)
		if options.recoverDuringPause {
			require.Equal(t, beforeMerge, mergedRef, "恢复后拒绝旧授权，真实引用保持原值")
		} else {
			require.NotEqual(t, beforeMerge, mergedRef)
		}
		var authorization governance_model.MergeAuthorization
		has, err = db.GetEngine(t.Context()).Where("pull_id = ?", nativePull.ID).Get(&authorization)
		require.NoError(t, err)
		require.True(t, has)
		if options.recoverDuringPause {
			require.Equal(t, "failed", authorization.State)
		} else {
			require.Equal(t, "succeeded", authorization.State)
			require.Equal(t, mergedRef, authorization.NewTarget)
		}
		require.NotEmpty(t, authorization.ApprovalProof)
		require.Equal(t, "user2", authorization.Actor.Name)
		require.Equal(t, "api", authorization.Actor.Transport)
		if authorization.State == "succeeded" {
			linked, err := db.GetEngine(t.Context()).Where("type = ? AND request_id = ? AND scope_id = ?", "git.reference_transaction", authorization.Actor.RequestID, repo.ID).Exist(new(governance_model.AuditEvent))
			require.NoError(t, err)
			require.True(t, linked, "API 合并与真实 Git 引用使用相同请求关联号")
		}
		var deniedMerge governance_model.AuditEvent
		found, err = db.GetEngine(t.Context()).Where("type = ? AND object_type = ? AND object_id = ?", "merge.denied", "pull", nativePull.ID).Desc("id").Get(&deniedMerge)
		require.NoError(t, err)
		require.True(t, found, "缺少批准时的最终授权拒绝必须有审计")
		require.Equal(t, "user2", deniedMerge.Actor.Name)
		require.Equal(t, "api", deniedMerge.Actor.Transport)
		require.Equal(t, "denied", deniedMerge.Result)
		if options.losePostReceive {
			recoveredPull, err := issues_model.GetPullRequestByID(t.Context(), nativePull.ID)
			require.NoError(t, err)
			require.True(t, recoveredPull.HasMerged)
			require.Equal(t, mergedRef, recoveredPull.MergedCommitID)
			require.NoError(t, recoveredPull.LoadIssue(t.Context()))
			require.True(t, recoveredPull.Issue.IsClosed)
			count, err := db.GetEngine(t.Context()).Where("type = ? AND object_id = ?", "merge.native_state_recovered", nativePull.ID).Count(new(governance_model.AuditEvent))
			require.NoError(t, err)
			require.EqualValues(t, 1, count, "原生状态恢复与审计原子完成")
		}

		pending, err := db.GetEngine(t.Context()).Where("authorization_id = ?", authorization.ID).Count(new(governance_model.Reservation))
		require.NoError(t, err)
		require.Zero(t, pending, "核对真实引用后释放最终授权占用")
	})
}

func TestGovernanceForkAutomaticReferenceHook(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		token := getUserToken(t, "user1", auth_model.AccessTokenScopeAll)
		name := "automatic-hook-fork"
		response := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo1/forks", api.CreateForkOption{Name: &name}).AddTokenAuth(token), http.StatusAccepted)
		created := DecodeJSON(t, response, &api.Repository{})
		repo, err := repo_model.GetRepositoryByID(t.Context(), created.ID)
		require.NoError(t, err)
		installed, err := gitrepo.ReferenceTransactionHookInstalled(repo)
		require.NoError(t, err)
		require.True(t, installed, "Fork 应在交付前接入引用事务入口")
		cloneURL := *baseURL
		cloneURL.User, cloneURL.Path = url.UserPassword("user1", "password"), "/user1/"+name+".git"
		local := filepath.Join(t.TempDir(), "分叉仓库")
		doGitClone(local, &cloneURL)(t)
		doGitCheckoutWriteFileCommit(localGitAddCommitOptions{LocalRepoPath: local, CheckoutBranch: created.DefaultBranch, TreeFilePath: "分叉审计.txt", TreeFileContent: "分叉后的独立变更\n"})(t)
		doGitPushTestRepository(local, "origin", created.DefaultBranch)(t)
		var operations []governance_model.ReferenceTransaction
		require.NoError(t, db.GetEngine(t.Context()).Where("repo_id = ?", repo.ID).Find(&operations))
		require.Len(t, operations, 1)
		require.Equal(t, "committed", operations[0].State)
		require.Equal(t, int64(1), operations[0].Actor.ID)
	})
}
