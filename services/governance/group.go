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
	"time"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	"gitea.dev/models/organization"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/structs"

	"github.com/google/uuid"
	"xorm.io/builder"
)

type GroupOption struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	ParentID   int64  `json:"parent_id"`
	Visibility int    `json:"visibility"`
	Revision   int64  `json:"revision"`
}

type GroupState struct {
	*governance_model.Namespace
	Name         string                     `json:"name"`
	InternalName string                     `json:"compatibility_name"`
	Grants       []governance_model.Grant   `json:"grants"`
	Abilities    governance_model.Abilities `json:"abilities"`
}

type GroupMoveImpact struct {
	OldPath                       string   `json:"old_path"`
	NewPath                       string   `json:"new_path"`
	Groups                        int      `json:"groups"`
	Repositories                  int      `json:"repositories"`
	RemovedAncestorPaths          []string `json:"removed_ancestor_paths"`
	AddedAncestorPaths            []string `json:"added_ancestor_paths"`
	RemovedMembershipSources      int64    `json:"removed_membership_sources"`
	AddedMembershipSources        int64    `json:"added_membership_sources"`
	RemovedShareSources           int64    `json:"removed_share_sources"`
	AddedShareSources             int64    `json:"added_share_sources"`
	RemovedApprovalPolicies       int64    `json:"removed_approval_policies"`
	AddedApprovalPolicies         int64    `json:"added_approval_policies"`
	RemovedApprovalSettings       int64    `json:"removed_approval_settings"`
	AddedApprovalSettings         int64    `json:"added_approval_settings"`
	RemovedAuditStreams           int64    `json:"removed_audit_streams"`
	AddedAuditStreams             int64    `json:"added_audit_streams"`
	CustomRoleMembershipsRemapped int64    `json:"custom_role_memberships_remapped"`
	CustomRoleInvitationsRemapped int64    `json:"custom_role_invitations_remapped"`
	CompatibilityAliasRetained    bool     `json:"compatibility_alias_retained"`
}

