// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package main

import (
	"crypto/subtle"
	"io"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"gitea.dev/modules/json"
)

// 邮件凭据只在隔离验收进程内存中保存，读取要求本次随机秘密。
func registerInvitations(mux *http.ServeMux) error {
	listener, err := net.Listen("tcp", ":1025")
	if err != nil {
		return err
	}
	var mu sync.Mutex
	invitations := map[string]map[string]string{}
	pattern := regexp.MustCompile(`governance/invitations/([0-9]+)#token=([a-f0-9]{64})`)
	mux.HandleFunc("GET /invitations", func(w http.ResponseWriter, r *http.Request) {
		expected := os.Getenv("AUDIT_TEST_TOKEN")
		if expected == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Test-Token")), []byte(expected)) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(invitations)
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				protocol := textproto.NewConn(conn)
				_ = protocol.PrintfLine("220 localhost SMTP")
				recipient := ""
				for {
					line, err := protocol.ReadLine()
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "RCPT TO:"):
						recipient = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "RCPT TO:")), "<>")
						_ = protocol.PrintfLine("250 已接收收件人")
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"), strings.HasPrefix(line, "MAIL FROM:"):
						_ = protocol.PrintfLine("250 localhost")
					case line == "DATA":
						_ = protocol.PrintfLine("354 发送正文")
						body, err := io.ReadAll(io.LimitReader(protocol.DotReader(), 1024*1024))
						if err != nil {
							return
						}
						decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(string(body))))
						if err != nil {
							return
						}
						match := pattern.FindStringSubmatch(string(decoded))
						if len(match) == 3 {
							mu.Lock()
							invitations[recipient] = map[string]string{"邀请ID": match[1], "令牌": match[2]}
							mu.Unlock()
						}
						_ = protocol.PrintfLine("250 已接收")
					case line == "QUIT":
						_ = protocol.PrintfLine("221 再见")
						return
					default:
						_ = protocol.PrintfLine("500 不支持")
					}
				}
			}()
		}
	}()
	return nil
}
