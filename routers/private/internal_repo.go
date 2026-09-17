// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package private

import (
	"context"
	"fmt"
	"net/http"

	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/log"
	"gitea.dev/modules/private"
	"gitea.dev/modules/web"
	gitea_context "gitea.dev/services/context"
)

// RepoAssignmentByStorage 仅用于从物理 cwd 发起的引用事务；稳定 ID 和实际存储路径必须同时匹配。
func RepoAssignmentByStorage(ctx *gitea_context.PrivateContext) {
	option := web.GetForm(ctx).(*private.HookOptions)
	repo, err := repositoryByStorageIdentity(ctx, option.RepositoryID, ctx.PathParam("owner"), ctx.PathParam("repo"))
	if err != nil {
		log.Error("Failed to resolve repository storage identity: id=%d path=%s/%s Error: %v", option.RepositoryID, ctx.PathParam("owner"), ctx.PathParam("repo"), err)
		ctx.JSON(http.StatusNotFound, private.Response{Err: "repository storage identity mismatch"})
		return
	}
	assignRepository(ctx, repo, repo.StorageOwnerName(), repo.StorageName())
}

func repositoryByStorageIdentity(ctx context.Context, id int64, owner, name string) (*repo_model.Repository, error) {
	repo, err := repo_model.GetRepositoryByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if repo.RelativePath() != repo_model.RelativePath(owner, name) {
		return nil, governance_model.ErrNotFound
	}
	return repo, nil
}

// This file contains common functions relating to setting the Repository for the internal routes

// RepoAssignment assigns the repository and git repository to the private context
func RepoAssignment(ctx *gitea_context.PrivateContext) {
	ownerName := ctx.PathParam("owner")
	repoName := ctx.PathParam("repo")

	repo := loadRepository(ctx, ownerName, repoName)
	if ctx.Written() {
		// Error handled in loadRepository
		return
	}

	assignRepository(ctx, repo, ownerName, repoName)
}

func assignRepository(ctx *gitea_context.PrivateContext, repo *repo_model.Repository, ownerName, repoName string) {
	gitRepo, err := gitrepo.RepositoryFromRequestContextOrOpen(ctx, repo)
	if err != nil {
		log.Error("Failed to open repository: %s/%s Error: %v", ownerName, repoName, err)
		ctx.JSON(http.StatusInternalServerError, private.Response{
			Err: fmt.Sprintf("Failed to open repository: %s/%s Error: %v", ownerName, repoName, err),
		})
		return
	}
	ctx.Repo = &gitea_context.Repository{
		Repository: repo,
		GitRepo:    gitRepo,
	}
}

func loadRepository(ctx *gitea_context.PrivateContext, ownerName, repoName string) *repo_model.Repository {
	repo, err := repo_model.GetRepositoryByOwnerAndName(ctx, ownerName, repoName)
	if err != nil {
		log.Error("Failed to get repository: %s/%s Error: %v", ownerName, repoName, err)
		ctx.JSON(http.StatusInternalServerError, private.Response{
			Err: fmt.Sprintf("Failed to get repository: %s/%s Error: %v", ownerName, repoName, err),
		})
		return nil
	}
	if repo.OwnerName == "" {
		repo.OwnerName = ownerName
	}
	return repo
}
