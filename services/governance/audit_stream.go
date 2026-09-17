// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/secret"
	"gitea.dev/modules/setting"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7/pkg/s3utils"
	"golang.org/x/net/http/httpguts"
	"golang.org/x/oauth2/google"
)

// AuditStreamCredentials 从不进入审计正文、日志和普通读接口。
type AuditStreamCredentials struct {
	VerificationToken  string            `json:"verification_token,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
	AccessKeyID        string            `json:"access_key_id,omitempty"`
	SecretAccessKey    string            `json:"secret_access_key,omitempty"`
	SessionToken       string            `json:"session_token,omitempty"`
	ServiceAccountJSON string            `json:"service_account_json,omitempty"`
}

// AuditStreamOption 更新需要当前修订号；省略凭据表示保留已有加密值。
// swagger:model
type AuditStreamOption struct {
	ScopeType   string                            `json:"scope_type"`
	ScopeID     int64                             `json:"scope_id"`
	Name        string                            `json:"name"`
	Kind        string                            `json:"kind"`
	Endpoint    string                            `json:"endpoint"`
	Destination governance_model.AuditDestination `json:"destination"`
	EventTypes  []string                          `json:"event_types"`
	Enabled     bool                              `json:"enabled"`
	Revision    int64                             `json:"revision"`
	Credentials *AuditStreamCredentials           `json:"credentials,omitempty"`
}

type AuditStreamState struct {
	*governance_model.AuditStream
	Pending        int64    `json:"pending"`
	InFlight       int64    `json:"in_flight"`
	HasCredentials bool     `json:"has_credentials"`
	HeaderNames    []string `json:"header_names"`
}

type AuditStreamSaved struct {
	*AuditStreamState
	VerificationToken string `json:"verification_token,omitempty"`
}

func CheckAuditStreamAccess(ctx context.Context, userID int64, scope string, scopeID int64) error {
	if (scope != "instance" && scope != "group") || (scope == "instance" && scopeID != 0) || (scope == "group" && scopeID <= 0) {
		return governance_model.ErrNotFound
	}
	user, err := user_model.GetUserByID(ctx, userID)
	if user_model.IsErrUserNotExist(err) {
		return governance_model.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !user.IsActive || user.ProhibitLogin || user.IsOrganization() || user.IsGiteaActions() || user.IsGhost() {
		return governance_model.ErrNotFound
	}
	if scope == "group" {
		group, err := user_model.GetUserByID(ctx, scopeID)
		if user_model.IsErrUserNotExist(err) {
			return governance_model.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !group.IsOrganization() {
			return governance_model.ErrNotFound
		}
		namespace, err := governance_model.GetNamespace(ctx, scopeID)
		if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
			return err
		}
		if namespace != nil && namespace.ParentID != 0 {
			return governance_model.ErrNotFound
		}
	}
	if user.IsAdmin {
		return nil
	}
	if scope != "group" {
		return governance_model.ErrNotFound
	}
	owner, err := organization.IsOrganizationOwner(ctx, scopeID, userID)
	if err != nil {
		return err
	}
	if owner {
		return nil
	}
	grants, err := governance_model.GroupGrants(ctx, scopeID, userID, time.Now())
	if err != nil {
		return err
	}
	if governance_model.EffectiveAbilities(grants)[governance_model.ManageAudit] {
		return nil
	}
	return governance_model.ErrNotFound
}

var auditCloudID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func validateAuditStream(option *AuditStreamOption, credentials *AuditStreamCredentials) error {
	if len(option.Name) == 0 || len(option.Name) > 100 || len(option.Endpoint) > 2048 || len(option.EventTypes) > len(governance_model.EventCatalog) {
		return governance_model.ErrInvalid
	}
	for _, eventType := range option.EventTypes {
		if _, ok := governance_model.EventCatalog[eventType]; !ok {
			return fmt.Errorf("%w：未知审计事件类型", governance_model.ErrInvalid)
		}
	}
	u, err := url.Parse(option.Endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("%w：目标必须是无内嵌凭据、查询串或片段的 HTTP(S) 地址", governance_model.ErrInvalid)
	}
	d := option.Destination
	switch option.Kind {
	case "http":
		if d != (governance_model.AuditDestination{}) || credentials.AccessKeyID != "" || credentials.SecretAccessKey != "" || credentials.SessionToken != "" || credentials.ServiceAccountJSON != "" {
			return governance_model.ErrInvalid
		}
		if len(credentials.VerificationToken) < 16 || len(credentials.VerificationToken) > 256 || !httpguts.ValidHeaderFieldValue(credentials.VerificationToken) || len(credentials.Headers) > 20 {
			return governance_model.ErrInvalid
		}
		normalized := make(map[string]string, len(credentials.Headers))
		for name, value := range credentials.Headers {
			name = textproto.CanonicalMIMEHeaderKey(name)
			if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(value) || len(value) > 4096 || slices.Contains([]string{"Host", "Content-Length", "Content-Type", "Connection", "Transfer-Encoding", "Trailer", "Te", "Upgrade", "Proxy-Authorization", "Cookie", "X-Gitea-Event-Id", "X-Gitlab-Event-Streaming-Token"}, name) {
				return governance_model.ErrInvalid
			}
			if _, exists := normalized[name]; exists {
				return governance_model.ErrInvalid
			}
			normalized[name] = value
		}
		credentials.Headers = normalized
	case "s3":
		if credentials.VerificationToken != "" || len(credentials.Headers) != 0 || credentials.ServiceAccountJSON != "" || credentials.AccessKeyID == "" || credentials.SecretAccessKey == "" || len(credentials.AccessKeyID) > 128 || len(credentials.SecretAccessKey) > 256 || len(credentials.SessionToken) > 8192 {
			return governance_model.ErrInvalid
		}
		if (u.Path != "" && u.Path != "/") || !auditCloudID.MatchString(d.Region) || s3utils.CheckValidBucketNameStrict(d.Bucket) != nil || len(d.Prefix) > 512 || strings.ContainsAny(d.Prefix, "\\\r\n") || strings.Contains(d.Prefix, "..") || strings.HasPrefix(d.Prefix, "/") || d.ProjectID != "" || d.LogID != "" {
			return governance_model.ErrInvalid
		}
	case "google_logging":
		if option.Endpoint != "https://logging.googleapis.com/v2/entries:write" || !auditCloudID.MatchString(d.ProjectID) || !auditCloudID.MatchString(d.LogID) || d.Region != "" || d.Bucket != "" || d.Prefix != "" || credentials.VerificationToken != "" || len(credentials.Headers) != 0 || credentials.AccessKeyID != "" || credentials.SecretAccessKey != "" || credentials.SessionToken != "" || len(credentials.ServiceAccountJSON) > 16384 {
			return governance_model.ErrInvalid
		}
		var account struct {
			Type      string `json:"type"`
			ProjectID string `json:"project_id"`
			TokenURI  string `json:"token_uri"`
		}
		if json.Unmarshal([]byte(credentials.ServiceAccountJSON), &account) != nil || account.Type != "service_account" || account.ProjectID != d.ProjectID || account.TokenURI != "https://oauth2.googleapis.com/token" {
			return governance_model.ErrInvalid
		}
		config, err := google.JWTConfigFromJSON([]byte(credentials.ServiceAccountJSON), "https://www.googleapis.com/auth/logging.write")
		if err != nil || config.Email == "" || len(config.PrivateKey) == 0 {
			return governance_model.ErrInvalid
		}
		block, _ := pem.Decode(config.PrivateKey)
		if block == nil {
			return governance_model.ErrInvalid
		}
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if err != nil || !ok || rsaKey.N.BitLen() < 2048 || rsaKey.Validate() != nil {
			return governance_model.ErrInvalid
		}
	default:
		return governance_model.ErrInvalid
	}
	return nil
}

func decryptAuditCredentials(stream *governance_model.AuditStream) (*AuditStreamCredentials, error) {
	raw, err := secret.DecryptSecret(setting.SecretKey, stream.Secret)
	if err != nil {
		return nil, errors.New("无法解密审计外送凭据，请核对实例主密钥")
	}
	var credentials AuditStreamCredentials
	if json.Unmarshal([]byte(raw), &credentials) != nil {
		return nil, errors.New("审计外送凭据格式损坏")
	}
	return &credentials, nil
}

func streamState(ctx context.Context, stream *governance_model.AuditStream) (*AuditStreamState, error) {
	state := &AuditStreamState{AuditStream: stream, HasCredentials: stream.Secret != "", HeaderNames: []string{}}
	var err error
	state.Pending, err = db.GetEngine(ctx).Where("stream_id = ?", stream.ID).Count(new(governance_model.AuditDelivery))
	if err != nil {
		return nil, err
	}
	state.InFlight, err = db.GetEngine(ctx).Where("stream_id = ? AND lease_until > ?", stream.ID, time.Now().Unix()).Count(new(governance_model.AuditDelivery))
	if err != nil {
		return nil, err
	}
	credentials, err := decryptAuditCredentials(stream)
	if err != nil {
		return nil, err
	}
	for name := range credentials.Headers {
		state.HeaderNames = append(state.HeaderNames, name)
	}
	slices.Sort(state.HeaderNames)
	return state, nil
}

func ListAuditStreams(ctx context.Context, userID int64, scope string, scopeID int64) ([]*AuditStreamState, error) {
	if err := CheckAuditStreamAccess(ctx, userID, scope, scopeID); err != nil {
		return nil, err
	}
	var streams []*governance_model.AuditStream
	if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", scope, scopeID).Asc("id").Find(&streams); err != nil {
		return nil, err
	}
	states := make([]*AuditStreamState, 0, len(streams))
	for _, stream := range streams {
		state, err := streamState(ctx, stream)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func SaveAuditStream(ctx context.Context, actor governance_model.Actor, id int64, option AuditStreamOption) (*AuditStreamSaved, error) {
	var stream *governance_model.AuditStream
	var createdVerification string
	err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		if err := checkDelegation(ctx, actor); err != nil {
			return err
		}
		if err := CheckAuditStreamAccess(ctx, actor.EffectiveUserID(), option.ScopeType, option.ScopeID); err != nil {
			return err
		}
		var before any
		credentials := option.Credentials
		if id != 0 {
			current, has, err := db.GetByID[governance_model.AuditStream](ctx, id)
			if err != nil {
				return err
			}
			if !has || current.ScopeType != option.ScopeType || current.ScopeID != option.ScopeID {
				return governance_model.ErrNotFound
			}
			if current.Revision != option.Revision {
				return governance_model.ErrConflict
			}
			oldCredentials, err := decryptAuditCredentials(current)
			if err != nil {
				return err
			}
			if credentials == nil {
				credentials = oldCredentials
			}
			if current.Kind == "http" && option.Kind == "http" && credentials.VerificationToken == "" {
				credentials.VerificationToken = oldCredentials.VerificationToken
			}
			if current.Kind == "http" && option.Kind == "http" && credentials.VerificationToken != oldCredentials.VerificationToken {
				return fmt.Errorf("%w：验证标识固定于目标，变更请创建新目标", governance_model.ErrInvalid)
			}
			active, err := db.GetEngine(ctx).Where("stream_id = ? AND lease_until > ?", id, time.Now().Unix()).Exist(new(governance_model.AuditDelivery))
			if err != nil {
				return err
			}
			if active {
				return fmt.Errorf("%w：当前有在途投递，完成后才能修改目标", governance_model.ErrConflict)
			}
			if current.Endpoint != option.Endpoint || current.Kind != option.Kind || current.Destination != option.Destination {
				pending, err := db.GetEngine(ctx).Where("stream_id = ?", id).Exist(new(governance_model.AuditDelivery))
				if err != nil {
					return err
				}
				if pending {
					return fmt.Errorf("%w：先完成积压投递，再变更目标地址或云资源", governance_model.ErrConflict)
				}
			}
			before = streamAuditSnapshot(current)
			stream = current
		} else {
			count, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", option.ScopeType, option.ScopeID).Count(new(governance_model.AuditStream))
			if err != nil {
				return err
			}
			if count >= 20 {
				return fmt.Errorf("%w：每个范围最多二十个外送目标", governance_model.ErrConflict)
			}
			stream = &governance_model.AuditStream{ScopeType: option.ScopeType, ScopeID: option.ScopeID}
			if credentials == nil {
				credentials = &AuditStreamCredentials{}
			}
			if option.Kind == "http" && credentials.VerificationToken == "" {
				credentials.VerificationToken = uuid.NewString()
			}
			createdVerification = credentials.VerificationToken
		}
		if err := validateAuditStream(&option, credentials); err != nil {
			return err
		}
		raw, err := json.Marshal(credentials)
		if err != nil {
			return err
		}
		encrypted, err := secret.EncryptSecret(setting.SecretKey, string(raw))
		if err != nil {
			return err
		}
		stream.Name, stream.Kind, stream.Endpoint = option.Name, option.Kind, option.Endpoint
		stream.Destination, stream.EventTypes, stream.Enabled, stream.Secret = option.Destination, option.EventTypes, option.Enabled, encrypted
		stream.Headers = nil
		stream.Revision++
		if id == 0 {
			if err := db.Insert(ctx, stream); err != nil {
				return err
			}
		} else {
			if _, err := db.GetEngine(ctx).ID(id).Cols("name", "kind", "endpoint", "destination", "event_types", "enabled", "secret", "headers", "revision").Update(stream); err != nil {
				return err
			}
		}
		details, err := json.Marshal(map[string]any{"before": before, "after": streamAuditSnapshot(stream), "credentials_changed": option.Credentials != nil || id == 0})
		if err != nil {
			return err
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "audit.stream_changed", Actor: actor, ScopeType: stream.ScopeType, ScopeID: stream.ScopeID, ObjectType: "audit_stream", ObjectID: stream.ID, ObjectPath: stream.Name, Result: "success", Details: details})
	})
	if err != nil {
		return nil, err
	}
	state, err := streamState(ctx, stream)
	if err != nil {
		return nil, err
	}
	return &AuditStreamSaved{AuditStreamState: state, VerificationToken: createdVerification}, nil
}

func streamAuditSnapshot(stream *governance_model.AuditStream) map[string]any {
	return map[string]any{"name": stream.Name, "kind": stream.Kind, "endpoint": stream.Endpoint, "destination": stream.Destination, "event_types": stream.EventTypes, "enabled": stream.Enabled, "revision": stream.Revision}
}

// 阻止重定向，防止目标重定向时携带认证头访问另一个地址。
func auditNoRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
