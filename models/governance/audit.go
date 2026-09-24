// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"

	"github.com/google/uuid"
	"xorm.io/builder"
)

var auditFailureCount atomic.Int64
var auditLastFailureUnix atomic.Int64

// AuditHealthStatus 不依赖治理数据库，数据库故障时管理能力接口仍可暴露告警。
func AuditHealthStatus() map[string]any {
	return map[string]any{"healthy": auditFailureCount.Load() == 0, "failure_count": auditFailureCount.Load(), "last_failure_unix": auditLastFailureUnix.Load()}
}

func recordAuditFailure() {
	auditFailureCount.Add(1)
	auditLastFailureUnix.Store(time.Now().Unix())
	log.Error("治理审计保存失败；已更新进程健康告警（未记录原始错误，避免泄露敏感数据）")
}

var ErrNoAuditDestination = errors.New("详细访问审计尚未配置有效外送目标")

type EventDefinition struct {
	Description string `json:"description"`
	StreamOnly  bool   `json:"stream_only"`
}

// EventCatalog 只登记已有服务端触发定义，接口不能接受任意事件类型。
var EventCatalog = map[string]EventDefinition{
	"repository.tag_protection_created":        {Description: "创建保护标签规则"},
	"repository.tag_protection_updated":        {Description: "修改保护标签规则"},
	"repository.tag_protection_deleted":        {Description: "删除保护标签规则"},
	"repository.branch_protection_created":     {Description: "创建保护分支规则"},
	"repository.branch_protection_updated":     {Description: "修改保护分支规则或顺序"},
	"repository.branch_protection_deleted":     {Description: "删除保护分支规则"},
	"webhook.created":                          {Description: "创建 Webhook"},
	"webhook.updated":                          {Description: "修改 Webhook"},
	"webhook.deleted":                          {Description: "删除 Webhook"},
	"repository.created":                       {Description: "创建、导入或派生项目"},
	"repository.creation_recovery_required":    {Description: "项目创建中断，需要管理员核对"},
	"repository.updated":                       {Description: "修改项目设置"},
	"repository.transferred":                   {Description: "转移项目"},
	"repository.mirror_configured":             {Description: "配置项目镜像"},
	"repository.mirror_updated":                {Description: "修改项目镜像配置"},
	"repository.mirror_recovery_required":      {Description: "镜像配置中断，需要管理员核对"},
	"repository.reference_hook_installed":      {Description: "安装并回读验证引用事务入口"},
	"repository.reference_hook_install_failed": {Description: "引用事务入口安装被拒绝或冲突"},
	"issue.created":                            {Description: "创建议题"},
	"issue.updated":                            {Description: "修改议题"},
	"issue.closed":                             {Description: "关闭议题"},
	"issue.reopened":                           {Description: "重新打开议题"},
	"issue.deleted":                            {Description: "删除议题"},
	"comment.created":                          {Description: "创建评论"},
	"comment.updated":                          {Description: "修改评论"},
	"comment.deleted":                          {Description: "删除评论"},
	"pull.created":                             {Description: "创建合并请求"},
	"pull.updated":                             {Description: "修改合并请求"},
	"pull.closed":                              {Description: "关闭合并请求"},
	"pull.reopened":                            {Description: "重新打开合并请求"},
	"wiki.created":                             {Description: "创建 Wiki 页面"},
	"wiki.updated":                             {Description: "修改 Wiki 页面"},
	"wiki.deleted":                             {Description: "删除 Wiki 页面"},
	"release.created":                          {Description: "创建版本发布"},
	"release.updated":                          {Description: "修改版本发布"},
	"release.deleted":                          {Description: "删除版本发布"},
	"repository.label_created":                 {Description: "创建项目标签"},
	"repository.label_updated":                 {Description: "修改项目标签"},
	"repository.label_deleted":                 {Description: "删除项目标签"},
	"attachment.created":                       {Description: "创建附件"},
	"attachment.updated":                       {Description: "修改附件公开文件名"},
	"attachment.deleted":                       {Description: "删除附件"},
	"credential.totp_enabled":                  {Description: "启用双因素认证"},
	"credential.totp_disabled":                 {Description: "停用双因素认证"},
	"credential.totp_updated":                  {Description: "轮换双因素恢复码"},
	"credential.recovery_code_consumed":        {Description: "消费一次性双因素恢复码"},
	"credential.webauthn_created":              {Description: "登记通行密钥"},
	"credential.webauthn_revoked":              {Description: "撤销通行密钥"},
	"user.security_changed":                    {Description: "修改账号安全设置或状态"},
	"user.blocked":                             {Description: "屏蔽用户 / Block user"},
	"user.unblocked":                           {Description: "解除屏蔽 / Unblock user"},
	"user.block_note_updated":                  {Description: "修改屏蔽备注 / Update block note"},
	"user.created":                             {Description: "创建用户账号"},
	"user.deleted":                             {Description: "删除用户账号"},
	"credential.email_created":                 {Description: "添加账号邮箱"},
	"credential.email_deleted":                 {Description: "删除账号邮箱"},
	"credential.email_updated":                 {Description: "修改账号邮箱验证状态"},
	"credential.primary_email_changed":         {Description: "修改账号主邮箱"},
	"credential.password_changed":              {Description: "修改账号密码并撤销持久登录凭据"},
	"user.auditor_changed":                     {Description: "修改全站审计员身份"},
	"approval.denied":                          {Description: "拒绝不符合身份要求的批准"},
	"authentication.login_authorized":          {Description: "登录认证通过，开始建立会话"},
	"authentication.login_succeeded":           {Description: "登录会话建立成功"},
	"authentication.login_failed":              {Description: "登录失败"},
	"authentication.source_created":            {Description: "创建认证源"},
	"authentication.source_updated":            {Description: "修改认证源"},
	"authentication.source_deleted":            {Description: "删除认证源"},
	"oauth.application_created":                {Description: "创建 OAuth 应用"},
	"oauth.application_updated":                {Description: "修改 OAuth 应用"},
	"oauth.application_deleted":                {Description: "删除 OAuth 应用"},
	"oauth.application_secret_rotated":         {Description: "轮换 OAuth 应用密钥"},
	"oauth.authorization_granted":              {Description: "授权 OAuth 应用"},
	"oauth.authorization_revoked":              {Description: "撤销 OAuth 应用授权"},
	"oauth.authorization_code_issued":          {Description: "签发 OAuth 授权码"},
	"oauth.token_issued":                       {Description: "签发 OAuth 访问令牌"},
	"oauth.token_refreshed":                    {Description: "刷新 OAuth 访问令牌"},
	"actions.secret_created":                   {Description: "创建 Actions 秘密"},
	"actions.secret_updated":                   {Description: "更新 Actions 秘密"},
	"actions.secret_deleted":                   {Description: "删除 Actions 秘密"},
	"actions.variable_created":                 {Description: "创建 Actions 变量"},
	"actions.variable_updated":                 {Description: "更新 Actions 变量"},
	"actions.variable_deleted":                 {Description: "删除 Actions 变量"},
	"actions.runner_token_rotated":             {Description: "轮换 Actions Runner 注册令牌"},
	"actions.runner_created":                   {Description: "注册 Actions Runner"},
	"actions.runner_updated":                   {Description: "修改 Actions Runner"},
	"actions.runner_deleted":                   {Description: "删除 Actions Runner"},
	"actions.config_updated":                   {Description: "修改 Actions 权限配置"},
	"actions.run_triggered":                    {Description: "触发 Actions 工作流"},
	"actions.run_cancelled":                    {Description: "取消 Actions 工作流"},
	"actions.run_deleted":                      {Description: "删除 Actions 工作流记录"},
	"actions.run_rerun":                        {Description: "重跑 Actions 工作流"},
	"admin.task_triggered":                     {Description: "管理员手动触发后台维护任务"},
	"admin.task_completed":                     {Description: "后台维护任务执行完成"},
	"package.version_published":                {Description: "发布软件包版本"},
	"package.version_deleted":                  {Description: "删除软件包版本"},
	"package.file_added":                       {Description: "添加软件包文件"},
	"package.file_deleted":                     {Description: "删除软件包文件"},
	"package.repository_link_changed":          {Description: "修改软件包项目关联"},
	"package.tag_changed":                      {Description: "修改软件包分发标签"},
	"package.cleanup_rule_created":             {Description: "创建软件包清理规则"},
	"package.cleanup_rule_updated":             {Description: "修改软件包清理规则"},
	"package.cleanup_rule_deleted":             {Description: "删除软件包清理规则"},
	"credential.ssh_key_verified":              {Description: "验证 SSH 公钥持有证明"},
	"credential.ssh_key_created":               {Description: "创建 SSH 公钥"},
	"credential.ssh_key_revoked":               {Description: "撤销 SSH 公钥"},
	"credential.deploy_key_created":            {Description: "创建项目部署密钥"},
	"credential.deploy_key_revoked":            {Description: "撤销项目部署密钥"},
	"credential.gpg_key_created":               {Description: "创建 GPG 公钥"},
	"credential.gpg_key_verified":              {Description: "验证 GPG 公钥"},
	"credential.gpg_key_revoked":               {Description: "撤销 GPG 公钥"},
	"credential.access_token_created":          {Description: "创建个人访问令牌"},
	"credential.access_token_revoked":          {Description: "撤销个人访问令牌"},
	"group.unarchived":                         {Description: "恢复归档群组"},
	"pull.closed_by_repository_deletion":       {Description: "删除来源项目时关闭外部合并请求"},
	"repository.deletion_scheduled":            {Description: "项目计划删除"},
	"repository.restored":                      {Description: "恢复待删除项目"},
	"repository.archived":                      {Description: "归档项目"},
	"repository.unarchived":                    {Description: "恢复归档项目"},
	"review.changes_requested":                 {Description: "请求修改"},
	"review.dismissed":                         {Description: "撤销评审状态"},
	"review.restored":                          {Description: "恢复评审状态"},
	"review.deleted":                           {Description: "删除评审"},
	"repository.approval_hook_installed":       {Description: "接入原生审批引用事务入口"},
	"git.push_denied":                          {Description: "审批门禁拒绝目标分支写入"},
	"git.reference_transaction":                {Description: "Git 引用事务准备与结果核对"},
	"user.renamed":                             {Description: "修改个人账号名称"},
	"group.created":                            {Description: "创建群组"},
	"group.updated":                            {Description: "修改群组"},
	"group.owner_attention_required":           {Description: "群组所有者需要管理员关注"},
	"group.transferred":                        {Description: "转移群组"},
	"group.archived":                           {Description: "归档群组"},
	"group.deletion_scheduled":                 {Description: "计划删除群组"},
	"group.restored":                           {Description: "恢复群组"},
	"invitation.created":                       {Description: "邀请成员"},
	"invitation.accepted":                      {Description: "接受成员邀请"},
	"invitation.declined":                      {Description: "拒绝成员邀请"},
	"invitation.revoked":                       {Description: "撤销成员邀请"},
	"invitation.expired":                       {Description: "成员邀请过期"},
	"access_request.fulfilled":                 {Description: "接受邀请后完成待处理申请"},
	"access_request.created":                   {Description: "申请访问资源"},
	"access_request.approved":                  {Description: "批准访问申请"},
	"access_request.denied":                    {Description: "拒绝访问申请"},
	"access_request.withdrawn":                 {Description: "撤回访问申请"},
	"access_request.setting_changed":           {Description: "修改访问申请开关"},
	"member.added":                             {Description: "添加成员"},
	"member.updated":                           {Description: "修改成员权限"},
	"member.removed":                           {Description: "移除成员"},
	"member.expired":                           {Description: "成员授权到期"},
	"share.expired":                            {Description: "共享授权到期"},
	"share.created":                            {Description: "建立共享"},
	"share.updated":                            {Description: "修改共享"},
	"share.removed":                            {Description: "撤销共享"},
	"role.created":                             {Description: "创建自定义角色"},
	"role.updated":                             {Description: "修改自定义角色"},
	"role.removed":                             {Description: "删除自定义角色"},
	"approval.rule_changed":                    {Description: "修改审批规则"},
	"approval.policy_changed":                  {Description: "修改实例或顶级群组强制审批策略"},
	"approval.settings_changed":                {Description: "修改审批设置"},
	"approval.reauthentication_attempt":        {Description: "开始批准再次认证"},
	"approval.reauthenticated":                 {Description: "批准再次认证结果"},
	"approval.approved":                        {Description: "批准变更"},
	"approval.withdrawn":                       {Description: "撤回批准"},
	"approval.invalidated":                     {Description: "批准失效"},
	"merge.authorized":                         {Description: "取得最终合并授权"},
	"merge.reconciled":                         {Description: "核对合并结果"},
	"merge.native_state_recovered":             {Description: "恢复已成功合并的原生 PR 状态"},
	"governance.operation_recovered":           {Description: "通过真实引用锁恢复治理操作"},
	"merge.denied":                             {Description: "拒绝合并"},
	"audit.stream_changed":                     {Description: "修改审计外送"},
	"audit.stream_test":                        {Description: "测试审计外送连通性"},
	"audit.stream_test_result":                 {Description: "审计外送连通性测试结果"},
	"audit.exported":                           {Description: "导出审计证据"},
	"audit.export_requested":                   {Description: "申请导出审计证据"},
	"resource.deletion_committed":              {Description: "提交资源删除并等待持久化存储清理"},
	"resource.cleanup_completed":               {Description: "完成已删除资源的持久化存储清理"},
	"access.git_http":                          {Description: "通过 HTTP 访问 Git", StreamOnly: true},
	"access.git_ssh":                           {Description: "通过 SSH 访问 Git", StreamOnly: true},
	"access.code":                              {Description: "读取代码文件", StreamOnly: true},
	"access.archive":                           {Description: "下载代码归档", StreamOnly: true},
	"access.attachment":                        {Description: "访问附件", StreamOnly: true},
	"access.package_download":                  {Description: "下载软件包文件", StreamOnly: true},
	"access.actions_log":                       {Description: "下载 Actions 运行日志", StreamOnly: true},
	"access.actions_artifact":                  {Description: "下载 Actions 构件", StreamOnly: true},

	"actions.runner_token_deactivated_on_transfer": {Description: "转移项目时失效 Actions Runner 注册令牌"},
}

