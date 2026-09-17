// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"bytes"
	"context"
	"fmt"
	"reflect"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/log"
	"gitea.dev/modules/optional"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
	"xorm.io/xorm"
	"xorm.io/xorm/convert"
)

// Type represents an login type.
type Type int

// Note: new type must append to the end of list to maintain compatibility.
const (
	NoType Type = iota
	Plain       // 1
	LDAP        // 2
	SMTP        // 3
	PAM         // 4
	DLDAP       // 5
	OAuth2      // 6
	SSPI        // 7
)

// String returns the string name of the LoginType
func (typ Type) String() string {
	return Names[typ]
}

// Int returns the int value of the LoginType
func (typ Type) Int() int {
	return int(typ)
}

// Names contains the name of LoginType values.
var Names = map[Type]string{
	LDAP:   "LDAP (via BindDN)",
	DLDAP:  "LDAP (simple auth)", // Via direct bind
	SMTP:   "SMTP",
	PAM:    "PAM",
	OAuth2: "OAuth2",
	SSPI:   "SPNEGO with SSPI",
}

// Config represents login config as far as the db is concerned
type Config interface {
	convert.Conversion
	SetAuthSource(*Source)
}

// AuditableSourceConfig 返回显式白名单配置；不得包含密码、令牌或完整认证 URL。
type AuditableSourceConfig interface {
	AuditSourceConfig() map[string]any
}

type ConfigBase struct {
	AuthSource *Source
}

func (p *ConfigBase) SetAuthSource(s *Source) {
	p.AuthSource = s
}

// SkipVerifiable configurations provide a IsSkipVerify to check if SkipVerify is set
type SkipVerifiable interface {
	IsSkipVerify() bool
}

// HasTLSer configurations provide a HasTLS to check if TLS can be enabled
type HasTLSer interface {
	HasTLS() bool
}

// UseTLSer configurations provide a HasTLS to check if TLS is enabled
type UseTLSer interface {
	UseTLS() bool
}

// SSHKeyProvider configurations provide ProvidesSSHKeys to check if they provide SSHKeys
type SSHKeyProvider interface {
	ProvidesSSHKeys() bool
}

// RegisterableSource configurations provide RegisterSource which needs to be run on creation
type RegisterableSource interface {
	RegisterSource() error
	UnregisterSource() error
}

// PreparedSourceChange 在外部发现完成后，负责用注册表写锁包住短数据库事务与不可失败的内存发布。
type PreparedSourceChange interface {
	Commit(func() error) error
}

// TransactionalRegisterableSource 保证认证源数据库、审计与进程内注册表一致切换。
type TransactionalRegisterableSource interface {
	PrepareSourceChange(previous, next *Source) (PreparedSourceChange, error)
}

type directSourceChange struct{}

func (directSourceChange) Commit(commit func() error) error { return commit() }

// PrepareSourceChange 在治理锁外完成可能发生网络访问的认证提供者准备。
func PrepareSourceChange(previous, next *Source) (PreparedSourceChange, error) {
	var cfg Config
	if next != nil {
		cfg = next.Cfg
	} else if previous != nil {
		cfg = previous.Cfg
	}
	if cfg == nil {
		return directSourceChange{}, nil
	}
	if next != nil {
		next.Cfg.SetAuthSource(next)
	}
	if prepared, ok := cfg.(TransactionalRegisterableSource); ok {
		return prepared.PrepareSourceChange(previous, next)
	}
	if _, ok := cfg.(RegisterableSource); ok {
		return nil, fmt.Errorf("认证源不支持事务化注册")
	}
	return directSourceChange{}, nil
}

// RequireSourceAdministrator 在提交点重新核对管理员及代办原身份，不能只信请求入口。
func RequireSourceAdministrator(ctx context.Context) error {
	actor := governance_model.AuditActor(ctx)
	if actor.EffectiveUserID() <= 0 {
		return nil
	}
	type administrator struct {
		ID            int64
		IsAdmin       bool
		IsActive      bool
		ProhibitLogin bool
	}
	check := func(id int64) error {
		var user administrator
		has, err := db.GetEngine(ctx).Table("user").ID(id).Get(&user)
		if err != nil {
			return err
		}
		if !has || !user.IsAdmin || !user.IsActive || user.ProhibitLogin {
			return governance_model.ErrForbidden
		}
		return nil
	}
	if err := check(actor.EffectiveUserID()); err != nil {
		return err
	}
	if actor.ActingAsID > 0 {
		return check(actor.ID)
	}
	return nil
}

var registeredConfigs = map[Type]func() Config{}

// RegisterTypeConfig register a config for a provided type
func RegisterTypeConfig(typ Type, exemplar Config) {
	if reflect.TypeOf(exemplar).Kind() == reflect.Pointer {
		// Pointer:
		registeredConfigs[typ] = func() Config {
			return reflect.New(reflect.ValueOf(exemplar).Elem().Type()).Interface().(Config)
		}
		return
	}

	// Not a Pointer
	registeredConfigs[typ] = func() Config {
		return reflect.New(reflect.TypeOf(exemplar)).Elem().Interface().(Config)
	}
}

