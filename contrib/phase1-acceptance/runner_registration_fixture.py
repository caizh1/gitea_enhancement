#!/usr/bin/env python3
"""创建独立群组和私存注册令牌，供 S13-a 页面轮换验收。"""

import argparse
import hashlib
import json
import pathlib
import sys
import time

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--owner-user", required=True)
    parser.add_argument("--temporary-owner", required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    args = parser.parse_args()
    if args.state.exists():
        raise RuntimeError("夹具结果已存在；不得覆盖")
    args.state.parent.mkdir(parents=True, exist_ok=True)
    api = Client(args.base, args.credentials, args.owner_user)
    state = {"说明": "S13-a 独立群组 Runner 注册令牌夹具", "步骤": [],
        "identities": {"owner": args.owner_user, "temporaryOwner": args.temporary_owner}}

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
        owner = call("普通 Owner 身份", "/api/v1/user")
        temporary = call("临时 Owner 身份", "/api/v1/user", user=args.temporary_owner)
        if owner["is_admin"] or temporary["is_admin"]:
            raise AssertionError("验收账号不能是实例管理员")
        suffix = str(time.time_ns())
        group = call("创建独立私有组", "/api/v1/governance/groups", "POST",
            {"name": "注册令牌轮换验收", "path": "phase1-registration-" + suffix,
             "parent_id": 0, "visibility": 2}, expected=201)
        state["group"] = {k: group[k] for k in ("id", "compatibility_name", "parent_id", "revision")}
        save()
        group_id = group["id"]
        current = call("读取组修订", f"/api/v1/governance/groups/{group_id}")
        call("赋予临时当前 Owner", f"/api/v1/governance/groups/{group_id}/members/{temporary['id']}",
            "PUT", {"role": 50, "revision": current["revision"]}, expected=204)
        path = "/api/v1/orgs/" + group["compatibility_name"] + "/actions/runners/registration-token"
        token = call("源 Owner 读取注册令牌", path, "POST")["token"]
        token_by_temporary = call("临时 Owner 读取同一注册令牌", path, "POST", user=args.temporary_owner)["token"]
        if token != token_by_temporary:
            raise AssertionError("两个当前 Owner 读取到不同令牌")
        secret = args.state.parent / "registration-token-before.json"
        secret.write_text(json.dumps({"token": token}) + "\n")
        secret.chmod(0o600)
        state["tokenBeforeSHA256"] = hashlib.sha256(token.encode()).hexdigest()
        state["temporaryOwnerID"] = temporary["id"]
        state["runnerUI"] = args.base.rstrip("/") + "/org/" + group["compatibility_name"] + "/settings/actions/runners"
        state["tokenAPI"] = path
        state["结果"] = "等待页面轮换"
    except Exception as error:
        state["结果"], state["首错"] = "失败", str(error)
        raise
    finally:
        save()
    print("夹具可用：群组", group["id"], "页面", state["runnerUI"])


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
