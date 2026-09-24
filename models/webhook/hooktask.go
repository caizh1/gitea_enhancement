// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"
	"gitea.dev/modules/secret"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	webhook_module "gitea.dev/modules/webhook"

	gouuid "github.com/google/uuid"
	"xorm.io/builder"
)

//   ___ ___                __   ___________              __
//  /   |   \  ____   ____ |  | _\__    ___/____    _____|  | __
// /    ~    \/  _ \ /  _ \|  |/ / |    |  \__  \  /  ___/  |/ /
// \    Y    (  <_> |  <_> )    <  |    |   / __ \_\___ \|    <
//  \___|_  / \____/ \____/|__|_ \ |____|  (____  /____  >__|_ \
//        \/                    \/              \/     \/     \/

// HookRequest represents hook task request information.
type HookRequest struct {
	URL        string            `json:"url"`
	HTTPMethod string            `json:"http_method"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}

// HookResponse represents hook task response information.
type HookResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// HookTask represents a hook task.
var ErrLegacyHookSnapshot = errors.New("legacy webhook task has no snapshot")

type HookTask struct {
	ID             int64  `xorm:"pk autoincr"`
	HookID         int64  `xorm:"index"`
	UUID           string `xorm:"unique"`
	EventUUID      string
	PayloadContent string `xorm:"LONGTEXT"`
	// PayloadVersion number to allow for smooth version upgrades:
	//  - PayloadVersion 1: PayloadContent contains the JSON as sent to the URL
	//  - PayloadVersion 2: PayloadContent contains the original event
	PayloadVersion        int   `xorm:"DEFAULT 1"`
	RepoID                int64 `xorm:"INDEX"`
	ScopeRevision         int64
	ScopeOwnerIDs         string `xorm:"TEXT"`
	HookRevision          int64
	HookSnapshotEncrypted string `xorm:"TEXT"`

	EventType   webhook_module.HookEventType
	IsDelivered bool
	Delivered   timeutil.TimeStampNano

	// History info.
	IsSucceed       bool
	RequestContent  string        `xorm:"LONGTEXT"`
	RequestInfo     *HookRequest  `xorm:"-"`
	ResponseContent string        `xorm:"LONGTEXT"`
	ResponseInfo    *HookResponse `xorm:"-"`
}

// CaptureHook preserves the delivery configuration selected for this event.
func (t *HookTask) CaptureHook(w *Webhook, repoID, scopeRevision int64, ownerIDs []int64) error {
	if w == nil || w.ID == 0 {
		return errors.New("invalid webhook snapshot")
	}
	data, err := json.Marshal(w)
	if err != nil {
		return err
	}
	ciphertext, err := secret.EncryptSecret(setting.SecretKey, string(data))
	if err != nil {
		return err
	}
	owners, err := json.Marshal(ownerIDs)
	if err != nil {
		return err
	}
	t.RepoID, t.ScopeRevision, t.ScopeOwnerIDs, t.HookRevision = repoID, scopeRevision, string(owners), w.ConfigRevision
	if t.EventUUID == "" {
		t.EventUUID = gouuid.New().String()
	}
	signature, err := t.snapshotSignature(ciphertext, "v2")
	if err != nil {
		return err
	}
	t.HookSnapshotEncrypted = "v2:" + ciphertext + ":" + hex.EncodeToString(signature)
	return nil
}

func (t *HookTask) snapshotSignature(ciphertext, version string) ([]byte, error) {
	data, err := json.Marshal(struct {
		Ciphertext     string
		HookID         int64
		RepoID         int64
		ScopeRevision  int64
		ScopeOwnerIDs  string
		HookRevision   int64
		PayloadContent string
		PayloadVersion int
		EventType      webhook_module.HookEventType
	}{ciphertext, t.HookID, t.RepoID, t.ScopeRevision, t.ScopeOwnerIDs, t.HookRevision, t.PayloadContent, t.PayloadVersion, t.EventType})
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte("gitea-webhook-snapshot-" + version + ":" + setting.SecretKey))
	mac := hmac.New(sha256.New, key[:])
	if _, err := mac.Write(data); err != nil {
		return nil, err
	}
	if version == "v2" {
		if _, err := mac.Write([]byte(t.EventUUID)); err != nil {
			return nil, err
		}
	}
	return mac.Sum(nil), nil
}

func (t *HookTask) HookSnapshot() (*Webhook, error) {
	if t.HookSnapshotEncrypted == "" {
		return nil, ErrLegacyHookSnapshot
	}
	parts := strings.Split(t.HookSnapshotEncrypted, ":")
	if len(parts) != 3 || (parts[0] != "v1" && parts[0] != "v2") {
		return nil, errors.New("invalid webhook snapshot format")
	}
	want, err := hex.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	got, err := t.snapshotSignature(parts[1], parts[0])
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(got, want) {
		return nil, errors.New("webhook snapshot integrity mismatch")
	}
	data, err := secret.DecryptSecret(setting.SecretKey, parts[1])
	if err != nil {
		return nil, err
	}
	w := new(Webhook)
	if err := json.Unmarshal([]byte(data), w); err != nil {
		return nil, err
	}
	if w.ID != t.HookID || w.ConfigRevision != t.HookRevision {
		return nil, errors.New("webhook snapshot identity mismatch")
	}
	w.AfterLoad()
	return w, nil
}

func init() {
	db.RegisterModel(new(HookTask))
}

// BeforeUpdate will be invoked by XORM before updating a record
// representing this object
func (t *HookTask) BeforeUpdate() {
	if t.RequestInfo != nil {
		t.RequestContent = t.simpleMarshalJSON(t.RequestInfo)
	}
	if t.ResponseInfo != nil {
		t.ResponseContent = t.simpleMarshalJSON(t.ResponseInfo)
	}
}

// AfterLoad updates the webhook object upon setting a column
func (t *HookTask) AfterLoad() {
	if len(t.RequestContent) > 0 {
		t.RequestInfo = &HookRequest{}
		if err := json.Unmarshal([]byte(t.RequestContent), t.RequestInfo); err != nil {
			log.Error("Unmarshal RequestContent[%d]: %v", t.ID, err)
		}
	}

	if len(t.ResponseContent) > 0 {
		t.ResponseInfo = &HookResponse{}
		if err := json.Unmarshal([]byte(t.ResponseContent), t.ResponseInfo); err != nil {
			log.Error("Unmarshal ResponseContent[%d]: %v", t.ID, err)
		}
	}
}

func (t *HookTask) simpleMarshalJSON(v any) string {
	p, err := json.Marshal(v)
	if err != nil {
		log.Error("Marshal [%d]: %v", t.ID, err)
	}
	return string(p)
}

// HookTasks returns a list of hook tasks by given conditions.
func HookTasks(ctx context.Context, hookID int64, page int) ([]*HookTask, error) {
	tasks := make([]*HookTask, 0, setting.Webhook.PagingNum)
	return tasks, db.GetEngine(ctx).
		Limit(setting.Webhook.PagingNum, (page-1)*setting.Webhook.PagingNum).
		Where("hook_id=?", hookID).
		Desc("id").
		Find(&tasks)
}

// CreateHookTask creates a new hook task,
// it handles conversion from Payload to PayloadContent.
func CreateHookTask(ctx context.Context, t *HookTask) (*HookTask, error) {
	t.UUID = gouuid.New().String()
	if t.EventUUID == "" {
		t.EventUUID = t.UUID
	}
	if t.Delivered == 0 {
		t.Delivered = timeutil.TimeStampNanoNow()
	}
	if t.PayloadVersion == 0 {
		return nil, errors.New("missing HookTask.PayloadVersion")
	}
	return t, db.Insert(ctx, t)
}

func GetHookTaskByID(ctx context.Context, id int64) (*HookTask, error) {
	t := &HookTask{}

	has, err := db.GetEngine(ctx).ID(id).Get(t)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrHookTaskNotExist{
			TaskID: id,
		}
	}
	return t, nil
}

func GetHookTaskByUUID(ctx context.Context, hookID int64, uuid string) (*HookTask, error) {
	task, exists, err := db.Get[HookTask](ctx, builder.Eq{"hook_id": hookID, "uuid": uuid})
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrHookTaskNotExist{HookID: hookID, UUID: uuid}
	}
	return task, nil
}

// UpdateHookTask updates information of hook task.
func UpdateHookTask(ctx context.Context, t *HookTask) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		stored, err := GetHookTaskByID(ctx, t.ID)
		if err != nil {
			return err
		}
		var hook Webhook
		exists, err := db.GetEngine(ctx).ID(stored.HookID).Get(&hook)
		if err != nil {
			return err
		}
		columns := []string{"is_delivered", "delivered", "is_succeed"}
		if exists {
			columns = append(columns, "request_content", "response_content")
			if stored.HookSnapshotEncrypted == "" { // Preserve legacy task update behavior.
				columns = append(columns, "payload_content")
			}
		}
		_, err = db.GetEngine(ctx).ID(t.ID).Cols(columns...).Update(t)
		return err
	})
}

// FindUndeliveredHookTaskIDs will find the next 100 undelivered hook tasks with ID greater than the provided lowerID
func FindUndeliveredHookTaskIDs(ctx context.Context, lowerID int64) ([]int64, error) {
	const batchSize = 100

	tasks := make([]int64, 0, batchSize)
	return tasks, db.GetEngine(ctx).
		Select("id").
		Table(new(HookTask)).
		Where("is_delivered=?", false).
		And("id > ?", lowerID).
		Asc("id").
		Limit(batchSize).
		Find(&tasks)
}

func MarkTaskDelivered(ctx context.Context, task *HookTask) (bool, error) {
	count, err := db.GetEngine(ctx).ID(task.ID).Where("is_delivered = ?", false).Cols("is_delivered").Update(&HookTask{
		ID:          task.ID,
		IsDelivered: true,
	})

	return count != 0, err
}

func CancelHookTask(ctx context.Context, taskID int64, reason string) error {
	response, err := json.Marshal(&HookResponse{Body: reason})
	if err != nil {
		return err
	}
	_, err = db.GetEngine(ctx).ID(taskID).Where("is_delivered = ?", false).
		Cols("is_delivered", "delivered", "is_succeed", "response_content").Update(&HookTask{
		IsDelivered: true, Delivered: timeutil.TimeStampNanoNow(), ResponseContent: string(response),
	})
	return err
}

// CancelPendingHookTasks makes a pause terminal for tasks not yet authorized to send.
func CancelPendingHookTasks(ctx context.Context, hookID int64, reason string) error {
	response, err := json.Marshal(&HookResponse{Body: reason})
	if err != nil {
		return err
	}
	_, err = db.GetEngine(ctx).Where("hook_id = ? AND is_delivered = ?", hookID, false).
		Cols("is_delivered", "delivered", "is_succeed", "response_content").Update(&HookTask{
		IsDelivered: true, Delivered: timeutil.TimeStampNanoNow(), ResponseContent: string(response),
	})
	return err
}

// RedactDeletedHookTasks retains delivery facts without payloads or credentials.
func RedactDeletedHookTasks(ctx context.Context, hookID int64) error {
	if err := CancelPendingHookTasks(ctx, hookID, "webhook deleted"); err != nil {
		return err
	}
	response, err := json.Marshal(&HookResponse{Body: "webhook deleted; delivery details redacted"})
	if err != nil {
		return err
	}
	_, err = db.GetEngine(ctx).Where("hook_id = ?", hookID).
		Cols("payload_content", "hook_snapshot_encrypted", "request_content", "response_content").
		Update(&HookTask{ResponseContent: string(response)})
	return err
}

// CleanupHookTaskTable deletes rows from hook_task as needed.
func CleanupHookTaskTable(ctx context.Context, cleanupType HookTaskCleanupType, olderThan time.Duration, numberToKeep int) error {
	log.Trace("Doing: CleanupHookTaskTable")

	switch cleanupType {
	case OlderThan:
		deleteOlderThan := time.Now().Add(-olderThan).UnixNano()
		deletes, err := db.GetEngine(ctx).
			Where("is_delivered = ? and delivered < ?", true, deleteOlderThan).
			Delete(new(HookTask))
		if err != nil {
			return err
		}
		log.Trace("Deleted %d rows from hook_task", deletes)
	case PerWebhook:
		hookIDs := make([]int64, 0, 10)
		err := db.GetEngine(ctx).
			Table("webhook").
			Where("id > 0").
			Cols("id").
			Find(&hookIDs)
		if err != nil {
			return err
		}
		for _, hookID := range hookIDs {
			select {
			case <-ctx.Done():
				return db.ErrCancelledf("Before deleting hook_task records for hook id %d", hookID)
			default:
			}
			if err = deleteDeliveredHookTasksByWebhook(ctx, hookID, numberToKeep); err != nil {
				return err
			}
		}
	}
	log.Trace("Finished: CleanupHookTaskTable")
	return nil
}

func deleteDeliveredHookTasksByWebhook(ctx context.Context, hookID int64, numberDeliveriesToKeep int) error {
	log.Trace("Deleting hook_task rows for webhook %d, keeping the most recent %d deliveries", hookID, numberDeliveriesToKeep)
	deliveryDates := make([]int64, 0, 10)
	err := db.GetEngine(ctx).Table("hook_task").
		Where("hook_task.hook_id = ? AND hook_task.is_delivered = ? AND hook_task.delivered is not null", hookID, true).
		Cols("hook_task.delivered").
		Join("INNER", "webhook", "hook_task.hook_id = webhook.id").
		OrderBy("hook_task.delivered desc").
		Limit(1, numberDeliveriesToKeep).
		Find(&deliveryDates)
	if err != nil {
		return err
	}

	if len(deliveryDates) > 0 {
		deletes, err := db.GetEngine(ctx).
			Where("hook_id = ? and is_delivered = ? and delivered <= ?", hookID, true, deliveryDates[0]).
			Delete(new(HookTask))
		if err != nil {
			return err
		}
		log.Trace("Deleted %d hook_task rows for webhook %d", deletes, hookID)
	} else {
		log.Trace("No hook_task rows to delete for webhook %d", hookID)
	}

	return nil
}
