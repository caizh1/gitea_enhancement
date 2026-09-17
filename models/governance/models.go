// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"errors"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/json"
)

var (
	ErrInvalid   = errors.New("治理参数无效")
	ErrConflict  = errors.New("数据已变化或相关合并尚未完成")
	ErrNotFound  = errors.New("治理资源不存在")
	ErrForbidden = errors.New("没有执行此操作的权限")
)

// Namespace 复用原有用户或组织 ID，公开路径独立于磁盘目录。
type Namespace struct {
	ID                     int64  `xorm:"pk" json:"id"`
	ParentID               int64  `xorm:"INDEX UNIQUE(parent_slug) NOT NULL DEFAULT 0" json:"parent_id"`
	Slug                   string `xorm:"VARCHAR(100) NOT NULL" json:"path"`
	LowerSlug              string `xorm:"VARCHAR(100) UNIQUE(parent_slug) NOT NULL" json:"-"`
	FullPath               string `xorm:"VARCHAR(2048) NOT NULL" json:"full_path"`
	LowerPath              string `xorm:"VARCHAR(2048) UNIQUE NOT NULL" json:"-"`
	Kind                   string `xorm:"VARCHAR(16) NOT NULL" json:"kind"`
	Visibility             int    `xorm:"NOT NULL DEFAULT 2" json:"visibility"`
	Archived               bool   `xorm:"NOT NULL DEFAULT false" json:"archived"`
	DeleteAfter            int64  `xorm:"INDEX NOT NULL DEFAULT 0" json:"delete_after"`
	DeleteActorID          int64  `json:"-"`
	Revision               int64  `xorm:"NOT NULL DEFAULT 1" json:"revision"`
	NativeOwnerTeamID      int64  `xorm:"NOT NULL DEFAULT 0" json:"native_owner_team_id,omitempty"`
	RestrictExternalShares bool   `xorm:"NOT NULL DEFAULT false" json:"restrict_external_shares"`
}

func (*Namespace) TableName() string { return "governance_namespace" }

// ResourcePath 同时占用群组和仓库地址，旧地址不能被重新注册。
type ResourcePath struct {
	Path       string `xorm:"VARCHAR(2048) pk" json:"path"`
	Kind       string `xorm:"VARCHAR(16) INDEX(resource) NOT NULL" json:"kind"`
	ResourceID int64  `xorm:"INDEX(resource) NOT NULL" json:"resource_id"`
	Alias      bool   `xorm:"NOT NULL DEFAULT false" json:"alias"`
}

func (*ResourcePath) TableName() string { return "governance_resource_path" }

type Membership struct {
	ID           int64  `xorm:"pk autoincr" json:"id"`
	ScopeType    string `xorm:"VARCHAR(16) UNIQUE(scope_user) NOT NULL" json:"scope_type"`
	ScopeID      int64  `xorm:"UNIQUE(scope_user) NOT NULL" json:"scope_id"`
	UserID       int64  `xorm:"UNIQUE(scope_user) INDEX NOT NULL" json:"user_id"`
	Role         Role   `xorm:"NOT NULL" json:"role"`
	CustomRoleID int64  `json:"custom_role_id"`
	ExpiresUnix  int64  `xorm:"INDEX NOT NULL DEFAULT 0" json:"expires_unix"`
}

func (*Membership) TableName() string { return "governance_membership" }

type Share struct {
	ID          int64  `xorm:"pk autoincr" json:"id"`
	ScopeType   string `xorm:"VARCHAR(16) UNIQUE(scope_group) NOT NULL" json:"scope_type"`
	ScopeID     int64  `xorm:"UNIQUE(scope_group) NOT NULL" json:"scope_id"`
	GroupID     int64  `xorm:"UNIQUE(scope_group) INDEX NOT NULL" json:"group_id"`
	MaxRole     Role   `xorm:"NOT NULL" json:"max_role"`
	ExpiresUnix int64  `xorm:"INDEX NOT NULL DEFAULT 0" json:"expires_unix"`
}

func (*Share) TableName() string { return "governance_share" }

type CustomRole struct {
	ID        int64    `xorm:"pk autoincr" json:"id"`
	RootID    int64    `xorm:"INDEX NOT NULL" json:"root_id"`
	Name      string   `xorm:"VARCHAR(100) NOT NULL" json:"name"`
	BaseRole  Role     `xorm:"NOT NULL" json:"base_role"`
	Abilities []string `xorm:"JSON TEXT" json:"abilities"`
}

func (*CustomRole) TableName() string { return "governance_custom_role" }

