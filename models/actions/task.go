// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	runnerv1 "gitea.dev/actions-proto-go/runner/v1"
	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/actions/jobparser"
	"gitea.dev/modules/git"
	"gitea.dev/modules/globallock"
	"gitea.dev/modules/log"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	lru "github.com/hashicorp/golang-lru/v2"
	"google.golang.org/protobuf/types/known/timestamppb"
	"xorm.io/builder"
)

// ActionTask represents a distribution of job
type ActionTask struct {
	ID       int64
	JobID    int64
	Job      *ActionRunJob     `xorm:"-"`
	Steps    []*ActionTaskStep `xorm:"-"`
	Attempt  int64
	RunnerID int64              `xorm:"index"`
	Status   Status             `xorm:"index"`
	Started  timeutil.TimeStamp `xorm:"index"`
	Stopped  timeutil.TimeStamp `xorm:"index(stopped_log_expired)"`

	RepoID            int64  `xorm:"index"`
	OwnerID           int64  `xorm:"index"`
	CommitSHA         string `xorm:"index"`
	IsForkPullRequest bool

	Token          string `xorm:"-"`
	TokenHash      string `xorm:"UNIQUE"` // sha256 of token
	TokenSalt      string
	TokenLastEight string `xorm:"index token_last_eight"`

	LogFilename  string     // file name of log
	LogInStorage bool       // read log from database or from storage
	LogLength    int64      // lines count
	LogSize      int64      // blob size
	LogIndexes   LogIndexes `xorm:"LONGBLOB"`                   // line number to offset
	LogExpired   bool       `xorm:"index(stopped_log_expired)"` // files that are too old will be deleted

	Created timeutil.TimeStamp `xorm:"created"`
	Updated timeutil.TimeStamp `xorm:"updated index"`
}

// taskReportTimeout is how long a task may go without contact from its runner before the
// runner is assumed gone. Runners report state and stream logs every few seconds, both of
// which refresh ActionTask.Updated. Shorter than setting.Actions.ZombieTaskTimeout because
// it only decides whether the runner is reachable, not whether the task should be killed.
const taskReportTimeout = time.Minute

var successfulTokenTaskCache *lru.Cache[string, any]

func init() {
	db.RegisterModel(new(ActionTask), func() error {
		if setting.SuccessfulTokensCacheSize > 0 {
			var err error
			successfulTokenTaskCache, err = lru.New[string, any](setting.SuccessfulTokensCacheSize)
			if err != nil {
				return fmt.Errorf("unable to allocate Task cache: %v", err)
			}
		} else {
			successfulTokenTaskCache = nil
		}
		return nil
	})
}

func (task *ActionTask) Duration() time.Duration {
	return calculateDuration(task.Started, task.Stopped, task.Status, task.Updated)
}

func (task *ActionTask) IsStopped() bool {
	return task.Stopped > 0
}

func (task *ActionTask) GetRunJobLink() string {
	// Run.Repo can be nil when the repository was deleted while task/run rows remain
	// (TaskList.LoadAttributes copies job.Repo into run.Repo, leaving it nil on a miss).
	// Run.Link() already returns "" in that case, so guard here to avoid emitting a
	// broken relative "/jobs/N" link from the Sprintf below.
	if task.Job == nil || task.Job.Run == nil || task.Job.Run.Repo == nil {
		return ""
	}
	return fmt.Sprintf("%s/jobs/%d", task.Job.Run.Link(), task.Job.ID)
}

func (task *ActionTask) GetCommitLink() string {
	if task.Job == nil || task.Job.Run == nil || task.Job.Run.Repo == nil {
		return ""
	}
	return task.Job.Run.Repo.CommitLink(task.CommitSHA)
}

func (task *ActionTask) GetRepoName() string {
	if task.Job == nil || task.Job.Run == nil || task.Job.Run.Repo == nil {
		return ""
	}
	return task.Job.Run.Repo.FullName()
}

func (task *ActionTask) GetRepoLink() string {
	if task.Job == nil || task.Job.Run == nil || task.Job.Run.Repo == nil {
		return ""
	}
	return task.Job.Run.Repo.Link()
}

func (task *ActionTask) LoadJob(ctx context.Context) error {
	if task.Job == nil {
		job, err := GetRunJobByRepoAndID(ctx, task.RepoID, task.JobID)
		if err != nil {
			return err
		}
		task.Job = job
	}
	return nil
}

