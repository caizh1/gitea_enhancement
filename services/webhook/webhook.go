// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package webhook

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	webhook_model "gitea.dev/models/webhook"
	"gitea.dev/modules/git"
	"gitea.dev/modules/glob"
	"gitea.dev/modules/graceful"
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"
	"gitea.dev/modules/optional"
	"gitea.dev/modules/queue"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/util"
	webhook_module "gitea.dev/modules/webhook"
)

type Requester func(context.Context, *webhook_model.Webhook, *webhook_model.HookTask) (req *http.Request, body []byte, err error)

var webhookRequesters = map[webhook_module.HookType]Requester{}

func RegisterWebhookRequester(hookType webhook_module.HookType, requester Requester) {
	webhookRequesters[hookType] = requester
}

// IsValidHookTaskType returns true if a webhook registered
func IsValidHookTaskType(name string) bool {
	if name == webhook_module.GITEA || name == webhook_module.GOGS {
		return true
	}
	_, ok := webhookRequesters[name]
	return ok
}

// hookQueue is a global queue of web hooks
var hookQueue *queue.WorkerPoolQueue[int64]

// getPayloadRef returns the full ref name for hook event, if applicable.
func getPayloadRef(p api.Payloader) git.RefName {
	switch pp := p.(type) {
	case *api.CreatePayload:
		switch pp.RefType {
		case "branch":
			return git.RefNameFromBranch(pp.Ref)
		case "tag":
			return git.RefNameFromTag(pp.Ref)
		}
	case *api.DeletePayload:
		switch pp.RefType {
		case "branch":
			return git.RefNameFromBranch(pp.Ref)
		case "tag":
			return git.RefNameFromTag(pp.Ref)
		}
	case *api.PushPayload:
		return git.RefName(pp.Ref)
	}
	return ""
}

// EventSource represents the source of a webhook action. Repository and/or Owner must be set.
type EventSource struct {
	Repository *repo_model.Repository
	Owner      *user_model.User
}

// handle delivers hook tasks
func handler(items ...int64) []int64 {
	ctx := graceful.GetManager().HammerContext()

	for _, taskID := range items {
		task, err := webhook_model.GetHookTaskByID(ctx, taskID)
		if err != nil {
			if errors.Is(err, util.ErrNotExist) {
				log.Warn("GetHookTaskByID[%d] warn: %v", taskID, err)
			} else {
				log.Error("GetHookTaskByID[%d] failed: %v", taskID, err)
			}
			continue
		}

		if task.IsDelivered {
			// Already delivered in the meantime
			log.Trace("Task[%d] has already been delivered", task.ID)
			continue
		}

		if err := Deliver(ctx, task); err != nil {
			log.Error("Unable to deliver webhook task[%d]: %v", task.ID, err)
		}
	}

	return nil
}

func enqueueHookTask(taskID int64) error {
	err := hookQueue.Push(taskID)
	if err != nil && err != queue.ErrAlreadyInQueue {
		return err
	}
	return nil
}

func checkBranchFilter(branchFilter string, ref git.RefName) bool {
	if branchFilter == "" || branchFilter == "*" || branchFilter == "**" {
		return true
	}

	g, err := glob.Compile(branchFilter)
	if err != nil {
		// should not really happen as BranchFilter is validated
		log.Debug("checkBranchFilter failed to compile filer %q, err: %s", branchFilter, err)
		return false
	}

	if ref.IsBranch() && g.Match(ref.BranchName()) {
		return true
	}
	return g.Match(ref.String())
}

// PrepareTestWebhook always creates and enqueues a hook task for manual testing.
// Unlike PrepareWebhook, it ignores event subscriptions and branch filters so the
// Test Push Event control can verify delivery even when those gates would suppress
// a real event.
func PrepareTestWebhook(ctx context.Context, w *webhook_model.Webhook, event webhook_module.HookEventType, p api.Payloader) error {
	if setting.DisableWebhooks {
		return nil
	}

	payload, err := p.JSONPayload()
	if err != nil {
		return fmt.Errorf("JSONPayload for %s: %w", event, err)
	}

	return prepareSingleWebhook(ctx, w.ID, event, p, string(payload), true)
}

// PrepareWebhook creates a hook task and enqueues it for processing.
// The payload is saved as-is. The adjustments depending on the webhook type happen
// right before delivery, in the [Deliver] method.
func PrepareWebhook(ctx context.Context, w *webhook_model.Webhook, event webhook_module.HookEventType, p api.Payloader) error {
	payload, err := p.JSONPayload()
	if err != nil {
		return fmt.Errorf("JSONPayload for %s: %w", event, err)
	}
	return prepareSingleWebhook(ctx, w.ID, event, p, string(payload), false)
}

type hookEventScope struct {
	repoID, revision int64
	ownerIDs         []int64
}

