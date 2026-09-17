// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/git/gitcmd"
	"gitea.dev/modules/json"
	"gitea.dev/tests"

	"github.com/stretchr/testify/require"
)

func TestGovernanceCodeAccessDurableQueue(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	stream := &governance_model.AuditStream{ScopeType: "instance", Name: "暂停的访问审计接收器", Kind: "http", Endpoint: "https://audit.example.invalid", EventTypes: []string{"access.code"}, Enabled: false, Revision: 1}
	require.NoError(t, db.Insert(t.Context(), stream))
	// 显式确认暂停状态，排除 ORM 默认值使此用例意外变成启用外送。
	_, err := db.GetEngine(t.Context()).ID(stream.ID).Cols("enabled").Update(&governance_model.AuditStream{Enabled: false})
	require.NoError(t, err)
	for _, path := range []string{"/api/v1/repos/user2/repo1/raw/master/README.md", "/api/v1/repos/user2/repo1/media/master/README.md", "/api/v1/repos/user2/repo1/contents/README.md", "/api/v1/repos/user2/repo1/contents-ext/README.md?includes=file_content"} {
		req := NewRequest(t, "GET", path).AddBasicAuth("user2")
		req.RemoteAddr = "192.0.2.20:12345"
		MakeRequest(t, req, http.StatusOK)
	}
	session := loginUser(t, "user2")
	for _, path := range []string{"/user2/repo1/raw/branch/master/README.md", "/user2/repo1/media/branch/master/README.md", "/user2/repo1/src/branch/master/README.md"} {
		req := NewRequest(t, "GET", path)
		req.RemoteAddr = "192.0.2.20:12345"
		session.MakeRequest(t, req, http.StatusOK)
	}
	var deliveries []governance_model.AuditDelivery
	require.NoError(t, db.GetEngine(t.Context()).Where("stream_id = ?", stream.ID).Asc("id").Find(&deliveries))
	require.Len(t, deliveries, 14)
	for i := 0; i < len(deliveries); i += 2 {
		var started, finished governance_model.AuditEvent
		require.NoError(t, json.Unmarshal([]byte(deliveries[i].Payload), &started))
		require.NoError(t, json.Unmarshal([]byte(deliveries[i+1].Payload), &finished))
		require.Equal(t, "pending", started.Result)
		require.Equal(t, "success", finished.Result)
		require.Equal(t, started.RequestID, finished.RequestID)
		require.NotEqual(t, started.EventID, finished.EventID)
		require.Equal(t, int64(2), started.Actor.ID)
		require.Equal(t, "192.0.2.20", started.Actor.IP)
		require.Equal(t, "user2/repo1", started.ObjectPath)
		require.Contains(t, string(started.Details), "README.md")
	}
	count, err := db.GetEngine(t.Context()).Where("type = ?", "access.code").Count(new(governance_model.AuditEvent))
	require.NoError(t, err)
	require.Zero(t, count, "访问正文只进入持久化队列，不误报本地永久保存")
	_, err = db.GetEngine(t.Context()).Exec("CREATE TRIGGER governance_access_fail BEFORE INSERT ON governance_audit_delivery BEGIN SELECT RAISE(ABORT, '访问审计存储故障样本'); END")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.GetEngine(context.WithoutCancel(t.Context())).Exec("DROP TRIGGER IF EXISTS governance_access_fail")
		require.NoError(t, err)
	})
	response := MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/user2/repo1/raw/master/README.md").AddBasicAuth("user2"), http.StatusInternalServerError)
	require.NotEqual(t, "file", response.Header().Get("X-Gitea-Object-Type"), "保存失败时不能开始返回代码内容")
}

