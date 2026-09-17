// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	lru "github.com/hashicorp/golang-lru/v2"
	"xorm.io/builder"
)

var successfulAccessTokenCache *lru.Cache[string, any]

// AccessToken represents a personal access token.
type AccessToken struct {
	ID             int64 `xorm:"pk autoincr"`
	UID            int64 `xorm:"INDEX"`
	Name           string
	Token          string `xorm:"-"`
	TokenHash      string `xorm:"UNIQUE"` // sha256 of token
	TokenSalt      string
	TokenLastEight string `xorm:"INDEX token_last_eight"`
	Scope          AccessTokenScope

	CreatedUnix       timeutil.TimeStamp `xorm:"INDEX created"`
	UpdatedUnix       timeutil.TimeStamp `xorm:"INDEX updated"`
	HasRecentActivity bool               `xorm:"-"`
	HasUsed           bool               `xorm:"-"`
}

// AfterLoad is invoked from XORM after setting the values of all fields of this object.
func (t *AccessToken) AfterLoad() {
	t.HasUsed = t.UpdatedUnix > t.CreatedUnix
	t.HasRecentActivity = t.UpdatedUnix.AddDuration(7*24*time.Hour) > timeutil.TimeStampNow()
}

func init() {
	db.RegisterModel(new(AccessToken), func() error {
		if setting.SuccessfulTokensCacheSize > 0 {
			var err error
			successfulAccessTokenCache, err = lru.New[string, any](setting.SuccessfulTokensCacheSize)
			if err != nil {
				return fmt.Errorf("unable to allocate AccessToken cache: %w", err)
			}
		} else {
			successfulAccessTokenCache = nil
		}
		return nil
	})
}

// NewAccessToken creates new access token.
func NewAccessToken(ctx context.Context, t *AccessToken) error {
	candidate := *t
	salt := util.CryptoRandomString(10)
	token := util.CryptoRandomBytes(20)
	candidate.TokenSalt = salt
	candidate.Token = hex.EncodeToString(token)
	candidate.TokenHash = HashToken(candidate.Token, candidate.TokenSalt)
	candidate.TokenLastEight = candidate.Token[len(candidate.Token)-8:]
	if err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		if _, err := db.GetEngine(ctx).Insert(&candidate); err != nil {
			return err
		}
		return appendAccessTokenAudit(ctx, &candidate, "credential.access_token_created")
	}); err != nil {
		return err
	}
	*t = candidate
	return nil
}

// DisplayPublicOnly whether to display this as a public-only token.
func (t *AccessToken) DisplayPublicOnly() bool {
	publicOnly, err := t.Scope.PublicOnly()
	if err != nil {
		return false
	}
	return publicOnly
}

func getAccessTokenIDFromCache(token string) int64 {
	if successfulAccessTokenCache == nil {
		return 0
	}
	tInterface, ok := successfulAccessTokenCache.Get(token)
	if !ok {
		return 0
	}
	t, ok := tInterface.(int64)
	if !ok {
		return 0
	}
	return t
}

// GetAccessTokenBySHA returns access token by given token value
func GetAccessTokenBySHA(ctx context.Context, token string) (*AccessToken, error) {
	if len(token) < 8 {
		return nil, util.NewNotExistErrorf("access token not found")
	}

	lastEight := token[len(token)-8:]
	if id := getAccessTokenIDFromCache(token); id > 0 {
		accessToken := &AccessToken{
			TokenLastEight: lastEight,
		}
		// Re-get the token from the db in case it has been deleted in the intervening period
		has, err := db.GetEngine(ctx).ID(id).Get(accessToken)
		if err != nil {
			return nil, err
		}
		if has {
			return accessToken, nil
		}
		successfulAccessTokenCache.Remove(token)
	}

	var tokens []AccessToken
	err := db.GetEngine(ctx).Table(&AccessToken{}).Where("token_last_eight = ?", lastEight).Find(&tokens)
	if err != nil {
		return nil, err
	} else if len(tokens) == 0 {
		return nil, util.NewNotExistErrorf("access token not found")
	}

	for _, t := range tokens {
		tempHash := HashToken(token, t.TokenSalt)
		if subtle.ConstantTimeCompare([]byte(t.TokenHash), []byte(tempHash)) == 1 {
			if successfulAccessTokenCache != nil {
				successfulAccessTokenCache.Add(token, t.ID)
			}
			return &t, nil
		}
	}
	return nil, util.NewNotExistErrorf("access token not found")
}

// AccessTokenByNameExists checks if a token name has been used already by a user.
func AccessTokenByNameExists(ctx context.Context, token *AccessToken) (bool, error) {
	return db.GetEngine(ctx).Table("access_token").Where("name = ?", token.Name).And("uid = ?", token.UID).Exist()
}

// ListAccessTokensOptions contain filter options
type ListAccessTokensOptions struct {
	db.ListOptions
	Name   string
	UserID int64
}

func (opts ListAccessTokensOptions) ToConds() builder.Cond {
	cond := builder.NewCond()
	// user id is required, otherwise it will return all result which maybe a possible bug
	cond = cond.And(builder.Eq{"uid": opts.UserID})
	if len(opts.Name) > 0 {
		cond = cond.And(builder.Eq{"name": opts.Name})
	}
	return cond
}

func (opts ListAccessTokensOptions) ToOrders() string {
	return "created_unix DESC"
}

// UpdateAccessToken updates information of access token.
func UpdateAccessToken(ctx context.Context, t *AccessToken) error {
	_, err := db.GetEngine(ctx).ID(t.ID).AllCols().Update(t)
	return err
}

// DeleteAccessTokenByID deletes access token by given ID.
func DeleteAccessTokenByID(ctx context.Context, id, userID int64) error {
	return governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		var token AccessToken
		has, err := db.GetEngine(ctx).ID(id).Where("uid = ?", userID).Get(&token)
		if err != nil {
			return err
		}
		if !has {
			return util.NewNotExistErrorf("access token not found")
		}
		if _, err := db.GetEngine(ctx).ID(id).Delete(new(AccessToken)); err != nil {
			return err
		}
		return appendAccessTokenAudit(ctx, &token, "credential.access_token_revoked")
	})
}

// 只保存令牌标识和权限范围，不记录正文、哈希、盐或尾部字符。
func appendAccessTokenAudit(ctx context.Context, token *AccessToken, kind string) error {
	details, err := json.Marshal(map[string]any{"owner_id": token.UID, "name": token.Name, "scopes": token.Scope.StringSlice()})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: kind, Actor: governance_model.AuditActor(ctx), ScopeType: "user", ScopeID: token.UID, ObjectType: "access_token", ObjectID: token.ID, ObjectPath: token.Name, Result: "success", Details: details})
}
