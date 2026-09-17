// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/hostmatcher"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/sync/errgroup"
)

type auditPinnedTransport struct {
	http.RoundTripper
	endpoint *url.URL
	google   bool
}

func (t auditPinnedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target := req.URL
	allowed := target.Scheme == t.endpoint.Scheme && target.Host == t.endpoint.Host
	if t.google {
		allowed = target.String() == "https://logging.googleapis.com/v2/entries:write" || target.String() == "https://oauth2.googleapis.com/token"
	}
	if !allowed {
		return nil, errors.New("外送请求超出已配置目标")
	}
	return t.RoundTripper.RoundTrip(req)
}

func auditTransport(stream *governance_model.AuditStream) (*http.Transport, http.RoundTripper, error) {
	endpoint, err := url.Parse(stream.Endpoint)
	if err != nil {
		return nil, nil, governance_model.ErrInvalid
	}
	allowed := hostmatcher.ParseHostMatchList("security.ALLOWED_HOST_LIST", setting.Security.AllowedHostList)
	base := hostmatcher.NewHTTPTransport("审计外送", allowed, nil, nil, nil, nil)
	base.ResponseHeaderTimeout = 15 * time.Second
	base.MaxResponseHeaderBytes = 64 * 1024
	return base, auditPinnedTransport{RoundTripper: base, endpoint: endpoint, google: stream.Kind == "google_logging"}, nil
}

func deliverAudit(ctx context.Context, stream *governance_model.AuditStream, delivery *governance_model.AuditDelivery) error {
	credentialsValue, err := decryptAuditCredentials(stream)
	if err != nil {
		return err
	}
	base, transport, err := auditTransport(stream)
	if err != nil {
		return err
	}
	defer base.CloseIdleConnections()
	return sendAuditPayload(ctx, stream, credentialsValue, delivery, transport)
}

// 网络调用只有一次逻辑投递；失败交给持久化队列重试，确认丢失仍保留相同事件 ID。
func sendAuditPayload(ctx context.Context, stream *governance_model.AuditStream, credentialsValue *AuditStreamCredentials, delivery *governance_model.AuditDelivery, transport http.RoundTripper) error {
	var event governance_model.AuditEvent
	if json.Unmarshal([]byte(delivery.Payload), &event) != nil || event.EventID != delivery.EventID || event.OccurredAt.IsZero() {
		return governance_model.ErrInvalid
	}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: auditNoRedirect}
	payload := []byte(delivery.Payload)
	switch stream.Kind {
	case "http":
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, stream.Endpoint, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		for name, value := range credentialsValue.Headers {
			req.Header.Set(name, value)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Gitlab-Event-Streaming-Token", credentialsValue.VerificationToken)
		req.Header.Set("X-Gitea-Event-Id", event.EventID)
		return sendAuditHTTP(client, req)
	case "s3":
		endpoint, err := url.Parse(stream.Endpoint)
		if err != nil {
			return err
		}
		d := stream.Destination
		s3, err := minio.New(endpoint.Host, &minio.Options{Creds: credentials.NewStaticV4(credentialsValue.AccessKeyID, credentialsValue.SecretAccessKey, credentialsValue.SessionToken), Secure: endpoint.Scheme == "https", Transport: transport, Region: d.Region, BucketLookup: minio.BucketLookupPath, MaxRetries: 1})
		if err != nil {
			return err
		}
		key := strings.TrimSuffix(d.Prefix, "/")
		if key != "" {
			key += "/"
		}
		key += event.OccurredAt.UTC().Format("2006/01/02/") + event.EventID + ".json"
		_, err = s3.PutObject(ctx, d.Bucket, key, bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{ContentType: "application/json", DisableMultipart: true})
		return err
	case "google_logging":
		config, err := google.JWTConfigFromJSON([]byte(credentialsValue.ServiceAccountJSON), "https://www.googleapis.com/auth/logging.write")
		if err != nil {
			return err
		}
		tokenContext := context.WithValue(ctx, oauth2.HTTPClient, client)
		authorizedClient := config.Client(tokenContext)
		authorizedClient.Timeout, authorizedClient.CheckRedirect = client.Timeout, auditNoRedirect
		d := stream.Destination
		body, err := json.Marshal(map[string]any{
			"logName":        "projects/" + d.ProjectID + "/logs/" + url.PathEscape(d.LogID),
			"resource":       map[string]any{"type": "global", "labels": map[string]string{"project_id": d.ProjectID}},
			"entries":        []any{map[string]any{"insertId": event.EventID, "timestamp": event.OccurredAt.UTC().Format(time.RFC3339Nano), "jsonPayload": &event}},
			"partialSuccess": false,
		})
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(tokenContext, http.MethodPost, stream.Endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		return sendAuditHTTP(authorizedClient, req)
	default:
		return governance_model.ErrInvalid
	}
}

func sendAuditHTTP(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("外送目标返回状态 %d", resp.StatusCode)
	}
	return nil
}

