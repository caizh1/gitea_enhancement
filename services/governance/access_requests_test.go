// Copyright 2026 Gitea.
// Copyright 2026 企业治理贡献者。
// SPDX-License-Identifier: MIT

package governance

import (
	"testing"

	"gitea.dev/models/db"
	governance_model "gitea.dev/models/governance"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/structs"

	"github.com/stretchr/testify/require"
)

func TestAccessRequestLifecycleAndPrivacy(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	applicant := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}
	group, err := CreateGroup(ctx, owner, GroupOption{Path: "request-group", Visibility: 0})
	require.NoError(t, err)
	state, err := GetAccessRequestState(ctx, 4, "group", group.ID, 0)
	require.NoError(t, err)
	require.True(t, state.CanRequest)
	require.False(t, state.CanManage)
	request, err := RequestAccess(ctx, applicant, "group", group.ID)
	require.NoError(t, err)
	_, err = RequestAccess(ctx, applicant, "group", group.ID)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 0)
	state, err = GetAccessRequestState(ctx, 4, "group", group.ID, 0)
	require.NoError(t, err)
	require.Equal(t, request.ID, state.OwnRequest.ID)
	require.Empty(t, state.Requests, "申请人不能读取管理员待处理队列")
	stranger, err := GetAccessRequestState(ctx, 5, "group", group.ID, 0)
	require.NoError(t, err)
	require.Nil(t, stranger.OwnRequest)
	require.Empty(t, stranger.Requests)
	_, err = SaveAccessRequestSetting(ctx, owner, "group", group.ID, AccessRequestSettingOption{Disabled: true})
	require.NoError(t, err)
	_, err = RequestAccess(ctx, governance_model.Actor{ID: 5, Name: "user5", Kind: "user", Transport: "api"}, "group", group.ID)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	require.NoError(t, DecideAccessRequest(ctx, applicant, "group", group.ID, request.ID, GroupMemberOption{}, false))
	_, err = SaveAccessRequestSetting(ctx, owner, "group", group.ID, AccessRequestSettingOption{Revision: 1})
	require.NoError(t, err)
	replacement, err := RequestAccess(ctx, applicant, "group", group.ID)
	require.NoError(t, err)
	require.NotEqual(t, request.ID, replacement.ID)
	option := GroupMemberOption{Role: governance_model.Developer, Revision: group.Revision}
	require.ErrorIs(t, DecideAccessRequest(ctx, owner, "group", group.ID, request.ID, option, true), governance_model.ErrNotFound)
	bad := owner
	bad.Transport = ""
	require.Error(t, DecideAccessRequest(ctx, bad, "group", group.ID, replacement.ID, option, true))
	unittest.AssertExistsAndLoadBean(t, &governance_model.AccessRequest{ID: replacement.ID})
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 0)
	require.NoError(t, DecideAccessRequest(ctx, owner, "group", group.ID, replacement.ID, option, true))
	unittest.AssertCount(t, &governance_model.AccessRequest{ID: replacement.ID}, 0)
	unittest.AssertExistsAndLoadBean(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4, Role: governance_model.Developer})
	_, err = RequestAccess(ctx, applicant, "group", group.ID)
	require.ErrorIs(t, err, governance_model.ErrConflict)
	private, err := CreateGroup(ctx, owner, GroupOption{Path: "request-private", Visibility: 2})
	require.NoError(t, err)
	_, err = RequestAccess(ctx, applicant, "group", private.ID)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	_, err = GetAccessRequestState(ctx, 4, "group", private.ID, 0)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
}

func TestAccessRequestIdentityAndPrivateWithdrawal(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	applicant := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}
	group, err := CreateGroup(ctx, owner, GroupOption{Path: "request-identity", Visibility: 0})
	require.NoError(t, err)
	request, err := RequestAccess(ctx, applicant, "group", group.ID)
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(4).Cols("name", "lower_name", "prohibit_login").Update(&user_model.User{Name: "renamed-applicant", LowerName: "renamed-applicant", ProhibitLogin: true})
	require.NoError(t, err)
	state, err := GetAccessRequestState(ctx, 2, "group", group.ID, 0)
	require.NoError(t, err)
	require.Len(t, state.Requests, 1)
	require.Equal(t, "user4", state.Requests[0].Username)
	require.Equal(t, "renamed-applicant", state.Requests[0].CurrentUsername)
	require.Equal(t, "unavailable", state.Requests[0].UserState)
	require.Error(t, DecideAccessRequest(ctx, owner, "group", group.ID, request.ID, GroupMemberOption{Role: governance_model.Developer, Revision: state.Revision}, true))
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 4}, 0)
	_, err = db.GetEngine(ctx).ID(group.ID).Cols("visibility").Update(&governance_model.Namespace{Visibility: 2})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(group.ID).Cols("visibility").Update(&user_model.User{Visibility: structs.VisibleTypePrivate})
	require.NoError(t, err)
	require.Error(t, DecideAccessRequest(ctx, applicant, "group", group.ID, request.ID, GroupMemberOption{}, false), "停用账户不能借私有资源撤回分支绕过身份校验")
	_, err = db.GetEngine(ctx).ID(4).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: false})
	require.NoError(t, err)
	_, err = GetAccessRequestState(ctx, 4, "group", group.ID, 0)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	own, err := OwnAccessRequests(ctx, 4, 0)
	require.NoError(t, err)
	require.Len(t, own, 1)
	require.Equal(t, "request-identity", own[0].ScopePath)
	other, err := OwnAccessRequests(ctx, 5, 0)
	require.NoError(t, err)
	require.Empty(t, other)
	stranger := governance_model.Actor{ID: 5, Name: "user5", Kind: "user", Transport: "web"}
	require.ErrorIs(t, WithdrawOwnAccessRequest(ctx, stranger, request.ID), governance_model.ErrNotFound)
	require.NoError(t, WithdrawOwnAccessRequest(ctx, applicant, request.ID))

	unittest.AssertCount(t, &governance_model.AccessRequest{ID: request.ID}, 0)
}