func activeActor(ctx context.Context, actorID int64) (*user_model.User, error) {
	if actorID <= 0 {
		return nil, governance_model.ErrNotFound
	}
	user, err := user_model.GetUserByID(ctx, actorID)
	if user_model.IsErrUserNotExist(err) {
		return nil, governance_model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !user.IsActive || user.ProhibitLogin || user.IsOrganization() || user.IsGiteaActions() || user.IsGhost() {
		return nil, governance_model.ErrNotFound
	}
	return user, nil
}

func CheckGroupAccess(ctx context.Context, actorID, id int64, ability string) (*GroupState, error) {
	doer, err := activeActor(ctx, actorID)
	if err != nil {
		return nil, err
	}
	n, err := governance_model.GetNamespace(ctx, id)
	if err != nil {
		return nil, err
	}
	if n.Kind != "group" {
		return nil, governance_model.ErrNotFound
	}
	org, err := user_model.GetUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	grants, err := governance_model.GroupGrants(ctx, id, actorID, time.Now())
	if err != nil {
		return nil, err
	}
	abilities := governance_model.EffectiveAbilities(grants)
	if doer.IsAuditor && doer.Type == user_model.UserTypeIndividual {
		governance_model.IncludeAuditorAbilities(abilities)
	}
	if doer.IsAdmin {
		abilities, _ = governance_model.AbilitiesFor(governance_model.Owner, nil)
	}
	if ability == governance_model.ReadGroup && organization.HasOrgOrUserVisible(ctx, org, doer) {
		abilities[ability] = true
	}
	if !abilities[ability] {
		return nil, governance_model.ErrNotFound
	}
	name := org.FullName
	if name == "" {
		name = n.Slug
	}
	return &GroupState{Namespace: n, Name: name, InternalName: org.Name, Grants: grants, Abilities: abilities}, nil
}

func validateGroupPath(path string) error {
	if err := governance_model.ValidateSlug(path); err != nil {
		return err
	}
	if err := user_model.IsUsableUsername(path); err != nil {
		return fmt.Errorf("%w：名称与系统入口冲突", governance_model.ErrInvalid)
	}
	// 原生群组管理与 API 操作段保留，避免最长路径匹配改变既有入口的含义。
	switch strings.ToLower(path) {
	case "settings", "members", "public_members", "teams", "repos", "hooks", "labels", "projects", "packages", "actions", "avatars", "avatar", "invitations", "healthcheck":
		return fmt.Errorf("%w：名称与群组管理入口冲突", governance_model.ErrInvalid)
	}
	return nil
}

func CreateGroup(ctx context.Context, actor governance_model.Actor, option GroupOption) (*GroupState, error) {
	if err := validateGroupPath(option.Path); err != nil {
		return nil, err
	}
	if len(option.Name) > 100 || option.ParentID < 0 || option.Visibility < 0 || option.Visibility > 2 {
		return nil, governance_model.ErrInvalid
	}
	var id int64
	err := withActorWrite(ctx, actor, []string{governance_model.Resource("group", option.ParentID)}, func(ctx context.Context) error {
		doer, err := activeActor(ctx, actor.EffectiveUserID())
		if err != nil {
			return err
		}
		if option.ParentID == 0 {
			if !doer.CanCreateOrganization() {
				return governance_model.ErrNotFound
			}
		} else {
			if _, err := CheckGroupAccess(ctx, doer.ID, option.ParentID, governance_model.CreateGroup); err != nil {
				return err
			}
		}
		internal := option.Path
		if option.ParentID != 0 {
			internal = "g_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
		if option.ParentID != 0 {
			exists, err := db.GetEngine(ctx).Table("repository").Where("owner_id = ? AND lower_name = ?", option.ParentID, strings.ToLower(option.Path)).Exist()
			if err != nil {
				return err
			}
			if exists {
				return governance_model.ErrConflict
			}
		}
		name := option.Name
		if name == "" {
			name = option.Path
		}
		org := &organization.Organization{Name: internal, FullName: name, Visibility: structs.VisibleType(option.Visibility)}
		n := &governance_model.Namespace{ParentID: option.ParentID, Slug: option.Path, Visibility: option.Visibility}
		if err := organization.CreateOrganization(ctx, org, doer, n); err != nil {
			return err
		}
		id = org.ID
		return groupAudit(ctx, actor, n, "group.created", map[string]any{"name": name, "parent_id": n.ParentID, "visibility": n.Visibility})
	})
	if err != nil {
		return nil, err
	}
	return CheckGroupAccess(ctx, actor.EffectiveUserID(), id, governance_model.ReadGroup)
}

func groupAudit(ctx context.Context, actor governance_model.Actor, n *governance_model.Namespace, kind string, details any) error {
	return groupSubjectAudit(ctx, actor, n, kind, "group", n.ID, n.FullPath, details)
}

func groupSubjectAudit(ctx context.Context, actor governance_model.Actor, n *governance_model.Namespace, kind, objectType string, objectID int64, objectPath string, details any) error {
	chain, err := governance_model.Ancestors(ctx, n.ID)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(chain))
	for _, ancestor := range chain {
		ids = append(ids, ancestor.ID)
	}
	data, err := json.Marshal(details)
	if err != nil {
		return err
	}
	scopeType := "group"
	if n.Kind == "user" {
		scopeType, ids = "user", nil
	}
	return governance_model.AppendAudit(ctx, &governance_model.AuditEvent{Type: kind, Actor: actor, ScopeType: scopeType, ScopeID: n.ID, AncestorIDs: ids, ObjectType: objectType, ObjectID: objectID, ObjectPath: objectPath, Result: "success", Details: data})
}

func ListGroups(ctx context.Context, userID, parentID, afterID int64, limit int) ([]*GroupState, error) {
	if _, err := activeActor(ctx, userID); err != nil {
		return nil, err
	}
	if parentID < 0 || afterID < 0 || limit < 1 || limit > 100 {
		return nil, governance_model.ErrInvalid
	}
	if parentID > 0 {
		if _, err := CheckGroupAccess(ctx, userID, parentID, governance_model.ReadGroup); err != nil {
			return nil, err
		}
	}
	result := make([]*GroupState, 0, limit)
	// 游标按稳定 ID；不可见资源不计入返回条数和总数。
	for len(result) < limit {
		var rows []*governance_model.Namespace
		if err := db.GetEngine(ctx).Where("kind = ? AND parent_id = ? AND id > ?", "group", parentID, afterID).Asc("id").Limit(100).Find(&rows); err != nil {
			return nil, err
		}
		for _, row := range rows {
			afterID = row.ID
			group, err := CheckGroupAccess(ctx, userID, row.ID, governance_model.ReadGroup)
			if errors.Is(err, governance_model.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			result = append(result, group)
			if len(result) == limit {
				break
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	return result, nil
}

func MoveGroup(ctx context.Context, actor governance_model.Actor, id int64, option GroupOption) (*GroupState, error) {
	if err := validateGroupPath(option.Path); err != nil {
		return nil, err
	}
	var result *GroupState
	var crossRoot bool
	var oldRootID, newRootID int64
	err := withActorWrite(ctx, actor, nil, func(ctx context.Context) error {
		err := governance_model.MoveNamespace(ctx, id, option.ParentID, option.Revision, option.Path, actor, func(ctx context.Context, source, target *governance_model.Namespace) error {
			var err error
			result, err = CheckGroupAccess(ctx, actor.EffectiveUserID(), source.ID, governance_model.ManageGroup)
			if err != nil {
				return err
			}
			if target != nil {
				if err := checkGroupShareCycle(ctx, source.ID, target.ID); err != nil {
					return err
				}
				if _, err := CheckGroupAccess(ctx, actor.EffectiveUserID(), target.ID, governance_model.CreateGroup); err != nil {
					return err
				}
				sourceChain, err := governance_model.Ancestors(ctx, source.ID)
				if err != nil {
					return err
				}
				targetChain, err := governance_model.Ancestors(ctx, target.ID)
				if err != nil {
					return err
				}
				oldRootID, newRootID = sourceChain[len(sourceChain)-1].ID, targetChain[len(targetChain)-1].ID
				crossRoot = oldRootID != newRootID
			} else {
				doer, err := activeActor(ctx, actor.EffectiveUserID())
				if err != nil {
					return err
				}
				if !doer.CanCreateOrganization() {
					return governance_model.ErrNotFound
				}
				sourceChain, err := governance_model.Ancestors(ctx, source.ID)
				if err != nil {
					return err
				}
				oldRootID, newRootID = sourceChain[len(sourceChain)-1].ID, source.ID
				crossRoot = oldRootID != newRootID
			}
			return nil
		})
		if err != nil {
			return err
		}
		root, err := governance_model.GetNamespace(ctx, id)
		if err != nil {
			return err
		}
		if crossRoot {
			memberships, invitations, rolesCreated, err := remapMovedCustomRoles(ctx, actor, root, oldRootID, newRootID)
			if err != nil {
				return err
			}
			if memberships > 0 || invitations > 0 {
				if err := groupAudit(ctx, actor, root, "group.updated", map[string]any{"custom_role_policy": "preserve_explicit_role", "memberships_remapped": memberships, "invitations_remapped": invitations, "roles_created": rolesCreated}); err != nil {
					return err
				}
			}
		}
		if err := syncMovedRequestPaths(ctx, root); err != nil {
			return err
		}
		var namespaces []*governance_model.Namespace
		escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(root.LowerPath) + "/%"
		if err := db.GetEngine(ctx).Where("id = ? OR lower_path LIKE ? ESCAPE '!'", id, escaped).Find(&namespaces); err != nil {
			return err
		}
		for _, n := range namespaces {
			if err := governance_model.SyncNativeNamespacePath(ctx, n); err != nil {
				return err
			}
		}
		current, err := CheckGroupAccess(ctx, actor.EffectiveUserID(), id, governance_model.ReadGroup)
		if errors.Is(err, governance_model.ErrNotFound) {
			// 移动成功后可能失去继承权限；返回已授权操作的回执，不能误报写入失败。
			result.Namespace, result.Grants, result.Abilities = root, []governance_model.Grant{}, governance_model.Abilities{}
		} else if err != nil {
			return err
		} else {
			result = current
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// PreviewGroupMove 汇总移动后会切换的继承范围；实际移动仍在事务内重新校验权限和修订号。
func PreviewGroupMove(ctx context.Context, actorID, id int64, option GroupOption) (*GroupMoveImpact, error) {
	if err := validateGroupPath(option.Path); err != nil {
		return nil, err
	}
	source, err := CheckGroupAccess(ctx, actorID, id, governance_model.ManageGroup)
	if err != nil {
		return nil, err
	}
	if source.Revision != option.Revision || source.Archived || source.DeleteAfter != 0 {
		return nil, governance_model.ErrConflict
	}
	var targetChain []*governance_model.Namespace
	newPath := option.Path
	if option.ParentID == 0 {
		doer, err := activeActor(ctx, actorID)
		if err != nil {
			return nil, err
		}
		if !doer.CanCreateOrganization() {
			return nil, governance_model.ErrNotFound
		}
	} else {
		target, err := CheckGroupAccess(ctx, actorID, option.ParentID, governance_model.CreateGroup)
		if err != nil {
			return nil, err
		}
		if target.Archived || target.DeleteAfter != 0 || source.Visibility < target.Visibility {
			return nil, governance_model.ErrConflict
		}
		if err := checkGroupShareCycle(ctx, id, option.ParentID); err != nil {
			return nil, err
		}
		targetChain, err = governance_model.Ancestors(ctx, option.ParentID)
		if err != nil {
			return nil, err
		}
		newPath = target.FullPath + "/" + option.Path
	}
	if path, err := governance_model.ResolvePath(ctx, newPath); err == nil && (path.Kind != "group" || path.ResourceID != id) {
		return nil, governance_model.ErrConflict
	} else if err != nil && !errors.Is(err, governance_model.ErrNotFound) {
		return nil, err
	}

	oldChain, err := governance_model.Ancestors(ctx, id)
	if err != nil {
		return nil, err
	}
	oldAncestors := oldChain[1:]
	removed, added := namespaceDifference(oldAncestors, targetChain)
	impact := &GroupMoveImpact{
		OldPath: source.FullPath, NewPath: newPath,
		RemovedAncestorPaths: namespacePaths(removed), AddedAncestorPaths: namespacePaths(added),
		CompatibilityAliasRetained: true,
	}
	prefix := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(source.LowerPath) + "/%"
	var movedGroups []*governance_model.Namespace
	if err := db.GetEngine(ctx).Where("lower_path = ? OR lower_path LIKE ? ESCAPE '!'", source.LowerPath, prefix).Find(&movedGroups); err != nil {
		return nil, err
	}
	impact.Groups = len(movedGroups)
	sourceDepth, targetDepth := len(strings.Split(source.FullPath, "/")), len(targetChain)+1
	for _, group := range movedGroups {
		if targetDepth+len(strings.Split(group.FullPath, "/"))-sourceDepth > governance_model.MaxDepth {
			return nil, fmt.Errorf("%w：移动后超出二十层限制", governance_model.ErrInvalid)
		}
	}
	impact.Repositories, err = countCanonicalResources(ctx, "repository", source.LowerPath, prefix)
	if err != nil {
		return nil, err
	}
	if err := fillMoveScopeImpact(ctx, impact, removed, added); err != nil {
		return nil, err
	}
	oldRootID, newRootID := oldChain[len(oldChain)-1].ID, id
	if len(targetChain) > 0 {
		newRootID = targetChain[len(targetChain)-1].ID
	}
	if oldRootID != newRootID {
		groupIDs := namespaceIDs(movedGroups)
		repositoryIDs, err := canonicalResourceIDs(ctx, "repository", source.LowerPath, prefix)
		if err != nil {
			return nil, err
		}
		impact.CustomRoleMembershipsRemapped, impact.CustomRoleInvitationsRemapped, err = countCustomRoleReferences(ctx, groupIDs, repositoryIDs)
		if err != nil {
			return nil, err
		}
	}
	return impact, nil
}

func namespaceDifference(oldChain, newChain []*governance_model.Namespace) (removed, added []*governance_model.Namespace) {
	oldIDs, newIDs := map[int64]bool{}, map[int64]bool{}
	for _, n := range oldChain {
		oldIDs[n.ID] = true
	}
	for _, n := range newChain {
		newIDs[n.ID] = true
	}
	for _, n := range oldChain {
		if !newIDs[n.ID] {
			removed = append(removed, n)
		}
	}
	for _, n := range newChain {
		if !oldIDs[n.ID] {
			added = append(added, n)
		}
	}
	return removed, added
}

func namespacePaths(chain []*governance_model.Namespace) []string {
	paths := make([]string, 0, len(chain))
	for _, n := range chain {
		paths = append(paths, n.FullPath)
	}
	return paths
}

func namespaceIDs(chain []*governance_model.Namespace) []int64 {
	ids := make([]int64, 0, len(chain))
	for _, n := range chain {
		ids = append(ids, n.ID)
	}
	return ids
}

func countCanonicalResources(ctx context.Context, kind, path, prefix string) (int, error) {
	ids, err := canonicalResourceIDs(ctx, kind, path, prefix)
	return len(ids), err
}

func canonicalResourceIDs(ctx context.Context, kind, path, prefix string) ([]int64, error) {
	var ids []int64
	err := db.GetEngine(ctx).Table(new(governance_model.ResourcePath)).Cols("resource_id").Where("kind = ? AND alias = ?", kind, false).And(builder.Expr("path = ? OR path LIKE ? ESCAPE '!'", path, prefix)).Find(&ids)
	return ids, err
}

func countCustomRoleReferences(ctx context.Context, groupIDs, repositoryIDs []int64) (memberships, invitations int64, err error) {
	for _, scope := range []struct {
		kind string
		ids  []int64
	}{{"group", groupIDs}, {"repository", repositoryIDs}} {
		if len(scope.ids) == 0 {
			continue
		}
		count, queryErr := db.GetEngine(ctx).Where("scope_type = ? AND custom_role_id > ?", scope.kind, 0).In("scope_id", scope.ids).Count(new(governance_model.Membership))
		if queryErr != nil {
			return 0, 0, queryErr
		}
		memberships += count
		count, queryErr = db.GetEngine(ctx).Where("scope_type = ? AND custom_role_id > ?", scope.kind, 0).In("scope_id", scope.ids).Count(new(governance_model.Invitation))
		if queryErr != nil {
			return 0, 0, queryErr
		}
		invitations += count
	}
	return memberships, invitations, nil
}

func remapMovedCustomRoles(ctx context.Context, actor governance_model.Actor, root *governance_model.Namespace, oldRootID, newRootID int64) (int64, int64, int, error) {
	prefix := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(root.LowerPath) + "/%"
	var groups []*governance_model.Namespace
	if err := db.GetEngine(ctx).Where("lower_path = ? OR lower_path LIKE ? ESCAPE '!'", root.LowerPath, prefix).Find(&groups); err != nil {
		return 0, 0, 0, err
	}
	repositories, err := canonicalResourceIDs(ctx, "repository", root.LowerPath, prefix)
	if err != nil {
		return 0, 0, 0, err
	}
	groupIDs := namespaceIDs(groups)
	memberships, invitations, err := countCustomRoleReferences(ctx, groupIDs, repositories)
	if err != nil {
		return 0, 0, 0, err
	}
	var roleIDs []int64
	for _, scope := range []struct {
		kind string
		ids  []int64
	}{{"group", groupIDs}, {"repository", repositories}} {
		if len(scope.ids) == 0 {
			continue
		}
		var memberRoles, invitationRoles []int64
		if err := db.GetEngine(ctx).Table(new(governance_model.Membership)).Cols("custom_role_id").Where("scope_type = ? AND custom_role_id > ?", scope.kind, 0).In("scope_id", scope.ids).Find(&memberRoles); err != nil {
			return 0, 0, 0, err
		}
		if err := db.GetEngine(ctx).Table(new(governance_model.Invitation)).Cols("custom_role_id").Where("scope_type = ? AND custom_role_id > ?", scope.kind, 0).In("scope_id", scope.ids).Find(&invitationRoles); err != nil {
			return 0, 0, 0, err
		}
		roleIDs = append(roleIDs, memberRoles...)
		roleIDs = append(roleIDs, invitationRoles...)
	}
	slices.Sort(roleIDs)
	roleIDs = slices.Compact(roleIDs)
	created := 0
	for _, roleID := range roleIDs {
		role, has, err := db.GetByID[governance_model.CustomRole](ctx, roleID)
		if err != nil {
			return 0, 0, 0, err
		}
		if !has || role.RootID != oldRootID {
			return 0, 0, 0, governance_model.ErrConflict
		}
		mappedID, wasCreated, err := findOrCreateTransferredRole(ctx, actor, role, newRootID)
		if err != nil {
			return 0, 0, 0, err
		}
		if wasCreated {
			created++
		}
		for _, scope := range []struct {
			kind string
			ids  []int64
		}{{"group", groupIDs}, {"repository", repositories}} {
			if len(scope.ids) == 0 {
				continue
			}
			if _, err := db.GetEngine(ctx).Where("scope_type = ? AND custom_role_id = ?", scope.kind, roleID).In("scope_id", scope.ids).Cols("custom_role_id").Update(&governance_model.Membership{CustomRoleID: mappedID}); err != nil {
				return 0, 0, 0, err
			}
			if _, err := db.GetEngine(ctx).Where("scope_type = ? AND custom_role_id = ?", scope.kind, roleID).In("scope_id", scope.ids).Cols("custom_role_id").Update(&governance_model.Invitation{CustomRoleID: mappedID}); err != nil {
				return 0, 0, 0, err
			}
		}
	}
	if created > 0 {
		if _, err := db.GetEngine(ctx).ID(newRootID).Incr("revision").Update(new(governance_model.Namespace)); err != nil {
			return 0, 0, 0, err
		}
	}
	return memberships, invitations, created, nil
}

func findOrCreateTransferredRole(ctx context.Context, actor governance_model.Actor, source *governance_model.CustomRole, newRootID int64) (int64, bool, error) {
	var roles []*governance_model.CustomRole
	if err := db.GetEngine(ctx).Where("root_id = ?", newRootID).Find(&roles); err != nil {
		return 0, false, err
	}
	sourceAbilities := slices.Clone(source.Abilities)
	slices.Sort(sourceAbilities)
	sourceAbilities = slices.Compact(sourceAbilities)
	for _, role := range roles {
		abilities := slices.Clone(role.Abilities)
		slices.Sort(abilities)
		abilities = slices.Compact(abilities)
		if role.BaseRole == source.BaseRole && slices.Equal(abilities, sourceAbilities) {
			return role.ID, false, nil
		}
	}
	usedNames := make(map[string]bool, len(roles))
	for _, role := range roles {
		usedNames[role.Name] = true
	}
	name := source.Name
	base := []rune(source.Name)
	if len(base) > 60 {
		base = base[:60]
	}
	for suffix := 0; usedNames[name]; suffix++ {
		if suffix == 0 {
			name = fmt.Sprintf("%s（转移角色 %d）", string(base), source.ID)
		} else {
			name = fmt.Sprintf("%s（转移角色 %d-%d）", string(base), source.ID, suffix+1)
		}
	}
	role := &governance_model.CustomRole{RootID: newRootID, Name: name, BaseRole: source.BaseRole, Abilities: sourceAbilities}
	if err := db.Insert(ctx, role); err != nil {
		return 0, false, err
	}
	target, err := governance_model.GetNamespace(ctx, newRootID)
	if err != nil {
		return 0, false, err
	}
	if err := groupSubjectAudit(ctx, actor, target, "role.created", "custom_role", role.ID, role.Name, map[string]any{"source_role_id": source.ID, "target_role_id": role.ID, "base_role": role.BaseRole, "abilities": role.Abilities, "reason": "cross_root_transfer"}); err != nil {
		return 0, false, err
	}
	return role.ID, true, nil
}

// RemapTransferredRepositoryCustomRoles 保留跨根项目的显式自定义角色，并同步邀请与申请路径。
func RemapTransferredRepositoryCustomRoles(ctx context.Context, actor governance_model.Actor, repositoryID, oldOwnerID, newOwnerID int64, newPath string) error {
	oldChain, err := governance_model.Ancestors(ctx, oldOwnerID)
	if errors.Is(err, governance_model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	newChain, err := governance_model.Ancestors(ctx, newOwnerID)
	if err != nil {
		return err
	}
	oldRootID, newRootID := oldChain[len(oldChain)-1].ID, newChain[len(newChain)-1].ID
	if oldRootID != newRootID {
		var roleIDs []int64
		for _, bean := range []any{new(governance_model.Membership), new(governance_model.Invitation)} {
			var ids []int64
			if err := db.GetEngine(ctx).Table(bean).Cols("custom_role_id").Where("scope_type = ? AND scope_id = ? AND custom_role_id > ?", "repository", repositoryID, 0).Find(&ids); err != nil {
				return err
			}
			roleIDs = append(roleIDs, ids...)
		}
		slices.Sort(roleIDs)
		roleIDs = slices.Compact(roleIDs)
		for _, roleID := range roleIDs {
			role, has, err := db.GetByID[governance_model.CustomRole](ctx, roleID)
			if err != nil {
				return err
			}
			if !has || role.RootID != oldRootID {
				return governance_model.ErrConflict
			}
			mappedID, _, err := findOrCreateTransferredRole(ctx, actor, role, newRootID)
			if err != nil {
				return err
			}
			if _, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND custom_role_id = ?", "repository", repositoryID, roleID).Cols("custom_role_id").Update(&governance_model.Membership{CustomRoleID: mappedID}); err != nil {
				return err
			}
			if _, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND custom_role_id = ?", "repository", repositoryID, roleID).Cols("custom_role_id").Update(&governance_model.Invitation{CustomRoleID: mappedID}); err != nil {
				return err
			}
		}
	}
	if _, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repositoryID).Cols("scope_path").Update(&governance_model.Invitation{ScopePath: newPath}); err != nil {
		return err
	}
	_, err = db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repositoryID).Cols("scope_path").Update(&governance_model.AccessRequest{ScopePath: newPath})
	return err
}

func syncMovedRequestPaths(ctx context.Context, root *governance_model.Namespace) error {
	prefix := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(root.LowerPath) + "/%"
	var groups []*governance_model.Namespace
	if err := db.GetEngine(ctx).Where("lower_path = ? OR lower_path LIKE ? ESCAPE '!'", root.LowerPath, prefix).Find(&groups); err != nil {
		return err
	}
	for _, group := range groups {
		if _, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "group", group.ID).Cols("scope_path").Update(&governance_model.Invitation{ScopePath: group.FullPath}); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "group", group.ID).Cols("scope_path").Update(&governance_model.AccessRequest{ScopePath: group.FullPath}); err != nil {
			return err
		}
	}
	var repositories []*governance_model.ResourcePath
	if err := db.GetEngine(ctx).Where("kind = ? AND alias = ?", "repository", false).And(builder.Expr("path = ? OR path LIKE ? ESCAPE '!'", root.LowerPath, prefix)).Find(&repositories); err != nil {
		return err
	}
	for _, repository := range repositories {
		if _, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repository.ResourceID).Cols("scope_path").Update(&governance_model.Invitation{ScopePath: repository.Path}); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repository.ResourceID).Cols("scope_path").Update(&governance_model.AccessRequest{ScopePath: repository.Path}); err != nil {
			return err
		}
	}
	return nil
}

func fillMoveScopeImpact(ctx context.Context, impact *GroupMoveImpact, removed, added []*governance_model.Namespace) error {
	var err error
	impact.RemovedMembershipSources, impact.RemovedShareSources, impact.RemovedApprovalPolicies, impact.RemovedApprovalSettings, impact.RemovedAuditStreams, err = countMoveScopes(ctx, namespaceIDs(removed))
	if err != nil {
		return err
	}
	impact.AddedMembershipSources, impact.AddedShareSources, impact.AddedApprovalPolicies, impact.AddedApprovalSettings, impact.AddedAuditStreams, err = countMoveScopes(ctx, namespaceIDs(added))
	return err
}

func countMoveScopes(ctx context.Context, ids []int64) (memberships, shares, policies, settings, streams int64, err error) {
	if len(ids) == 0 {
		return 0, 0, 0, 0, 0, nil
	}
	if memberships, err = db.GetEngine(ctx).Where("scope_type = ?", "group").In("scope_id", ids).Count(new(governance_model.Membership)); err != nil {
		return
	}
	if shares, err = db.GetEngine(ctx).Where("scope_type = ?", "group").In("scope_id", ids).Count(new(governance_model.Share)); err != nil {
		return
	}
	if policies, err = db.GetEngine(ctx).Where("scope_type = ? AND enabled = ?", "group", true).In("scope_id", ids).Count(new(governance_model.ApprovalRule)); err != nil {
		return
	}
	if settings, err = db.GetEngine(ctx).Where("scope_type = ? AND inherit = ?", "group", false).In("scope_id", ids).Count(new(governance_model.ApprovalSettings)); err != nil {
		return
	}
	streams, err = db.GetEngine(ctx).Where("scope_type = ? AND enabled = ?", "group", true).In("scope_id", ids).Count(new(governance_model.AuditStream))
	return
}

type GroupMemberOption struct {
	UserID       int64                 `json:"user_id"`
	Role         governance_model.Role `json:"role"`
	CustomRoleID int64                 `json:"custom_role_id"`
	ExpiresUnix  int64                 `json:"expires_unix"`
	Revision     int64                 `json:"revision"`
}

func SetGroupMember(ctx context.Context, actor governance_model.Actor, groupID int64, option GroupMemberOption, remove bool) error {
	return setGroupMember(ctx, actor, actor, groupID, option, remove)
}

func setGroupMember(ctx context.Context, actor, auditActor governance_model.Actor, groupID int64, option GroupMemberOption, remove bool) error {
	return withActorWrite(ctx, actor, []string{governance_model.Resource("group", groupID), governance_model.Resource("user", option.UserID)}, func(ctx context.Context) error {
		group, err := CheckGroupAccess(ctx, actor.EffectiveUserID(), groupID, governance_model.ManageGroupMembers)
		if err != nil {
			return err
		}
		if group.Revision != option.Revision {
			return governance_model.ErrConflict
		}
		if !remove {
			if _, err := activeActor(ctx, option.UserID); err != nil {
				return err
			}
			if err := checkMemberRole(ctx, "group", groupID, option, group.Abilities); err != nil {
				return err
			}
		}
		var previous governance_model.Membership
		exists, err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ? AND user_id = ?", "group", groupID, option.UserID).Get(&previous)
		if err != nil {
			return err
		}
		// 归档阻止新增成员，但保留已有成员的角色调整和撤权。
		if (group.Archived || group.DeleteAfter != 0) && (!exists || previous.ExpiresUnix > 0 && previous.ExpiresUnix <= time.Now().Unix()) && !remove {
			return governance_model.ErrConflict
		}
		if exists && previous.Role == governance_model.Owner && !group.Abilities[governance_model.ManageGroup] {
			return governance_model.ErrNotFound
		}
		if exists {
			if err := auditGrantExpiry(ctx, previous.ScopeType, previous.ScopeID, previous.ExpiresUnix, "member.expired", &previous, time.Now()); err != nil {
				return err
			}
		}
		member := &governance_model.Membership{ID: previous.ID, ScopeType: "group", ScopeID: groupID, UserID: option.UserID, Role: option.Role, CustomRoleID: option.CustomRoleID, ExpiresUnix: option.ExpiresUnix}
		if remove {
			if !exists {
				return governance_model.ErrNotFound
			}
			if _, err := db.GetEngine(ctx).ID(previous.ID).Delete(new(governance_model.Membership)); err != nil {
				return err
			}
		} else if exists {
			if _, err := db.GetEngine(ctx).ID(previous.ID).Cols("role", "custom_role_id", "expires_unix").Update(member); err != nil {
				return err
			}
		} else if err := db.Insert(ctx, member); err != nil {
			return err
		}
		if err := requirePermanentOwner(ctx, groupID); err != nil {
			return err
		}
		if _, err := db.GetEngine(ctx).ID(groupID).Incr("revision").Update(new(governance_model.Namespace)); err != nil {
			return err
		}
		kind := "member.updated"
		if !exists {
			kind = "member.added"
		}
		if remove {
			kind = "member.removed"
		}
		target, err := user_model.GetUserByID(ctx, option.UserID)
		if err != nil && !user_model.IsErrUserNotExist(err) {
			return err
		}
		targetName := ""
		if target != nil {
			targetName = target.Name
		}
		return groupSubjectAudit(ctx, auditActor, group.Namespace, kind, "user", option.UserID, targetName, map[string]any{"before": previous, "after": member, "source": "direct", "removed": remove, "group_path": group.FullPath})
	})
}

func requirePermanentOwner(ctx context.Context, groupID int64) error {
	return governance_model.EnsurePermanentGroupOwner(ctx, groupID, 0)
}