func leaseAuditStream(ctx context.Context, id int64) (*governance_model.AuditStream, *governance_model.AuditDelivery, error) {
	var stream *governance_model.AuditStream
	var delivery *governance_model.AuditDelivery
	err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		var has bool
		var err error
		stream, has, err = db.GetByID[governance_model.AuditStream](ctx, id)
		if err != nil {
			return err
		}
		if !has || !stream.Enabled {
			return nil
		}
		delivery, err = governance_model.LeaseDelivery(ctx, id, time.Now())
		return err
	})
	return stream, delivery, err
}

// RunAuditStreams 使用原生定时器和四个有界发送工作者；队列正文在数据库确认成功后清理。
func RunAuditStreams(ctx context.Context) error {
	var streams []*governance_model.AuditStream
	if err := db.GetEngine(ctx).Where("enabled = ?", true).Asc("id").Find(&streams); err != nil {
		return err
	}
	batchCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	workers, workerCtx := errgroup.WithContext(batchCtx)
	workers.SetLimit(4)
	for _, candidate := range streams {
		workers.Go(func() error {
			for range 100 {
				if workerCtx.Err() != nil {
					return nil
				}
				stream, delivery, err := leaseAuditStream(workerCtx, candidate.ID)
				if err != nil {
					return err
				}
				if delivery == nil {
					return nil
				}
				// 进程暂停后不能重新获得完整发送时限，网络必须在原租约失效前结束。
				deadline := time.Now().Add(25 * time.Second)
				if leaseDeadline := time.Unix(delivery.LeaseUntil, 0).Add(-10 * time.Second); leaseDeadline.Before(deadline) {
					deadline = leaseDeadline
				}
				sendCtx, stop := context.WithDeadline(workerCtx, deadline)
				sendErr := deliverAudit(sendCtx, stream, delivery)
				stop()
				// 网络退出后仍尝试记录结果；进程被杀时由租约到期恢复。
				finishCtx, finishStop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				err = governance_model.FinishDelivery(finishCtx, delivery, sendErr == nil, time.Now())
				finishStop()
				if err != nil && !errors.Is(err, governance_model.ErrConflict) {
					return err
				}
				if sendErr != nil {
					return nil
				}
			}
			return nil
		})
	}
	return workers.Wait()
}

// TestAuditStream 将测试事件持久化后交给相同投递链路，不生成无法追踪的临时网络请求。
func TestAuditStream(ctx context.Context, actor governance_model.Actor, id, revision int64) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		if err := checkDelegation(ctx, actor); err != nil {
			return err
		}
		stream, has, err := db.GetByID[governance_model.AuditStream](ctx, id)
		if err != nil {
			return err
		}
		if !has {
			return governance_model.ErrNotFound
		}
		if err := CheckAuditStreamAccess(ctx, actor.EffectiveUserID(), stream.ScopeType, stream.ScopeID); err != nil {
			return err
		}
		if revision != stream.Revision || !stream.Enabled {
			return governance_model.ErrConflict
		}
		event := &governance_model.AuditEvent{Type: "audit.stream_test", ScopeType: stream.ScopeType, ScopeID: stream.ScopeID, ObjectType: "audit_stream", ObjectID: id, Actor: actor, Result: "pending", ObjectPath: stream.Name}
		if err := governance_model.AppendAudit(ctx, event); err != nil {
			return err
		}
		exists, err := db.GetEngine(ctx).Where("stream_id = ? AND event_id = ?", id, event.EventID).Exist(new(governance_model.AuditDelivery))
		if err != nil || exists {
			return err
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		return db.Insert(ctx, &governance_model.AuditDelivery{StreamID: id, StreamRevision: stream.Revision, EventID: event.EventID, Payload: string(payload), LeaseToken: uuid.Nil.String()})
	})
}
