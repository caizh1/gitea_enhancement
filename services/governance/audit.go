// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"

	"github.com/google/uuid"
)

// CheckAuditAccess 每次读取真实账号和授权，不信任导出申请时的身份快照。
func CheckAuditAccess(ctx context.Context, userID int64, scope string, scopeID int64) error {
	if userID <= 0 || !slices.Contains([]string{"instance", "group", "repository", "user"}, scope) || (scope == "instance" && scopeID != 0) || (scope != "instance" && scopeID <= 0) {
		return governance_model.ErrNotFound
	}
	doer, err := user_model.GetUserByID(ctx, userID)
	if err != nil {
		if user_model.IsErrUserNotExist(err) {
			return governance_model.ErrNotFound
		}
		return err
	}
	if !doer.IsActive || doer.ProhibitLogin || doer.IsGiteaActions() || doer.IsGhost() || doer.IsOrganization() {
		return governance_model.ErrNotFound
	}
	if doer.IsAdmin || (doer.IsAuditor && doer.Type == user_model.UserTypeIndividual) {
		return nil
	}
	switch scope {
	case "user":
		if userID == scopeID {
			return nil
		}
	case "group":
		owner, err := organization.IsOrganizationOwner(ctx, scopeID, userID)
		if err != nil {
			return err
		}
		if owner {
			return nil
		}
		grants, err := governance_model.GroupGrants(ctx, scopeID, userID, time.Now())
		if errors.Is(err, governance_model.ErrNotFound) {
			return governance_model.ErrNotFound
		}
		if err != nil {
			return err
		}
		if governance_model.HasOwnerGrant(grants) {
			return nil
		}
		for _, grant := range grants {
			if !grant.Abilities[governance_model.ReadAudit] {
				continue
			}
			if grant.CustomRoleID > 0 {
				role, has, err := db.GetByID[governance_model.CustomRole](ctx, grant.CustomRoleID)
				if err != nil {
					return err
				}
				if has && slices.Contains(role.Abilities, governance_model.ReadAudit) {
					return nil
				}
			}
		}
	case "repository":
		repo, err := repo_model.GetRepositoryByID(ctx, scopeID)
		if err != nil {
			if repo_model.IsErrRepoNotExist(err) {
				return governance_model.ErrNotFound
			}
			return err
		}
		admin, err := access_model.IsUserRepoAdmin(ctx, repo, doer)
		if err != nil {
			return err
		}
		if admin {
			return nil
		}
		grants, err := governance_model.RepositoryGrants(ctx, repo.ID, repo.OwnerID, doer.ID, time.Now())
		if errors.Is(err, governance_model.ErrNotFound) {
			return governance_model.ErrNotFound
		}
		if err != nil {
			return err
		}
		if governance_model.EffectiveAbilities(grants)[governance_model.ReadAudit] {
			return nil
		}
	}
	// 范围不存在与无权访问使用相同响应，避免探测私有资源。
	return governance_model.ErrNotFound
}

func AuditScopeTitle(ctx context.Context, scope string, id int64) (string, error) {
	if scope == "instance" {
		return "整个实例", nil
	}
	if scope == "repository" {
		repo, err := repo_model.GetRepositoryByID(ctx, id)
		if err != nil {
			return "", err
		}
		if namespace, err := governance_model.GetNamespace(ctx, repo.OwnerID); err == nil {
			return namespace.FullPath + "/" + repo.Name, nil
		}
		return repo.FullName(), nil
	}
	if namespace, err := governance_model.GetNamespace(ctx, id); err == nil {
		return namespace.FullPath, nil
	}
	user, err := user_model.GetUserByID(ctx, id)
	if err != nil {
		return "", err
	}
	return user.Name, nil
}

// RequestActor 的地址必须来自 Gitea 已执行可信代理处理后的 RemoteAddr。
func RequestActor(user *user_model.User, remoteAddr, transport string) governance_model.Actor {
	if user == nil {
		return governance_model.Actor{Kind: "anonymous", Name: "未认证访问者", Transport: transport, RequestID: uuid.NewString()}
	}
	ip := remoteAddr
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		ip = host
	}
	if net.ParseIP(ip) == nil {
		ip = ""
	}
	kind := "user"
	if user.Type == user_model.UserTypeBot {
		kind = "service_account"
	}
	return governance_model.Actor{ID: user.ID, Name: user.Name, Kind: kind, IP: ip, Transport: transport, RequestID: uuid.NewString()}
}

