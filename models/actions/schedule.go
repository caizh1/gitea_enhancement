// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"fmt"
	"time"

	"gitea.dev/models/db"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/git"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"
	webhook_module "gitea.dev/modules/webhook"
)

// ActionSchedule represents a schedule of a workflow file
type ActionSchedule struct {
	ID                          int64
	Title                       string
	Specs                       []string
	RepoID                      int64                  `xorm:"index"`
	Repo                        *repo_model.Repository `xorm:"-"`
	OwnerID                     int64                  `xorm:"index"`
	ScopeRevision               int64                  `xorm:"NOT NULL DEFAULT 0"`
	WorkflowID                  string
	WorkflowRepoID              int64            `xorm:"NOT NULL DEFAULT 0"`
	WorkflowCommitSHA           string           `xorm:"VARCHAR(64) NOT NULL DEFAULT ''"`
	WorkflowSourceScopeRevision int64            `xorm:"NOT NULL DEFAULT 0"`
	ScopedConfigRevisions       map[string]int64 `xorm:"JSON TEXT"`
	IsScopedRun                 bool             `xorm:"NOT NULL DEFAULT false"`
	TriggerUserID               int64
	TriggerUser                 *user_model.User `xorm:"-"`
	Ref                         string
	CommitSHA                   string
	Event                       webhook_module.HookEventType
	EventPayload                string `xorm:"LONGTEXT"`
	Content                     []byte
	Created                     timeutil.TimeStamp `xorm:"created"`
	Updated                     timeutil.TimeStamp `xorm:"updated"`
}

type scheduleBranchState struct {
	RepoID    int64
	Name      string
	CommitID  string
	IsDeleted bool
}

func (*scheduleBranchState) TableName() string { return "branch" }

func init() {
	db.RegisterModel(new(ActionSchedule))
}

// ScheduleBranchCurrent checks the persisted default branch used by a schedule.
func ScheduleBranchCurrent(ctx context.Context, repo *repo_model.Repository, ref, sha string) (bool, error) {
	if sha == "" || ref != git.RefNameFromBranch(repo.DefaultBranch).String() {
		return false, nil
	}
	if db.InTransaction(ctx) || !setting.Database.Type.IsSQLite3() {
		return LockScheduleBranchCurrent(ctx, repo, ref, sha)
	}
	return readScheduleBranch(ctx, repo, sha)
}

func readScheduleBranch(ctx context.Context, repo *repo_model.Repository, sha string) (bool, error) {
	var branch scheduleBranchState
	has, err := db.GetEngine(ctx).Where("repo_id = ? AND name = ?", repo.ID, repo.DefaultBranch).Get(&branch)
	if err != nil || !has {
		return false, err
	}
	return !branch.IsDeleted && branch.CommitID == sha, nil
}

// LockScheduleBranchCurrent serializes a schedule write with branch updates.
// Call only inside the transaction that replaces plans, inserts a run, or claims a job.
func LockScheduleBranchCurrent(ctx context.Context, repo *repo_model.Repository, ref, sha string) (bool, error) {
	if sha == "" || ref != git.RefNameFromBranch(repo.DefaultBranch).String() {
		return false, nil
	}
	if setting.Database.Type.IsSQLite3() {
		// SQLite's write lock serializes the following read with post-receive updates.
		if _, err := db.GetEngine(ctx).Where("repo_id = ? AND name = ? AND commit_id = ? AND is_deleted = ?", repo.ID, repo.DefaultBranch, sha, false).
			Cols("commit_id").Update(&scheduleBranchState{CommitID: sha}); err != nil {
			return false, err
		}
		return readScheduleBranch(ctx, repo, sha)
	}
	engine := db.GetEngine(ctx).Context(ctx).Engine()
	table := engine.Quote(engine.TableName(new(scheduleBranchState), true))
	query := fmt.Sprintf("SELECT commit_id, is_deleted FROM %s WHERE repo_id = ? AND name = ? FOR UPDATE", table)
	if setting.Database.Type.IsMSSQL() {
		query = fmt.Sprintf("SELECT commit_id, is_deleted FROM %s WITH (UPDLOCK, ROWLOCK) WHERE repo_id = ? AND name = ?", table)
	}
	var branch scheduleBranchState
	has, err := db.GetEngine(ctx).SQL(query, repo.ID, repo.DefaultBranch).Get(&branch)
	return has && !branch.IsDeleted && branch.CommitID == sha, err
}

