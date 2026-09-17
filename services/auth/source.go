// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"fmt"

	"gitea.dev/models/auth"
	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	user_model "gitea.dev/models/user"
)

// DeleteSource deletes a AuthSource record in DB.
func DeleteSource(ctx context.Context, source *auth.Source) error {
	if db.InTransaction(ctx) {
		return fmt.Errorf("认证源注销不能嵌套在外层数据库事务中")
	}
	prepared, err := auth.PrepareSourceChange(source, nil)
	if err != nil {
		return err
	}
	return prepared.Commit(func() error {
		return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
			if err := auth.RequireSourceAdministrator(ctx); err != nil {
				return err
			}
			fresh, err := auth.GetSourceByID(ctx, source.ID)
			if err != nil {
				return err
			}
			equal, err := auth.SameSourceRevision(source, fresh)
			if err != nil {
				return err
			}
			if !equal {
				return governance_model.ErrConflict
			}
			return deleteSource(ctx, fresh)
		})
	})
}

func deleteSource(ctx context.Context, source *auth.Source) error {
	count, err := db.GetEngine(ctx).Count(&user_model.User{LoginSource: source.ID})
	if err != nil {
		return err
	} else if count > 0 {
		return auth.ErrSourceInUse{
			ID: source.ID,
		}
	}

	count, err = db.GetEngine(ctx).Count(&user_model.ExternalLoginUser{LoginSourceID: source.ID})
	if err != nil {
		return err
	} else if count > 0 {
		return auth.ErrSourceInUse{
			ID: source.ID,
		}
	}

	if count, err = db.GetEngine(ctx).ID(source.ID).Delete(new(auth.Source)); err != nil {
		return err
	} else if count != 1 {
		return auth.ErrSourceNotExist{ID: source.ID}
	}
	return auth.AppendSourceAudit(ctx, source, nil, "deleted")
}