func TestAccessRequestKeepsSubmittedPathAfterPrivateGroupMove(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	applicant := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}
	group, err := CreateGroup(ctx, owner, GroupOption{Path: "request-before", Visibility: 0})
	require.NoError(t, err)
	request, err := RequestAccess(ctx, applicant, "group", group.ID)
	require.NoError(t, err)
	require.Equal(t, "request-before", request.ScopePath)
	secondRequest, err := RequestAccess(ctx, governance_model.Actor{ID: 5, Name: "user5", Kind: "user", Transport: "api"}, "group", group.ID)
	require.NoError(t, err)

	_, err = db.GetEngine(ctx).ID(group.ID).Cols("visibility").Update(&governance_model.Namespace{Visibility: 2})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).ID(group.ID).Cols("visibility").Update(&user_model.User{Visibility: structs.VisibleTypePrivate})
	require.NoError(t, err)
	_, err = CheckGroupAccess(ctx, applicant.ID, group.ID, governance_model.ReadGroup)
	require.ErrorIs(t, err, governance_model.ErrNotFound)
	_, err = MoveGroup(ctx, owner, group.ID, GroupOption{Path: "request-after-private", Revision: group.Revision})
	require.NoError(t, err)

	requests, err := OwnAccessRequests(ctx, applicant.ID, 0)
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.Equal(t, request.ID, requests[0].ID)
	require.Equal(t, "request-before", requests[0].ScopePath)
	state, err := GetAccessRequestState(ctx, owner.ID, "group", group.ID, 0)
	require.NoError(t, err)
	require.Equal(t, "request-after-private", state.FullPath)
	require.NoError(t, DecideAccessRequest(ctx, owner, "group", group.ID, secondRequest.ID, GroupMemberOption{Role: governance_model.Developer, Revision: state.Revision}, true))
	unittest.AssertExistsAndLoadBean(t, &governance_model.Membership{ScopeType: "group", ScopeID: group.ID, UserID: 5, Role: governance_model.Developer})
	require.NoError(t, WithdrawOwnAccessRequest(ctx, applicant, request.ID))
}

func TestProjectAccessRequestApproval(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx := t.Context()
	require.NoError(t, governance_model.InitializeLegacyNamespaces(ctx))
	owner := governance_model.Actor{ID: 2, Name: "user2", Kind: "user", Transport: "api"}
	applicant := governance_model.Actor{ID: 4, Name: "user4", Kind: "user", Transport: "api"}
	repo := &repo_model.Repository{OwnerID: 3, OwnerName: "org3", OwnerNamespace: "org3", Name: "request-project", LowerName: "request-project"}
	require.NoError(t, db.Insert(ctx, repo))
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeCode}))
	request, err := RequestAccess(ctx, applicant, "repository", repo.ID)
	require.NoError(t, err)
	state, err := GetAccessRequestState(ctx, 2, "repository", repo.ID, 0)
	require.NoError(t, err)
	require.True(t, state.CanManage)
	require.Len(t, state.Requests, 1)
	require.ErrorIs(t, DecideAccessRequest(ctx, applicant, "repository", repo.ID, request.ID, GroupMemberOption{Role: governance_model.Developer, Revision: state.Revision}, true), governance_model.ErrNotFound)
	_, err = SaveAccessRequestSetting(ctx, owner, "repository", repo.ID, AccessRequestSettingOption{Disabled: true})
	require.NoError(t, err)
	_, err = SaveAccessRequestSetting(ctx, owner, "repository", repo.ID, AccessRequestSettingOption{Revision: 0})
	require.ErrorIs(t, err, governance_model.ErrConflict)
	require.ErrorIs(t, DecideAccessRequest(ctx, owner, "repository", repo.ID, request.ID, GroupMemberOption{Role: governance_model.Developer, Revision: state.Revision + 1}, true), governance_model.ErrConflict)
	require.NoError(t, DecideAccessRequest(ctx, owner, "repository", repo.ID, request.ID, GroupMemberOption{UserID: 5, Role: governance_model.Developer, Revision: state.Revision}, true), "关闭新申请后仍允许处理待审申请")
	unittest.AssertExistsAndLoadBean(t, &governance_model.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 4, Role: governance_model.Developer})
	unittest.AssertCount(t, &governance_model.Membership{ScopeType: "repository", ScopeID: repo.ID, UserID: 5}, 0)
	unittest.AssertCount(t, &governance_model.AccessRequest{ID: request.ID}, 0)
}