// APIRequestActor 保留经管理员代办中间件认证的原始身份。
func APIRequestActor(effective, authenticated *user_model.User, remoteAddr string) governance_model.Actor {
	actor := RequestActor(effective, remoteAddr, "api")
	if authenticated != nil && authenticated.ID != effective.ID {
		actor = RequestActor(authenticated, remoteAddr, "api")
		actor.ActingAsID, actor.ActingAsName = effective.ID, effective.Name
	}
	return actor
}

func ParseAuditFilter(values url.Values, userID int64, export bool) (governance_model.AuditFilter, error) {
	f := governance_model.AuditFilter{ScopeType: values.Get("scope_type"), EventType: values.Get("event_type"), ObjectType: values.Get("entity_type"), Result: values.Get("result"), RequestID: values.Get("request_id")}
	if f.ScopeType == "" {
		f.ScopeType = "user"
	}
	for key, dest := range map[string]*int64{"scope_id": &f.ScopeID, "actor_id": &f.ActorID, "entity_id": &f.ObjectID} {
		if value := values.Get(key); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return f, governance_model.ErrInvalid
			}
			*dest = n
		}
	}
	if f.ScopeType == "user" && values.Get("scope_id") == "" {
		f.ScopeID = userID
	}
	f.To = time.Now().UTC()
	var err error
	if values.Get("to") != "" {
		f.To, err = time.Parse(time.RFC3339Nano, values.Get("to"))
		if err != nil {
			return f, governance_model.ErrInvalid
		}
	}
	f.From = f.To.Add(-governance_model.AuditQueryWindow)
	if values.Get("from") != "" {
		f.From, err = time.Parse(time.RFC3339Nano, values.Get("from"))
		if err != nil {
			return f, governance_model.ErrInvalid
		}
	}
	return f, f.Validate(export)
}

func GetAuditExport(ctx context.Context, userID int64, id string) (*governance_model.AuditExport, error) {
	job := new(governance_model.AuditExport)
	has, err := db.GetEngine(ctx).ID(id).Get(job)
	if err != nil {
		return nil, err
	}
	if !has || job.UserID != userID || !job.ExpiresAt.After(time.Now()) {
		return nil, governance_model.ErrNotFound
	}
	if err := CheckAuditAccess(ctx, userID, job.Filter.ScopeType, job.Filter.ScopeID); err != nil {
		return nil, err
	}
	job.CreatedAt, job.ExpiresAt = job.CreatedAt.UTC(), job.ExpiresAt.UTC()
	return job, nil
}

func checkDelegation(ctx context.Context, actor governance_model.Actor) error {
	if actor.ActingAsID == 0 {
		return nil
	}
	user, err := user_model.GetUserByID(ctx, actor.ID)
	if err != nil {
		if user_model.IsErrUserNotExist(err) {
			return governance_model.ErrNotFound
		}
		return err
	}
	if !user.IsAdmin || !user.IsActive || user.ProhibitLogin {
		return governance_model.ErrNotFound
	}
	return nil
}

