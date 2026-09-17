// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package asymkey

import (
	"context"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/json"
)

func AppendGPGKeyAudit(ctx context.Context, key *GPGKey, action string) error {
	details, err := json.Marshal(map[string]any{
		"key_id": key.KeyID, "verified": key.Verified,
		"can_sign": key.CanSign, "can_encrypt": key.CanEncryptComms || key.CanEncryptStorage, "can_certify": key.CanCertify,
	})
	if err != nil {
		return err
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{
		Type: "credential.gpg_key_" + action, Actor: governance_model.AuditActor(ctx),
		ScopeType: "user", ScopeID: key.OwnerID, ObjectType: "gpg_key", ObjectID: key.ID, ObjectPath: key.KeyID,
		Result: "success", Details: details,
	})
}
