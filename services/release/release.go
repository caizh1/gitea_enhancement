// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package release

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/container"
	"gitea.dev/modules/git"
	"gitea.dev/modules/git/gitcmd"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/graceful"
	"gitea.dev/modules/log"
	"gitea.dev/modules/repository"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/storage"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"
	"gitea.dev/services/context/upload"
	governance_service "gitea.dev/services/governance"
	notify_service "gitea.dev/services/notify"
)

// ErrInvalidTagName represents a "InvalidTagName" kind of error.
type ErrInvalidTagName struct {
	TagName string
}

// IsErrInvalidTagName checks if an error is a ErrInvalidTagName.
func IsErrInvalidTagName(err error) bool {
	_, ok := err.(ErrInvalidTagName)
	return ok
}

func (err ErrInvalidTagName) Error() string {
	return fmt.Sprintf("release tag name is not valid [tag_name: %s]", err.TagName)
}

func (err ErrInvalidTagName) Unwrap() error {
	return util.ErrInvalidArgument
}

// ErrProtectedTagName represents a "ProtectedTagName" kind of error.
type ErrProtectedTagName struct {
	TagName string
}

// IsErrProtectedTagName checks if an error is a ErrProtectedTagName.
func IsErrProtectedTagName(err error) bool {
	_, ok := err.(ErrProtectedTagName)
	return ok
}

func (err ErrProtectedTagName) Error() string {
	return fmt.Sprintf("release tag name is protected [tag_name: %s]", err.TagName)
}

func (err ErrProtectedTagName) Unwrap() error {
	return util.ErrPermissionDenied
}

func createTag(ctx context.Context, gitRepo *git.Repository, rel *repo_model.Release, doer *user_model.User, msg string, recovery *releaseReferencePayload) (bool, string, error) {
	err := rel.LoadAttributes(ctx)
	if err != nil {
		return false, "", err
	}

	err = rel.Repo.MustNotBeArchived()
	if err != nil {
		return false, "", err
	}

	var created bool
	var operationID string
	// Only actual create when publish.
	if !rel.IsDraft {
		if !gitrepo.IsTagExist(ctx, rel.Repo, rel.TagName) {
			if err := rel.LoadAttributes(ctx); err != nil {
				log.Error("LoadAttributes: %v", err)
				return false, "", err
			}

			protectedTags, err := git_model.GetProtectedTags(ctx, rel.Repo.ID)
			if err != nil {
				return false, "", fmt.Errorf("GetProtectedTags: %w", err)
			}

			// Trim '--' prefix to prevent command line argument vulnerability.
			rel.TagName = strings.TrimPrefix(rel.TagName, "--")
			isAllowed, err := git_model.IsUserAllowedToControlTag(ctx, protectedTags, rel.TagName, rel.PublisherID)
			if err != nil {
				return false, "", err
			}
			if !isAllowed {
				return false, "", ErrProtectedTagName{
					TagName: rel.TagName,
				}
			}

			commit, err := gitRepo.GetCommit(rel.Target)
			if err != nil {
				return false, "", err
			}
			publisher, err := user_model.GetUserByID(ctx, rel.PublisherID)
			if err != nil {
				return false, "", err
			}
			if !setting.IsInTesting {
				if err := gitrepo.InstallReferenceTransactionHook(ctx, rel.Repo); err != nil {
					return false, "", fmt.Errorf("安装标签引用事务入口: %w", err)
				}
			}
			committer := publisher
			if actorID := governance_model.AuditActor(ctx).EffectiveUserID(); actorID > 0 && actorID != publisher.ID {
				committer, err = user_model.GetUserByID(ctx, actorID)
				if err != nil {
					return false, "", err
				}
			}
			env := repository.FullPushingEnvironment(publisher, committer, rel.Repo, rel.Repo.Name, 0, 0)
			if recovery != nil && !setting.IsInTesting {
				operationID, err = planReleaseTagCreate(ctx, rel, doer, recovery)
				if err != nil {
					return false, "", err
				}
				finish := governance_model.BeginReferenceBusinessOperation(operationID)
				defer finish()
				defer func() {
					if !created {
						reconcileReleasePlan(ctx, rel.Repo, operationID, rel.TagName)
					}
				}()
				env = appendReleaseReferenceEnv(env, operationID, "release_tag_create", rel.TagName)
			}

			if len(msg) > 0 {
				if err = gitRepo.CreateAnnotatedTagWithEnv(rel.TagName, msg, commit.ID.String(), env); err != nil {
					if strings.Contains(err.Error(), "is not a valid tag name") {
						return false, "", ErrInvalidTagName{
							TagName: rel.TagName,
						}
					}
					return false, "", err
				}
			} else if err = gitRepo.CreateTagWithEnv(rel.TagName, commit.ID.String(), env); err != nil {
				if strings.Contains(err.Error(), "is not a valid tag name") {
					return false, "", ErrInvalidTagName{
						TagName: rel.TagName,
					}
				}
				return false, "", err
			}
			created = true
			rel.LowerTagName = strings.ToLower(rel.TagName)

			objectFormat := git.ObjectFormatFromName(rel.Repo.ObjectFormatName)
			commits := repository.NewPushCommits()
			commits.HeadCommit = repository.CommitToPushCommit(commit)
			commits.CompareURL = rel.Repo.ComposeCompareURL(objectFormat.EmptyObjectID().String(), commit.ID.String())

			refFullName := git.RefNameFromTag(rel.TagName)
			notify_service.PushCommits(
				ctx, rel.Publisher, rel.Repo,
				&repository.PushUpdateOptions{
					RefFullName: refFullName,
					OldCommitID: objectFormat.EmptyObjectID().String(),
					NewCommitID: commit.ID.String(),
				}, commits)
			notify_service.CreateRef(ctx, rel.Publisher, rel.Repo, refFullName, commit.ID.String())
			rel.CreatedUnix = timeutil.TimeStampNow()
		}
		commit, err := gitRepo.GetTagCommit(rel.TagName)
		if err != nil {
			return false, "", fmt.Errorf("GetTagCommit: %w", err)
		}

		rel.Sha1 = commit.ID.String()
		rel.NumCommits, err = gitrepo.CommitsCountOfCommit(ctx, rel.Repo, commit.ID.String())
		if err != nil {
			return false, "", fmt.Errorf("CommitsCount: %w", err)
		}

		if rel.PublisherID <= 0 {
			u, err := user_model.GetUserByEmail(ctx, commit.Author.Email)
			if err == nil {
				rel.PublisherID = u.ID
			}
		}
	} else {
		rel.CreatedUnix = timeutil.TimeStampNow()
	}
	return created, operationID, nil
}