func TestGovernanceHTTPGitAccessAudit(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		stream := &governance_model.AuditStream{ScopeType: "instance", Name: "暂停的 Git 访问接收器", Kind: "http", Endpoint: "https://audit.example.invalid", EventTypes: []string{"access.git_http"}, Enabled: false, Revision: 1}
		require.NoError(t, db.Insert(t.Context(), stream))
		_, err := db.GetEngine(t.Context()).ID(stream.ID).Cols("enabled").Update(&governance_model.AuditStream{Enabled: false})
		require.NoError(t, err)
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/user2/repo1", map[string]any{"private": true}).AddBasicAuth("user2"), http.StatusOK)
		cloneURL := *baseURL
		cloneURL.User, cloneURL.Path = url.UserPassword("user2", "password"), "/user2/repo1.git"
		doGitClone(filepath.Join(t.TempDir(), "访问审计克隆"), &cloneURL)(t)
		var deliveries []governance_model.AuditDelivery
		require.NoError(t, db.GetEngine(t.Context()).Where("stream_id = ?", stream.ID).Asc("id").Find(&deliveries))
		require.NotEmpty(t, deliveries)
		operations := map[string]bool{}
		for _, delivery := range deliveries {
			var event governance_model.AuditEvent
			require.NoError(t, json.Unmarshal([]byte(delivery.Payload), &event))
			require.Equal(t, int64(2), event.Actor.ID)
			require.Equal(t, "git_http", event.Actor.Transport)
			require.NotEmpty(t, event.Actor.IP)
			if event.Result == "success" {
				var details map[string]any
				require.NoError(t, json.Unmarshal(event.Details, &details))
				operations[details["operation"].(string)] = true
			}
		}
		require.True(t, operations["advertise_refs"])
		require.True(t, operations["upload-pack"], "真实克隆必须经过 Git 协议数据传输")
		_, err = db.GetEngine(t.Context()).Exec("CREATE TRIGGER governance_git_access_fail BEFORE INSERT ON governance_audit_delivery BEGIN SELECT RAISE(ABORT, 'Git 访问审计存储故障样本'); END")
		require.NoError(t, err)
		t.Cleanup(func() {
			_, err := db.GetEngine(context.WithoutCancel(t.Context())).Exec("DROP TRIGGER IF EXISTS governance_git_access_fail")
			require.NoError(t, err)
		})
		_, _, err = gitcmd.NewCommand("ls-remote").AddDynamicArguments(cloneURL.String()).RunStdString(t.Context())
		require.Error(t, err, "访问审计无法保存时真实 Git 客户端必须被拒绝")
	})
}

func TestGovernanceArchiveAccessAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	stream := &governance_model.AuditStream{ScopeType: "instance", Name: "暂停的归档访问接收器", Kind: "http", Endpoint: "https://audit.example.invalid", EventTypes: []string{"access.archive"}, Enabled: false, Revision: 1}
	require.NoError(t, db.Insert(t.Context(), stream))
	_, err := db.GetEngine(t.Context()).ID(stream.ID).Cols("enabled").Update(&governance_model.AuditStream{Enabled: false})
	require.NoError(t, err)
	session := loginUser(t, "user2")
	response := MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/user2/repo1/archive/master.zip").AddBasicAuth("user2"), http.StatusOK)
	require.True(t, bytes.HasPrefix(response.Body.Bytes(), []byte("PK")), "必须实际返回 ZIP 归档")
	response = session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/archive/master.zip"), http.StatusOK)
	require.True(t, bytes.HasPrefix(response.Body.Bytes(), []byte("PK")))
	var deliveries []governance_model.AuditDelivery
	require.NoError(t, db.GetEngine(t.Context()).Where("stream_id = ?", stream.ID).Asc("id").Find(&deliveries))
	require.Len(t, deliveries, 4)
	for _, delivery := range deliveries {
		var event governance_model.AuditEvent
		require.NoError(t, json.Unmarshal([]byte(delivery.Payload), &event))
		require.Equal(t, int64(2), event.Actor.ID)
		require.Equal(t, "access.archive", event.Type)
		require.Equal(t, "user2/repo1", event.ObjectPath)
		var details map[string]any
		require.NoError(t, json.Unmarshal(event.Details, &details))
		require.Equal(t, "zip", details["format"])
		require.NotEmpty(t, details["commit"])
	}
}

