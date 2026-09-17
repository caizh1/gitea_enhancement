// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package context

import (
	"net/http"
	"strings"
)

// AuditorReadRequest POST 默认按写入处理；仅放行已定义为读取的两个入口。
func AuditorReadRequest(ctx *Base, api bool) bool {
	switch ctx.Req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	case http.MethodPost:
		if api {
			return strings.HasSuffix(ctx.Req.URL.Path, "/file-contents")
		}
		if ctx.PathParam("run") != "" {
			for _, part := range []struct{ name, segment string }{{"run", "runs"}, {"attempt", "attempts"}, {"job", "jobs"}} {
				if value := ctx.PathParam(part.name); value != "" && strings.HasSuffix(ctx.Req.URL.Path, "/"+part.segment+"/"+value) {
					return true
				}
			}
		}
	}
	return false
}