// CreateRelease creates a new release of repository.
func CreateRelease(gitRepo *git.Repository, rel *repo_model.Release, attachmentUUIDs []string, msg string) error {
	has, err := repo_model.IsReleaseExist(gitRepo.Ctx, rel.RepoID, rel.TagName)
	if err != nil {
		return err
	} else if has {
		return repo_model.ErrReleaseAlreadyExist{
			TagName: rel.TagName,
		}
	}

	_, operationID, err := createTag(gitRepo.Ctx, gitRepo, rel, rel.Publisher, msg, &releaseReferencePayload{Action: "create", Release: *rel, AttachmentUUIDs: attachmentUUIDs})
	if err != nil {
		return err
	}
	if operationID != "" {
		if err := governance_model.CompleteRegisteredReferenceBusinessOperation(gitRepo.Ctx, operationID); err != nil {
			return err
		}
		if err := reloadReleaseByTag(gitRepo.Ctx, rel); err != nil {
			return err
		}
		if !rel.IsDraft {
			notify_service.NewRelease(gitRepo.Ctx, rel)
		}
		return nil
	}

	rel.Title = util.EllipsisDisplayString(rel.Title, 255)
	rel.LowerTagName = strings.ToLower(rel.TagName)
	if err = governance_model.WithWrite(gitRepo.Ctx, []string{governance_model.Resource("repository", rel.RepoID)}, func(ctx context.Context) error {
		return db.WithTx(ctx, func(ctx context.Context) error {
			if err := governance_service.CheckRepositoryContentWrite(ctx, rel.Publisher, rel.RepoID, unit.TypeReleases); err != nil {
				return err
			}
			if err := db.Insert(ctx, rel); err != nil {
				return err
			}
			if err := repo_model.AddReleaseAttachments(ctx, rel.ID, attachmentUUIDs); err != nil {
				return err
			}
			return issues_model.AppendRepositoryContentAudit(ctx, "release.created", rel.RepoID, "release", rel.ID, "/releases/"+rel.TagName, map[string]any{"after": map[string]any{"title": rel.Title, "tag_name": rel.TagName, "draft": rel.IsDraft, "prerelease": rel.IsPrerelease}})
		})
	}); err != nil {
		return err
	}

	if !rel.IsDraft {
		notify_service.NewRelease(gitRepo.Ctx, rel)
	}

	return nil
}

