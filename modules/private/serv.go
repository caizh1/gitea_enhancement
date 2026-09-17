// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package private

import (
	"context"
	"fmt"
	"net/url"

	asymkey_model "gitea.dev/models/asymkey"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/perm"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
)

// KeyAndOwner is the response from ServNoCommand
type KeyAndOwner struct {
	Key   *asymkey_model.PublicKey `json:"key"`
	Owner *user_model.User         `json:"user"`
}

// ServNoCommand returns information about the provided key
func ServNoCommand(ctx context.Context, keyID int64) (*asymkey_model.PublicKey, *user_model.User, error) {
	reqURL := setting.LocalURL + fmt.Sprintf("api/internal/serv/none/%d", keyID)
	req := newInternalRequestAPI(ctx, reqURL, "GET")
	keyAndOwner, extra := requestJSONResp(req, &KeyAndOwner{})
	if extra.HasError() {
		return nil, nil, extra.Error
	}
	return keyAndOwner.Key, keyAndOwner.Owner, nil
}

// ServCommandResults are the results of a call to the private route serv
type ServCommandResults struct {
	AccessAudit *governance_model.AuditEvent
	IsWiki      bool
	DeployKeyID int64
	KeyID       int64  // public key
	KeyName     string // this field is ambiguous, it can be the name of DeployKey, or the name of the PublicKey
	UserName    string
	UserEmail   string
	UserID      int64
	OwnerName   string
	RepoName    string
	RepoID      int64
	// StorageRelativePath 仅供内部 SSH 执行定位稳定正文；不得用于公开 URL 或权限判断。
	StorageRelativePath string
}

// ServCommand preps for a serv call
func ServCommand(ctx context.Context, keyID int64, ownerName, repoName string, mode perm.AccessMode, verb, lfsVerb string) (*ServCommandResults, ResponseExtra) {
	reqURL := setting.LocalURL + fmt.Sprintf("api/internal/serv/command/%d/%s/%s?mode=%d",
		keyID,
		url.PathEscape(ownerName),
		url.PathEscape(repoName),
		mode,
	)
	reqURL += "&verb=" + url.QueryEscape(verb)
	// reqURL += "&lfs_verb=" + url.QueryEscape(lfsVerb) // TODO: actually there is no use of this parameter. In the future, the URL construction should be more flexible
	_ = lfsVerb
	req := newInternalRequestAPI(ctx, reqURL, "GET")
	return requestJSONResp(req, &ServCommandResults{})
}

// ServAuditCompletion 仅由持有内部认证的 SSH 子进程提交，不包含代码和认证正文。
type ServAuditCompletion struct {
	Event  governance_model.AuditEvent
	Result string
}

// CompleteServAudit 保存 SSH 子进程结束结果；请求失败时不自动重放操作。
func CompleteServAudit(ctx context.Context, event *governance_model.AuditEvent, result string) ResponseExtra {
	req := newInternalRequestAPI(ctx, setting.LocalURL+"api/internal/serv/audit-complete", "POST", ServAuditCompletion{Event: *event, Result: result})
	_, extra := requestJSONResp(req, &Response{})
	return extra
}
