// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/modules/secret"
	"gitea.dev/modules/setting"
	sender_service "gitea.dev/services/mailer/sender"

	"github.com/google/uuid"
)

var deliverInvitation = sendInvitationMail

func sendInvitationMail(invitation *governance_model.Invitation) error {
	if setting.MailService == nil || setting.MailService.Protocol == "dummy" {
		return errors.New("未配置可投递的邮件服务")
	}
	token, err := secret.DecryptSecret(setting.SecretKey, invitation.TokenEncrypted)
	if err != nil {
		return errors.New("无法解密邀请凭据")
	}
	if len(token) != 64 {
		return errors.New("邀请凭据无效")
	}
	link := setting.AppURL + "governance/invitations/" + strconv.FormatInt(invitation.ID, 10) + "#token=" + token
	body := "<html><body><p>你收到了资源 <strong>" + html.EscapeString(invitation.ScopePath) + "</strong> 的成员邀请。</p><p><a href=\"" + html.EscapeString(link) + "\">查看并决定是否接受邀请</a></p><p>请使用已验证邮箱 " + html.EscapeString(invitation.Email) + " 对应的账号登录。邀请到期时间：" + time.Unix(invitation.ExpiresUnix, 0).UTC().Format(time.RFC3339) + "。</p><p>如果浏览器无法自动读取邀请，请在邀请页面输入以下代码：</p><p><code>" + token + "</code></p></body></html>"
	// 原生 NewMessage 会在 Trace 级别输出正文，邀请凭据不能进入日志。
	message := &sender_service.Message{FromAddress: setting.MailService.FromEmail, FromDisplayName: setting.MailService.FromName, To: invitation.Email, Subject: "成员访问邀请", Body: body, Date: time.Now(), Headers: map[string][]string{}, Info: "治理成员邀请"}
	message.SetHeader("Message-ID", fmt.Sprintf("<governance-invitation-%d-%d@gitea.local>", invitation.ID, invitation.DeliveryStep))
	var sender sender_service.Sender = &sender_service.SMTPSender{Timeout: 30 * time.Second, RequireSTARTTLS: true}
	if setting.MailService.Protocol == "sendmail" {
		sender = &sender_service.SendmailSender{Timeout: 30 * time.Second}
	}
	return sender_service.Send(sender, message)
}

// RunInvitations 采用持久化租约和至少一次投递，SMTP 回执丢失可能重复发送同一邀请。
func RunInvitations(ctx context.Context) error {
	now := time.Now().Unix()
	var expired []*governance_model.Invitation
	if err := db.GetEngine(ctx).Where("expires_unix <= ?", now).Asc("id").Limit(100).Find(&expired); err != nil {
		return err
	}
	var failures []error
	for _, candidate := range expired {
		err := governance_model.WithWrite(ctx, []string{governance_model.Resource(candidate.ScopeType, candidate.ScopeID)}, func(ctx context.Context) error {
			invitation, has, err := db.GetByID[governance_model.Invitation](ctx, candidate.ID)
			if err != nil || !has || invitation.ExpiresUnix > time.Now().Unix() {
				return err
			}
			state, err := invitationResource(ctx, invitation)
			if err != nil {
				return err
			}
			actor := governance_model.Actor{Kind: "system", Name: "邀请到期任务", Transport: "background"}
			if err := invitationAudit(ctx, actor, state, invitation, "invitation.expired", 0); err != nil {
				return err
			}
			_, err = db.GetEngine(ctx).ID(invitation.ID).Delete(new(governance_model.Invitation))
			return err
		})
		if err != nil {
			failures = append(failures, err)
		}
	}
	var due []*governance_model.Invitation
	if err := db.GetEngine(ctx).Where("next_delivery_unix > 0 AND next_delivery_unix <= ? AND expires_unix > ? AND lease_until <= ?", now, now, now).Asc("next_delivery_unix", "id").Limit(10).Find(&due); err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, candidate := range due {
		var claimed *governance_model.Invitation
		err := governance_model.WithWrite(ctx, []string{governance_model.Resource(candidate.ScopeType, candidate.ScopeID)}, func(ctx context.Context) error {
			invitation, has, err := db.GetByID[governance_model.Invitation](ctx, candidate.ID)
			if err != nil || !has {
				return err
			}
			current := time.Now().Unix()
			if invitation.ExpiresUnix <= current || invitation.NextDeliveryUnix == 0 || invitation.NextDeliveryUnix > current || invitation.LeaseUntil > current {
				return nil
			}
			option := GroupMemberOption{Role: invitation.Role, CustomRoleID: invitation.CustomRoleID, ExpiresUnix: invitation.MembershipExpiresUnix}
			_, err = invitationAuthority(ctx, invitation.Inviter, invitation.ScopeType, invitation.ScopeID, option)
			if errors.Is(err, governance_model.ErrNotFound) || errors.Is(err, governance_model.ErrConflict) || errors.Is(err, governance_model.ErrInvalid) {
				invitation.DeliveryState = "blocked"
				invitation.NextDeliveryUnix = current + 3600
				_, err = db.GetEngine(ctx).ID(invitation.ID).Cols("delivery_state", "next_delivery_unix").Update(invitation)
				return err
			}
			if err != nil {
				return err
			}
			invitation.Lease = uuid.NewString()
			invitation.LeaseUntil = current + 120
			invitation.DeliveryState = "sending"
			if _, err := db.GetEngine(ctx).ID(invitation.ID).Cols("lease", "lease_until", "delivery_state").Update(invitation); err != nil {
				return err
			}
			claimed = invitation
			return nil
		})
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if claimed == nil {
			continue
		}
		sendErr := deliverInvitation(claimed)
		err = governance_model.WithWrite(ctx, []string{governance_model.Resource(claimed.ScopeType, claimed.ScopeID)}, func(ctx context.Context) error {
			invitation, has, err := db.GetByID[governance_model.Invitation](ctx, claimed.ID)
			if err != nil || !has || invitation.Lease != claimed.Lease {
				return err
			}
			invitation.Lease = ""
			invitation.LeaseUntil = 0
			current := time.Now().Unix()
			if sendErr != nil {
				invitation.DeliveryState = "failed"
				invitation.DeliveryFailures++
				invitation.NextDeliveryUnix = current + min(int64(3600), int64(10)*(1<<min(invitation.DeliveryFailures, 8)))
			} else {
				invitation.LastSentUnix = current
				invitation.DeliveryState = "sent"
				invitation.DeliveryFailures = 0
				invitation.DeliveryStep++
				// 错过的提醒不立即补发多封；下一次仍按创建后的第 2、5、10 天计算。
				days := []int64{0, 2, 5, 10}
				invitation.NextDeliveryUnix = 0
				for invitation.DeliveryStep < len(days) {
					next := invitation.CreatedUnix + days[invitation.DeliveryStep]*86400
					if next > current {
						invitation.NextDeliveryUnix = next
						break
					}
					invitation.DeliveryStep++
				}
			}
			_, err = db.GetEngine(ctx).ID(invitation.ID).Cols("lease", "lease_until", "delivery_state", "delivery_failures", "next_delivery_unix", "last_sent_unix", "delivery_step").Update(invitation)
			return err
		})
		if err != nil {
			failures = append(failures, err)
		}
		if sendErr != nil {
			failures = append(failures, fmt.Errorf("邀请 %d 投递失败，已保留重试状态", claimed.ID))
		}
	}
	return errors.Join(failures...)
}
