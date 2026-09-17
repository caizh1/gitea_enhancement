// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/log"

	"github.com/go-chi/chi/v5"
)

// ResolveRepositoryIdentity 只解析身份，调用方必须继续执行原生仓库及单元权限检查。
func ResolveRepositoryIdentity(ctx context.Context, path string) (*repo_model.Repository, error) {
	resource, err := governance_model.ResolvePath(ctx, path)
	if err != nil {
		return nil, err
	}
	if resource.Kind != "repository" {
		return nil, governance_model.ErrNotFound
	}
	repo, err := repo_model.GetRepositoryByID(ctx, resource.ResourceID)
	if repo_model.IsErrRepoNotExist(err) {
		return nil, governance_model.ErrNotFound
	}
	return repo, err
}

// NamespaceRouting 仅替换 chi 的匹配路径，保留客户端 URL 和后续原生鉴权。
// 包坐标及 Actions 引用仍使用其原有稳定标识，不在此重写。
func NamespaceRouting(api bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := chi.RouteContext(r.Context())
			path := rc.RoutePath
			if path == "" {
				path = r.URL.EscapedPath()
			}
			rewritten, err := rewriteNamespaceRoute(r.Context(), path, api)
			if err != nil {
				log.Error("解析治理命名空间失败：%v", err)
				http.Error(w, "资源暂时不可用", http.StatusServiceUnavailable)
				return
			}
			rc.RoutePath = rewritten
			next.ServeHTTP(w, r)
		})
	}
}

func rewriteNamespaceRoute(ctx context.Context, route string, api bool) (string, error) {
	prefix, path, onlyGroups := "/", strings.TrimPrefix(route, "/"), false
	if api {
		switch {
		case strings.HasPrefix(route, "/repos/"):
			prefix, path = "/repos/", strings.TrimPrefix(route, "/repos/")
		case strings.HasPrefix(route, "/orgs/"):
			prefix, path, onlyGroups = "/orgs/", strings.TrimPrefix(route, "/orgs/"), true
		default:
			return route, nil
		}
	} else if rest, ok := strings.CutPrefix(route, "/org/"); ok {
		prefix, path, onlyGroups = "/org/", rest, true
	} else {
		first, _, _ := strings.Cut(path, "/")
		switch first {
		case "api", "assets", "avatars", "repo-avatars", "user", "admin", "governance", "explore", "repo", "issues", "pulls", "notifications", "v2":
			return route, nil
		}
	}
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return route, nil
	}
	// 候选按原始段边界保存，分支和文件路径中的编码保持原样。
	type candidate struct {
		path   string
		count  int
		suffix string
	}
	var candidates []candidate
	var names []string
	var decoded []string
	for i, part := range parts {
		if i > governance_model.MaxDepth {
			break
		}
		segment, err := url.PathUnescape(part)
		if err != nil {
			return route, nil
		}
		// API 允许将完整 owner 编码在一个参数中。
		for item := range strings.SplitSeq(segment, "/") {
			if item == "" || item == "." || item == ".." || strings.ContainsAny(item, "\\\x00") {
				return route, nil
			}
		}
		decoded = append(decoded, segment)
		name := strings.ToLower(strings.Join(decoded, "/"))
		candidates = append(candidates, candidate{name, i + 1, ""})
		names = append(names, name)
		if !onlyGroups {
			if base, ok := strings.CutSuffix(name, ".git"); ok {
				candidates = append(candidates, candidate{base, i + 1, ".git"})
				names = append(names, base)
				if base, ok := strings.CutSuffix(base, ".wiki"); ok {
					candidates = append(candidates, candidate{base, i + 1, ".wiki.git"})
					names = append(names, base)
				}
			}
		}
	}
	var resources []*governance_model.ResourcePath
	if err := db.GetEngine(ctx).In("path", names).Find(&resources); err != nil {
		return "", err
	}
	byPath := make(map[string]*governance_model.ResourcePath, len(resources))
	for _, resource := range resources {
		byPath[resource.Path] = resource
	}
	var group *governance_model.ResourcePath
	var groupCount int
	for _, candidate := range candidates {
		resource := byPath[candidate.path]
		if resource == nil {
			continue
		}
		if resource.Kind == "repository" && !onlyGroups {
			repo, err := repo_model.GetRepositoryByID(ctx, resource.ResourceID)
			if repo_model.IsErrRepoNotExist(err) {
				return route, nil
			}
			if err != nil {
				return "", err
			}
			suffix := ""
			if candidate.count < len(parts) {
				suffix = "/" + strings.Join(parts[candidate.count:], "/")
			}
			return prefix + url.PathEscape(repo.OwnerName) + "/" + url.PathEscape(repo.Name) + candidate.suffix + suffix, nil
		}
		if candidate.suffix == "" && (resource.Kind == "group" || (!onlyGroups && resource.Kind == "user")) {
			group, groupCount = resource, candidate.count
		}
	}
	if group != nil {
		owner, err := user_model.GetUserByID(ctx, group.ResourceID)
		if user_model.IsErrUserNotExist(err) {
			return route, nil
		}
		if err != nil {
			return "", err
		}
		suffix := ""
		if groupCount < len(parts) {
			suffix = "/" + strings.Join(parts[groupCount:], "/")
		}
		return prefix + url.PathEscape(owner.Name) + suffix, nil
	}
	return route, nil
}