// ErrTagAlreadyExists represents an error that tag with such name already exists.
type ErrTagAlreadyExists struct {
	TagName string
}

// IsErrTagAlreadyExists checks if an error is an ErrTagAlreadyExists.
func IsErrTagAlreadyExists(err error) bool {
	_, ok := err.(ErrTagAlreadyExists)
	return ok
}

func (err ErrTagAlreadyExists) Error() string {
	return fmt.Sprintf("tag already exists [name: %s]", err.TagName)
}

func (err ErrTagAlreadyExists) Unwrap() error {
	return util.ErrAlreadyExist
}

// CreateNewTag creates a new repository tag
func CreateNewTag(ctx context.Context, doer *user_model.User, repo *repo_model.Repository, commit, tagName, msg string) error {
	has, err := repo_model.IsReleaseExist(ctx, repo.ID, tagName)
	if err != nil {
		return err
	} else if has {
		return ErrTagAlreadyExists{
			TagName: tagName,
		}
	}

	gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, repo)
	if err != nil {
		return err
	}
	defer closer.Close()

	rel := &repo_model.Release{
		RepoID:       repo.ID,
		Repo:         repo,
		PublisherID:  doer.ID,
		Publisher:    doer,
		TagName:      tagName,
		Target:       commit,
		IsDraft:      false,
		IsPrerelease: false,
		IsTag:        true,
	}

	_, operationID, err := createTag(ctx, gitRepo, rel, doer, msg, &releaseReferencePayload{Action: "create", Release: *rel})
	if err != nil {
		return err
	}
	if operationID != "" {
		if err := governance_model.CompleteRegisteredReferenceBusinessOperation(ctx, operationID); err != nil {
			return err
		}
		return reloadReleaseByTag(ctx, rel)
	}

	return governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", repo.ID)}, func(ctx context.Context) error {
		if err := governance_service.CheckRepositoryContentWrite(ctx, doer, repo.ID, unit.TypeCode); err != nil {
			return err
		}
		return db.Insert(ctx, rel)
	})
}

