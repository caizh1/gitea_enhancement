// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	stdcontext "context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/log"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
)

func respondError(ctx *context.Context, err error) {
	switch {
	case errors.Is(err, governance_model.ErrNotFound), errors.Is(err, governance_model.ErrForbidden):
		ctx.NotFound(nil)
	case errors.Is(err, governance_model.ErrInvalid):
		ctx.HTTPError(http.StatusBadRequest, err.Error())
	case errors.Is(err, governance_model.ErrConflict):
		ctx.HTTPError(http.StatusConflict, err.Error())
	default:
		ctx.ServerError("操作审计", err)
	}
}

func readFilter(ctx *context.Context, export bool) (governance_model.AuditFilter, error) {
	if err := ctx.Req.ParseForm(); err != nil {
		return governance_model.AuditFilter{}, governance_model.ErrInvalid
	}
	values := ctx.Req.Form
	for _, key := range []string{"from", "to"} {
		if parsed, err := time.Parse("2006-01-02T15:04", values.Get(key)); err == nil {
			values.Set(key, parsed.UTC().Format(time.RFC3339))
		}
	}
	filter, err := governance_service.ParseAuditFilter(values, ctx.Doer.ID, export)
	if err != nil {
		return filter, err
	}
	return filter, governance_service.CheckAuditAccess(ctx, ctx.Doer.ID, filter.ScopeType, filter.ScopeID)
}

func Audit(ctx *context.Context) {
	filter, err := readFilter(ctx, false)
	if err != nil {
		if errors.Is(err, governance_model.ErrInvalid) {
			renderAudit(ctx, filter, &governance_model.AuditPage{}, "请检查筛选条件和 UTC 时间；单次查询最多三十天，跨期取证请使用导出。")
		} else {
			respondError(ctx, err)
		}
		return
	}
	before := int64(0)
	if raw := ctx.FormString("before"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			respondError(ctx, governance_model.ErrInvalid)
			return
		}
	}
	page, err := governance_model.QueryAudit(ctx, filter, before, 50)
	if err != nil {
		respondError(ctx, err)
		return
	}
	renderAudit(ctx, filter, page, "")
}

func renderAudit(ctx *context.Context, filter governance_model.AuditFilter, page *governance_model.AuditPage, validationError string) {
	// 无效筛选也必须重新验证范围，不能通过错误页面读取私有群组名称。
	if err := governance_service.CheckAuditAccess(ctx, ctx.Doer.ID, filter.ScopeType, filter.ScopeID); err != nil {
		respondError(ctx, err)
		return
	}
	var jobs []*governance_model.AuditExport
	if err := db.GetEngine(ctx).Where("user_id = ? AND expires_at > ?", ctx.Doer.ID, time.Now().UTC()).Desc("created_at").Limit(20).Find(&jobs); err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Data["Title"], ctx.Data["Filter"], ctx.Data["Events"], ctx.Data["Exports"] = "操作审计", filter, page.Events, jobs
	ctx.Data["ValidationError"] = validationError
	scopeTitle, err := governance_service.AuditScopeTitle(ctx, filter.ScopeType, filter.ScopeID)
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Data["ScopeTitle"] = scopeTitle
	streamScope, streamID := filter.ScopeType, filter.ScopeID
	if ctx.Doer.IsAdmin && streamScope != "group" {
		streamScope, streamID = "instance", 0
	}
	if governance_service.CheckAuditStreamAccess(ctx, ctx.Doer.ID, streamScope, streamID) == nil {
		ctx.Data["StreamURL"] = streamURL(streamScope, streamID)
	}
	ctx.Data["From"], ctx.Data["To"] = filter.From.UTC().Format("2006-01-02T15:04"), filter.To.UTC().Format("2006-01-02T15:04")
	ctx.Data["EventCatalog"] = governance_model.EventCatalog
	ctx.Data["ResultNames"] = map[string]string{"success": "成功", "failure": "失败", "denied": "拒绝", "pending": "待核对", "unknown": "结果不明"}
	ctx.Data["ExportStates"] = map[string]string{"queued": "等待生成", "running": "生成中", "ready": "可下载", "denied": "权限已撤销"}
	ctx.Data["ScopeNames"] = map[string]string{"instance": "实例", "group": "群组", "repository": "项目", "user": "个人"}
	if page.NextBefore > 0 {
		values := make(url.Values)
		for _, key := range []string{"scope_type", "scope_id", "actor_id", "event_type", "entity_type", "entity_id", "result", "request_id"} {
			values.Set(key, ctx.Req.Form.Get(key))
		}
		values.Set("from", filter.From.UTC().Format(time.RFC3339Nano))
		values.Set("to", filter.To.UTC().Format(time.RFC3339Nano))
		values.Set("before", strconv.FormatInt(page.NextBefore, 10))
		ctx.Data["NextURL"] = setting.AppSubURL + "/governance/audit?" + values.Encode()
	}
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	status := http.StatusOK
	if validationError != "" {
		status = http.StatusBadRequest
	}
	ctx.HTML(status, "governance/audit")
}

func CreateExport(ctx *context.Context) {
	filter, err := readFilter(ctx, true)
	if err != nil {
		respondError(ctx, err)
		return
	}
	_, err = governance_model.CreateAuditExport(ctx, governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web"), filter, ctx.FormString("format"), func(c stdcontext.Context) error {
		return governance_service.CheckAuditAccess(c, ctx.Doer.ID, filter.ScopeType, filter.ScopeID)
	})
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Flash.Success("导出任务已创建，后台完成后可在审计页下载；临时副本保存七天。")
	values := url.Values{"scope_type": {filter.ScopeType}, "scope_id": {strconv.FormatInt(filter.ScopeID, 10)}}
	ctx.Redirect(setting.AppSubURL + "/governance/audit?" + values.Encode())
}

func DownloadExport(ctx *context.Context) {
	actor := governance_service.RequestActor(ctx.Doer, ctx.RemoteAddr(), "web")
	job, err := governance_service.PrepareAuditDownload(ctx, actor, ctx.PathParam("id"))
	if err != nil {
		respondError(ctx, err)
		return
	}
	ctx.Resp.Header().Set("Cache-Control", "no-store")
	ctx.Resp.Header().Set("Content-Disposition", "attachment; filename=audit-"+job.ID+"."+job.Format)
	contentType := "application/json; charset=utf-8"
	if job.Format == "csv" {
		contentType = "text/csv; charset=utf-8"
	}
	ctx.Resp.Header().Set("Content-Type", contentType)
	if err := governance_service.WriteAuditExport(ctx, actor, job, ctx.Resp); err != nil {
		log.Error("审计导出下载中断 %s：%v", job.ID, err)
		panic(http.ErrAbortHandler)
	}
}
