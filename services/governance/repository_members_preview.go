// Copyright 2026 Gitea.
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json" //nolint:depguard // 预览摘要需要稳定的映射键排序。
	"errors"
	"slices"

	"gitea.dev/models/db"
	gm "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
)

type RepositoryMemberChange struct {
	Kind      string            `json:"kind"`
	Username  string            `json:"username"`
	GroupPath string            `json:"group_path"`
	Remove    bool              `json:"remove"`
	Member    GroupMemberOption `json:"member"`
	Share     GroupShareOption  `json:"share"`
}

type RepositoryMemberImpact struct {
	Username string                   `json:"username"`
	Before   string                   `json:"before"`
	After    string                   `json:"after"`
	Sources  []RepositoryMemberSource `json:"sources"`
}

type RepositoryChangePreview struct {
	RequiresConfirmation bool                     `json:"requires_confirmation"`
	Token                string                   `json:"token"`
	Impacts              []RepositoryMemberImpact `json:"impacts"`
	VisibilityNotice     string                   `json:"visibility_notice,omitempty"`
	SelfLosesManagement  bool                     `json:"self_loses_management"`
}

func repositoryMemberSnapshot(ctx context.Context, actorID, repoID int64) ([]*GroupMemberState, string, error) {
	repo, abilities, err := repositoryMemberManager(ctx, actorID, repoID)
	if err != nil {
		return nil, "", err
	}
	members := []*GroupMemberState{}
	var state *RepositoryMembersState
	for cursor := int64(0); ; {
		state, err = listRepositoryAllMembers(ctx, repo, true, cursor)
		if err != nil {
			return nil, "", err
		}
		members = append(members, state.Members...)
		if state.NextID == 0 {
			break
		}
		cursor = state.NextID
	}
	var shares []*gm.Share
	if err := db.GetEngine(ctx).Where("scope_type = ? AND scope_id = ?", "repository", repoID).Asc("id").Find(&shares); err != nil {
		return nil, "", err
	}
	// 包含所有实际来源及账号状态；共享或成员到期会改变 Grants，使旧预览失效。
	raw, err := json.Marshal(struct {
		RepositoryID   int64
		ActorID        int64
		Members        []*GroupMemberState
		Shares         []*gm.Share
		OwnerID        int64
		Visibility     int
		Archived       bool
		Revision       int64
		Roles          []*gm.CustomRole
		ActorAbilities gm.Abilities
	}{repo.ID, actorID, members, shares, repo.OwnerID, repo.EffectiveVisibility(), state.Archived, state.Revision, state.Roles, abilities})
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return members, hex.EncodeToString(sum[:]), nil
}

func repositoryChangePreviewToken(snapshot string, change RepositoryMemberChange) string {
	// 只绑定实际写入字段；显示名称解析后的 ID 才是授权对象身份。
	change.Username, change.GroupPath = "", ""
	change.Member.PreviewToken, change.Share.PreviewToken = "", ""
	change.Member.Revision, change.Share.Revision = 0, 0
	if change.Kind == "share" {
		change.Member = GroupMemberOption{}
		if change.Remove {
			change.Share = GroupShareOption{GroupID: change.Share.GroupID}
		}
	} else {
		change.Share = GroupShareOption{}
		if change.Remove {
			change.Member = GroupMemberOption{UserID: change.Member.UserID}
		}
	}
	raw, _ := json.Marshal(change)
	sum := sha256.Sum256(append([]byte(snapshot), raw...))
	return hex.EncodeToString(sum[:])
}

func checkRepositoryPreview(ctx context.Context, actorID, repoID int64, token string, change RepositoryMemberChange) error {
	if token == "" {
		return nil
	}
	_, actual, err := repositoryMemberSnapshot(ctx, actorID, repoID)
	if err != nil {
		return err
	}
	if token != repositoryChangePreviewToken(actual, change) {
		return shareConflict("变更内容、授权来源、账号状态或有效期已经变化，请重新预览后提交")
	}
	return nil
}

func resolveRepositoryChange(ctx context.Context, actor gm.Actor, repoID int64, change *RepositoryMemberChange) error {
	if _, _, err := repositoryMemberManager(ctx, actor.EffectiveUserID(), repoID); err != nil {
		return err
	}
	if (change.Kind == "member" || change.Kind == "native") && change.Username != "" {
		u, err := user_model.GetUserByName(ctx, change.Username)
		if err != nil {
			return gm.ErrNotFound
		}
		change.Member.UserID = u.ID
	}
	if change.Kind == "share" && change.GroupPath != "" {
		p, err := gm.ResolvePath(ctx, change.GroupPath)
		if err != nil {
			return err
		}
		if p.Kind != "group" {
			return gm.ErrNotFound
		}
		change.Share.GroupID = p.ResourceID
	}
	return nil
}

