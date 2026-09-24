// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"context"
	"errors"
	"fmt"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/gitrepo"
)

type PullApprovalSnapshot struct {
	Version            governance_model.PullVersion
	Previous           *governance_model.PullVersion
	ReferenceRevisions map[int64]int64
	BaseHead           string
	ExpectedBaseBranch string
	HeadBranch         string
	BaseRepoID         int64
	HeadRepoID         int64
	ResetOnChange      bool
}

// CapturePullApprovalSnapshot 读取固定 Git 对象；Apply 必须在原生变更事务最前面执行。
func CapturePullApprovalSnapshot(ctx context.Context, pr *issues_model.PullRequest, targetBranch string) (*PullApprovalSnapshot, error) {
	return capturePullApprovalSnapshot(ctx, pr, targetBranch, false)
}

func CaptureInitialPullApprovalSnapshot(ctx context.Context, pr *issues_model.PullRequest) (*PullApprovalSnapshot, error) {
	return capturePullApprovalSnapshot(ctx, pr, pr.BaseBranch, true)
}

func capturePullApprovalSnapshot(ctx context.Context, pr *issues_model.PullRequest, targetBranch string, initialize bool) (*PullApprovalSnapshot, error) {
	revisions := make(map[int64]int64)
	if err := governance_model.WithStableRead(ctx, func(ctx context.Context) error {
		for _, id := range []int64{pr.BaseRepoID, pr.HeadRepoID} {
			value, err := governance_model.ReadReferenceRevision(ctx, id)
			if err != nil {
				return err
			}
			revisions[id] = value
		}
		return nil
	}); err != nil {
		return nil, err
	}
	fresh, err := issues_model.GetPullRequestByID(ctx, pr.ID)
	if err != nil {
		return nil, err
	}
	if fresh.BaseRepoID != pr.BaseRepoID || fresh.HeadRepoID != pr.HeadRepoID || fresh.HeadBranch != pr.HeadBranch {
		return nil, governance_model.ErrConflict
	}
	previous, _, err := db.GetByID[governance_model.PullVersion](ctx, pr.ID)
	if err != nil {
		return nil, err
	}
	if previous == nil && !initialize {
		return nil, nil //nolint:nilnil // 未接入的历史 PR 保持原生行为。
	}
	base, err := repo_model.GetRepositoryByID(ctx, pr.BaseRepoID)
	if err != nil {
		return nil, err
	}
	head, err := repo_model.GetRepositoryByID(ctx, pr.HeadRepoID)
	if err != nil {
		return nil, err
	}
	if initialize {
		for _, repository := range []*repo_model.Repository{base, head} {
			installed, err := gitrepo.ReferenceTransactionHookInstalled(repository)
			if err != nil {
				return nil, err
			}
			if !installed {
				if previous != nil {
					return nil, governance_model.ErrConflict
				}
				return nil, nil //nolint:nilnil // 两端未接入的 PR 继续使用原生评审。
			}
		}
	}
	baseGit, err := gitrepo.OpenRepository(ctx, base)
	if err != nil {
		return nil, err
	}
	defer baseGit.Close()
	headGit, err := gitrepo.OpenRepository(ctx, head)
	if err != nil {
		return nil, err
	}
	defer headGit.Close()
	baseID, err := baseGit.GetBranchCommitID(targetBranch)
	if err != nil {
		return nil, err
	}
	headID, err := headGit.GetRefCommitID(pullHeadRef(pr))
	if err != nil {
		return nil, err
	}
	if previous != nil && previous.Head != headID {
		return nil, fmt.Errorf("%w：PR 源分支提交与已记录版本不一致", governance_model.ErrConflict)
	}
	patch, err := gitrepo.ApprovalPatchID(ctx, base, head, baseID, headID, nil)
	if err != nil {
		return nil, err
	}
	settings, err := governance_model.ResolveApprovalSettings(ctx, base.ID, base.OwnerID)
	if err != nil {
		return nil, err
	}
	return &PullApprovalSnapshot{Version: governance_model.PullVersion{PullID: pr.ID, Head: headID, BaseBranch: targetBranch, PatchID: patch}, Previous: previous, ReferenceRevisions: revisions, ExpectedBaseBranch: fresh.BaseBranch, HeadBranch: fresh.HeadBranch, BaseHead: baseID, BaseRepoID: base.ID, HeadRepoID: head.ID, ResetOnChange: settings.Settings.ResetOnChange}, nil
}

func ApplyPullApprovalSnapshot(ctx context.Context, snapshot *PullApprovalSnapshot) error {
	if snapshot == nil {
		return nil
	}
	return governance_model.WithWrite(ctx, []string{governance_model.Resource("pull", snapshot.Version.PullID), governance_model.Resource("repository", snapshot.BaseRepoID), governance_model.Resource("repository", snapshot.HeadRepoID)}, func(ctx context.Context) error {
		for _, id := range []int64{snapshot.BaseRepoID, snapshot.HeadRepoID} {
			expected, exists := snapshot.ReferenceRevisions[id]
			current, err := governance_model.ReadReferenceRevision(ctx, id)
			if err != nil {
				return err
			}
			if !exists || current != expected {
				return governance_model.ErrConflict
			}
		}
		fresh, err := issues_model.GetPullRequestByID(ctx, snapshot.Version.PullID)
		if err != nil {
			return err
		}
		if fresh.BaseRepoID != snapshot.BaseRepoID || fresh.HeadRepoID != snapshot.HeadRepoID || fresh.HeadBranch != snapshot.HeadBranch || fresh.BaseBranch != snapshot.ExpectedBaseBranch {
			return governance_model.ErrConflict
		}
		current, has, err := db.GetByID[governance_model.PullVersion](ctx, snapshot.Version.PullID)
		if err != nil {
			return err
		}
		expectedHead := ""
		if snapshot.Previous != nil {
			if !has || *current != *snapshot.Previous {
				return governance_model.ErrConflict
			}
			expectedHead = current.Head
		} else if has {
			return governance_model.ErrConflict
		}
		if _, err := governance_model.SnapshotProjectPullRules(ctx, snapshot.Version.PullID, snapshot.BaseRepoID, false); err != nil {
			return err
		}
		repo, err := repo_model.GetRepositoryByID(ctx, snapshot.BaseRepoID)
		if err != nil {
			return err
		}
		settings, err := governance_model.ResolveApprovalSettings(ctx, repo.ID, repo.OwnerID)
		if err != nil {
			return err
		}
		scope, err := pullVersionAuditScope(ctx, repo)
		if err != nil {
			return err
		}
		_, err = governance_model.AdvancePullVersion(ctx, snapshot.Version, expectedHead, settings.Settings.ResetOnChange, scope)
		return err
	})
}

func pullVersionAuditScope(ctx context.Context, repo *repo_model.Repository) (*governance_model.PullVersionAuditScope, error) {
	chain, err := governance_model.Ancestors(ctx, repo.OwnerID)
	scope := &governance_model.PullVersionAuditScope{RepoID: repo.ID, Path: repo.FullPath()}
	if errors.Is(err, governance_model.ErrNotFound) {
		if err := repo.LoadOwner(ctx); err != nil {
			return nil, err
		}
		if repo.Owner.IsOrganization() {
			scope.AncestorIDs = []int64{repo.OwnerID}
		}
		return scope, nil
	}
	if err != nil {
		return nil, err
	}
	for _, group := range chain {
		if group.Kind == "group" {
			scope.AncestorIDs = append(scope.AncestorIDs, group.ID)
		}
	}
	return scope, nil
}
