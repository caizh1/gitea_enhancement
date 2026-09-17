// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package user

import (
	"context"
	"strings"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

func SetMustChangePassword(ctx context.Context, all, mustChangePassword bool, include, exclude []string) (int64, error) {
	sliceTrimSpaceDropEmpty := func(input []string) []string {
		output := make([]string, 0, len(input))
		for _, in := range input {
			in = strings.ToLower(strings.TrimSpace(in))
			if in == "" {
				continue
			}
			output = append(output, in)
		}
		return output
	}

	var cond builder.Cond

	// Only include the users where something changes to get an accurate count
	cond = builder.Neq{"must_change_password": mustChangePassword}

	if !all {
		include = sliceTrimSpaceDropEmpty(include)
		if len(include) == 0 {
			return 0, util.ErrorWrap(util.ErrInvalidArgument, "no users to include provided")
		}

		cond = cond.And(builder.In("lower_name", include))
	}

	exclude = sliceTrimSpaceDropEmpty(exclude)
	if len(exclude) > 0 {
		cond = cond.And(builder.NotIn("lower_name", exclude))
	}

	var count int64
	err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		for {
			var users []*User
			if err := db.GetEngine(ctx).Where(cond).OrderBy("id").Limit(100).Find(&users); err != nil {
				return err
			}
			if len(users) == 0 {
				return nil
			}
			for _, user := range users {
				user.MustChangePassword = mustChangePassword
				if err := UpdateUserCols(ctx, user, "must_change_password"); err != nil {
					return err
				}
				count++
			}
		}
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