// RunAuditExports 复用原生后台调度；数据库错误保留任务，下次恢复，撤权则明确标记拒绝。
func RunAuditExports(ctx context.Context) error {
	if err := governance_model.ExpireAuditExports(ctx, time.Now()); err != nil {
		return err
	}
	var jobs []*governance_model.AuditExport
	if err := db.GetEngine(ctx).In("state", "queued", "running").Asc("created_at").Limit(20).Find(&jobs); err != nil {
		return err
	}
	for _, job := range jobs {
		err := governance_model.AdvanceAuditExport(ctx, job.ID, func(ctx context.Context, current *governance_model.AuditExport) error {
			if err := checkDelegation(ctx, current.Actor); err != nil {
				return err
			}
			return CheckAuditAccess(ctx, current.UserID, current.Filter.ScopeType, current.Filter.ScopeID)
		})
		if errors.Is(err, governance_model.ErrNotFound) {
			_, err = db.GetEngine(ctx).ID(job.ID).In("state", "queued", "running").Cols("state").Update(&governance_model.AuditExport{State: "denied"})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func csvSafe(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func PrepareAuditDownload(ctx context.Context, actor governance_model.Actor, id string) (*governance_model.AuditExport, error) {
	var job *governance_model.AuditExport
	err := governance_model.WithWrite(ctx, nil, func(ctx context.Context) error {
		if err := checkDelegation(ctx, actor); err != nil {
			return err
		}
		var err error
		job, err = GetAuditExport(ctx, actor.EffectiveUserID(), id)
		if err != nil {
			return err
		}
		if job.State != "ready" {
			return governance_model.ErrConflict
		}
		details, err := json.Marshal(map[string]any{"export_id": job.ID, "format": job.Format, "rows": job.Rows, "phase": "download_started"})
		if err != nil {
			return err
		}
		return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "audit.exported", Actor: actor, ScopeType: job.Filter.ScopeType, ScopeID: job.Filter.ScopeID, AncestorIDs: job.AncestorIDs, ObjectType: "audit_export", Result: "pending", Details: details})
	})
	return job, err
}

// WriteAuditExport 只从已完成的证据块读取；每块下载前再次核对权限。
func WriteAuditExport(ctx context.Context, actor governance_model.Actor, job *governance_model.AuditExport, writer io.Writer) (retErr error) {
	defer func() {
		result := "success"
		if retErr != nil || ctx.Err() != nil {
			result = "failure"
		}
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		details, err := json.Marshal(map[string]any{"export_id": job.ID, "format": job.Format, "rows": job.Rows, "phase": "download_finished"})
		if err == nil {
			err = governance_model.WithWrite(recoveryCtx, nil, func(ctx context.Context) error {
				return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: "audit.exported", Actor: actor, ScopeType: job.Filter.ScopeType, ScopeID: job.Filter.ScopeID, AncestorIDs: job.AncestorIDs, ObjectType: "audit_export", Result: result, Details: details})
			})
		}
		if err != nil {
			log.Error("审计导出下载完成事件保存失败（export_id=%s, result=%s）", job.ID, result)
		}
	}()
	current, err := GetAuditExport(ctx, actor.EffectiveUserID(), job.ID)
	if err != nil {
		return err
	}
	if current.State != "ready" {
		return governance_model.ErrConflict
	}
	var csvWriter *csv.Writer
	if job.Format == "csv" {
		csvWriter = csv.NewWriter(writer)
		if err := csvWriter.Write([]string{"事件ID", "UTC时间", "事件类型", "操作者ID", "操作者", "身份类型", "入口", "来源IP", "关联ID", "对象类型", "对象ID", "当时路径", "结果", "详情JSON", "被代办用户ID", "被代办用户", "凭据ID"}); err != nil {
			return err
		}
	} else {
		if _, err := io.WriteString(writer, "[\n"); err != nil {
			return err
		}
	}
	first := true
	for ordinal := int64(1); ordinal <= current.Chunks; ordinal++ {
		if err := CheckAuditAccess(ctx, actor.EffectiveUserID(), job.Filter.ScopeType, job.Filter.ScopeID); err != nil {
			return err
		}
		var chunk governance_model.AuditExportChunk
		has, err := db.GetEngine(ctx).Where("export_id = ? AND ordinal = ?", job.ID, ordinal).Get(&chunk)
		if err != nil {
			return err
		}
		if !has {
			return governance_model.ErrConflict
		}
		for _, event := range chunk.Events {
			if csvWriter != nil {
				fields := []string{event.EventID, event.OccurredAt.UTC().Format(time.RFC3339Nano), event.Type, strconv.FormatInt(event.ActorID, 10), event.Actor.Name, event.Actor.Kind, event.Actor.Transport, event.Actor.IP, event.RequestID, event.ObjectType, strconv.FormatInt(event.ObjectID, 10), event.ObjectPath, event.Result, string(event.Details), strconv.FormatInt(event.Actor.ActingAsID, 10), event.Actor.ActingAsName, strconv.FormatInt(event.Actor.CredentialID, 10)}
				for i := range fields {
					fields[i] = csvSafe(fields[i])
				}
				if err := csvWriter.Write(fields); err != nil {
					return err
				}
			} else {
				if !first {
					if _, err := io.WriteString(writer, ",\n"); err != nil {
						return err
					}
				}
				if err := json.NewEncoder(writer).Encode(event); err != nil {
					return err
				}
				first = false
			}
		}
		if csvWriter != nil {
			csvWriter.Flush()
			if err := csvWriter.Error(); err != nil {
				return err
			}
		}
	}
	if csvWriter != nil {
		csvWriter.Flush()
		return csvWriter.Error()
	}
	_, err = fmt.Fprint(writer, "]\n")
	return err
}