func hookScopeForRepository(ctx context.Context, repo *repo_model.Repository) (hookEventScope, error) {
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	if err != nil {
		if errors.Is(err, governance_model.ErrNotFound) {
			return hookEventScope{repoID: repo.ID, revision: repo.ActionsScopeRevision, ownerIDs: []int64{repo.OwnerID}}, nil
		}
		return hookEventScope{}, err
	}
	scope := hookEventScope{repoID: repo.ID, revision: repo.ActionsScopeRevision}
	for _, namespace := range chain {
		if namespace.Kind == "group" || namespace.ID == repo.OwnerID {
			scope.ownerIDs = append(scope.ownerIDs, namespace.ID)
		}
	}
	return scope, nil
}

func hookAcceptsEvent(w *webhook_model.Webhook, event webhook_module.HookEventType, p api.Payloader) bool {
	if !w.HasEvent(event) {
		return false
	}
	if push, ok := p.(*api.PushPayload); ok && w.Type != webhook_module.GITEA && w.Type != webhook_module.GOGS && len(push.Commits) == 0 {
		return false
	}
	if ref := getPayloadRef(p); ref != "" && !checkBranchFilter(w.BranchFilter, ref) {
		return false
	}
	return true
}

func inheritsAncestorHookEvent(event webhook_module.HookEventType) bool {
	switch event {
	case webhook_module.HookEventPush,
		webhook_module.HookEventIssues, webhook_module.HookEventIssueAssign, webhook_module.HookEventIssueLabel, webhook_module.HookEventIssueMilestone, webhook_module.HookEventIssueComment,
		webhook_module.HookEventPullRequest, webhook_module.HookEventPullRequestAssign, webhook_module.HookEventPullRequestLabel, webhook_module.HookEventPullRequestMilestone,
		webhook_module.HookEventPullRequestComment, webhook_module.HookEventPullRequestReview, webhook_module.HookEventPullRequestReviewApproved,
		webhook_module.HookEventPullRequestReviewRejected, webhook_module.HookEventPullRequestReviewComment, webhook_module.HookEventPullRequestSync,
		webhook_module.HookEventPullRequestReviewRequest, webhook_module.HookEventWorkflowRun, webhook_module.HookEventWorkflowJob:
		return true
	default:
		return false
	}
}

func createScopedHookTask(ctx context.Context, w *webhook_model.Webhook, scope hookEventScope, event webhook_module.HookEventType, p api.Payloader, payload string, force bool) (int64, error) {
	if !w.IsActive || (!force && !hookAcceptsEvent(w, event, p)) {
		return 0, nil
	}
	task := &webhook_model.HookTask{HookID: w.ID, PayloadContent: payload, EventType: event, PayloadVersion: 2}
	if err := task.CaptureHook(w, scope.repoID, scope.revision, scope.ownerIDs); err != nil {
		return 0, err
	}
	task, err := webhook_model.CreateHookTask(ctx, task)
	if err != nil {
		return 0, err
	}
	return task.ID, nil
}

func prepareSingleWebhook(ctx context.Context, hookID int64, event webhook_module.HookEventType, p api.Payloader, payload string, force bool) error {
	if setting.DisableWebhooks {
		return nil
	}
	var taskID int64
	err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		w, err := webhook_model.GetWebhookByID(ctx, hookID)
		if err != nil {
			return err
		}
		if force {
			if err := webhook_model.RequireWebhookManager(ctx, w); err != nil {
				return err
			}
			if !w.IsActive {
				return governance_model.ErrConflict
			}
		}
		scope := hookEventScope{}
		if w.RepoID != 0 {
			repo, err := repo_model.GetRepositoryByID(ctx, w.RepoID)
			if err != nil {
				return err
			}
			scope, err = hookScopeForRepository(ctx, repo)
			if err != nil {
				return err
			}
		} else if w.OwnerID != 0 {
			scope.ownerIDs = []int64{w.OwnerID}
		}
		taskID, err = createScopedHookTask(ctx, w, scope, event, p, payload, force)
		return err
	})
	if err != nil || taskID == 0 {
		return err
	}
	return enqueueHookTask(taskID)
}

