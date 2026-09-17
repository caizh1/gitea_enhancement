// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package release

import (
	"context"
	"strings"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/git"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/json"
	repo_module "gitea.dev/modules/repository"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"
	"gitea.dev/services/context/upload"
)

type releaseReferencePayload struct {
	Action            string             `json:"action"`
	Release           repo_model.Release `json:"release"`
	AttachmentUUIDs   []string           `json:"attachment_uuids,omitempty"`
	AddAttachments    []string           `json:"add_attachments,omitempty"`
	DeleteAttachments []string           `json:"delete_attachments,omitempty"`
	EditAttachments   map[string]string  `json:"edit_attachments,omitempty"`
	DeleteTag         bool               `json:"delete_tag,omitempty"`
}

func init() {
	governance_model.RegisterReferenceBusinessApplier("release_tag_create", applyReleaseReferenceOperation)
	governance_model.RegisterReferenceBusinessApplier("release_tag_delete", applyReleaseReferenceOperation)
}

func appendReleaseReferenceEnv(env []string, id, operation, tag string) []string {
	return append(env, repo_module.EnvReferenceOperationID+"="+id, repo_module.EnvReferenceOperation+"="+operation, repo_module.EnvReferenceOldBranch+"="+tag)
}

func planReleaseTagCreate(ctx context.Context, rel *repo_model.Release, payload *releaseReferencePayload) (string, error) {
	payload.Release = *rel
	payload.Release.Repo, payload.Release.Publisher, payload.Release.Attachments = nil, nil, nil
	payload.Release.RenderedNote, payload.Release.TargetBehind = "", ""
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	zero := git.ObjectFormatFromName(rel.Repo.ObjectFormatName).EmptyObjectID().String()
	actor := governance_model.AuditActor(ctx)
	ancestors, err := releaseAncestorIDs(ctx, rel.Repo.OwnerID)
	if err != nil {
		return "", err
	}
	op := &governance_model.ReferenceTransaction{RepoID: rel.RepoID, BusinessOperation: "release_tag_create", BusinessOldBranch: rel.TagName, Actor: actor, ObjectPath: rel.Repo.FullPath(), AncestorIDs: ancestors, BusinessPayload: raw, Changes: []governance_model.ReferenceChange{{Ref: string(git.RefNameFromTag(rel.TagName)), Old: zero, New: zero}}}
	if err := governance_model.PlanReferenceBusinessOperation(ctx, op); err != nil {
		return "", err
	}
	return op.BusinessOperationID, nil
}

func planReleaseTagDelete(ctx context.Context, repo *repo_model.Repository, rel *repo_model.Release, old string) (string, error) {
	payload := &releaseReferencePayload{Action: "delete", Release: *rel, DeleteTag: true}
	payload.Release.Repo, payload.Release.Publisher, payload.Release.Attachments = nil, nil, nil
	payload.Release.RenderedNote, payload.Release.TargetBehind = "", ""
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	zero := git.ObjectFormatFromName(repo.ObjectFormatName).EmptyObjectID().String()
	actor := governance_model.AuditActor(ctx)
	ancestors, err := releaseAncestorIDs(ctx, repo.OwnerID)
	if err != nil {
		return "", err
	}
	op := &governance_model.ReferenceTransaction{RepoID: repo.ID, BusinessOperation: "release_tag_delete", BusinessOldBranch: rel.TagName, Actor: actor, ObjectPath: repo.FullPath(), AncestorIDs: ancestors, BusinessPayload: raw, Changes: []governance_model.ReferenceChange{{Ref: string(git.RefNameFromTag(rel.TagName)), Old: old, New: zero}}}
	if err := governance_model.PlanReferenceBusinessOperation(ctx, op); err != nil {
		return "", err
	}
	return op.BusinessOperationID, nil
}

