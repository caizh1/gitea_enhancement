// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type auditTestTransport func(*http.Request) (*http.Response, error)

func (f auditTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuditCloudProtocols(t *testing.T) {
	event := &governance_model.AuditEvent{EventID: "795a856a-087b-4a19-8b08-ab5fa6dc36fe", OccurredAt: time.Date(2026, 9, 15, 7, 0, 0, 0, time.UTC), Type: "member.added", ObjectPath: "测试组织/测试项目"}
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	delivery := &governance_model.AuditDelivery{EventID: event.EventID, Payload: string(raw)}
	var objectPaths []string
	s3Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Contains(t, r.Header.Get("Authorization"), "AWS4-HMAC-SHA256")
		assert.Contains(t, r.Header.Get("Authorization"), "/us-east-1/s3/aws4_request")
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), event.EventID)
		objectPaths = append(objectPaths, r.URL.Path)
		w.Header().Set("ETag", `"测试对象回执"`)
	}))
	defer s3Server.Close()
	stream := &governance_model.AuditStream{Kind: "s3", Endpoint: s3Server.URL, Destination: governance_model.AuditDestination{Region: "us-east-1", Bucket: "audit-test", Prefix: "governance"}}
	credentials := &AuditStreamCredentials{AccessKeyID: "isolated-test-key", SecretAccessKey: "isolated-test-secret"}
	for range 2 {
		require.NoError(t, sendAuditPayload(t.Context(), stream, credentials, delivery, http.DefaultTransport))
	}
	require.Len(t, objectPaths, 2)
	assert.Equal(t, objectPaths[0], objectPaths[1], "重发使用同一对象键，接收端可确定性去重")
	assert.Equal(t, "/audit-test/governance/2026/09/15/"+event.EventID+".json", objectPaths[0])

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	account, err := json.Marshal(map[string]string{"type": "service_account", "project_id": "audit-test", "client_email": "audit-test@example.invalid", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": "https://oauth2.googleapis.com/token"})
	require.NoError(t, err)
	googleStream := &governance_model.AuditStream{Name: "Google 协议验收", Kind: "google_logging", Endpoint: "https://logging.googleapis.com/v2/entries:write", Destination: governance_model.AuditDestination{ProjectID: "audit-test", LogID: "governance"}}
	googleCredentials := &AuditStreamCredentials{ServiceAccountJSON: string(account)}
	require.NoError(t, validateAuditStream(&AuditStreamOption{Name: googleStream.Name, Kind: googleStream.Kind, Endpoint: googleStream.Endpoint, Destination: googleStream.Destination}, googleCredentials))
	var tokenRequests, logRequests int
	transport := auditTestTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://oauth2.googleapis.com/token":
			tokenRequests++
			require.NoError(t, r.ParseForm())
			assert.Equal(t, "urn:ietf:params:oauth:grant-type:jwt-bearer", r.Form.Get("grant_type"))
			assert.Len(t, strings.Split(r.Form.Get("assertion"), "."), 3)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"isolated-token","token_type":"Bearer","expires_in":3600}`))}, nil
		case "https://logging.googleapis.com/v2/entries:write":
			logRequests++
			assert.Equal(t, "Bearer isolated-token", r.Header.Get("Authorization"))
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			var request struct {
				LogName string `json:"logName"`
				Entries []struct {
					InsertID  string                      `json:"insertId"`
					Timestamp string                      `json:"timestamp"`
					Payload   governance_model.AuditEvent `json:"jsonPayload"`
				} `json:"entries"`
			}
			require.NoError(t, json.Unmarshal(body, &request))
			assert.Equal(t, "projects/audit-test/logs/governance", request.LogName)
			require.Len(t, request.Entries, 1)
			assert.Equal(t, event.EventID, request.Entries[0].InsertID)
			assert.Equal(t, event.EventID, request.Entries[0].Payload.EventID)
			assert.Equal(t, "2026-09-15T07:00:00Z", request.Entries[0].Timestamp)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		default:
			t.Errorf("外送越过固定 Google 地址：%s", r.URL)
			return nil, governance_model.ErrForbidden
		}
	})
	require.NoError(t, sendAuditPayload(t.Context(), googleStream, googleCredentials, delivery, transport))
	assert.Equal(t, 1, tokenRequests)
	assert.Equal(t, 1, logRequests)
}