type ApprovalRule struct {
	SubjectsHidden bool    `xorm:"-" json:"subjects_hidden,omitempty"`
	ID             int64   `xorm:"pk autoincr" json:"id"`
	ScopeType      string  `xorm:"VARCHAR(16) INDEX(scope) NOT NULL" json:"scope_type"`
	ScopeID        int64   `xorm:"INDEX(scope) NOT NULL" json:"scope_id"`
	Name           string  `xorm:"VARCHAR(100) NOT NULL" json:"name"`
	Required       int     `xorm:"NOT NULL" json:"required"`
	UserIDs        []int64 `xorm:"JSON TEXT" json:"user_ids"`
	GroupIDs       []int64 `xorm:"JSON TEXT" json:"group_ids"`
	TeamIDs        []int64 `xorm:"JSON TEXT" json:"team_ids"`
	AllEligible    bool    `xorm:"NOT NULL DEFAULT false" json:"all_eligible"`
	// RespectNativeApprovalPool 将动态主体限制到目标分支当前的原生审批资格池。
	RespectNativeApprovalPool bool     `xorm:"NOT NULL DEFAULT false" json:"respect_native_approval_pool"`
	BranchMode                string   `xorm:"VARCHAR(20) NOT NULL" json:"branch_mode"`
	Branches                  []string `xorm:"JSON TEXT" json:"branches"`
	ProtectionIDs             []int64  `xorm:"JSON TEXT" json:"protection_ids"`
	NativeProtectionID        int64    `xorm:"INDEX NOT NULL DEFAULT 0" json:"native_protection_id"`
	NativeIgnoreStale         bool     `xorm:"NOT NULL DEFAULT false" json:"native_ignore_stale"`
	NativeDismissStale        bool     `xorm:"NOT NULL DEFAULT false" json:"native_dismiss_stale"`
	Enabled                   bool     `xorm:"NOT NULL DEFAULT true" json:"enabled"`
	Locked                    bool     `xorm:"NOT NULL DEFAULT true" json:"locked"`
	Revision                  int64    `xorm:"NOT NULL DEFAULT 1" json:"revision"`
}

func (*ApprovalRule) TableName() string { return "governance_approval_rule" }

type ApprovalSettings struct {
	Inherit                 bool   `xorm:"NOT NULL DEFAULT false" json:"inherit"`
	ID                      int64  `xorm:"pk autoincr" json:"id"`
	ScopeType               string `xorm:"VARCHAR(16) UNIQUE(scope) NOT NULL" json:"scope_type"`
	ScopeID                 int64  `xorm:"UNIQUE(scope) NOT NULL" json:"scope_id"`
	PreventAuthor           bool   `xorm:"NOT NULL DEFAULT true" json:"prevent_author"`
	PreventCommitter        bool   `xorm:"NOT NULL DEFAULT false" json:"prevent_committer"`
	PreventOverrides        bool   `xorm:"NOT NULL DEFAULT false" json:"prevent_overrides"`
	ResetOnChange           bool   `xorm:"NOT NULL DEFAULT true" json:"reset_on_change"`
	RequireReauthentication bool   `xorm:"NOT NULL DEFAULT false" json:"require_reauthentication"`
	Locked                  bool   `xorm:"NOT NULL DEFAULT false" json:"locked"`
	Revision                int64  `xorm:"NOT NULL DEFAULT 1" json:"revision"`
}

func (*ApprovalSettings) TableName() string { return "governance_approval_settings" }

type PullVersion struct {
	PullID     int64  `xorm:"pk" json:"pull_id"`
	Head       string `xorm:"VARCHAR(64) NOT NULL" json:"head"`
	BaseBranch string `xorm:"VARCHAR(255) NOT NULL" json:"base_branch"`
	PatchID    string `xorm:"VARCHAR(64) NOT NULL" json:"patch_id"`
	Generation int64  `xorm:"NOT NULL DEFAULT 1" json:"generation"`
}

func (*PullVersion) TableName() string { return "governance_pull_version" }

type ApprovalEvidence struct {
	ReviewID        int64  `xorm:"pk" json:"review_id"`
	RuleVersionID   int64  `xorm:"NOT NULL DEFAULT 0" json:"rule_version_id"`
	PullID          int64  `xorm:"INDEX NOT NULL" json:"pull_id"`
	UserID          int64  `xorm:"INDEX NOT NULL" json:"user_id"`
	Generation      int64  `xorm:"NOT NULL" json:"generation"`
	Head            string `xorm:"VARCHAR(64) NOT NULL" json:"head"`
	Reauthenticated bool   `xorm:"NOT NULL DEFAULT false" json:"reauthenticated"`
}

