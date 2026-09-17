// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package org

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	org_model "gitea.dev/models/organization"
	packages_model "gitea.dev/models/packages"
	"gitea.dev/services/governance"
	packages_service "gitea.dev/services/packages"
)

// DeleteScheduledGroup 共用原生数据库删除；外层事务提交后才允许资源清理任务删除文件。
func DeleteScheduledGroup(ctx context.Context, id int64, actor *governance_model.Actor, option governance.GroupDeletionOption) error {
	return governance.ProcessGroupDeletion(ctx, id, time.Now(), actor, option, func(ctx context.Context, groups []*governance_model.Namespace) error {
		slices.SortFunc(groups, func(a, b *governance_model.Namespace) int {
			return strings.Count(b.FullPath, "/") - strings.Count(a.FullPath, "/")
		})
		for _, group := range groups {
			org, err := org_model.GetOrgByID(ctx, group.ID)
			if err != nil {
				return err
			}
			if _, err := packages_service.RemoveAllPackages(ctx, group.ID); err != nil {
				return err
			}
			var packages []*packages_model.Package
			if err := db.GetEngine(ctx).Where("owner_id = ?", group.ID).Find(&packages); err != nil {
				return err
			}
			for _, pkg := range packages {
				if err := packages_model.DeleteAllProperties(ctx, packages_model.PropertyTypePackage, pkg.ID); err != nil {
					return err
				}
				if err := packages_model.DeletePackageByID(ctx, pkg.ID); err != nil {
					return err
				}
			}
			if err := DeleteOrganization(ctx, org, true); err != nil {
				return err
			}
		}
		return nil
	})
}

func RunScheduledGroupDeletions(ctx context.Context) error {
	var schedules []*governance_model.GroupDeletion
	if err := db.GetEngine(ctx).Where("due_unix <= ? AND next_attempt_unix <= ?", time.Now().Unix(), time.Now().Unix()).Asc("due_unix").Limit(100).Find(&schedules); err != nil {
		return err
	}
	var failures []error
	for _, schedule := range schedules {
		err := DeleteScheduledGroup(ctx, schedule.GroupID, nil, governance.GroupDeletionOption{})
		if err == nil || errors.Is(err, governance_model.ErrNotFound) {
			continue
		}
		// 坏任务延后重试，不能永久占住前一百条而饿死其他已到期群组。
		_, saveErr := db.GetEngine(ctx).ID(schedule.GroupID).Cols("next_attempt_unix").Incr("failures").Update(&governance_model.GroupDeletion{NextAttemptUnix: time.Now().Add(time.Minute).Unix()})
		if saveErr != nil {
			failures = append(failures, saveErr)
		}
		if !errors.Is(err, governance_model.ErrConflict) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
