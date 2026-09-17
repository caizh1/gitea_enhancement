// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import "gitea.dev/models/db"

// Invitation 保存未接受邀请及持久化投递状态。令牌字段不通过 JSON 或审计公开。
type Invitation struct {
	ID                    int64  `xorm:"pk autoincr" json:"id"`
	ScopeType             string `xorm:"VARCHAR(16) UNIQUE(scope_email) NOT NULL" json:"scope_type"`
	ScopeID               int64  `xorm:"UNIQUE(scope_email) NOT NULL" json:"scope_id"`
	Email                 string `xorm:"VARCHAR(254) NOT NULL" json:"email"`
	LowerEmail            string `xorm:"VARCHAR(254) UNIQUE(scope_email) NOT NULL" json:"-"`
	ScopePath             string `xorm:"VARCHAR(2048) NOT NULL" json:"scope_path"`
	Inviter               Actor  `xorm:"JSON TEXT" json:"inviter"`
	Role                  Role   `xorm:"NOT NULL" json:"role"`
	CustomRoleID          int64  `json:"custom_role_id"`
	MembershipExpiresUnix int64  `json:"membership_expires_unix"`
	CreatedUnix           int64  `xorm:"NOT NULL" json:"created_unix"`
	ExpiresUnix           int64  `xorm:"INDEX NOT NULL" json:"expires_unix"`
	TokenHash             string `xorm:"VARCHAR(64) NOT NULL" json:"-"`
	TokenEncrypted        string `xorm:"TEXT NOT NULL" json:"-"`
	NextDeliveryUnix      int64  `xorm:"INDEX NOT NULL" json:"next_delivery_unix"`
	DeliveryStep          int    `xorm:"NOT NULL DEFAULT 0" json:"delivery_step"`
	DeliveryFailures      int    `xorm:"NOT NULL DEFAULT 0" json:"delivery_failures"`
	DeliveryState         string `xorm:"VARCHAR(32) NOT NULL" json:"delivery_state"`
	Lease                 string `xorm:"VARCHAR(64)" json:"-"`
	LeaseUntil            int64  `xorm:"INDEX NOT NULL DEFAULT 0" json:"-"`
	LastSentUnix          int64  `json:"last_sent_unix"`
}

func (*Invitation) TableName() string                { return "governance_invitation" }
func AddInvitations(engine db.EngineMigration) error { return engine.Sync(new(Invitation)) }
