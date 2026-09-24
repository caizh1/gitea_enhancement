#!/usr/bin/env python3
"""在既有群组下创建独立的令牌上限父子组和消费仓。"""

import argparse
import json
import pathlib
import sys
import time

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--ancestor-id", type=int, required=True)
    parser.add_argument("--owner-user", required=True)
    parser.add_argument("--repo-admin", required=True)
    parser.add_argument("--unrelated", required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    args = parser.parse_args()
    if args.state.exists():
        raise RuntimeError("夹具结果已存在；不得覆盖")
    args.state.parent.mkdir(parents=True, exist_ok=True)
    api = Client(args.base, args.credentials, args.owner_user)
    state = {"说明": "CI06-a 独立父子组、消费仓和角色夹具", "ancestorID": args.ancestor_id,
        "identities": {"owner": args.owner_user, "repoAdmin": args.repo_admin, "unrelated": args.unrelated}, "步骤": []}

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def call(label, path, method="GET", body=None, user=None, expected=200):
        code, result = api.call(path, method, body, user)
        state["步骤"].append({"检查": label, "实际": code, "期望": expected, "通过": code == expected})
        save()
        if code != expected:
            raise AssertionError(f"{label}：HTTP {code}")
        return result

    try:
        ancestor = call("确认祖先组", f"/api/v1/governance/groups/{args.ancestor_id}")
        owner = call("确认普通 Owner", "/api/v1/user")
        repo_admin = call("确认独立仓库管理员", "/api/v1/user", user=args.repo_admin)
        unrelated = call("确认无关账号", "/api/v1/user", user=args.unrelated)
        if any(user["is_admin"] for user in (owner, repo_admin, unrelated)):
            raise AssertionError("夹具账号不得是实例管理员")
        for user in (args.repo_admin, args.unrelated):
            code, _ = api.call(f"/api/v1/governance/groups/{args.ancestor_id}", user=user)
            if code != 404:
                raise AssertionError(user + " 已拥有祖先组权限")
        suffix = str(time.time_ns())
        parent = call("创建令牌上限父组", "/api/v1/governance/groups", "POST",
            {"name": "令牌上限父组", "path": "token-limit-" + suffix,
             "parent_id": args.ancestor_id, "visibility": 2}, expected=201)
        state["parent"] = {k: parent[k] for k in ("id", "compatibility_name", "parent_id", "revision")}
        save()
        child = call("创建令牌上限子组", "/api/v1/governance/groups", "POST",
            {"name": "令牌上限子组", "path": "consumer", "parent_id": parent["id"], "visibility": 2}, expected=201)
        state["child"] = {k: child[k] for k in ("id", "compatibility_name", "parent_id", "revision")}
        save()
        child_owner = child["compatibility_name"]
        repo = call("创建私有消费仓", f"/api/v1/orgs/{child_owner}/repos", "POST",
            {"name": "token-limit-probe", "private": True, "auto_init": True}, expected=201)
        state["repo"] = {k: repo[k] for k in ("id", "name", "default_branch", "private")}
        save()
        call("独立账号只授消费仓 Admin", f"/api/v1/repos/{child_owner}/token-limit-probe/collaborators/{args.repo_admin}",
            "PUT", {"permission": "admin"}, expected=204)
        call("仓库 Admin 可读消费仓", f"/api/v1/repos/{child_owner}/token-limit-probe", user=args.repo_admin)
        code, _ = api.call(f"/api/v1/governance/groups/{child['id']}", user=args.repo_admin)
        if code != 404:
            raise AssertionError("仓库 Admin 意外获得子组权限")
        code, _ = api.call(f"/api/v1/repos/{child_owner}/token-limit-probe", user=args.unrelated)
        if code != 404:
            raise AssertionError("无关账号意外可读私有消费仓")
        state["URLs"] = {
            "parent": args.base.rstrip("/") + "/org/" + parent["compatibility_name"] + "/settings/actions/general",
            "child": args.base.rstrip("/") + "/org/" + child_owner + "/settings/actions/general",
            "repo": args.base.rstrip("/") + "/" + child_owner + "/token-limit-probe/settings/actions/general",
        }
        state["结果"] = "夹具可用"
    except Exception as error:
        state["结果"], state["首错"] = "失败", str(error)
        raise
    finally:
        save()
    print("夹具可用：父组", parent["id"], "子组", child["id"], "仓", repo["id"], args.state)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