// UpdateRelease updates information, attachments of a release and will create tag if it's not a draft and tag not exist.
// addAttachmentUUIDs accept a slice of new created attachments' uuids which will be reassigned release_id as the created release
// delAttachmentUUIDs accept a slice of attachments' uuids which will be deleted from the release
// editAttachments accept a map of attachment uuid to new attachment name which will be updated with attachments.
func UpdateRelease(ctx context.Context, doer *user_model.User, gitRepo *git.Repository, rel *repo_model.Release,
	addAttachmentUUIDs, delAttachmentUUIDs []string, editAttachments map[string]string,
) error {
	if rel.ID == 0 {
		return errors.New("UpdateRelease only accepts an exist release")
	}
	isTagCreated, operationID, err := createTag(ctx, gitRepo, rel, doer, "", &releaseReferencePayload{Action: "update", Release: *rel, AddAttachments: addAttachmentUUIDs, DeleteAttachments: delAttachmentUUIDs, EditAttachments: editAttachments})
	if err != nil {
		return err
	}
	if operationID != "" {
		if err := governance_model.CompleteRegisteredReferenceBusinessOperation(ctx, operationID); err != nil {
			return err
		}
		if err := reloadReleaseByTag(ctx, rel); err != nil {
			return err
		}
		if !rel.IsDraft {
			notify_service.NewRelease(ctx, rel)
		}
		return nil
	}
	rel.LowerTagName = strings.ToLower(rel.TagName)

	var isConvertedFromTag bool

	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", rel.RepoID)}, func(ctx context.Context) error {
		return db.WithTx(ctx, func(ctx context.Context) error {
			if err := governance_service.CheckRepositoryContentWrite(ctx, doer, rel.RepoID, unit.TypeReleases); err != nil {
				return err
			}
			oldRelease, err := repo_model.GetReleaseByID(ctx, rel.ID)
			if err != nil {
				return err
			}
			isConvertedFromTag = oldRelease.IsTag && !rel.IsTag
			if err = repo_model.UpdateRelease(ctx, rel); err != nil {
				return err
			}

			if err = repo_model.AddReleaseAttachments(ctx, rel.ID, addAttachmentUUIDs); err != nil {
				return fmt.Errorf("AddReleaseAttachments: %w", err)
			}

			deletedUUIDs := make(container.Set[string])
			if len(delAttachmentUUIDs) > 0 {
				// Check attachments
				attachments, err := repo_model.GetAttachmentsByUUIDs(ctx, delAttachmentUUIDs)
				if err != nil {
					return fmt.Errorf("GetAttachmentsByUUIDs [uuids: %v]: %w", delAttachmentUUIDs, err)
				}
				for _, attach := range attachments {
					if attach.ReleaseID != rel.ID {
						return util.NewPermissionDeniedErrorf("delete attachment of release permission denied")
					}
					deletedUUIDs.Add(attach.UUID)
				}

				if _, err := repo_model.DeleteAttachments(ctx, attachments, true); err != nil {
					return fmt.Errorf("DeleteAttachments [uuids: %v]: %w", delAttachmentUUIDs, err)
				}
			}

			if len(editAttachments) > 0 {
				updateAttachmentsList := make([]string, 0, len(editAttachments))
				for k := range editAttachments {
					updateAttachmentsList = append(updateAttachmentsList, k)
				}
				// Check attachments
				attachments, err := repo_model.GetAttachmentsByUUIDs(ctx, updateAttachmentsList)
				if err != nil {
					return fmt.Errorf("GetAttachmentsByUUIDs [uuids: %v]: %w", updateAttachmentsList, err)
				}
				for _, attach := range attachments {
					if attach.ReleaseID != rel.ID {
						return util.NewPermissionDeniedErrorf("update attachment of release permission denied")
					}
				}

				for uuid, newName := range editAttachments {
					if deletedUUIDs.Contains(uuid) {
						continue
					}
					if err = upload.Verify(nil, newName, setting.Repository.Release.AllowedTypes); err != nil {
						return err
					}
					if err = repo_model.UpdateAttachmentByUUID(ctx, &repo_model.Attachment{
						UUID: uuid,
						Name: newName,
					}, "name"); err != nil {
						return err
					}
				}
			}
			return issues_model.AppendRepositoryContentAudit(ctx, "release.updated", rel.RepoID, "release", rel.ID, "/releases/"+rel.TagName, map[string]any{"before": map[string]any{"title": oldRelease.Title, "tag_name": oldRelease.TagName, "draft": oldRelease.IsDraft, "prerelease": oldRelease.IsPrerelease}, "after": map[string]any{"title": rel.Title, "tag_name": rel.TagName, "draft": rel.IsDraft, "prerelease": rel.IsPrerelease}})
		})
	}); err != nil {
		return err
	}

	for _, uuid := range delAttachmentUUIDs {
		if err := storage.Attachments.Delete(repo_model.AttachmentRelativePath(uuid)); err != nil {
			// Even delete files failed, but the attachments has been removed from database, so we
			// should not return error but only record the error on logs.
			// users have to delete this attachments manually or we should have a
			// synchronize between database attachment table and attachment storage
			log.Error("delete attachment[uuid: %s] failed: %v", uuid, err)
		}
	}

	if !rel.IsDraft {
		if !isTagCreated && !isConvertedFromTag {
			notify_service.UpdateRelease(gitRepo.Ctx, doer, rel)
			return nil
		}
		notify_service.NewRelease(gitRepo.Ctx, rel)
	}
	return nil
}

