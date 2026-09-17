// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

// 隔离验收接收器不保存审计正文；邮件邀请凭据仅在内存中供受限验收读取。
package main

import (
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"gitea.dev/modules/json"
)

func main() {
	var mu sync.Mutex
	fail, drop := false, false
	events := map[string]int{}
	types := map[string]int{}
	invalid := 0
	mux := http.NewServeMux()
	if registerInvitations(mux) != nil {
		os.Exit(1)
	}
	mux.HandleFunc("POST /events", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var event struct {
			ID   string `json:"id"`
			Type string `json:"event_type"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 128*1024)).Decode(&event) != nil || event.ID == "" || event.ID != r.Header.Get("X-Gitea-Event-Id") || r.Header.Get("X-Gitlab-Event-Streaming-Token") != os.Getenv("AUDIT_TEST_TOKEN") {
			invalid++
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if events[event.ID] == 0 {
			types[event.Type]++
		}
		events[event.ID]++
		if drop {
			drop = false
			if hijacker, ok := w.(http.Hijacker); ok {
				if connection, _, err := hijacker.Hijack(); err == nil {
					connection.Close()
					return
				}
			}
		}
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /control", func(w http.ResponseWriter, r *http.Request) {
		var value struct {
			Fail bool `json:"失败"`
			Drop bool `json:"丢失回执"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&value) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		fail, drop = value.Fail, value.Drop
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"事件": events, "类型": types, "校验失败": invalid})
	})
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	if server.ListenAndServe() != nil {
		os.Exit(1)
	}
}
