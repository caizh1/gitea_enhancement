// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"fmt"
	"strings"

	"gitea.dev/models/db"

	"xorm.io/xorm"
	"xorm.io/xorm/schemas"
)

type repositoryStableStorage struct {
	ID                     int64  `xorm:"pk autoincr"`
	GovernanceStorageOwner string `xorm:"VARCHAR(255) NOT NULL DEFAULT ''"`
}

func (*repositoryStableStorage) TableName() string { return "repository" }

type namespacePathHashMigration struct {
	ID            int64  `xorm:"pk"`
	LowerPath     string `xorm:"VARCHAR(2048) NOT NULL"`
	LowerPathHash string `xorm:"CHAR(64) NOT NULL DEFAULT ''"`
}

func (*namespacePathHashMigration) TableName() string { return "governance_namespace" }

type resourcePathHashMigration struct {
	Path     string `xorm:"VARCHAR(2048) NOT NULL"`
	PathHash string `xorm:"CHAR(64) NOT NULL DEFAULT ''"`
}

func (*resourcePathHashMigration) TableName() string { return "governance_resource_path" }

type namespacePathHashUnique struct {
	LowerPathHash string `xorm:"CHAR(64) UNIQUE NOT NULL"`
}

func (*namespacePathHashUnique) TableName() string { return "governance_namespace" }

type resourcePathHashUnique struct {
	PathHash string `xorm:"CHAR(64) UNIQUE NOT NULL"`
}

func (*resourcePathHashUnique) TableName() string { return "governance_resource_path" }

func ensurePathHashUnique(engine db.EngineMigration, tableName, column string, bean any) error {
	tables, err := engine.DBMetas()
	if err != nil {
		return err
	}
	for _, table := range tables {
		if table.Name != tableName {
			continue
		}
		for _, index := range table.Indexes {
			if index.Type == schemas.UniqueType && len(index.Cols) == 1 && strings.EqualFold(index.Cols[0], column) {
				return nil
			}
		}
		session := engine.NewSession()
		defer session.Close()
		return session.CreateUniques(bean)
	}
	return fmt.Errorf("治理路径表 %s 不存在", tableName)
}

func dropPostgresLegacyPathConstraint(engine db.EngineMigration) error {
	if engine.Dialect().URI().DBType != schemas.POSTGRES {
		return nil
	}
	rows, err := engine.Query(`SELECT c.conname FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.conrelid = to_regclass(?) AND c.contype = 'u'
		AND array_length(c.conkey, 1) = 1 AND a.attname = 'lower_path'`, engine.TableName(new(Namespace), true))
	if err != nil {
		return err
	}
	for _, row := range rows {
		name := strings.ReplaceAll(string(row["conname"]), `"`, `""`)
		if _, err := engine.Exec("ALTER TABLE " + engine.TableName(new(Namespace), true) + ` DROP CONSTRAINT "` + name + `"`); err != nil {
			return err
		}
	}
	return nil
}

// AddPathHashes 先回填完整路径摘要，再创建短唯一索引；旧约束只在新索引就绪后处理。
func AddPathHashes(engine db.EngineMigration) error {
	if _, err := engine.SyncWithOptions(xorm.SyncOptions{IgnoreIndices: true, IgnoreConstrains: true}, new(namespacePathHashMigration), new(resourcePathHashMigration)); err != nil {
		return err
	}
	var namespaces []namespacePathHashMigration
	if err := engine.Table("governance_namespace").Find(&namespaces); err != nil {
		return err
	}
	for _, namespace := range namespaces {
		if _, err := engine.Table("governance_namespace").Where("id = ?", namespace.ID).
			Update(map[string]any{"lower_path_hash": pathHash(namespace.LowerPath)}); err != nil {
			return err
		}
	}
	var paths []resourcePathHashMigration
	if err := engine.Table("governance_resource_path").Find(&paths); err != nil {
		return err
	}
	for _, path := range paths {
		if _, err := engine.Table("governance_resource_path").Where("path = ?", path.Path).
			Update(map[string]any{"path_hash": pathHash(path.Path)}); err != nil {
			return err
		}
	}
	if err := ensurePathHashUnique(engine, "governance_namespace", "lower_path_hash", new(namespacePathHashUnique)); err != nil {
		return err
	}
	if err := ensurePathHashUnique(engine, "governance_resource_path", "path_hash", new(resourcePathHashUnique)); err != nil {
		return err
	}
	return dropPostgresLegacyPathConstraint(engine)
}