// GetSchedulesMapByIDs returns the schedules by given id slice.
func GetSchedulesMapByIDs(ctx context.Context, ids []int64) (map[int64]*ActionSchedule, error) {
	schedules := make(map[int64]*ActionSchedule, len(ids))
	if len(ids) == 0 {
		return schedules, nil
	}
	return schedules, db.GetEngine(ctx).In("id", ids).Find(&schedules)
}

// CreateScheduleTask creates new schedule task.
func CreateScheduleTask(ctx context.Context, rows []*ActionSchedule) error {
	// Return early if there are no rows to insert
	if len(rows) == 0 {
		return nil
	}

	return db.WithTx(ctx, func(ctx context.Context) error {
		// Loop through each schedule row
		for _, row := range rows {
			row.Title = util.EllipsisDisplayString(row.Title, 255)
			// Create new schedule row
			if err := db.Insert(ctx, row); err != nil {
				return err
			}

			// Loop through each schedule spec and create a new spec row
			now := time.Now()

			for _, spec := range row.Specs {
				specRow := &ActionScheduleSpec{
					RepoID:     row.RepoID,
					ScheduleID: row.ID,
					Spec:       spec,
				}
				// Parse the spec and check for errors
				schedule, err := specRow.Parse()
				if err != nil {
					continue // skip to the next spec if there's an error
				}

				specRow.Next = timeutil.TimeStamp(schedule.Next(now).Unix())

				// Insert the new schedule spec row
				if err = db.Insert(ctx, specRow); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func DeleteScheduleTaskByRepo(ctx context.Context, id int64) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := db.GetEngine(ctx).Delete(&ActionSchedule{RepoID: id}); err != nil {
			return err
		}

		if _, err := db.GetEngine(ctx).Delete(&ActionScheduleSpec{RepoID: id}); err != nil {
			return err
		}

		return nil
	})
}

// CleanScheduleTasksByIDs removes replaced plans and settles runs without a current plan.
func CleanScheduleTasksByIDs(ctx context.Context, repoID int64, scheduleIDs []int64) ([]*ActionRunJob, error) {
	if len(scheduleIDs) == 0 {
		return nil, nil
	}
	var cancelledJobs []*ActionRunJob
	err := db.WithTx(ctx, func(ctx context.Context) error {
		if _, err := db.GetEngine(ctx).Where("repo_id = ?", repoID).In("schedule_id", scheduleIDs).Delete(new(ActionScheduleSpec)); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).Where("repo_id = ?", repoID).In("id", scheduleIDs).Delete(new(ActionSchedule)); err != nil {
			return err
		}
		var runs []*ActionRun
		if err := db.GetEngine(ctx).Where("action_run.repo_id = ?", repoID).
			And("action_run.trigger_event = ?", webhook_module.HookEventSchedule).
			And("NOT EXISTS (SELECT 1 FROM action_schedule WHERE action_schedule.id = action_run.schedule_id AND action_schedule.repo_id = action_run.repo_id)").
			In("action_run.status", StatusRunning, StatusWaiting, StatusBlocked, StatusCancelling).Find(&runs); err != nil {
			return err
		}
		for _, run := range runs {
			jobs, err := db.Find[ActionRunJob](ctx, FindRunJobOptions{RunID: run.ID})
			if err != nil {
				return err
			}
			cancelled, err := CancelJobs(ctx, jobs)
			if err != nil {
				return err
			}
			cancelledJobs = append(cancelledJobs, cancelled...)
			if len(cancelled) == 0 {
				if err := SettleRunAfterCancel(ctx, run); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return cancelledJobs, err
}

func CleanRepoScheduleTasks(ctx context.Context, repo *repo_model.Repository) ([]*ActionRunJob, error) {
	// If actions disabled when there is schedule task, this will remove the outdated schedule tasks
	// There is no other place we can do this because the app.ini will be changed manually
	if err := DeleteScheduleTaskByRepo(ctx, repo.ID); err != nil {
		return nil, fmt.Errorf("DeleteCronTaskByRepo: %v", err)
	}
	// cancel running cron jobs of this repository and delete old schedules
	jobs, err := CancelPreviousJobs(
		ctx,
		repo.ID,
		"",
		"",
		webhook_module.HookEventSchedule,
	)
	if err != nil {
		return jobs, fmt.Errorf("CancelPreviousJobs: %v", err)
	}
	return jobs, nil
}
