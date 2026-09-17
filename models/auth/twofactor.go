// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/secret"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/pbkdf2"
	"xorm.io/builder"
)

//
// Two-factor authentication
//

// ErrTwoFactorNotEnrolled indicates that a user is not enrolled in two-factor authentication.
type ErrTwoFactorNotEnrolled struct {
	UID int64
}

// IsErrTwoFactorNotEnrolled checks if an error is a ErrTwoFactorNotEnrolled.
func IsErrTwoFactorNotEnrolled(err error) bool {
	_, ok := err.(ErrTwoFactorNotEnrolled)
	return ok
}

func (err ErrTwoFactorNotEnrolled) Error() string {
	return fmt.Sprintf("user not enrolled in 2FA [uid: %d]", err.UID)
}

// Unwrap unwraps this as a ErrNotExist err
func (err ErrTwoFactorNotEnrolled) Unwrap() error {
	return util.ErrNotExist
}

// TwoFactor represents a two-factor authentication token.
type TwoFactor struct {
	ID               int64 `xorm:"pk autoincr"`
	UID              int64 `xorm:"UNIQUE"`
	Secret           string
	ScratchSalt      string
	ScratchHash      string
	LastUsedPasscode string             `xorm:"VARCHAR(10)"`
	CreatedUnix      timeutil.TimeStamp `xorm:"INDEX created"`
	UpdatedUnix      timeutil.TimeStamp `xorm:"INDEX updated"`
}

func init() {
	db.RegisterModel(new(TwoFactor))
}

// GenerateScratchToken recreates the scratch token the user is using.
func (t *TwoFactor) GenerateScratchToken() (string, error) {
	tokenBytes := util.CryptoRandomBytes(6)
	// these chars are specially chosen, avoid ambiguous chars like `0`, `O`, `1`, `I`.
	const base32Chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	token := base32.NewEncoding(base32Chars).WithPadding(base32.NoPadding).EncodeToString(tokenBytes)
	t.ScratchSalt = util.CryptoRandomString(10)
	t.ScratchHash = HashToken(token, t.ScratchSalt)
	return token, nil
}

// HashToken return the hashable salt
func HashToken(token, salt string) string {
	tempHash := pbkdf2.Key([]byte(token), []byte(salt), 10000, 50, sha256.New)
	return hex.EncodeToString(tempHash)
}

// VerifyScratchToken verifies if the specified scratch token is valid.
func (t *TwoFactor) VerifyScratchToken(token string) bool {
	if len(token) == 0 {
		return false
	}
	tempHash := HashToken(token, t.ScratchSalt)
	return subtle.ConstantTimeCompare([]byte(t.ScratchHash), []byte(tempHash)) == 1
}

func (t *TwoFactor) getEncryptionKey() []byte {
	k := md5.Sum([]byte(setting.SecretKey))
	return k[:]
}

// SetSecret sets the 2FA secret.
func (t *TwoFactor) SetSecret(secretString string) error {
	secretBytes, err := secret.AesEncrypt(t.getEncryptionKey(), []byte(secretString))
	if err != nil {
		return err
	}
	t.Secret = base64.StdEncoding.EncodeToString(secretBytes)
	return nil
}

// validateTOTP validates the provided passcode. It does not consume the passcode; all login
// surfaces must go through ValidateAndConsumeTOTP so that a passcode cannot be redeemed twice.
func (t *TwoFactor) validateTOTP(passcode string) (bool, error) {
	decodedStoredSecret, err := base64.StdEncoding.DecodeString(t.Secret)
	if err != nil {
		return false, fmt.Errorf("validateTOTP invalid base64: %w", err)
	}
	secretBytes, err := secret.AesDecrypt(t.getEncryptionKey(), decodedStoredSecret)
	if err != nil {
		return false, fmt.Errorf("validateTOTP unable to decrypt (maybe SECRET_KEY is wrong): %w", err)
	}
	secretStr := string(secretBytes)
	return totp.Validate(passcode, secretStr), nil
}