// PrepareWebhooks adds new webhooks to task queue for given payload.
func PrepareWebhooks(ctx context.Context, source EventSource, event webhook_module.HookEventType, p api.Payloader) error {
	if setting.DisableWebhooks {
		return nil
	}
	payload, err := p.JSONPayload()
	if err != nil {
		return fmt.Errorf("JSONPayload for %s: %w", event, err)
	}
	var taskIDs []int64
	err = governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		scope := hookEventScope{}
		var ws []*webhook_model.Webhook
		if source.Repository != nil {
			repo, err := repo_model.GetRepositoryByID(ctx, source.Repository.ID)
			if err != nil {
				return err
			}
			if repo.OwnerID != source.Repository.OwnerID || repo.ActionsScopeRevision != source.Repository.ActionsScopeRevision {
				return fmt.Errorf("%w: stale webhook event repository scope %d", governance_model.ErrConflict, repo.ID)
			}
			scope, err = hookScopeForRepository(ctx, repo)
			if err != nil {
				return err
			}
			ws, err = db.Find[webhook_model.Webhook](ctx, webhook_model.ListWebhookOptions{RepoID: repo.ID, IsActive: optional.Some(true)})
			if err != nil {
				return err
			}
		} else if source.Owner != nil {
			scope.ownerIDs = []int64{source.Owner.ID}
		}
		if len(scope.ownerIDs) > 0 {
			ownerIDs := scope.ownerIDs
			if source.Repository != nil && !inheritsAncestorHookEvent(event) {
				ownerIDs = []int64{source.Repository.OwnerID}
			}
			var ownerHooks []*webhook_model.Webhook
			if err := db.GetEngine(ctx).In("owner_id", ownerIDs).And("is_active = ?", true).Find(&ownerHooks); err != nil {
				return err
			}
			ws = append(ws, ownerHooks...)
		}
		systemHooks, err := webhook_model.GetSystemWebhooks(ctx, optional.Some(true))
		if err != nil {
			return err
		}
		ws = append(ws, systemHooks...)
		seen := make(map[int64]bool, len(ws))
		for _, w := range ws {
			if seen[w.ID] {
				continue
			}
			seen[w.ID] = true
			id, err := createScopedHookTask(ctx, w, scope, event, p, string(payload), false)
			if err != nil {
				return err
			}
			if id != 0 {
				taskIDs = append(taskIDs, id)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, id := range taskIDs {
		if err := enqueueHookTask(id); err != nil {
			return err
		}
	}
	return nil
}

// ReplayHookTask replays a webhook task
func ReplayHookTask(ctx context.Context, w *webhook_model.Webhook, uuid string) error {
	var taskID int64
	err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		current, err := webhook_model.GetWebhookByID(ctx, w.ID)
		if err != nil {
			return err
		}
		if err := webhook_model.RequireWebhookManager(ctx, current); err != nil {
			return err
		}
		if !current.IsActive {
			return governance_model.ErrConflict
		}
		original, err := webhook_model.GetHookTaskByUUID(ctx, current.ID, uuid)
		if err != nil {
			return err
		}
		if original.HookSnapshotEncrypted != "" {
			if _, err := original.HookSnapshot(); err != nil {
				return err
			}
		}
		scope := hookEventScope{}
		repoID := original.RepoID
		if repoID == 0 && original.HookSnapshotEncrypted == "" && original.PayloadVersion == 2 {
			var source struct {
				Repository struct {
					ID int64 `json:"id"`
				} `json:"repository"`
			}
			if err := json.Unmarshal([]byte(original.PayloadContent), &source); err == nil {
				repoID = source.Repository.ID
			}
		}
		if repoID != 0 {
			repo, err := repo_model.GetRepositoryByID(ctx, repoID)
			if err != nil {
				return err
			}
			scope, err = hookScopeForRepository(ctx, repo)
			if err != nil {
				return err
			}
			if (current.RepoID != 0 && current.RepoID != repo.ID) || (current.OwnerID != 0 && !slices.Contains(scope.ownerIDs, current.OwnerID)) {
				return governance_model.ErrConflict
			}
		} else if original.HookSnapshotEncrypted != "" {
			if err := json.Unmarshal([]byte(original.ScopeOwnerIDs), &scope.ownerIDs); err != nil {
				return err
			}
			if current.RepoID != 0 || (current.OwnerID != 0 && !slices.Contains(scope.ownerIDs, current.OwnerID)) {
				return governance_model.ErrConflict
			}
		} else if current.RepoID != 0 { // Legacy repository tasks have no saved source.
			repo, err := repo_model.GetRepositoryByID(ctx, current.RepoID)
			if err != nil {
				return err
			}
			scope, err = hookScopeForRepository(ctx, repo)
			if err != nil {
				return err
			}
		} else {
			return governance_model.ErrConflict // Legacy owner tasks cannot prove their source repository.
		}
		eventUUID := original.EventUUID
		if eventUUID == "" {
			eventUUID = original.UUID
		}
		newTask := &webhook_model.HookTask{HookID: current.ID, EventUUID: eventUUID, PayloadContent: original.PayloadContent, PayloadVersion: original.PayloadVersion, EventType: original.EventType}
		if err := newTask.CaptureHook(current, scope.repoID, scope.revision, scope.ownerIDs); err != nil {
			return err
		}
		newTask, err = webhook_model.CreateHookTask(ctx, newTask)
		if err != nil {
			return err
		}
		taskID = newTask.ID
		return nil
	})
	if err != nil {
		return err
	}
	return enqueueHookTask(taskID)
}