func releaseAncestorIDs(ctx context.Context, ownerID int64) ([]int64, error) {
	chain, err := governance_model.Ancestors(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	result := make([]int64, 0, len(chain))
	for _, namespace := range chain {
		if namespace.Kind == "group" {
			result = append(result, namespace.ID)
		}
	}
	return result, nil
}

func applyReleaseReferenceOperation(ctx context.Context, operation *governance_model.ReferenceTransaction) error {
	var payload releaseReferencePayload
	if err := json.Unmarshal(operation.BusinessPayload, &payload); err != nil {
		return err
	}
	rel := &payload.Release
	rel.RepoID = operation.RepoID
	repo, err := repo_model.GetRepositoryByID(ctx, operation.RepoID)
	if err != nil {
		return err
	}
	rel.Repo = repo
	switch payload.Action {
	case "create":
		gitRepo, err := gitrepo.OpenRepository(ctx, repo)
		if err != nil {
			return err
		}
		commit, err := gitRepo.GetTagCommit(rel.TagName)
		_ = gitRepo.Close()
		if err != nil {
			return err
		}
		rel.Sha1 = commit.ID.String()
		rel.NumCommits, err = gitrepo.CommitsCountOfCommit(ctx, repo, rel.Sha1)
		if err != nil {
			return err
		}
		rel.LowerTagName = strings.ToLower(rel.TagName)
		rel.Title = util.EllipsisDisplayString(rel.Title, 255)
		if rel.CreatedUnix == 0 {
			rel.CreatedUnix = timeutil.TimeStampNow()
		}
		if err := db.Insert(ctx, rel); err != nil {
			return err
		}
		if err := repo_model.AddReleaseAttachments(ctx, rel.ID, payload.AttachmentUUIDs); err != nil {
			return err
		}
		return issues_model.AppendRepositoryContentAudit(ctx, "release.created", rel.RepoID, "release", rel.ID, "/releases/"+rel.TagName, map[string]any{"after": map[string]any{"title": rel.Title, "tag_name": rel.TagName, "draft": rel.IsDraft, "prerelease": rel.IsPrerelease}})
	case "delete":
		current, err := repo_model.GetReleaseByID(ctx, rel.ID)
		if err != nil {
			return err
		}
		if payload.DeleteTag {
			_, err = db.DeleteByID[repo_model.Release](ctx, rel.ID)
		} else {
			current.IsTag = true
			err = repo_model.UpdateRelease(ctx, current)
		}
		if err != nil {
			return err
		}
		if err := repo_model.DeleteAttachmentsByRelease(ctx, rel.ID); err != nil {
			return err
		}
		return issues_model.AppendRepositoryContentAudit(ctx, "release.deleted", rel.RepoID, "release", rel.ID, "/releases/"+current.TagName, map[string]any{"before": map[string]any{"title": current.Title, "tag_name": current.TagName, "draft": current.IsDraft, "prerelease": current.IsPrerelease}})
	case "update":
		old, err := repo_model.GetReleaseByID(ctx, rel.ID)
		if err != nil {
			return err
		}
		gitRepo, err := gitrepo.OpenRepository(ctx, repo)
		if err != nil {
			return err
		}
		commit, err := gitRepo.GetTagCommit(rel.TagName)
		_ = gitRepo.Close()
		if err != nil {
			return err
		}
		rel.Sha1, rel.LowerTagName = commit.ID.String(), strings.ToLower(rel.TagName)
		rel.NumCommits, err = gitrepo.CommitsCountOfCommit(ctx, repo, rel.Sha1)
		if err != nil {
			return err
		}
		rel.Title = util.EllipsisDisplayString(rel.Title, 255)
		if err := repo_model.UpdateRelease(ctx, rel); err != nil {
			return err
		}
		if err := repo_model.AddReleaseAttachments(ctx, rel.ID, payload.AddAttachments); err != nil {
			return err
		}
		ids := append(append([]string{}, payload.DeleteAttachments...), releaseMapKeys(payload.EditAttachments)...)
		attachments, err := repo_model.GetAttachmentsByUUIDs(ctx, ids)
		if err != nil {
			return err
		}
		owned := make(map[string]*repo_model.Attachment, len(attachments))
		for _, attachment := range attachments {
			if attachment.ReleaseID != rel.ID {
				return governance_model.ErrForbidden
			}
			owned[attachment.UUID] = attachment
		}
		for uuid, name := range payload.EditAttachments {
			if err := upload.Verify(nil, name, setting.Repository.Release.AllowedTypes); err != nil {
				return err
			}
			if !releaseContains(payload.DeleteAttachments, uuid) {
				if err := repo_model.UpdateAttachmentByUUID(ctx, &repo_model.Attachment{UUID: uuid, Name: name}, "name"); err != nil {
					return err
				}
			}
		}
		for _, uuid := range payload.DeleteAttachments {
			if attachment := owned[uuid]; attachment != nil {
				if _, err := repo_model.DeleteAttachments(ctx, []*repo_model.Attachment{attachment}, true); err != nil {
					return err
				}
			}
		}
		return issues_model.AppendRepositoryContentAudit(ctx, "release.updated", rel.RepoID, "release", rel.ID, "/releases/"+rel.TagName, map[string]any{"before": map[string]any{"title": old.Title, "tag_name": old.TagName, "draft": old.IsDraft, "prerelease": old.IsPrerelease}, "after": map[string]any{"title": rel.Title, "tag_name": rel.TagName, "draft": rel.IsDraft, "prerelease": rel.IsPrerelease}})
	default:
		return governance_model.ErrInvalid
	}
}

func releaseMapKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}

func releaseContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func reloadReleaseByTag(ctx context.Context, rel *repo_model.Release) error {
	saved := new(repo_model.Release)
	has, err := db.GetEngine(ctx).Where("repo_id = ? AND lower_tag_name = ?", rel.RepoID, strings.ToLower(rel.TagName)).Get(saved)
	if err != nil {
		return err
	}
	if !has {
		return governance_model.ErrNotFound
	}
	*rel = *saved
	return nil
}

func reconcileReleasePlan(ctx context.Context, repo *repo_model.Repository, operationID, tag string) {
	if operationID == "" {
		return
	}
	op, err := governance_model.GetReferenceBusinessOperation(ctx, operationID)
	if err != nil {
		return
	}
	gitRepo, err := gitrepo.OpenRepository(ctx, repo)
	if err != nil {
		return
	}
	actual, err := gitRepo.GetRefCommitID(string(git.RefNameFromTag(tag)))
	_ = gitRepo.Close()
	if git.IsErrNotExist(err) {
		actual = git.ObjectFormatFromName(repo.ObjectFormatName).EmptyObjectID().String()
	} else if err != nil {
		return
	}
	_, _ = governance_model.ReconcileReferenceTransaction(ctx, op.ID, map[string]string{string(git.RefNameFromTag(tag)): actual}, nil)
}