// ValidateAndConsumeTOTP validates the passcode and atomically records it as used so that the
// same passcode cannot be redeemed more than once (RFC 6238 §5.2). It returns false for an
// invalid passcode as well as for a replay, including the case where a concurrent request with
// the same passcode won the race first. All TOTP login surfaces must go through this helper.
func (t *TwoFactor) ValidateAndConsumeTOTP(ctx context.Context, passcode string) (bool, error) {
	ok, err := t.validateTOTP(passcode)
	if err != nil || !ok {
		return false, err
	}
	// Conditional update: only a row whose stored passcode differs from this one is updated, so a
	// replay (or a concurrent duplicate) matches zero rows and is rejected. The row lock taken by
	// the UPDATE serializes racing requests, closing the read-validate-write TOCTOU window.
	t.LastUsedPasscode = passcode
	n, err := db.GetEngine(ctx).ID(t.ID).
		Where("uid = ? AND secret = ?", t.UID, t.Secret).
		Where(builder.Or(builder.IsNull{"last_used_passcode"}, builder.Neq{"last_used_passcode": passcode})).
		Cols("last_used_passcode").Update(t)
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// NewTwoFactor creates a new two-factor authentication token.
func NewTwoFactor(ctx context.Context, t *TwoFactor) error {
	candidate := *t
	err := governance_model.WithWrite(ctx, []string{governance_model.Resource("user", t.UID)}, func(ctx context.Context) error {
		if _, err := db.GetEngine(ctx).Insert(&candidate); err != nil {
			return err
		}
		return appendCredentialAudit(ctx, "credential.totp_enabled", "totp", "双因素认证", candidate.ID, t.UID)
	})
	if err == nil {
		*t = candidate
	}
	return err
}

// UpdateTwoFactor updates a two-factor authentication token.
func UpdateTwoFactor(ctx context.Context, t *TwoFactor) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("user", t.UID)}, func(ctx context.Context) error {
		before, err := GetTwoFactorByUID(ctx, t.UID)
		if err != nil {
			return err
		}
		if before.ID != t.ID {
			return ErrTwoFactorNotEnrolled{t.UID}
		}
		if before.Secret != t.Secret {
			return governance_model.ErrConflict
		}
		if before.ScratchHash == t.ScratchHash && before.ScratchSalt == t.ScratchSalt {
			return nil
		}
		// 恢复码轮换不能覆盖其他请求刚写入的一次性验证码消费状态。
		if _, err := db.GetEngine(ctx).ID(t.ID).Cols("scratch_hash", "scratch_salt").Update(t); err != nil {
			return err
		}
		return appendCredentialAudit(ctx, "credential.totp_updated", "totp", "双因素认证及恢复码", t.ID, t.UID)
	})
}

// ConsumeScratchToken 将恢复码校验、一次性消费与审计放在同一事务内。
func (t *TwoFactor) ConsumeScratchToken(ctx context.Context, token string) (bool, error) {
	if !t.VerifyScratchToken(token) {
		return false, nil
	}
	candidate := *t
	if _, err := candidate.GenerateScratchToken(); err != nil {
		return false, err
	}
	consumed := false
	err := governance_model.WithWrite(ctx, []string{governance_model.Resource("user", t.UID)}, func(ctx context.Context) error {
		count, err := db.GetEngine(ctx).ID(t.ID).Where("uid = ? AND secret = ? AND scratch_hash = ? AND scratch_salt = ?", t.UID, t.Secret, t.ScratchHash, t.ScratchSalt).
			Cols("scratch_hash", "scratch_salt").Update(&candidate)
		if err != nil || count == 0 {
			return err
		}
		if err := appendCredentialAudit(ctx, "credential.recovery_code_consumed", "totp", "双因素恢复码", t.ID, t.UID); err != nil {
			return err
		}
		consumed = true
		return nil
	})
	if err == nil && consumed {
		*t = candidate
	}
	return consumed && err == nil, err
}

// GetTwoFactorByUID returns the two-factor authentication token associated with
// the user, if any.
func GetTwoFactorByUID(ctx context.Context, uid int64) (*TwoFactor, error) {
	twofa := &TwoFactor{}
	has, err := db.GetEngine(ctx).Where("uid=?", uid).Get(twofa)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrTwoFactorNotEnrolled{uid}
	}
	return twofa, nil
}

// HasTwoFactorByUID returns the two-factor authentication token associated with
// the user, if any.
func HasTwoFactorByUID(ctx context.Context, uid int64) (bool, error) {
	return db.GetEngine(ctx).Where("uid=?", uid).Exist(&TwoFactor{})
}

// DeleteTwoFactorByID deletes two-factor authentication token by given ID.
func DeleteTwoFactorByID(ctx context.Context, id, userID int64) error {
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("user", userID)}, func(ctx context.Context) error {
		cnt, err := db.GetEngine(ctx).ID(id).Where("uid = ?", userID).Delete(new(TwoFactor))
		if err != nil {
			return err
		} else if cnt != 1 {
			return ErrTwoFactorNotEnrolled{userID}
		}
		return appendCredentialAudit(ctx, "credential.totp_disabled", "totp", "双因素认证", id, userID)
	})
}

func HasTwoFactorOrWebAuthn(ctx context.Context, id int64) (bool, error) {
	has, err := HasTwoFactorByUID(ctx, id)
	if err != nil {
		return false, err
	} else if has {
		return true, nil
	}
	return HasWebAuthnRegistrationsByUID(ctx, id)
}