func TestGovernanceSSHAccessAudit(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, baseURL *url.URL) {
		require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
		stream := &governance_model.AuditStream{ScopeType: "instance", Name: "SSH 访问接收器", Kind: "http", Endpoint: "https://audit.example.invalid", EventTypes: []string{"access.git_ssh"}, Revision: 1}
		require.NoError(t, db.Insert(t.Context(), stream))
		_, err := db.GetEngine(t.Context()).ID(stream.ID).Cols("enabled").Update(&governance_model.AuditStream{Enabled: false})
		require.NoError(t, err)
		ctx := NewAPITestContext(t, "user2", "repo1", auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)
		for _, kind := range []string{"user", "deploy_key"} {
			t.Run(kind, func(t *testing.T) {
				withKeyFile(t, "访问审计-"+kind, func(keyFile string) {
					if kind == "user" {
						doAPICreateUserKey(ctx, "访问审计-"+kind, keyFile)(t)
					} else {
						doAPICreateDeployKey(ctx, "访问审计-"+kind, keyFile, true)(t)
					}
					sshURL := createSSHUrl(ctx.GitPath(), baseURL)
					doGitClone(filepath.Join(t.TempDir(), "SSH克隆"), sshURL)(t)
					var deliveries []governance_model.AuditDelivery
					require.NoError(t, db.GetEngine(t.Context()).Where("stream_id = ?", stream.ID).Asc("id").Find(&deliveries))
					var matched []governance_model.AuditEvent
					for _, delivery := range deliveries {
						var event governance_model.AuditEvent
						require.NoError(t, json.Unmarshal([]byte(delivery.Payload), &event))
						if event.Actor.Kind == kind {
							matched = append(matched, event)
						}
					}
					require.Len(t, matched, 2)
					require.Equal(t, "pending", matched[0].Result)
					require.Equal(t, "success", matched[1].Result)
					require.Equal(t, matched[0].RequestID, matched[1].RequestID)
					require.NotEqual(t, matched[0].EventID, matched[1].EventID)
					for _, event := range matched {
						require.Equal(t, "git_ssh", event.Actor.Transport)
						require.NotEmpty(t, event.Actor.IP)
						require.Positive(t, event.Actor.CredentialID)
						if kind == "user" {
							require.Equal(t, int64(2), event.Actor.ID)
						} else {
							require.Zero(t, event.Actor.ID)
						}
					}
					_, err := db.GetEngine(t.Context()).Exec("CREATE TRIGGER governance_ssh_access_fail BEFORE INSERT ON governance_audit_delivery BEGIN SELECT RAISE(ABORT, 'SSH访问审计存储故障样本'); END")
					require.NoError(t, err)
					defer func() {
						_, err := db.GetEngine(context.WithoutCancel(t.Context())).Exec("DROP TRIGGER IF EXISTS governance_ssh_access_fail")
						require.NoError(t, err)
					}()
					_, _, err = gitcmd.NewCommand("ls-remote").AddDynamicArguments(sshURL.String()).RunStdString(t.Context())
					require.Error(t, err, "审计无法保存时不能读取 Git 引用")
				})
			})
		}
	})
}

func TestGovernanceBatchAndObjectAccessAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	stream := &governance_model.AuditStream{ScopeType: "instance", Name: "批量和对象审计", Kind: "http", Endpoint: "https://audit.example.invalid", EventTypes: []string{"access.code"}, Revision: 1}
	require.NoError(t, db.Insert(t.Context(), stream))
	_, err := db.GetEngine(t.Context()).ID(stream.ID).Cols("enabled").Update(&governance_model.AuditStream{Enabled: false})
	require.NoError(t, err)
	paths := []string{
		"/api/v1/repos/user2/repo1/git/blobs/65f1bf27bc3bf70f64657658635e66094edbcb4d",
		"/api/v1/repos/user2/repo1/git/trees/master",
		"/api/v1/repos/user2/repo1/file-contents?body=" + url.QueryEscape(`{"files":["README.md"]}`),
	}
	for _, path := range paths {
		MakeRequest(t, NewRequest(t, "GET", path).AddBasicAuth("user2"), http.StatusOK)
	}
	MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/user2/repo1/file-contents", map[string]any{"files": []string{"README.md"}}).AddBasicAuth("user2"), http.StatusOK)
	var deliveries []governance_model.AuditDelivery
	require.NoError(t, db.GetEngine(t.Context()).Where("stream_id = ?", stream.ID).Asc("id").Find(&deliveries))
	require.Len(t, deliveries, 8)
	for i := 0; i < len(deliveries); i += 2 {
		var started, finished governance_model.AuditEvent
		require.NoError(t, json.Unmarshal([]byte(deliveries[i].Payload), &started))
		require.NoError(t, json.Unmarshal([]byte(deliveries[i+1].Payload), &finished))
		require.Equal(t, "pending", started.Result)
		require.Equal(t, "success", finished.Result)
		require.Equal(t, started.RequestID, finished.RequestID)
		require.Equal(t, int64(2), started.Actor.ID)
		require.NotContains(t, string(started.Details), "content")
	}
	_, err = db.GetEngine(t.Context()).Exec("CREATE TRIGGER governance_object_access_fail BEFORE INSERT ON governance_audit_delivery BEGIN SELECT RAISE(ABORT, '对象访问审计存储故障样本'); END")
	require.NoError(t, err)
	defer func() {
		_, err := db.GetEngine(context.WithoutCancel(t.Context())).Exec("DROP TRIGGER IF EXISTS governance_object_access_fail")
		require.NoError(t, err)
	}()
	for _, path := range paths {
		MakeRequest(t, NewRequest(t, "GET", path).AddBasicAuth("user2"), http.StatusInternalServerError)
	}
}

func TestGovernanceAttachmentAccessAudit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(t.Context()))
	session := loginUser(t, "user2")
	content := testGeneratePngBytes()
	uuid := testCreateIssueAttachment(t, session, "/user2/repo1", "审计样本.png", content, http.StatusOK)
	stream := &governance_model.AuditStream{ScopeType: "instance", Name: "附件访问审计", Kind: "http", Endpoint: "https://audit.example.invalid", EventTypes: []string{"access.attachment"}, Revision: 1}
	require.NoError(t, db.Insert(t.Context(), stream))
	_, err := db.GetEngine(t.Context()).ID(stream.ID).Cols("enabled").Update(&governance_model.AuditStream{Enabled: false})
	require.NoError(t, err)
	for _, path := range []string{"/attachments/" + uuid, "/user2/repo1/attachments/" + uuid} {
		response := session.MakeRequest(t, NewRequest(t, "GET", path), http.StatusOK)
		require.Equal(t, content, response.Body.Bytes())
	}
	var deliveries []governance_model.AuditDelivery
	require.NoError(t, db.GetEngine(t.Context()).Where("stream_id = ?", stream.ID).Asc("id").Find(&deliveries))
	require.Len(t, deliveries, 4)
	for i, delivery := range deliveries {
		var event governance_model.AuditEvent
		require.NoError(t, json.Unmarshal([]byte(delivery.Payload), &event))
		require.Equal(t, int64(2), event.Actor.ID)
		require.Equal(t, "user2/repo1", event.ObjectPath)
		require.Contains(t, string(event.Details), uuid)
		if i%2 == 0 {
			require.Equal(t, "pending", event.Result)
		} else {
			require.Equal(t, "success", event.Result)
		}
	}
	_, err = db.GetEngine(t.Context()).Exec("CREATE TRIGGER governance_attachment_access_fail BEFORE INSERT ON governance_audit_delivery BEGIN SELECT RAISE(ABORT, '附件访问审计存储故障样本'); END")
	require.NoError(t, err)
	defer func() {
		_, err := db.GetEngine(context.WithoutCancel(t.Context())).Exec("DROP TRIGGER IF EXISTS governance_attachment_access_fail")
		require.NoError(t, err)
	}()
	session.MakeRequest(t, NewRequest(t, "GET", "/attachments/"+uuid), http.StatusInternalServerError)
}