// DeleteReleaseByID deletes a release and corresponding Git tag by given ID.
func DeleteReleaseByID(ctx context.Context, repo *repo_model.Repository, rel *repo_model.Release, doer *user_model.User, delTag bool) error {
	if delTag && !rel.IsTag {
		return governance_model.ErrForbidden
	}
	operationID := ""
	tagOld := rel.Sha1
	if delTag {
		protectedTags, err := git_model.GetProtectedTags(ctx, rel.RepoID)
		if err != nil {
			return fmt.Errorf("GetProtectedTags: %w", err)
		}
		isAllowed, err := git_model.IsUserAllowedToControlTag(ctx, protectedTags, rel.TagName, doer.ID)
		if err != nil {
			return err
		}
		if !isAllowed {
			return ErrProtectedTagName{
				TagName: rel.TagName,
			}
		}

		if !setting.IsInTesting {
			if err := gitrepo.InstallReferenceTransactionHook(ctx, repo); err != nil {
				return fmt.Errorf("安装标签引用事务入口: %w", err)
			}
		}
		env := repository.FullPushingEnvironment(doer, doer, repo, repo.Name, 0, 0)
		if !setting.IsInTesting {
			gitRepo, err := gitrepo.OpenRepository(ctx, repo)
			if err != nil {
				return err
			}
			old, err := gitRepo.GetRefCommitID(string(git.RefNameFromTag(rel.TagName)))
			_ = gitRepo.Close()
			if err != nil {
				return err
			}
			tagOld = old
			operationID, err = planReleaseTagDelete(ctx, repo, rel, doer, old)
			if err != nil {
				return err
			}
			finish := governance_model.BeginReferenceBusinessOperation(operationID)
			defer finish()
			defer func() { reconcileReleasePlan(ctx, repo, operationID, rel.TagName) }()
			env = appendReleaseReferenceEnv(env, operationID, "release_tag_delete", rel.TagName)
		}
		if stdout, _, err := gitrepo.RunCmdStringWithEnv(ctx, repo,
			gitcmd.NewCommand("update-ref", "-d").AddDynamicArguments(string(git.RefNameFromTag(rel.TagName)), tagOld),
			env,
		); err != nil && !strings.Contains(err.Error(), "not found") {
			log.Error("DeleteReleaseByID (git tag -d): %d in %v Failed:\nStdout: %s\nError: %v", rel.ID, repo, stdout, err)
			return fmt.Errorf("git tag -d: %w", err)
		}
		if operationID != "" {
			if err := governance_model.CompleteRegisteredReferenceBusinessOperation(ctx, operationID); err != nil {
				return err
			}
			if !rel.IsDraft {
				notify_service.DeleteRelease(ctx, doer, rel)
			}
			return nil
		}

		refName := git.RefNameFromTag(rel.TagName)
		objectFormat := git.ObjectFormatFromName(repo.ObjectFormatName)
		notify_service.PushCommits(
			ctx, doer, repo,
			&repository.PushUpdateOptions{
				RefFullName: refName,
				OldCommitID: rel.Sha1,
				NewCommitID: objectFormat.EmptyObjectID().String(),
			}, repository.NewPushCommits())
		notify_service.DeleteRef(ctx, doer, repo, refName)
	}

	if err := governance_model.WithWrite(ctx, []string{governance_model.Resource("repository", rel.RepoID)}, func(ctx context.Context) error {
		return db.WithTx(ctx, func(ctx context.Context) error {
			unitType := unit.TypeReleases
			if delTag {
				unitType = unit.TypeCode
			}
			if err := governance_service.CheckRepositoryContentWrite(ctx, doer, rel.RepoID, unitType); err != nil {
				return err
			}
			current, err := repo_model.GetReleaseByID(ctx, rel.ID)
			if err != nil {
				return err
			}
			if delTag && !current.IsTag {
				return governance_model.ErrForbidden
			}
			current.Repo = repo
			if err := current.LoadAttributes(ctx); err != nil {
				return fmt.Errorf("LoadAttributes: %w", err)
			}
			rel.Attachments = current.Attachments
			if delTag {
				if _, err := db.DeleteByID[repo_model.Release](ctx, rel.ID); err != nil {
					return fmt.Errorf("DeleteReleaseByID: %w", err)
				}
			} else {
				rel.IsTag = true
				if err := repo_model.UpdateRelease(ctx, rel); err != nil {
					return fmt.Errorf("Update: %w", err)
				}
			}
			if err := repo_model.DeleteAttachmentsByRelease(ctx, rel.ID); err != nil {
				return fmt.Errorf("DeleteAttachments: %w", err)
			}
			return issues_model.AppendRepositoryContentAudit(ctx, "release.deleted", rel.RepoID, "release", rel.ID, "/releases/"+current.TagName, map[string]any{"before": map[string]any{"title": current.Title, "tag_name": current.TagName, "draft": current.IsDraft, "prerelease": current.IsPrerelease}})
		})
	}); err != nil {
		return err
	}

	if !rel.IsDraft {
		notify_service.DeleteRelease(ctx, doer, rel)
	}
	return nil
}

// Init start release service
func Init() error {
	return initTagSyncQueue(graceful.GetManager().ShutdownContext())
}