// Source represents an external way for authorizing users.
type Source struct {
	ID              int64 `xorm:"pk autoincr"`
	Type            Type
	Name            string `xorm:"UNIQUE"` // it can be the OIDC's provider name, see services/auth/source/oauth2/source_register.go: RegisterSource
	IsActive        bool   `xorm:"INDEX NOT NULL DEFAULT false"`
	IsSyncEnabled   bool   `xorm:"INDEX NOT NULL DEFAULT false"`
	TwoFactorPolicy string `xorm:"two_factor_policy NOT NULL DEFAULT ''"`
	Cfg             Config `xorm:"TEXT"`

	CreatedUnix timeutil.TimeStamp `xorm:"INDEX created"`
	UpdatedUnix timeutil.TimeStamp `xorm:"INDEX updated"`
}

// TableName xorm will read the table name from this method
func (Source) TableName() string {
	return "login_source"
}

func init() {
	db.RegisterModel(new(Source))
}

// BeforeSet is invoked from XORM before setting the value of a field of this object.
func (source *Source) BeforeSet(colName string, val xorm.Cell) {
	if colName == "type" {
		typ, _, err := db.CellToInt(val, NoType)
		if err != nil {
			setting.PanicInDevOrTesting("Unable to convert login source (id=%d) type: %v", source.ID, err)
		}
		constructor, ok := registeredConfigs[typ]
		if !ok {
			return
		}
		source.Cfg = constructor()
		source.Cfg.SetAuthSource(source)
	}
}

// TypeName return name of this login source type.
func (source *Source) TypeName() string {
	return Names[source.Type]
}

// IsLDAP returns true of this source is of the LDAP type.
func (source *Source) IsLDAP() bool {
	return source.Type == LDAP
}

// IsDLDAP returns true of this source is of the DLDAP type.
func (source *Source) IsDLDAP() bool {
	return source.Type == DLDAP
}

// IsSMTP returns true of this source is of the SMTP type.
func (source *Source) IsSMTP() bool {
	return source.Type == SMTP
}

// IsPAM returns true of this source is of the PAM type.
func (source *Source) IsPAM() bool {
	return source.Type == PAM
}

// IsOAuth2 returns true of this source is of the OAuth2 type.
func (source *Source) IsOAuth2() bool {
	return source.Type == OAuth2
}

// IsSSPI returns true of this source is of the SSPI type.
func (source *Source) IsSSPI() bool {
	return source.Type == SSPI
}

// HasTLS returns true of this source supports TLS.
func (source *Source) HasTLS() bool {
	hasTLSer, ok := source.Cfg.(HasTLSer)
	return ok && hasTLSer.HasTLS()
}

// UseTLS returns true of this source is configured to use TLS.
func (source *Source) UseTLS() bool {
	useTLSer, ok := source.Cfg.(UseTLSer)
	return ok && useTLSer.UseTLS()
}

// SkipVerify returns true if this source is configured to skip SSL
// verification.
func (source *Source) SkipVerify() bool {
	skipVerifiable, ok := source.Cfg.(SkipVerifiable)
	return ok && skipVerifiable.IsSkipVerify()
}

func (source *Source) TwoFactorShouldSkip() bool {
	return source.TwoFactorPolicy == "skip"
}

// CreateSource inserts a AuthSource in the DB if not already
// existing with the given name.
func CreateSource(ctx context.Context, source *Source) error {
	if db.InTransaction(ctx) {
		return fmt.Errorf("认证源注册不能嵌套在外层数据库事务中")
	}
	has, err := db.GetEngine(ctx).Where("name=?", source.Name).Exist(new(Source))
	if err != nil {
		return err
	} else if has {
		return ErrSourceAlreadyExist{source.Name}
	}
	// Synchronization is only available with LDAP for now
	if !source.IsLDAP() && !source.IsOAuth2() {
		source.IsSyncEnabled = false
	}

	candidate := *source
	prepared, err := PrepareSourceChange(nil, &candidate)
	if err != nil {
		return err
	}
	err = prepared.Commit(func() error {
		return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
			if err := RequireSourceAdministrator(ctx); err != nil {
				return err
			}
			has, err := db.GetEngine(ctx).Where("name=?", candidate.Name).Exist(new(Source))
			if err != nil {
				return err
			}
			if has {
				return ErrSourceAlreadyExist{candidate.Name}
			}
			if err := db.Insert(ctx, &candidate); err != nil {
				return err
			}
			return AppendSourceAudit(ctx, nil, &candidate, "created")
		})
	})
	if err == nil {
		*source = candidate
		source.Cfg.SetAuthSource(source)
	}
	return err
}

type FindSourcesOptions struct {
	db.ListOptions
	IsActive  optional.Option[bool]
	LoginType Type
}