// LoadAttributes load Job Steps if not loaded
func (task *ActionTask) LoadAttributes(ctx context.Context) error {
	if err := task.LoadJob(ctx); err != nil {
		return err
	}

	if err := task.Job.LoadAttributes(ctx); err != nil {
		return err
	}

	if task.Steps == nil { // be careful, an empty slice (not nil) also means loaded
		steps, err := GetTaskStepsByTaskID(ctx, task.ID)
		if err != nil {
			return err
		}
		task.Steps = steps
	}

	return nil
}

func (task *ActionTask) GenerateAndFillToken() {
	task.Token, task.TokenSalt, task.TokenHash, task.TokenLastEight = generateSaltedToken()
}

func GetTaskByID(ctx context.Context, id int64) (*ActionTask, error) {
	var task ActionTask
	has, err := db.GetEngine(ctx).Where("id=?", id).Get(&task)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("task with id %d: %w", id, util.ErrNotExist)
	}

	return &task, nil
}

// TaskCredentialValid rejects credentials from workflows whose repository
// scope or lifecycle has changed since the task was issued.
func TaskCredentialValid(ctx context.Context, task *ActionTask) (bool, error) {
	if !task.Status.In(StatusRunning, StatusCancelling) || task.TokenSalt == "" {
		return false, nil
	}
	repo, err := repo_model.GetRepositoryByID(ctx, task.RepoID)
	if err != nil {
		if errors.Is(err, util.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if repo.OwnerID != task.OwnerID || repo.IsArchived || repo.Status != repo_model.RepositoryReady && repo.Status != repo_model.RepositoryPendingTransfer {
		return false, nil
	}
	unitEnabled, err := db.GetEngine(ctx).Where("repo_id = ? AND type = ?", repo.ID, unit.TypeActions).Exist(new(repo_model.RepoUnit))
	if err != nil || !unitEnabled {
		return false, err
	}
	active, err := ownerActionsActive(ctx, repo.OwnerID)
	if err != nil || !active {
		return false, err
	}
	runner, err := GetRunnerByID(ctx, task.RunnerID)
	if err != nil {
		if errors.Is(err, util.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	allowed, err := runnerCanUseRepo(ctx, runner, task.RepoID, task.OwnerID)
	if err != nil || runner.IsDisabled || !allowed {
		return false, err
	}
	job, err := GetRunJobByRepoAndID(ctx, task.RepoID, task.JobID)
	if err != nil {
		return false, err
	}
	run, err := GetRunByRepoAndID(ctx, task.RepoID, job.RunID)
	if err != nil {
		return false, err
	}
	if job.OwnerID != task.OwnerID || run.OwnerID != task.OwnerID || run.ScopeInvalidated || job.TaskID != task.ID {
		return false, nil
	}
	if run.IsScopedRun {
		valid, err := ScopedWorkflowRunValid(ctx, run)
		if err != nil || !valid {
			return false, err
		}
		trusted, err := RequiredScopedRunnerAllowed(ctx, run, runner)
		if err != nil || !trusted {
			return false, err
		}
	}
	triggerUserID, err := TaskEffectiveTriggerUserID(ctx, job, run)
	if err != nil || triggerUserID == 0 {
		return false, err
	}
	if triggerUserID == user_model.ActionsUserID {
		return ScheduledRunValid(ctx, run, repo)
	}
	triggerer, err := user_model.GetUserByID(ctx, triggerUserID)
	if err != nil || !triggerer.IsActive || triggerer.ProhibitLogin {
		if errors.Is(err, util.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// TaskEffectiveTriggerUserID uses the actor who started this attempt, not the original run.
func TaskEffectiveTriggerUserID(ctx context.Context, job *ActionRunJob, run *ActionRun) (int64, error) {
	if job.RunAttemptID == 0 {
		return run.TriggerUserID, nil
	}
	var attempt ActionRunAttempt
	has, err := db.GetEngine(ctx).ID(job.RunAttemptID).Get(&attempt)
	if err != nil || !has || attempt.RunID != run.ID {
		return 0, err
	}
	return attempt.TriggerUserID, nil
}

// ScheduledRunValid binds the system actor to the current persisted schedule scope.
func ScheduledRunValid(ctx context.Context, run *ActionRun, repo *repo_model.Repository) (bool, error) {
	if run.ScheduleID == 0 || run.RepoID != repo.ID || run.OwnerID != repo.OwnerID || run.ScopeInvalidated {
		return false, nil
	}
	var schedule ActionSchedule
	has, err := db.GetEngine(ctx).ID(run.ScheduleID).Get(&schedule)
	if err != nil || !has {
		return false, err
	}
	if schedule.RepoID != repo.ID || schedule.OwnerID != repo.OwnerID || schedule.TriggerUserID != user_model.ActionsUserID || schedule.ScopeRevision != repo.ActionsScopeRevision ||
		run.Ref != schedule.Ref || run.CommitSHA != schedule.CommitSHA || run.WorkflowID != schedule.WorkflowID || run.IsScopedRun != schedule.IsScopedRun {
		return false, nil
	}
	if schedule.IsScopedRun {
		if run.WorkflowRepoID != schedule.WorkflowRepoID || run.WorkflowCommitSHA != schedule.WorkflowCommitSHA ||
			run.WorkflowSourceScopeRevision != schedule.WorkflowSourceScopeRevision || !maps.Equal(run.ScopedConfigRevisions, schedule.ScopedConfigRevisions) {
			return false, nil
		}
		registrations, err := GetEffectiveScopedWorkflowSources(ctx, repo.OwnerID)
		if err != nil {
			return false, err
		}
		currentRevisions := make(map[string]int64)
		validRegistrations := make([]*ActionScopedWorkflowSource, 0)
		for _, registration := range registrations {
			if registration.SourceRepoID != schedule.WorkflowRepoID {
				continue
			}
			valid, err := ScopedWorkflowRegistrationValid(ctx, registration)
			if err != nil {
				return false, err
			}
			if valid {
				validRegistrations = append(validRegistrations, registration)
				currentRevisions[strconv.FormatInt(registration.ID, 10)] = registration.ConfigRevision
			}
		}
		if len(currentRevisions) == 0 || !maps.Equal(currentRevisions, schedule.ScopedConfigRevisions) {
			return false, nil
		}
		unit, err := repo.GetUnit(ctx, unit.TypeActions)
		if repo_model.IsErrUnitTypeNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if ScopedWorkflowOptedOut(unit.ActionsConfig(), validRegistrations, schedule.WorkflowRepoID, schedule.WorkflowID) {
			return false, nil
		}
		valid, err := ScopedWorkflowRunValid(ctx, run)
		if err != nil || !valid {
			return valid, err
		}
		source, err := repo_model.GetRepositoryByID(ctx, schedule.WorkflowRepoID)
		if err != nil {
			return false, err
		}
		current, err := ScheduleBranchCurrent(ctx, source, git.RefNameFromBranch(source.DefaultBranch).String(), schedule.WorkflowCommitSHA)
		if err != nil || !current {
			return current, err
		}
	} else if run.WorkflowCommitSHA != schedule.CommitSHA || run.WorkflowRepoID != 0 && run.WorkflowRepoID != repo.ID {
		return false, nil
	}
	return ScheduleBranchCurrent(ctx, repo, schedule.Ref, schedule.CommitSHA)
}

// ScheduledRunValidForWrite also holds the branch row through a run or claim transaction.
func ScheduledRunValidForWrite(ctx context.Context, run *ActionRun, repo *repo_model.Repository) (bool, error) {
	valid, err := ScheduledRunValid(ctx, run, repo)
	if err != nil || !valid {
		return valid, err
	}
	valid, err = LockScheduleBranchCurrent(ctx, repo, run.Ref, run.CommitSHA)
	if err != nil || !valid || !run.IsScopedRun {
		return valid, err
	}
	source, err := repo_model.GetRepositoryByID(ctx, run.WorkflowRepoID)
	if err != nil {
		return false, err
	}
	return LockScheduleBranchCurrent(ctx, source, git.RefNameFromBranch(source.DefaultBranch).String(), run.WorkflowCommitSHA)
}

func ownerActionsActive(ctx context.Context, ownerID int64) (bool, error) {
	owner, err := user_model.GetUserByID(ctx, ownerID)
	if err != nil {
		return false, err
	}
	if !owner.IsOrganization() && !owner.IsActive {
		return false, nil
	}
	chain, err := governance_model.Ancestors(ctx, ownerID)
	if errors.Is(err, governance_model.ErrNotFound) {
		return true, nil // legacy native owner before governance migration
	}
	if err != nil {
		return false, err
	}
	for _, ancestor := range chain {
		if ancestor.Archived || ancestor.DeleteAfter != 0 {
			return false, nil
		}
	}
	return true, nil
}

func runnerCanUseRepo(ctx context.Context, runner *ActionRunner, repoID, ownerID int64) (bool, error) {
	if runner.RepoID != 0 {
		return runner.RepoID == repoID, nil
	}
	if runner.OwnerID == 0 || runner.OwnerID == ownerID {
		return true, nil
	}
	chain, err := governance_model.Ancestors(ctx, ownerID)
	if errors.Is(err, governance_model.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, ancestor := range chain {
		if ancestor.ID == runner.OwnerID {
			return true, nil
		}
	}
	return false, nil
}

func GetRunningTaskByToken(ctx context.Context, token string) (*ActionTask, error) {
	errNotExist := fmt.Errorf("task token: %w", util.ErrNotExist)
	if token == "" {
		return nil, errNotExist
	}
	// A token is defined as being SHA1 sum these are 40 hexadecimal bytes long
	if len(token) != 40 {
		return nil, errNotExist
	}
	for _, x := range []byte(token) {
		if x < '0' || (x > '9' && x < 'a') || x > 'f' {
			return nil, errNotExist
		}
	}

	lastEight := token[len(token)-8:]

	if id := getTaskIDFromCache(token); id > 0 {
		task := &ActionTask{
			TokenLastEight: lastEight,
		}
		// Re-get the task from the db in case it has been deleted in the intervening period
		has, err := db.GetEngine(ctx).ID(id).Get(task)
		if err != nil {
			return nil, err
		}
		if has {
			valid, err := TaskCredentialValid(ctx, task)
			if err != nil || !valid {
				return nil, errNotExist
			}
			return task, nil
		}
		successfulTokenTaskCache.Remove(token)
	}

	var tasks []*ActionTask
	// Cancelling tasks are still authenticating — post-run cleanup steps need API access (artifact uploads, cache saves, etc.) before the runner finalizes the task.
	err := db.GetEngine(ctx).Where("token_last_eight = ? AND status IN (?, ?)", lastEight, StatusRunning, StatusCancelling).Find(&tasks)
	if err != nil {
		return nil, err
	} else if len(tasks) == 0 {
		return nil, errNotExist
	}

	for _, t := range tasks {
		tempHash := auth_model.HashToken(token, t.TokenSalt)
		if subtle.ConstantTimeCompare([]byte(t.TokenHash), []byte(tempHash)) == 1 {
			valid, err := TaskCredentialValid(ctx, t)
			if err != nil || !valid {
				return nil, errNotExist
			}
			if successfulTokenTaskCache != nil {
				successfulTokenTaskCache.Add(token, t.ID)
			}
			return t, nil
		}
	}
	return nil, errNotExist
}

func makeTaskStepDisplayName(step *jobparser.Step, limit int) (name string) {
	if step.Name != "" {
		name = step.Name // the step has an explicit name
	} else {
		// for unnamed step, its "String()" method tries to get a display name by its "name", "uses",
		// "run" or "id" (last fallback), we add the "Run " prefix for unnamed steps for better display
		// for multi-line "run" scripts, only use the first line to match GitHub's behavior
		// https://github.com/actions/runner/blob/66800900843747f37591b077091dd2c8cf2c1796/src/Runner.Worker/Handlers/ScriptHandler.cs#L45-L58
		runStr, _, _ := strings.Cut(strings.TrimSpace(step.Run), "\n")
		name = "Run " + util.IfZero(strings.TrimSpace(runStr), step.String())
	}
	return util.EllipsisDisplayString(name, limit) // database column has a length limit
}

// errJobAlreadyClaimed is a sentinel used inside claimJobForRunner to signal that
// another runner won the optimistic-lock race; it is never returned to callers.
var errJobAlreadyClaimed = errors.New("job already claimed by another runner")

// ErrTaskIneligible tells the claim loop to skip a candidate whose current
// permission no longer allows disclosure.
var ErrTaskIneligible = errors.New("Actions task is no longer eligible")

// pickTaskBatchSize bounds how many waiting jobs each CreateTaskForRunner query loads,
// so a large backlog is not fetched into memory on every runner poll.
// It is a var only so tests can shrink it to exercise pagination cheaply.
var pickTaskBatchSize = 100

// CreateTaskForRunner finds a waiting job that matches the runner's labels and
// atomically claims it. It iterates through all matching jobs so that a
// concurrent claim by another runner (which would lose the optimistic lock on
// job #1) does not leave the remaining jobs permanently unassigned.
func CreateTaskForRunner(ctx context.Context, runner *ActionRunner) (*ActionTask, bool, error) {
	return createTaskForRunner(ctx, runner, nil)
}

// CreateTaskForRunnerWithPayload builds the runner payload inside the claim's
// authorization transaction. The callback may only perform local reads.
func CreateTaskForRunnerWithPayload(ctx context.Context, runner *ActionRunner, build func(context.Context, *ActionTask) error) (*ActionTask, bool, error) {
	return createTaskForRunner(ctx, runner, build)
}

func createTaskForRunner(ctx context.Context, runner *ActionRunner, build func(context.Context, *ActionTask) error) (*ActionTask, bool, error) {
	if db.InTransaction(ctx) {
		return nil, false, errors.New("CreateTaskForRunner must not be called within a database transaction")
	}
	e := db.GetEngine(ctx)

	jobCond := builder.NewCond()
	if runner.RepoID != 0 {
		jobCond = builder.Eq{"repo_id": runner.RepoID}
	} else if runner.OwnerID != 0 {
		ownerIDs := []int64{runner.OwnerID}
		namespace, err := governance_model.GetNamespace(ctx, runner.OwnerID)
		if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
			return nil, false, err
		}
		if err == nil && namespace.Kind == "group" {
			var groups []*governance_model.Namespace
			if err := e.Where("kind = ? AND lower_path LIKE ?", "group", namespace.LowerPath+"/%").Find(&groups); err != nil {
				return nil, false, err
			}
			for _, group := range groups {
				if strings.HasPrefix(group.LowerPath, namespace.LowerPath+"/") {
					ownerIDs = append(ownerIDs, group.ID)
				}
			}
		}
		jobCond = builder.In("repo_id", builder.Select("`repository`.id").From("repository").
			Join("INNER", "repo_unit", "`repository`.id = `repo_unit`.repo_id").
			Where(builder.And(builder.In("`repository`.owner_id", ownerIDs), builder.Eq{"`repo_unit`.type": unit.TypeActions})))
	}
	baseCond := builder.Eq{"task_id": 0, "status": StatusWaiting, "is_reusable_caller": false}.And(jobCond)

	// TODO: a more efficient way to filter labels
	log.Trace("runner labels: %v", runner.AgentLabels)

	// Page through the waiting jobs oldest-first instead of loading the whole backlog into memory on every poll.
	// Keyset pagination on (updated, id) is safe under concurrent claims:
	// updated only moves forward, so the advancing cursor never skips a still-waiting job even as claimed jobs drop out.
	var cursorUpdated timeutil.TimeStamp
	var cursorID int64
	for {
		cond := baseCond
		if cursorID > 0 {
			cond = cond.And(builder.Or(
				builder.Gt{"updated": cursorUpdated},
				builder.And(builder.Eq{"updated": cursorUpdated}, builder.Gt{"id": cursorID}),
			))
		}

		var jobs []*ActionRunJob
		if err := e.Where(cond).Asc("updated", "id").Limit(pickTaskBatchSize).Find(&jobs); err != nil {
			return nil, false, err
		}

		for _, v := range jobs {
			if !runner.CanMatchLabels(v.RunsOn) {
				continue
			}
			task, ok, err := claimJobForRunnerWithPayload(ctx, runner, v, build)
			if err != nil {
				return nil, false, err
			}
			if ok {
				return task, true, nil
			}
			// Another runner claimed this job concurrently; try the next one.
		}

		// A short page means no waiting jobs remain beyond it.
		if len(jobs) < pickTaskBatchSize {
			return nil, false, nil
		}
		last := jobs[len(jobs)-1]
		cursorUpdated, cursorID = last.Updated, last.ID
	}
}

// claimJobForRunner attempts to atomically claim job for runner inside its own
// transaction. Returns (task, true, nil) on success, or (nil, false, nil) when
// another runner wins the optimistic-lock race (the caller should try the next
// candidate job).
func claimJobForRunner(ctx context.Context, runner *ActionRunner, job *ActionRunJob) (*ActionTask, bool, error) {
	return claimJobForRunnerWithPayload(ctx, runner, job, nil)
}

func claimJobForRunnerWithPayload(ctx context.Context, runner *ActionRunner, job *ActionRunJob, build func(context.Context, *ActionTask) error) (*ActionTask, bool, error) {
	var resultTask *ActionTask

	err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		e := db.GetEngine(ctx)
		freshRunner, err := GetRunnerByID(ctx, runner.ID)
		if err != nil || freshRunner.IsDisabled || freshRunner.OwnerID != runner.OwnerID || freshRunner.RepoID != runner.RepoID {
			if err != nil && !errors.Is(err, util.ErrNotExist) {
				return err
			}
			return errJobAlreadyClaimed
		}
		if freshRunner.Ephemeral {
			used, err := e.Where("runner_id = ?", freshRunner.ID).Exist(new(ActionTask))
			if err != nil {
				return err
			}
			if used {
				return errJobAlreadyClaimed
			}
		}
		freshJob, err := GetRunJobByRepoAndID(ctx, job.RepoID, job.ID)
		if err != nil {
			if errors.Is(err, util.ErrNotExist) {
				return errJobAlreadyClaimed
			}
			return err
		}
		if freshJob.TaskID != 0 || freshJob.Status != StatusWaiting || freshJob.IsReusableCaller || !freshRunner.CanMatchLabels(freshJob.RunsOn) {
			return errJobAlreadyClaimed
		}
		repo, err := repo_model.GetRepositoryByID(ctx, freshJob.RepoID)
		if err != nil {
			if errors.Is(err, util.ErrNotExist) {
				return errJobAlreadyClaimed
			}
			return err
		}
		run, err := GetRunByRepoAndID(ctx, repo.ID, freshJob.RunID)
		if err != nil {
			return err
		}
		allowed, err := runnerCanUseRepo(ctx, freshRunner, repo.ID, repo.OwnerID)
		if err != nil {
			return err
		}
		if repo.OwnerID != freshJob.OwnerID || repo.OwnerID != run.OwnerID || run.ScopeInvalidated || repo.IsArchived || repo.Status != repo_model.RepositoryReady ||
			run.Status.IsDone() || run.Status == StatusCancelling || run.NeedApproval && run.ApprovedBy == 0 ||
			!allowed {
			return errJobAlreadyClaimed
		}
		unitEnabled, err := e.Where("repo_id = ? AND type = ?", repo.ID, unit.TypeActions).Exist(new(repo_model.RepoUnit))
		if err != nil {
			return err
		}
		if !unitEnabled {
			return errJobAlreadyClaimed
		}
		active, err := ownerActionsActive(ctx, repo.OwnerID)
		if err != nil {
			return err
		}
		if !active {
			return errJobAlreadyClaimed
		}
		if run.IsScopedRun {
			valid, err := ScopedWorkflowRunValid(ctx, run)
			if err != nil {
				return err
			}
			if !valid {
				return errJobAlreadyClaimed
			}
			trusted, err := RequiredScopedRunnerAllowed(ctx, run, freshRunner)
			if err != nil {
				return err
			}
			if !trusted {
				return errJobAlreadyClaimed
			}
		}
		triggerUserID, err := TaskEffectiveTriggerUserID(ctx, freshJob, run)
		if err != nil {
			return err
		}
		if triggerUserID == 0 {
			return errJobAlreadyClaimed
		}
		if triggerUserID == user_model.ActionsUserID {
			valid, err := ScheduledRunValidForWrite(ctx, run, repo)
			if err != nil {
				return err
			}
			if !valid {
				return errJobAlreadyClaimed
			}
		}

		if err := freshJob.LoadAttributes(ctx); err != nil {
			return err
		}
		job = freshJob

		now := timeutil.TimeStampNow()
		job.Started = now
		job.Status = StatusRunning

		task := &ActionTask{
			JobID:             job.ID,
			Attempt:           job.Attempt,
			RunnerID:          runner.ID,
			Started:           now,
			Status:            StatusRunning,
			RepoID:            job.RepoID,
			OwnerID:           job.OwnerID,
			CommitSHA:         job.CommitSHA,
			IsForkPullRequest: job.IsForkPullRequest,
		}
		task.GenerateAndFillToken()

		workflowJob, err := job.ParseJob()
		if err != nil {
			return fmt.Errorf("load job %d: %w", job.ID, err)
		}

		if _, err := e.Insert(task); err != nil {
			return err
		}

		task.LogFilename = logFileName(job.Run.Repo.FullName(), task.ID)
		if err := UpdateTask(ctx, task, "log_filename"); err != nil {
			return err
		}

		if len(workflowJob.Steps) > 0 {
			steps := make([]*ActionTaskStep, len(workflowJob.Steps))
			for i, v := range workflowJob.Steps {
				steps[i] = &ActionTaskStep{
					Name:   makeTaskStepDisplayName(v, 255),
					TaskID: task.ID,
					Index:  int64(i),
					RepoID: task.RepoID,
					Status: StatusWaiting,
				}
			}
			if _, err := e.Insert(steps); err != nil {
				return err
			}
			task.Steps = steps
		}

		job.TaskID = task.ID
		n, err := UpdateRunJob(ctx, job, builder.And(builder.Eq{"task_id": 0}, builder.Eq{"status": StatusWaiting}))
		if err != nil {
			return err
		}
		if n != 1 {
			// Another runner claimed this job between our scan and this update;
			// signal the outer loop to move on without treating this as an error.
			return errJobAlreadyClaimed
		}

		task.Job = job
		valid, err := TaskCredentialValid(ctx, task)
		if err != nil {
			return err
		}
		if !valid {
			return errJobAlreadyClaimed
		}
		if build != nil {
			if err := build(ctx, task); err != nil {
				if errors.Is(err, ErrTaskIneligible) {
					return errJobAlreadyClaimed
				}
				return err
			}
		}
		resultTask = task
		return nil
	})

	if errors.Is(err, errJobAlreadyClaimed) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return resultTask, true, nil
}

// ReleaseTaskForRunner reverts a freshly-claimed but undelivered task: it deletes
// the task together with its steps and returns the job to the waiting queue. It is
// used when assembling the runner response fails after the job was already claimed,
// so the job is not stranded in running state with no runner ever executing it.
func ReleaseTaskForRunner(ctx context.Context, task *ActionTask) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		e := db.GetEngine(ctx)

		job, err := GetRunJobByRepoAndID(ctx, task.RepoID, task.JobID)
		if err != nil {
			return err
		}

		job.Status = StatusWaiting
		job.Started = 0
		job.TaskID = 0
		// Guard on task_id and status so we only release while the job still
		// references this task and has not progressed past running.
		n, err := UpdateRunJob(ctx, job, builder.Eq{"task_id": task.ID, "status": StatusRunning}, "status", "started", "task_id")
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("release task %d: job %d no longer references it", task.ID, task.JobID)
		}

		if _, err := e.Delete(&ActionTaskStep{TaskID: task.ID}); err != nil {
			return err
		}
		if _, err := e.ID(task.ID).Delete(&ActionTask{}); err != nil {
			return err
		}
		return nil
	})
}

func UpdateTask(ctx context.Context, task *ActionTask, cols ...string) error {
	sess := db.GetEngine(ctx).ID(task.ID)
	if len(cols) > 0 {
		sess.Cols(cols...)
	}
	_, err := sess.Update(task)

	// Automatically delete the ephemeral runner if the task is done
	if err == nil && task.Status.IsDone() && util.SliceContainsString(cols, "status") {
		return DeleteEphemeralRunner(ctx, task.RunnerID)
	}
	return err
}

func getRunIDByTaskID(ctx context.Context, taskID int64) (runID int64, _ error) {
	if has, err := db.GetEngine(ctx).Cols("action_run_job.run_id").
		Table("action_task").
		Join("INNER", "action_run_job", "action_run_job.id = action_task.job_id").
		Where(builder.Eq{"action_task.id": taskID}).Get(&runID); err != nil {
		return runID, err
	} else if !has {
		return runID, util.ErrNotExist
	}
	return runID, nil
}

// UpdateTaskByState updates the task by the state.
// It will always update the task if the state is not final, even there is no change.
// So it will update ActionTask.Updated to avoid the task being judged as a zombie task.
func UpdateTaskByState(ctx context.Context, runnerID int64, state *runnerv1.TaskState) (*ActionTask, error) {
	stepStates := map[int64]*runnerv1.StepState{}
	for _, v := range state.Steps {
		stepStates[v.Id] = v
	}

	// Only one request can update the task because the final state needs to be calculated with all job states.
	// Otherwise, concurrent requests with transaction will make the SQL read stale job state and result in wrong final state.
	taskID := state.Id
	runID, err := getRunIDByTaskID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	task := &ActionTask{}
	applyState := func(ctx context.Context) error {
		if has, err := db.GetEngine(ctx).ID(taskID).Get(task); err != nil {
			return err
		} else if !has {
			return util.ErrNotExist
		} else if runnerID != task.RunnerID {
			return errors.New("invalid runner for task")
		}

		if task.Status.IsDone() {
			// the state is final, do nothing
			return nil
		}

		// state.Result is not unspecified means the task is finished
		if state.Result != runnerv1.Result_RESULT_UNSPECIFIED {
			if task.Status == StatusCancelling {
				// The runner may report SUCCESS/FAILURE for the cleanup phase; preserve user intent.
				task.Status = StatusCancelled
			} else {
				task.Status = StatusFromResult(state.Result)
			}
			task.Stopped = timeutil.TimeStamp(state.StoppedAt.AsTime().Unix())
			if err := UpdateTask(ctx, task, "status", "stopped"); err != nil {
				return err
			}
			if _, err := UpdateRunJob(ctx, &ActionRunJob{
				ID:      task.JobID,
				RepoID:  task.RepoID,
				Status:  task.Status,
				Stopped: task.Stopped,
			}, nil, "status", "stopped"); err != nil {
				return err
			}
		} else {
			// Force update ActionTask.Updated to avoid the task being judged as a zombie task
			task.Updated = timeutil.TimeStampNow()
			if err := UpdateTask(ctx, task, "updated"); err != nil {
				return err
			}
		}

		if err := task.LoadAttributes(ctx); err != nil {
			return err
		}

		for _, step := range task.Steps {
			var result runnerv1.Result
			if v, ok := stepStates[step.Index]; ok {
				result = v.Result
				step.LogIndex = v.LogIndex
				step.LogLength = v.LogLength
				step.Started = convertTimestamp(v.StartedAt)
				step.Stopped = convertTimestamp(v.StoppedAt)
			}
			if result != runnerv1.Result_RESULT_UNSPECIFIED {
				step.Status = StatusFromResult(result)
			} else if step.Started != 0 {
				step.Status = StatusRunning
			}
			if _, err := db.GetEngine(ctx).ID(step.ID).Update(step); err != nil {
				return err
			}
		}
		return nil
	}
	err = globallock.LockAndDo(ctx, fmt.Sprintf("UpdateTaskByState-run-%d", runID), func(ctx context.Context) error {
		// A half-written report leaves the task done with a running job, which no retry repairs.
		return db.WithTx(ctx, applyState)
	})
	return task, err
}

func StopTask(ctx context.Context, taskID int64, status Status) error {
	if !status.IsDone() && status != StatusCancelling {
		return fmt.Errorf("cannot stop task with status %v", status)
	}
	e := db.GetEngine(ctx)

	task := &ActionTask{}
	if has, err := e.ID(taskID).Get(task); err != nil {
		return err
	} else if !has {
		return util.ErrNotExist
	}
	if task.Status.IsDone() {
		return nil
	}

	now := timeutil.TimeStampNow()
	if status == StatusCancelling {
		runner, err := GetRunnerByID(ctx, task.RunnerID)
		if err != nil {
			if !errors.Is(err, util.ErrNotExist) {
				return err
			}
			status = StatusCancelled
		} else if !runner.HasCancellingSupport {
			status = StatusCancelled
		} else if task.Updated.AddDuration(taskReportTimeout) < now {
			// A runner that stopped reporting will never acknowledge the cancellation either,
			// so skip the handshake instead of waiting for the zombie task cleanup.
			status = StatusCancelled
		}
	}

	if status == StatusCancelling {
		task.Status = StatusCancelling

		if _, err := UpdateRunJob(ctx, &ActionRunJob{
			ID:     task.JobID,
			RepoID: task.RepoID,
			Status: StatusCancelling,
		}, nil, "status"); err != nil {
			return err
		}

		// NoAutoTime keeps "updated" at the runner's last contact: re-cancelling an already
		// cancelling task must not defer the timeout above or the zombie task cleanup.
		_, err := e.ID(task.ID).Cols("status").NoAutoTime().Update(task)
		return err
	}

	task.Status = status
	task.Stopped = now
	if _, err := UpdateRunJob(ctx, &ActionRunJob{
		ID:      task.JobID,
		RepoID:  task.RepoID,
		Status:  task.Status,
		Stopped: task.Stopped,
	}, nil); err != nil {
		return err
	}

	if err := UpdateTask(ctx, task, "status", "stopped"); err != nil {
		return err
	}

	if err := task.LoadAttributes(ctx); err != nil {
		return err
	}

	for _, step := range task.Steps {
		if !step.Status.IsDone() {
			step.Status = status
			if step.Started == 0 {
				step.Started = now
			}
			step.Stopped = now
		}
		if _, err := e.ID(step.ID).Update(step); err != nil {
			return err
		}
	}

	return nil
}

func FindOldTasksToExpire(ctx context.Context, olderThan timeutil.TimeStamp, limit int) ([]*ActionTask, error) {
	e := db.GetEngine(ctx)

	tasks := make([]*ActionTask, 0, limit)
	// Check "stopped > 0" to avoid deleting tasks that are still running
	return tasks, e.Where("stopped > 0 AND stopped < ? AND log_expired = ?", olderThan, false).
		Limit(limit).
		Find(&tasks)
}

func convertTimestamp(timestamp *timestamppb.Timestamp) timeutil.TimeStamp {
	if timestamp.GetSeconds() == 0 && timestamp.GetNanos() == 0 {
		return timeutil.TimeStamp(0)
	}
	return timeutil.TimeStamp(timestamp.AsTime().Unix())
}

func logFileName(repoFullName string, taskID int64) string {
	ret := fmt.Sprintf("%s/%02x/%d.log", repoFullName, taskID%256, taskID)

	if setting.Actions.LogCompression.IsZstd() {
		ret += ".zst"
	}

	return ret
}

func getTaskIDFromCache(token string) int64 {
	if successfulTokenTaskCache == nil {
		return 0
	}
	tInterface, ok := successfulTokenTaskCache.Get(token)
	if !ok {
		return 0
	}
	t, ok := tInterface.(int64)
	if !ok {
		return 0
	}
	return t
}