func ApplyRepositoryMemberChange(ctx context.Context, actor gm.Actor, repoID int64, change RepositoryMemberChange) error {
	if err := resolveRepositoryChange(ctx, actor, repoID, &change); err != nil {
		return err
	}

	switch change.Kind {
	case "native":
		return setNativeRepositoryMember(ctx, actor, repoID, change.Member, change.Remove)
	case "member":
		return SetRepositoryMember(ctx, actor, repoID, change.Member, change.Remove)
	case "share":
		return SetRepositoryShare(ctx, actor, repoID, change.Share, change.Remove)
	default:
		return gm.ErrInvalid
	}
}

// PreviewRepositoryMemberChange 使用真实写入校验并回滚事务，不复制一份授权规则。
func PreviewRepositoryMemberChange(ctx context.Context, actor gm.Actor, repoID int64, change RepositoryMemberChange) (*RepositoryChangePreview, error) {
	rollback := errors.New("成员变更预览完成，回滚")
	preview := &RepositoryChangePreview{Impacts: []RepositoryMemberImpact{}}
	err := withActorWrite(ctx, actor, nil, func(ctx context.Context) error {
		if err := resolveRepositoryChange(ctx, actor, repoID, &change); err != nil {
			return err
		}
		before, token, err := repositoryMemberSnapshot(ctx, actor.EffectiveUserID(), repoID)
		if err != nil {
			return err
		}
		preview.Token = repositoryChangePreviewToken(token, change)
		repo, viewer, manage, own, err := repositoryViewAccess(ctx, actor.EffectiveUserID(), repoID)
		if err != nil {
			return err
		}
		if !repo.IsPrivate || repo.IsInternal() {
			preview.VisibilityNotice = "撤销成员授权不会取消项目公开或内部可见性提供的基础访问。"
		}
		previous := map[int64]*RepositoryMemberView{}
		for _, member := range before {
			item, err := presentRepositoryMember(ctx, repo, viewer, member, manage, own)
			if err != nil {
				return err
			}
			previous[member.UserID] = item
		}
		change.Member.PreviewToken, change.Share.PreviewToken = "", ""
		if err := ApplyRepositoryMemberChange(ctx, actor, repoID, change); err != nil {
			return err
		}
		// 自身撤权后仍在同一事务中读取已授权预览，不能重新要求其拥有管理权限。
		after := map[int64]*RepositoryMemberView{}
		fresh, err := repo_model.GetRepositoryByID(ctx, repoID)
		if err != nil {
			return err
		}
		for cursor := int64(0); ; {
			page, err := listRepositoryAllMembers(ctx, fresh, true, cursor)
			if err != nil {
				return err
			}
			for _, member := range page.Members {
				item, err := presentRepositoryMember(ctx, fresh, viewer, member, manage, own)
				if err != nil {
					return err
				}
				after[member.UserID] = item
			}
			if page.NextID == 0 {
				break
			}
			cursor = page.NextID
		}
		ids := []int64{}
		for id := range previous {
			ids = append(ids, id)
		}
		for id := range after {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range slices.Compact(ids) {
			old, next := previous[id], after[id]
			left, _ := json.Marshal(old)
			right, _ := json.Marshal(next)
			if string(left) == string(right) {
				continue
			}
			if (old != nil && old.Role == gm.Owner) || (next != nil && next.Role == gm.Owner) || id == actor.EffectiveUserID() {
				preview.RequiresConfirmation = true
			}
			impact := RepositoryMemberImpact{Before: "无成员授权", After: "无成员授权", Sources: []RepositoryMemberSource{}}
			if old != nil {
				impact.Username, impact.Before = old.Username, old.RoleName
			}
			if next != nil {
				impact.Username, impact.After, impact.Sources = next.Username, next.RoleName, next.Sources
			}
			preview.Impacts = append(preview.Impacts, impact)
		}
		_, _, err = repositoryMemberManager(ctx, actor.EffectiveUserID(), repoID)
		if err != nil && !errors.Is(err, gm.ErrNotFound) {
			return err
		}
		preview.SelfLosesManagement = err != nil
		return rollback
	})
	if !errors.Is(err, rollback) {
		return nil, err
	}
	return preview, nil
}