func (opts FindSourcesOptions) ToConds() builder.Cond {
	conds := builder.NewCond()
	if opts.IsActive.Has() {
		conds = conds.And(builder.Eq{"is_active": opts.IsActive.Value()})
	}
	if opts.LoginType != NoType {
		conds = conds.And(builder.Eq{"`type`": opts.LoginType})
	}
	return conds
}

// IsSSPIEnabled returns true if there is at least one activated login
// source of type LoginSSPI
func IsSSPIEnabled(ctx context.Context) bool {
	exist, err := db.Exist[Source](ctx, FindSourcesOptions{
		IsActive:  optional.Some(true),
		LoginType: SSPI,
	}.ToConds())
	if err != nil {
		log.Error("IsSSPIEnabled: failed to query active SSPI sources: %v", err)
		return false
	}
	return exist
}

// GetSourceByID returns login source by given ID.
func GetSourceByID(ctx context.Context, id int64) (*Source, error) {
	source := new(Source)
	if id == 0 {
		source.Cfg = registeredConfigs[NoType]()
		// Set this source to active
		// FIXME: allow disabling of db based password authentication in future
		source.IsActive = true
		return source, nil
	}

	has, err := db.GetEngine(ctx).ID(id).Get(source)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrSourceNotExist{id}
	}
	return source, nil
}

// UpdateSource updates a Source record in DB.
func UpdateSource(ctx context.Context, source *Source) error {
	if db.InTransaction(ctx) {
		return fmt.Errorf("认证源注册不能嵌套在外层数据库事务中")
	}
	originalSource, err := GetSourceByID(ctx, source.ID)
	if err != nil {
		return err
	}

	has, err := db.GetEngine(ctx).Where("name=? AND id!=?", source.Name, source.ID).Exist(new(Source))
	if err != nil {
		return err
	} else if has {
		return ErrSourceAlreadyExist{source.Name}
	}

	prepared, err := PrepareSourceChange(originalSource, source)
	if err != nil {
		return err
	}
	return prepared.Commit(func() error {
		return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
			if err := RequireSourceAdministrator(ctx); err != nil {
				return err
			}
			fresh, err := GetSourceByID(ctx, source.ID)
			if err != nil {
				return err
			}
			equal, err := SameSourceRevision(originalSource, fresh)
			if err != nil {
				return err
			}
			if !equal {
				return governance_model.ErrConflict
			}
			count, err := db.GetEngine(ctx).ID(source.ID).AllCols().Update(source)
			if err != nil {
				return err
			}
			if count != 1 {
				return ErrSourceNotExist{source.ID}
			}
			return AppendSourceAudit(ctx, originalSource, source, "updated")
		})
	})
}

func SameSourceRevision(left, right *Source) (bool, error) {
	if left == nil || right == nil || left.ID != right.ID || left.Type != right.Type || left.Name != right.Name || left.IsActive != right.IsActive || left.IsSyncEnabled != right.IsSyncEnabled || left.TwoFactorPolicy != right.TwoFactorPolicy {
		return false, nil
	}
	leftCfg, err := left.Cfg.ToDB()
	if err != nil {
		return false, err
	}
	rightCfg, err := right.Cfg.ToDB()
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftCfg, rightCfg), nil
}

// ErrSourceNotExist represents a "SourceNotExist" kind of error.
type ErrSourceNotExist struct {
	ID int64
}

// IsErrSourceNotExist checks if an error is a ErrSourceNotExist.
func IsErrSourceNotExist(err error) bool {
	_, ok := err.(ErrSourceNotExist)
	return ok
}

func (err ErrSourceNotExist) Error() string {
	return fmt.Sprintf("login source does not exist [id: %d]", err.ID)
}

// Unwrap unwraps this as a ErrNotExist err
func (err ErrSourceNotExist) Unwrap() error {
	return util.ErrNotExist
}

// ErrSourceAlreadyExist represents a "SourceAlreadyExist" kind of error.
type ErrSourceAlreadyExist struct {
	Name string
}

// IsErrSourceAlreadyExist checks if an error is a ErrSourceAlreadyExist.
func IsErrSourceAlreadyExist(err error) bool {
	_, ok := err.(ErrSourceAlreadyExist)
	return ok
}

func (err ErrSourceAlreadyExist) Error() string {
	return fmt.Sprintf("login source already exists [name: %s]", err.Name)
}

// Unwrap unwraps this as a ErrExist err
func (err ErrSourceAlreadyExist) Unwrap() error {
	return util.ErrAlreadyExist
}

// ErrSourceInUse represents a "SourceInUse" kind of error.
type ErrSourceInUse struct {
	ID int64
}

// IsErrSourceInUse checks if an error is a ErrSourceInUse.
func IsErrSourceInUse(err error) bool {
	_, ok := err.(ErrSourceInUse)
	return ok
}

func (err ErrSourceInUse) Error() string {
	return fmt.Sprintf("login source is still used by some users [id: %d]", err.ID)
}
