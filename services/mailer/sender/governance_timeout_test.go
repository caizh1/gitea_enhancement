// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package sender

import (
	"bufio"
	"bytes"
	"net"
	"strings"
	"testing"
	"time"

	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/require"
)

func TestGovernanceSMTPDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "smtp", SMTPAddr: host, SMTPPort: port})()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Read(make([]byte, 1))
	}()
	sender := &SMTPSender{Timeout: 50 * time.Millisecond}
	start := time.Now()
	err = sender.Send("from@example.invalid", []string{"to@example.invalid"}, bytes.NewBufferString("验收邮件"))
	require.Error(t, err)
	require.Less(t, time.Since(start), time.Second, "不响应的 SMTP 服务器不能长期占用邀请投递租约")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("超时后未关闭 SMTP 连接")
	}
}

func TestGovernanceSMTPRejectsStartTLSDowngrade(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "smtp+starttls", SMTPAddr: host, SMTPPort: port})()
	commands := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			commands <- ""
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = conn.Write([]byte("220 localhost SMTP\r\n"))
		reader := bufio.NewReader(conn)
		var seen strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			seen.WriteString(line)
			_, _ = conn.Write([]byte("250 localhost\r\n"))
		}
		commands <- seen.String()
	}()
	sender := &SMTPSender{Timeout: time.Second, RequireSTARTTLS: true}
	err = sender.Send("from@example.invalid", []string{"to@example.invalid"}, bytes.NewBufferString("秘密邀请正文"))
	require.ErrorContains(t, err, "STARTTLS")
	seen := <-commands
	require.NotContains(t, seen, "MAIL FROM")
	require.NotContains(t, seen, "秘密邀请正文")
}
