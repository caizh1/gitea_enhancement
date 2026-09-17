// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"net/http"

	"gitea.dev/services/context"
	governance_service "gitea.dev/services/governance"
	files_service "gitea.dev/services/repository/files"
)

// GetBlob get the blob of a repository file.
func GetBlob(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/git/blobs/{sha} repository GetBlob
	// ---
	// summary: Gets the blob of a repository.
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo
	//   type: string
	//   required: true
	// - name: sha
	//   in: path
	//   description: sha of the commit
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/GitBlobResponse"
	//   "400":
	//     "$ref": "#/responses/error"
	//   "404":
	//     "$ref": "#/responses/notFound"

	sha := ctx.PathParam("sha")
	if len(sha) == 0 {
		ctx.APIError(http.StatusBadRequest, "sha not provided")
		return
	}

	if ctx.Doer != nil {
		finish, err := governance_service.BeginRepositoryAccessAudit(ctx, governance_service.APIRequestActor(ctx.Doer, ctx.AuthenticatedUser, ctx.RemoteAddr()), ctx.Repo.Repository, "access.code", map[string]any{"object_kind": "blob", "object_sha": sha})
		if err != nil {
			ctx.APIErrorInternal(err)
			return
		}
		if finish != nil {
			defer func() { finish(ctx.WrittenStatus()) }()
		}
	}
	if blob, err := files_service.GetBlobBySHA(ctx.Repo.Repository, ctx.Repo.GitRepo, sha); err != nil {
		ctx.APIError(http.StatusBadRequest, err.Error())
	} else {
		ctx.JSON(http.StatusOK, blob)
	}
}
