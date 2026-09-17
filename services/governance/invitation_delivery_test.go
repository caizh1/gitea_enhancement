// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"errors"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/log"
	"gitea.dev/modules/secret"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/require"
)

func TestInvitationDeliveryRetriesRemindersAndExpiry(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "dummy"})()
	defer test.MockVariableValue(&setting.SecretKey, "邀请投递验收密钥")()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	group, err := CreateGroup(ctx, actor, GroupOption{Path: "mail-delivery", Visibility: 2})
	require.NoError(t, err)
	invitation, err := CreateInvitation(ctx, actor, "group", group.ID, InvitationOption{Email: "delivery@example.invalid", Role: governance_model.Developer, Revision: group.Revision})
	require.NoError(t, err)
	sent := 0
	fail := true
	defer test.MockVariableValue(&deliverInvitation, func(value *governance_model.Invitation) error {
		sent++
		require.NotEmpty(t, value.Lease)
		if fail {
			return errors.New("模拟邮件服务器故障，不应保存正文或秘密")
		}
		return nil
	})()
	require.Error(t, RunInvitations(ctx))
	require.Equal(t, 1, sent)
	invitation = unittest.AssertExistsAndLoadBean(t, &governance_model.Invitation{ID: invitation.ID})
	require.Equal(t, "failed", invitation.DeliveryState)
	require.Equal(t, 1, invitation.DeliveryFailures)
	require.Greater(t, invitation.NextDeliveryUnix, time.Now().Unix())
	require.NoError(t, RunInvitations(ctx))
	require.Equal(t, 1, sent, "重试等待期间不得立即重复投递")
	fail = false
	_, err = db.GetEngine(ctx).ID(invitation.ID).Cols("next_delivery_unix").Update(&governance_model.Invitation{NextDeliveryUnix: time.Now().Unix() - 1})
	require.NoError(t, err)
	require.NoError(t, RunInvitations(ctx))
	invitation = unittest.AssertExistsAndLoadBean(t, &governance_model.Invitation{ID: invitation.ID})
	require.Equal(t, "sent", invitation.DeliveryState)
	require.Equal(t, 1, invitation.DeliveryStep)
	require.Equal(t, invitation.CreatedUnix+2*86400, invitation.NextDeliveryUnix)
	require.Empty(t, invitation.Lease)
	// 发送时间落后到第六天，只安排第十天提醒，不补发第 2、5 天的多封邮件。
	_, err = db.GetEngine(ctx).ID(invitation.ID).Cols("created_unix", "next_delivery_unix").Update(&governance_model.Invitation{CreatedUnix: time.Now().Add(-6 * 24 * time.Hour).Unix(), NextDeliveryUnix: time.Now().Unix() - 1})
	require.NoError(t, err)
	require.NoError(t, RunInvitations(ctx))
	invitation = unittest.AssertExistsAndLoadBean(t, &governance_model.Invitation{ID: invitation.ID})
	require.Equal(t, 3, invitation.DeliveryStep)
	require.Equal(t, invitation.CreatedUnix+10*86400, invitation.NextDeliveryUnix)
	// 租约未到期不发送；进程退出后租约到期会重新领取同一邀请。
	_, err = db.GetEngine(ctx).ID(invitation.ID).Cols("lease", "lease_until", "next_delivery_unix").Update(&governance_model.Invitation{Lease: "中断样本", LeaseUntil: time.Now().Unix() + 120, NextDeliveryUnix: time.Now().Unix() - 1})
	require.NoError(t, err)
	require.NoError(t, RunInvitations(ctx))
	require.Equal(t, 3, sent)
	_, err = db.GetEngine(ctx).ID(invitation.ID).Cols("lease_until").Update(&governance_model.Invitation{LeaseUntil: time.Now().Unix() - 1})
	require.NoError(t, err)
	require.NoError(t, RunInvitations(ctx))
	require.Equal(t, 4, sent)
	_, err = db.GetEngine(ctx).ID(invitation.ID).Cols("expires_unix").Update(&governance_model.Invitation{ExpiresUnix: time.Now().Unix() - 1})
	require.NoError(t, err)
	require.NoError(t, RunInvitations(ctx))
	require.NoError(t, RunInvitations(ctx))
	unittest.AssertCount(t, &governance_model.Invitation{ID: invitation.ID}, 0)
	unittest.AssertCount(t, &governance_model.AuditEvent{Type: "invitation.expired", ScopeID: group.ID}, 1)
}

