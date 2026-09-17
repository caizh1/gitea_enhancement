// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package packages

import (
	"context"
	"fmt"
	"regexp"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

var ErrPackageCleanupRuleNotExist = util.NewNotExistErrorf("package blob does not exist")

func init() {
	db.RegisterModel(new(PackageCleanupRule))
}

// PackageCleanupRule represents a rule which describes when to clean up package versions
type PackageCleanupRule struct {
	ID                   int64              `xorm:"pk autoincr"`
	Enabled              bool               `xorm:"INDEX NOT NULL DEFAULT false"`
	OwnerID              int64              `xorm:"UNIQUE(s) INDEX NOT NULL DEFAULT 0"`
	Type                 Type               `xorm:"UNIQUE(s) INDEX NOT NULL"`
	KeepCount            int                `xorm:"NOT NULL DEFAULT 0"`
	KeepPattern          string             `xorm:"NOT NULL DEFAULT ''"`
	KeepPatternMatcher   *regexp.Regexp     `xorm:"-"`
	RemoveDays           int                `xorm:"NOT NULL DEFAULT 0"`
	RemovePattern        string             `xorm:"NOT NULL DEFAULT ''"`
	RemovePatternMatcher *regexp.Regexp     `xorm:"-"`
	MatchFullName        bool               `xorm:"NOT NULL DEFAULT false"`
	CreatedUnix          timeutil.TimeStamp `xorm:"created NOT NULL DEFAULT 0"`
	UpdatedUnix          timeutil.TimeStamp `xorm:"updated NOT NULL DEFAULT 0"`
}

func (pcr *PackageCleanupRule) CompiledPattern() error {
	if pcr.KeepPatternMatcher != nil || pcr.RemovePatternMatcher != nil {
		return nil
	}

	if pcr.KeepPattern != "" {
		var err error
		pcr.KeepPatternMatcher, err = regexp.Compile(fmt.Sprintf(`(?i)\A%s\z`, pcr.KeepPattern))
		if err != nil {
			return err
		}
	}

	if pcr.RemovePattern != "" {
		var err error
		pcr.RemovePatternMatcher, err = regexp.Compile(fmt.Sprintf(`(?i)\A%s\z`, pcr.RemovePattern))
		if err != nil {
			return err
		}
	}

	return nil
}

func InsertCleanupRule(ctx context.Context, pcr *PackageCleanupRule) (*PackageCleanupRule, error) {
	err := withPackageOwnerWrite(ctx, pcr.OwnerID, func(ctx context.Context) error {
		if err := db.Insert(ctx, pcr); err != nil {
			return err
		}
		return appendCleanupRuleAudit(ctx, pcr, "package.cleanup_rule_created", []string{"created"})
	})
	return pcr, err
}

func GetCleanupRuleByID(ctx context.Context, id int64) (*PackageCleanupRule, error) {
	pcr := &PackageCleanupRule{}

	has, err := db.GetEngine(ctx).ID(id).Get(pcr)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, ErrPackageCleanupRuleNotExist
	}
	return pcr, nil
}

func UpdateCleanupRule(ctx context.Context, pcr *PackageCleanupRule) error {
	return withPackageOwnerWrite(ctx, pcr.OwnerID, func(ctx context.Context) error {
		fresh, err := GetCleanupRuleByID(ctx, pcr.ID)
		if err != nil {
			return err
		}
		if fresh.OwnerID != pcr.OwnerID || fresh.Type != pcr.Type {
			return governance_model.ErrConflict
		}
		affected, err := db.GetEngine(ctx).ID(pcr.ID).AllCols().Update(pcr)
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrPackageCleanupRuleNotExist
		}
		return appendCleanupRuleAudit(ctx, pcr, "package.cleanup_rule_updated", []string{"enabled", "keep_count", "keep_pattern", "remove_days", "remove_pattern", "match_full_name"})
	})
}

func GetCleanupRulesByOwner(ctx context.Context, ownerID int64) ([]*PackageCleanupRule, error) {
	pcrs := make([]*PackageCleanupRule, 0, 10)
	return pcrs, db.GetEngine(ctx).Where("owner_id = ?", ownerID).Find(&pcrs)
}

func DeleteCleanupRuleByID(ctx context.Context, ruleID int64) error {
	rule, err := GetCleanupRuleByID(ctx, ruleID)
	if err != nil {
		return err
	}
	return withPackageOwnerWrite(ctx, rule.OwnerID, func(ctx context.Context) error {
		fresh, err := GetCleanupRuleByID(ctx, ruleID)
		if err != nil {
			return err
		}
		if count, err := db.GetEngine(ctx).ID(ruleID).Delete(&PackageCleanupRule{}); err != nil {
			return err
		} else if count != 1 {
			return ErrPackageCleanupRuleNotExist
		}
		return appendCleanupRuleAudit(ctx, fresh, "package.cleanup_rule_deleted", []string{"deleted"})
	})
}

func HasOwnerCleanupRuleForPackageType(ctx context.Context, ownerID int64, packageType Type) (bool, error) {
	return db.GetEngine(ctx).
		Where("owner_id = ? AND type = ?", ownerID, packageType).
		Exist(&PackageCleanupRule{})
}

func IterateEnabledCleanupRules(ctx context.Context, callback func(context.Context, *PackageCleanupRule) error) error {
	return db.Iterate(
		ctx,
		builder.Eq{"enabled": true},
		callback,
	)
}
