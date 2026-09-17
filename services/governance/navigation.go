// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"

	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/setting"
)

type NavigationQuery struct {
	Q          string `json:"q"`
	State      string `json:"state"`
	Sort       string `json:"sort"`
	Direction  string `json:"direction"`
	GroupsOnly bool   `json:"groups_only"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"-"`
}

type NavigationNode struct {
	Key                  string   `json:"key"`
	ID                   int64    `json:"id"`
	Type                 string   `json:"type"`
	Name                 string   `json:"name"`
	FullPath             string   `json:"full_path"`
	URL                  string   `json:"url"`
	Visibility           int      `json:"visibility"`
	State                string   `json:"state"`
	CreatedAt            int64    `json:"created_at"`
	UpdatedAt            int64    `json:"updated_at"`
	Expandable           bool     `json:"expandable"`
	RestrictedNavigation bool     `json:"restricted_navigation"`
	AllowedActions       []string `json:"allowed_actions"`
}

type NavigationPage struct {
	Items      []NavigationNode `json:"items"`
	NextCursor string           `json:"next_cursor"`
	HasMore    bool             `json:"has_more"`
}

type navigationCursor struct {
	Actor int64           `json:"actor"`
	Scope int64           `json:"scope"`
	Query NavigationQuery `json:"query"`
	Last  NavigationNode  `json:"last"`
}

func normalizeNavigationQuery(q NavigationQuery) (NavigationQuery, error) {
	q.Q = strings.ToLower(strings.TrimSpace(q.Q))
	if q.State == "" {
		q.State = "active"
	}
	if q.Sort == "" {
		q.Sort = "name"
	}
	if q.Direction == "" {
		q.Direction = "asc"
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if len(q.Q) > 200 || q.Limit < 1 || q.Limit > 100 || !slices.Contains([]string{"active", "inactive"}, q.State) || !slices.Contains([]string{"name", "created", "updated"}, q.Sort) || !slices.Contains([]string{"asc", "desc"}, q.Direction) {
		return q, governance_model.ErrInvalid
	}
	return q, nil
}

func navigationLess(a, b NavigationNode, q NavigationQuery) bool {
	if a.Type != b.Type {
		return a.Type == "group"
	}
	var comparison int
	switch q.Sort {
	case "created":
		if a.CreatedAt < b.CreatedAt {
			comparison = -1
		} else if a.CreatedAt > b.CreatedAt {
			comparison = 1
		}
	case "updated":
		if a.UpdatedAt < b.UpdatedAt {
			comparison = -1
		} else if a.UpdatedAt > b.UpdatedAt {
			comparison = 1
		}
	default:
		comparison = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	}
	if comparison == 0 {
		if a.ID < b.ID {
			comparison = -1
		} else if a.ID > b.ID {
			comparison = 1
		}
	}
	if q.Direction == "desc" {
		return comparison > 0
	}
	return comparison < 0
}

// 游标带签名并绑定身份及查询，不赋予任何访问资格；调用方必须先重新鉴权。
func paginateNavigation(nodes []NavigationNode, actorID, scopeID int64, q NavigationQuery) (*NavigationPage, error) {
	var err error
	q, err = normalizeNavigationQuery(q)
	if err != nil {
		return nil, err
	}
	var after *NavigationNode
	if q.Cursor != "" {
		if len(q.Cursor) > 16384 {
			return nil, governance_model.ErrInvalid
		}
		parts := strings.Split(q.Cursor, ".")
		if len(parts) != 2 {
			return nil, governance_model.ErrInvalid
		}
		payload, e1 := base64.RawURLEncoding.DecodeString(parts[0])
		signature, e2 := base64.RawURLEncoding.DecodeString(parts[1])
		mac := hmac.New(sha256.New, []byte(setting.SecretKey))
		mac.Write(payload)
		var cursor navigationCursor
		if e1 != nil || e2 != nil || !hmac.Equal(signature, mac.Sum(nil)) || json.Unmarshal(payload, &cursor) != nil {
			return nil, governance_model.ErrInvalid
		}
		bound := q
		bound.Cursor = ""
		if cursor.Actor != actorID || cursor.Scope != scopeID || cursor.Query != bound {
			return nil, governance_model.ErrInvalid
		}
		after = &cursor.Last
	}
	slices.SortFunc(nodes, func(a, b NavigationNode) int {
		if navigationLess(a, b, q) {
			return -1
		}
		if navigationLess(b, a, q) {
			return 1
		}
		return 0
	})
	page := &NavigationPage{Items: make([]NavigationNode, 0, q.Limit)}
	for _, node := range nodes {
		if after != nil && !navigationLess(*after, node, q) {
			continue
		}
		if len(page.Items) == q.Limit {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, node)
	}
	if page.HasMore {
		q.Cursor = ""
		payload, err := json.Marshal(navigationCursor{Actor: actorID, Scope: scopeID, Query: q, Last: page.Items[len(page.Items)-1]})
		if err != nil {
			return nil, err
		}
		mac := hmac.New(sha256.New, []byte(setting.SecretKey))
		mac.Write(payload)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	return page, nil
}