func TestInvitationRealSMTPDelivery(t *testing.T) {
	unittest.PrepareTestEnv(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	defer test.MockVariableValue(&setting.MailService, &setting.Mailer{Protocol: "smtp", SMTPAddr: host, SMTPPort: port, FromEmail: "governance@example.invalid", SendAsPlainText: true})()
	defer test.MockVariableValue(&setting.SecretKey, "真实邮件投递验收密钥")()
	received := make(chan string, 1)
	failure := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			failure <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		protocol := textproto.NewConn(conn)
		_ = protocol.PrintfLine("220 localhost SMTP")
		for {
			line, err := protocol.ReadLine()
			if err != nil {
				failure <- err
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"), strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
				_ = protocol.PrintfLine("250 localhost")
			case line == "DATA":
				_ = protocol.PrintfLine("354 发送正文")
				body, err := protocol.ReadDotBytes()
				if err != nil {
					failure <- err
					return
				}
				received <- string(body)
				_ = protocol.PrintfLine("250 已接收")
			case line == "QUIT":
				_ = protocol.PrintfLine("221 再见")
				return
			default:
				failure <- fmt.Errorf("意外 SMTP 命令：%s", line)
				return
			}
		}
	}()
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	actor := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	group, err := CreateGroup(ctx, actor, GroupOption{Path: "real-smtp-invite", Visibility: 2})
	require.NoError(t, err)
	invitation, err := CreateInvitation(ctx, actor, "group", group.ID, InvitationOption{Email: "recipient@example.invalid", Role: governance_model.Reporter, Revision: group.Revision})
	require.NoError(t, err)
	token, err := secret.DecryptSecret(setting.SecretKey, invitation.TokenEncrypted)
	require.NoError(t, err)
	checker := &test.LogChecker{EventWriterBaseImpl: log.NewEventWriterBase("治理邀请日志验收", "test-log-checker", log.WriterMode{Level: log.TRACE})}
	logger := log.GetManager().GetLogger(log.DEFAULT)
	logger.AddWriters(checker)
	defer func() { require.NoError(t, logger.RemoveWriter(checker.GetWriterName())) }()
	checker.Filter(token, "NewMessageFrom (body)").StopMark("邀请投递日志检查结束")
	require.NoError(t, RunInvitations(ctx))
	log.Info("邀请投递日志检查结束")
	leaked, stopped := checker.Check(time.Second)
	require.True(t, stopped)
	require.Equal(t, []bool{false, false}, leaked, "邀请令牌和正文不得进入日志")
	var raw string
	select {
	case raw = <-received:
	case err := <-failure:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("未收到真实 SMTP 邮件")
	}
	message, err := mail.ReadMessage(strings.NewReader(raw))
	require.NoError(t, err)
	reader := message.Body
	if message.Header.Get("Content-Transfer-Encoding") == "quoted-printable" {
		reader = quotedprintable.NewReader(reader)
	}
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(body), "#token="+token)
	require.NotContains(t, string(body), "?token=")
	recipient, err := mail.ParseAddress(message.Header.Get("To"))
	require.NoError(t, err)
	require.Equal(t, "recipient@example.invalid", recipient.Address)
	stored := unittest.AssertExistsAndLoadBean(t, &governance_model.Invitation{ID: invitation.ID})
	require.Equal(t, "sent", stored.DeliveryState)
	require.Equal(t, 1, stored.DeliveryStep)
	require.Empty(t, stored.Lease)
}