// CreateSchema 对应定制版迁移 343；后续上游移植必须显式处理迁移编号。
func CreateSchema(engine db.EngineMigration) error {
	if err := engine.Sync(Beans()...); err != nil {
		return err
	}
	has, err := engine.ID(1).Exist(new(WriteLock))
	if err != nil || has {
		return err
	}
	_, err = engine.Insert(&WriteLock{ID: 1})
	return err
}

// AddAuditExports 兼容已安装的共同基础镜像，回填关联 ID 与个人历史索引。
func AddAuditExports(engine db.EngineMigration) error {
	if err := engine.Sync(new(AuditEvent), new(AuditExport), new(AuditExportChunk)); err != nil {
		return err
	}
	var cursor int64
	for {
		var events []*AuditEvent
		if err := engine.Where("id > ?", cursor).Asc("id").Limit(1000).Find(&events); err != nil {
			return err
		}
		for _, event := range events {
			cursor = event.ID
			event.RequestID = event.Actor.RequestID
			if _, err := engine.ID(event.ID).Cols("request_id").Update(event); err != nil {
				return err
			}
			userIDs := []int64{event.ActorID}
			if event.ObjectType == "user" {
				userIDs = append(userIDs, event.ObjectID)
			}
			for _, id := range userIDs {
				if id <= 0 {
					continue
				}
				scope := &AuditScope{EventSequence: event.ID, ScopeType: "user", ScopeID: id, OccurredAt: event.OccurredAt}
				has, err := engine.Where("event_sequence = ? AND scope_type = ? AND scope_id = ?", event.ID, "user", id).Exist(new(AuditScope))
				if err != nil {
					return err
				}
				if !has {
					if _, err := engine.Insert(scope); err != nil {
						return err
					}
				}
			}
		}
		if len(events) < 1000 {
			return nil
		}
	}
}

func InitializeLock(ctx context.Context) error {
	has, err := db.GetEngine(ctx).ID(1).Exist(new(WriteLock))
	if err != nil || has {
		return err
	}
	return db.Insert(ctx, &WriteLock{ID: 1})
}

func AddAuditStreamStatus(engine db.EngineMigration) error {
	return engine.Sync(new(AuditStream))
}