func validateAuditDetails(raw json.Value) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > 64*1024 {
		return fmt.Errorf("%w：审计详情超过 64 KiB", ErrInvalid)
	}
	var details map[string]any
	if err := json.Unmarshal(raw, &details); err != nil {
		return ErrInvalid
	}
	var walk func(any, int) error
	walk = func(value any, depth int) error {
		if depth > 10 {
			return ErrInvalid
		}
		switch v := value.(type) {
		case map[string]any:
			for key, item := range v {
				normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
				if slices.Contains([]string{"password", "passwd", "token", "secret", "cookie", "authorization", "privatekey", "code", "body"}, normalized) {
					return fmt.Errorf("%w：审计不得记录秘密或完整正文", ErrInvalid)
				}
				if err := walk(item, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range v {
				if err := walk(item, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(details, 0)
}

// AppendAudit 必须与业务变更共用调用者事务；只接受服务端构造的结构化字段。
func AppendAudit(ctx context.Context, event *AuditEvent) error {
	err := appendAudit(ctx, event, false)
	if err != nil {
		recordAuditFailure()
	}
	return err
}

// AppendConfiguredAccessAudit 未配置匹配目标时返回未启用；目标暂停仍持久化排队。
func AppendConfiguredAccessAudit(ctx context.Context, event *AuditEvent) (bool, error) {
	if definition, ok := EventCatalog[event.Type]; !ok || !definition.StreamOnly {
		return false, ErrInvalid
	}
	err := appendAudit(ctx, event, true)
	if errors.Is(err, ErrNoAuditDestination) {
		return false, nil
	}
	if err != nil {
		recordAuditFailure()
	}
	return err == nil, err
}

func appendAudit(ctx context.Context, event *AuditEvent, includePaused bool) error {
	definition, ok := EventCatalog[event.Type]
	if !ok || event.Actor.Kind == "" || event.Actor.Transport == "" || event.ObjectType == "" || event.ScopeID < 0 ||
		!slices.Contains([]string{"instance", "group", "repository", "user"}, event.ScopeType) ||
		(event.ScopeType == "instance" && event.ScopeID != 0) ||
		!slices.Contains([]string{"success", "failure", "denied", "pending", "unknown"}, event.Result) {
		return ErrInvalid
	}
	if err := validateAuditDetails(json.Value(event.Details)); err != nil {
		return err
	}
	// 同时排列事件入队与目标变更；导出序号也按治理事务提交顺序生成。
	return WithWrite(ctx, nil, func(ctx context.Context) error {
		event.ID, event.EventID, event.Version = 0, uuid.NewString(), 1
		// 与现有 DATETIME 列的秒精度一致，外送正文和本地读取的事件时间相同。
		event.OccurredAt = time.Now().UTC().Truncate(time.Second)
		event.ActorID = event.Actor.ID
		if event.Actor.RequestID == "" {
			event.Actor.RequestID = event.EventID
		}
		if len(event.Actor.RequestID) > 128 {
			return ErrInvalid
		}
		event.RequestID = event.Actor.RequestID
		groupIDs := slices.Clone(event.AncestorIDs)
		if event.ScopeType == "group" {
			groupIDs = append(groupIDs, event.ScopeID)
		}
		condition := builder.Eq{"scope_type": "instance", "scope_id": 0}
		var streams []*AuditStream
		query := db.GetEngine(ctx).Where(builder.Or(condition,
			builder.And(builder.Eq{"scope_type": "group"}, builder.In("scope_id", groupIDs))))
		if !includePaused {
			query = query.And("enabled = ?", true)
		}
		if err := query.Find(&streams); err != nil {
			return err
		}
		if !definition.StreamOnly {
			if err := db.Insert(ctx, event); err != nil {
				return err
			}
			scopes := []AuditScope{{ScopeType: "instance"}, {ScopeType: event.ScopeType, ScopeID: event.ScopeID}}
			if event.ActorID > 0 {
				scopes = append(scopes, AuditScope{ScopeType: "user", ScopeID: event.ActorID})
			}
			if event.Actor.ActingAsID > 0 {
				scopes = append(scopes, AuditScope{ScopeType: "user", ScopeID: event.Actor.ActingAsID})
			}
			if event.ObjectType == "user" && event.ObjectID > 0 {
				scopes = append(scopes, AuditScope{ScopeType: "user", ScopeID: event.ObjectID})
			}
			for _, groupID := range groupIDs {
				scopes = append(scopes, AuditScope{ScopeType: "group", ScopeID: groupID})
			}
			seen := make(map[string]bool)
			for _, scope := range scopes {
				key := Resource(scope.ScopeType, scope.ScopeID)
				if seen[key] {
					continue
				}
				seen[key] = true
				scope.EventSequence, scope.OccurredAt = event.ID, event.OccurredAt
				if err := db.Insert(ctx, &scope); err != nil {
					return err
				}
			}
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		matched := 0
		for _, stream := range streams {
			if len(stream.EventTypes) > 0 && !slices.Contains(stream.EventTypes, event.Type) {
				continue
			}
			matched++
			if err := db.Insert(ctx, &AuditDelivery{StreamID: stream.ID, StreamRevision: stream.Revision, EventID: event.EventID, Payload: string(payload)}); err != nil {
				return err
			}
		}
		if definition.StreamOnly && matched == 0 {
			return ErrNoAuditDestination
		}
		return nil
	})
}

// LeaseDelivery 使用条件更新领取任务；网络发送在事务外执行。
func LeaseDelivery(ctx context.Context, streamID int64, now time.Time) (*AuditDelivery, error) {
	var delivery AuditDelivery
	has, err := db.GetEngine(ctx).Where("stream_id = ? AND next_attempt <= ? AND lease_until <= ?", streamID, now.Unix(), now.Unix()).Asc("id").Get(&delivery)
	if err != nil || !has {
		return nil, err
	}
	previousToken := delivery.LeaseToken
	delivery.LeaseToken, delivery.LeaseUntil = uuid.NewString(), now.Add(time.Minute).Unix()
	n, err := db.GetEngine(ctx).ID(delivery.ID).Where("lease_token = ? AND lease_until <= ?", previousToken, now.Unix()).
		Cols("lease_token", "lease_until").Update(&delivery)
	if err != nil || n == 0 {
		return nil, err
	}
	return &delivery, nil
}

// FinishDelivery 旧工作者的迟到响应不能确认另一次领取；重复发送保留相同事件 ID。
func FinishDelivery(ctx context.Context, delivery *AuditDelivery, delivered bool, now time.Time) error {
	if delivery == nil || delivery.LeaseToken == "" {
		return ErrInvalid
	}
	return WithWrite(ctx, nil, func(ctx context.Context) error {
		return finishDelivery(ctx, delivery, delivered, now)
	})
}

func finishDelivery(ctx context.Context, delivery *AuditDelivery, delivered bool, now time.Time) error {
	var n int64
	var err error
	if delivered {
		n, err = db.GetEngine(ctx).ID(delivery.ID).Where("lease_token = ?", delivery.LeaseToken).Delete(new(AuditDelivery))
	} else {
		delivery.Attempts++
		delivery.NextAttempt = now.Add(time.Second * time.Duration(min(3600, 1<<min(delivery.Attempts, 12)))).Unix()
		delivery.LeaseUntil = 0
		// 不存远端响应正文，以免认证凭据或内部资源内容进入诊断。
		delivery.LastError = "外送未获确认，等待重新投递"
		n, err = db.GetEngine(ctx).ID(delivery.ID).Where("lease_token = ?", delivery.LeaseToken).
			Cols("attempts", "next_attempt", "lease_until", "last_error").Update(delivery)
	}
	if err == nil && n != 1 {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	stream, has, err := db.GetByID[AuditStream](ctx, delivery.StreamID)
	if err != nil {
		return err
	}
	if !has {
		return ErrNotFound
	}
	if delivered {
		stream.DeliveredCount++
		stream.LastDeliveredAt, stream.LastError = now.Unix(), ""
	} else {
		stream.LastError = delivery.LastError
	}
	_, err = db.GetEngine(ctx).ID(stream.ID).Cols("delivered_count", "last_delivered_at", "last_error").Update(stream)
	if err != nil {
		return err
	}
	var event AuditEvent
	if json.Unmarshal([]byte(delivery.Payload), &event) != nil {
		return ErrInvalid
	}
	if event.Type == "audit.stream_test" && (delivered || delivery.Attempts == 1) {
		result := "failure"
		attempts := delivery.Attempts
		if delivered {
			result = "success"
			attempts++
		}
		details, err := json.Marshal(map[string]any{"tested_event_id": event.EventID, "stream_id": stream.ID, "attempts": attempts})
		if err != nil {
			return err
		}
		return AppendAudit(ctx, &AuditEvent{Type: "audit.stream_test_result", Actor: Actor{Kind: "system", Name: "审计外送任务", Transport: "background", RequestID: event.RequestID}, ScopeType: event.ScopeType, ScopeID: event.ScopeID, AncestorIDs: event.AncestorIDs, ObjectType: "audit_stream", ObjectID: stream.ID, ObjectPath: stream.Name, Result: result, Details: details})
	}
	return nil
}