func (*ApprovalEvidence) TableName() string { return "governance_approval_evidence" }

type Actor struct {
	CredentialID int64  `json:"credential_id,omitempty"`
	ID           int64  `json:"id"`
	ActingAsID   int64  `json:"acting_as_id,omitempty"`
	ActingAsName string `json:"acting_as_name,omitempty"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	IP           string `json:"ip,omitempty"`
	Transport    string `json:"transport"`
	RequestID    string `json:"request_id"`
}

func (a Actor) EffectiveUserID() int64 {
	if a.ActingAsID > 0 {
		return a.ActingAsID
	}
	return a.ID
}

// AuditDetails 运行时保存 JSON 对象，接口文档不能将其误报为字节数组。
// swagger:type object
type AuditDetails json.Value

func (d AuditDetails) MarshalJSON() ([]byte, error) {
	if len(d) == 0 {
		return []byte("{}"), nil
	}
	return json.Value(d).MarshalJSON()
}
func (d *AuditDetails) UnmarshalJSON(raw []byte) error {
	var value json.Value
	if err := value.UnmarshalJSON(raw); err != nil {
		return err
	}
	*d = AuditDetails(value)
	return nil
}

type AuditEvent struct {
	ID          int64        `xorm:"pk autoincr" json:"sequence"`
	EventID     string       `xorm:"VARCHAR(36) UNIQUE NOT NULL" json:"id"`
	Version     int          `xorm:"NOT NULL DEFAULT 1" json:"schema_version"`
	Type        string       `xorm:"VARCHAR(100) INDEX(type_time) NOT NULL" json:"event_type"`
	OccurredAt  time.Time    `xorm:"INDEX(type_time) INDEX(scope_time) INDEX(actor_time) NOT NULL" json:"created_at"`
	ActorID     int64        `xorm:"INDEX(actor_time) NOT NULL" json:"author_id"`
	RequestID   string       `xorm:"VARCHAR(128) INDEX NOT NULL DEFAULT ''" json:"request_id"`
	Actor       Actor        `xorm:"JSON TEXT" json:"actor"`
	ScopeType   string       `xorm:"VARCHAR(16) INDEX(scope_time) NOT NULL" json:"scope_type"`
	ScopeID     int64        `xorm:"INDEX(scope_time) NOT NULL" json:"scope_id"`
	AncestorIDs []int64      `xorm:"JSON TEXT" json:"ancestor_ids"`
	ObjectType  string       `xorm:"VARCHAR(40) NOT NULL" json:"entity_type"`
	ObjectID    int64        `json:"entity_id"`
	ObjectPath  string       `xorm:"VARCHAR(2048)" json:"entity_path"`
	Result      string       `xorm:"VARCHAR(20) NOT NULL" json:"result"`
	Details     AuditDetails `xorm:"TEXT" json:"details"`
}

func (*AuditEvent) TableName() string { return "governance_audit_event" }

// AuditScope 保存事件发生时的范围，资源转移不会重写历史索引。
type AuditScope struct {
	ID            int64     `xorm:"pk autoincr"`
	EventSequence int64     `xorm:"UNIQUE(event_scope) INDEX NOT NULL"`
	ScopeType     string    `xorm:"VARCHAR(16) UNIQUE(event_scope) INDEX(query_scope) NOT NULL"`
	ScopeID       int64     `xorm:"UNIQUE(event_scope) INDEX(query_scope) NOT NULL"`
	OccurredAt    time.Time `xorm:"INDEX(query_scope) NOT NULL"`
}

func (*AuditScope) TableName() string { return "governance_audit_scope" }

type AuditStream struct {
	ID              int64             `xorm:"pk autoincr" json:"id"`
	ScopeType       string            `xorm:"VARCHAR(16) INDEX(scope) NOT NULL" json:"scope_type"`
	ScopeID         int64             `xorm:"INDEX(scope) NOT NULL" json:"scope_id"`
	Name            string            `xorm:"VARCHAR(100)" json:"name"`
	Kind            string            `xorm:"VARCHAR(20) NOT NULL" json:"kind"`
	Endpoint        string            `xorm:"VARCHAR(2048) NOT NULL" json:"endpoint"`
	Secret          string            `xorm:"TEXT" json:"-"`
	Headers         map[string]string `xorm:"JSON TEXT" json:"-"`
	EventTypes      []string          `xorm:"JSON TEXT" json:"event_types"`
	Enabled         bool              `xorm:"NOT NULL DEFAULT true" json:"enabled"`
	Revision        int64             `xorm:"NOT NULL DEFAULT 1" json:"revision"`
	Destination     AuditDestination  `xorm:"JSON TEXT" json:"destination"`
	DeliveredCount  int64             `xorm:"NOT NULL DEFAULT 0" json:"delivered_count"`
	LastDeliveredAt int64             `xorm:"NOT NULL DEFAULT 0" json:"last_delivered_at"`
	LastError       string            `xorm:"VARCHAR(200) NOT NULL DEFAULT ''" json:"last_error"`
}

// AuditDestination 只保存可公开配置；请求头与云凭据统一加密存入 Secret。
type AuditDestination struct {
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	LogID     string `json:"log_id,omitempty"`
}

func (*AuditStream) TableName() string { return "governance_audit_stream" }

type AuditDelivery struct {
	ID             int64  `xorm:"pk autoincr" json:"id"`
	StreamID       int64  `xorm:"UNIQUE(event_stream) INDEX(pending) NOT NULL" json:"stream_id"`
	StreamRevision int64  `xorm:"NOT NULL" json:"stream_revision"`
	EventID        string `xorm:"VARCHAR(36) UNIQUE(event_stream) NOT NULL" json:"event_id"`
	Payload        string `xorm:"TEXT NOT NULL" json:"-"`
	Attempts       int    `xorm:"NOT NULL DEFAULT 0" json:"attempts"`
	NextAttempt    int64  `xorm:"INDEX(pending) NOT NULL DEFAULT 0" json:"next_attempt"`
	LeaseUntil     int64  `xorm:"INDEX NOT NULL DEFAULT 0" json:"lease_until"`
	LeaseToken     string `xorm:"VARCHAR(36)" json:"-"`
	LastError      string `xorm:"TEXT" json:"last_error"`
}

func (*AuditDelivery) TableName() string { return "governance_audit_delivery" }

type MergeAuthorization struct {
	ObjectPath    string    `xorm:"VARCHAR(4096)" json:"object_path"`
	ApprovalProof []byte    `xorm:"BLOB" json:"approval_proof"`
	ID            string    `xorm:"VARCHAR(36) pk" json:"id"`
	PullID        int64     `xorm:"INDEX NOT NULL" json:"pull_id"`
	RepoID        int64     `xorm:"INDEX NOT NULL" json:"repo_id"`
	Head          string    `xorm:"VARCHAR(64) NOT NULL" json:"head"`
	OldTarget     string    `xorm:"VARCHAR(64) NOT NULL" json:"old_target"`
	NewTarget     string    `xorm:"VARCHAR(64) NOT NULL" json:"new_target"`
	Branch        string    `xorm:"VARCHAR(255) NOT NULL" json:"branch"`
	State         string    `xorm:"VARCHAR(20) INDEX NOT NULL" json:"state"`
	Actor         Actor     `xorm:"JSON TEXT" json:"actor"`
	AncestorIDs   []int64   `xorm:"JSON TEXT" json:"ancestor_ids"`
	CreatedAt     time.Time `xorm:"NOT NULL" json:"created_at"`
}

func (*MergeAuthorization) TableName() string { return "governance_merge_authorization" }

type Reservation struct {
	ID              int64  `xorm:"pk autoincr"`
	AuthorizationID string `xorm:"VARCHAR(36) UNIQUE(resource_operation) INDEX NOT NULL"`
	Resource        string `xorm:"VARCHAR(300) UNIQUE(resource_operation) INDEX NOT NULL"`
}

func (*Reservation) TableName() string { return "governance_reservation" }

type WriteLock struct {
	ID       int64 `xorm:"pk"`
	Revision int64 `xorm:"NOT NULL DEFAULT 0"`
}

func (*WriteLock) TableName() string { return "governance_write_lock" }

func Beans() []any {
	return []any{
		new(SSHKeyFileSync), new(Invitation), new(AccessRequest), new(AccessRequestSetting), new(RepositoryDeletion), new(PackageContentLock), new(GroupDeletion), new(LFSContentLock), new(ResourceCleanup), new(Namespace), new(ResourcePath), new(Membership), new(Share), new(CustomRole), new(ApprovalRule),
		new(ApprovalSettings), new(PullVersion), new(ApprovalEvidence), new(AuditEvent), new(AuditScope), new(AuditStream),
		new(AuditDelivery), new(MergeAuthorization), new(Reservation), new(WriteLock), new(AuditExport), new(AuditExportChunk), new(ReferenceTransaction), new(ReferenceReservation), new(ReferenceRevision), new(PullRuleVersion),
		new(RepositoryCreation), new(MirrorOperation),
	}
}

func init() {
	for _, bean := range Beans() {
		db.RegisterModel(bean)
	}
}
