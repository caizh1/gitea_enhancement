// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"

	"github.com/google/uuid"
)

func streamURL(scope string, id int64) string {
	return setting.AppSubURL + "/governance/audit/streams?" + url.Values{"scope_type": {scope}, "scope_id": {strconv.FormatInt(id, 10)}}.Encode()
}

func streamScope(ctx *context.Context) (string, int64, error) {
	scope := ctx.FormString("scope_type")
	id, err := strconv.ParseInt(ctx.FormString("scope_id"), 10, 64)
	if err != nil {
		return scope, 0, governance_model.ErrInvalid
	}
	return scope, id, governance_service.CheckAuditStreamAccess(ctx, ctx.Doer.ID, scope, id)
}

func AuditStreams(ctx *context.Context) {
	scope, id, err := streamScope(ctx)
	if err != nil {
		respondError(ctx, err)
		return
	}
	streams, err := governance_service.ListAuditStreams(ctx, ctx.Doer.ID, scope, id)
	if err != nil {
		respondError(ctx, err)
		return
	}
	var edit *governance_model.AuditStream
	if raw := ctx.FormString("edit"); raw != "" {
		editID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			respondError(ctx, governance_model.ErrInvalid)
			return
		}
		for _, stream := range streams {
			if stream.ID == editID {
				edit = stream.AuditStream
				break
			}
		}
		if edit == nil {
			respondError(ctx, governance_model.ErrNotFound)
			return
		}
	}
	if edit == nil {
		edit = &governance_model.AuditStream{ScopeType: scope, ScopeID: id, Kind: "http", Enabled: true}
	}
	title, err := governance_service.AuditScopeTitle(ctx, scope, id)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Data["Title"], ctx.Data["ScopeTitle"], ctx.Data["Streams"], ctx.Data["Edit"] = "审计外送", title, streams, edit
	ctx.Data["ScopeType"], ctx.Data["ScopeID"], ctx.Data["StreamURL"] = scope, id, streamURL(scope, id)
	ctx.Data["Filters"] = strings.Join(edit.EventTypes, "\n")
	ctx.Data["EventCatalog"] = governance_model.EventCatalog
	if edit.ID == 0 {
		ctx.Data["VerificationToken"] = uuid.NewString()
	}
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	ctx.HTML(http.StatusOK, "governance/audit_streams")
}

func SaveAuditStream(ctx *context.Context) {
	scope, scopeID, err := streamScope(ctx)
	if err != nil {
		respondError(ctx, err)
		return
	}
	id, err := strconv.ParseInt(ctx.FormString("id"), 10, 64)
	if err != nil || id < 0 {
		respondError(ctx, governance_model.ErrInvalid)
		return
	}
	revision, err := strconv.ParseInt(ctx.FormString("revision"), 10, 64)
	if err != nil {
		respondError(ctx, governance_model.ErrInvalid)
		return
	}
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	if ctx.FormString("action") == "test" {
		err = governance_service.TestAuditStream(ctx, actor, id, revision)
	} else {
		option := governance_service.AuditStreamOption{ScopeType: scope, ScopeID: scopeID, Revision: revision, Name: ctx.FormString("name"), Kind: ctx.FormString("kind"), Endpoint: ctx.FormString("endpoint"), Enabled: ctx.FormBool("enabled"), EventTypes: strings.Fields(ctx.FormString("event_types")), Destination: governance_model.AuditDestination{Region: ctx.FormString("region"), Bucket: ctx.FormString("bucket"), Prefix: ctx.FormString("prefix"), ProjectID: ctx.FormString("project_id"), LogID: ctx.FormString("log_id")}}
		if id == 0 || ctx.FormBool("replace_credentials") {
			credentials := &governance_service.AuditStreamCredentials{VerificationToken: ctx.FormString("verification_token"), AccessKeyID: ctx.FormString("access_key_id"), SecretAccessKey: ctx.FormString("secret_access_key"), SessionToken: ctx.FormString("session_token"), ServiceAccountJSON: ctx.FormString("service_account_json"), Headers: map[string]string{}}
			if option.Kind != "http" {
				credentials.VerificationToken = ""
			}
			for line := range strings.SplitSeq(ctx.FormString("headers"), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				name, value, ok := strings.Cut(line, ":")
				if !ok {
					respondError(ctx, governance_model.ErrInvalid)
					return
				}
				name = strings.TrimSpace(name)
				if _, exists := credentials.Headers[name]; exists {
					respondError(ctx, governance_model.ErrInvalid)
					return
				}
				credentials.Headers[name] = strings.TrimSpace(value)
			}
			option.Credentials = credentials
		}
		_, err = governance_service.SaveAuditStream(ctx, actor, id, option)
	}
	if err != nil {
		// 表单秘密不回显到错误页或闪存 Cookie。
		if strings.Contains(err.Error(), governance_model.ErrInvalid.Error()) || strings.Contains(err.Error(), governance_model.ErrConflict.Error()) {
			ctx.Flash.Error("保存未完成：请核对对应目标的字段和凭据；存在在途投递、积压或过期修订时需要刷新后处理。")
			ctx.Redirect(streamURL(scope, scopeID))
		} else {
			respondError(ctx, err)
		}
		return
	}
	ctx.Flash.Success("操作已记录。测试事件和积压由后台投递，请刷新查看确认数量及失败状态。")
	ctx.Redirect(streamURL(scope, scopeID))
}