// InitializeLegacyNamespaces 在维护窗口内导入路径，只新增治理数据，保留全部原生 ID 和权限。
func InitializeLegacyNamespaces(ctx context.Context) error {
	if err := InitializeLock(ctx); err != nil {
		return err
	}
	return WithWrite(ctx, nil, func(ctx context.Context) error {
		var users []struct {
			ID         int64
			Name       string
			Type       int
			Visibility int
		}
		if err := db.GetEngine(ctx).Table("user").Select("id, name, type, visibility").In("type", 0, 1, 4).Find(&users); err != nil {
			return err
		}
		var owners []struct {
			ID    int64
			OrgID int64
		}
		if err := db.GetEngine(ctx).Table("team").Select("id, org_id").Where("lower_name = ?", "owners").Find(&owners); err != nil {
			return err
		}
		ownerTeams := make(map[int64]int64, len(owners))
		for _, owner := range owners {
			ownerTeams[owner.OrgID] = owner.ID
		}
		for _, u := range users {
			has, err := db.ExistByID[Namespace](ctx, u.ID)
			if err != nil {
				return err
			}
			if has {
				continue
			}
			kind := "user"
			if u.Type == 1 {
				kind = "group"
			}
			// 旧版合法名字（例如 team.git）不能因新群组命名限制而丢失。
			if u.Name == "" || len(u.Name) > 100 || strings.ContainsAny(u.Name, "/\\") {
				return fmt.Errorf("%w：旧命名空间 %d 的路径需要人工核对", ErrInvalid, u.ID)
			}
			lower := strings.ToLower(u.Name)
			namespace := &Namespace{
				ID: u.ID, Slug: u.Name, LowerSlug: lower, FullPath: u.Name,
				LowerPath: lower, Kind: kind, Visibility: u.Visibility, Revision: 1,
				NativeOwnerTeamID: ownerTeams[u.ID],
			}
			if err := reservePath(ctx, u.Name, kind, u.ID); err != nil {
				return err
			}
			if err := db.Insert(ctx, namespace); err != nil {
				return fmt.Errorf("导入命名空间 %d：%w", u.ID, err)
			}
			if err := SyncNativeNamespacePath(ctx, namespace); err != nil {
				return err
			}
		}
		var repositories []struct {
			ID      int64
			OwnerID int64
			Name    string
		}
		if err := db.GetEngine(ctx).Table("repository").Select("id, owner_id, name").Find(&repositories); err != nil {
			return err
		}
		for _, repo := range repositories {
			exists, err := db.GetEngine(ctx).Where("kind = ? AND resource_id = ? AND alias = ?", "repository", repo.ID, false).Exist(new(ResourcePath))
			if err != nil {
				return err
			}
			if exists {
				continue
			}
			namespace, err := GetNamespace(ctx, repo.OwnerID)
			if err != nil {
				return fmt.Errorf("导入仓库 %d 的归属：%w", repo.ID, err)
			}
			if strings.ContainsAny(repo.Name, "/\\") || repo.Name == "" {
				return fmt.Errorf("%w：仓库 %d 路径无效", ErrInvalid, repo.ID)
			}
			if err := reservePath(ctx, namespace.FullPath+"/"+repo.Name, "repository", repo.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// AddReferenceTransactions 为 Git 引用实际写入保留恢复依据。
func AddReferenceTransactions(engine db.EngineMigration) error {
	return engine.Sync(new(ReferenceTransaction), new(ReferenceReservation))
}

// AddReferenceTransactionWiki 区分代码与 Wiki 引用，旧记录默认仍指向代码仓库。
func AddReferenceTransactionWiki(engine db.EngineMigration) error {
	return engine.Sync(new(ReferenceTransaction))
}

// AddReferenceBusinessOperations 保存跨 Git/数据库操作的恢复依据。
func AddReferenceBusinessOperations(engine db.EngineMigration) error {
	return engine.Sync(new(ReferenceTransaction))
}

// AddReferenceBusinessOperationHEAD 为已执行 DB368 的项目补齐默认分支恢复标记。
func AddReferenceBusinessOperationHEAD(engine db.EngineMigration) error {
	return engine.Sync(new(ReferenceTransaction))
}

// AddReferenceBusinessOperationPayload 保存跨 Git/数据库操作恢复所需的内部载荷，不通过 API 或审计外送。
func AddReferenceBusinessOperationPayload(engine db.EngineMigration) error {
	return engine.Sync(new(ReferenceTransaction))
}

// AddApprovalRuleNativePool 保留旧规则语义，并允许导入规则动态遵循原生审批资格池。
func AddApprovalRuleNativePool(engine db.EngineMigration) error {
	return engine.Sync(new(ApprovalRule))
}

// AddPullRuleVersions 保留 PR 的独立规则版本。
func AddPullRuleVersions(engine db.EngineMigration) error {
	return engine.Sync(new(PullRuleVersion), new(ApprovalSettings), new(ApprovalEvidence))
}

// AddMergeApprovalProof 把引用事务与最终批准依据关联，保留崩溃恢复所需身份。
func AddMergeApprovalProof(engine db.EngineMigration) error {
	return engine.Sync(new(MergeAuthorization), new(ReferenceTransaction))
}

func AddReferenceRevisions(engine db.EngineMigration) error {
	return engine.Sync(new(ReferenceRevision), new(MergeAuthorization))
}

// AddApprovalSettingsHistory 保留恢复继承后的修订号，拒绝旧页面覆盖重新启用的设置。
func AddApprovalSettingsHistory(engine db.EngineMigration) error {
	return engine.Sync(new(ApprovalSettings))
}

// AddNativeApprovalGate 为已有保护分支增加明确的审批后写入开关，升级默认关闭。
func AddNativeApprovalGate(engine db.EngineMigration) error {
	type ProtectedBranch struct {
		RequireGovernanceApproval bool `xorm:"NOT NULL DEFAULT false"`
	}
	return engine.Sync(new(ProtectedBranch))
}

// AddRepositoryStableStorageOwner 让逻辑拥有者变更不再搬动 Git 正文目录。
func AddRepositoryStableStorageOwner(engine db.EngineMigration) error {
	return engine.Sync(new(repositoryStableStorage))
}

// AddExternalShareRestriction 仅向顶级群组增加新建跨层级共享的限制开关。
func AddExternalShareRestriction(engine db.EngineMigration) error {
	return engine.Sync(new(Namespace))
}
