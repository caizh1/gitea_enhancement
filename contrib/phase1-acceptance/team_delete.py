#!/usr/bin/env python3
"""隔离实例非空 Team 删除：保留其他 Team 与直接成员的独立授权。"""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

from capacity_probe import API, write_json


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("prepare", "verify"))
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=Path, required=True)
    parser.add_argument("--state", type=Path, required=True)
    parser.add_argument("--owner", required=True)
    parser.add_argument("--team-only", required=True)
    parser.add_argument("--two-teams", required=True)
    parser.add_argument("--direct", required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--isolated-instance", required=True, action="store_true")
    args = parser.parse_args()
    credentials = json.loads(args.credentials.read_text())

    def actor(name):
        return name, credentials.get(name, credentials.get("password"))

    api = API(args.base, *actor(args.owner))
    identities = {"Owner": args.owner, "仅Team": args.team_only, "两Team": args.two_teams, "直接": args.direct}
    state = json.loads(args.state.read_text()) if args.state.exists() else {
        "实例": args.base, "源码": args.source, "身份": identities, "步骤": [],
        "脚本SHA256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    assert state["实例"] == args.base and state["身份"] == identities and state["源码"] == args.source

    def check(method, path, expected=200, payload=None, user=args.owner):
        code, body, _, _ = api.call(method, path, payload, actor(user))
        state["步骤"].append({"操作": method + " " + path, "身份": user, "HTTP": code, "预期": expected})
        write_json(args.state, state)
        assert code == expected, f"{method} {path} HTTP{code}，预期{expected}"
        return body

    def git_read(user, expected):
        auth = base64.b64encode(":".join(actor(user)).encode()).decode()
        env = os.environ.copy()
        env.update(GIT_TERMINAL_PROMPT="0", GIT_CONFIG_COUNT="2", GIT_CONFIG_KEY_0="http.extraHeader",
                   GIT_CONFIG_VALUE_0="Authorization: Basic " + auth,
                   GIT_CONFIG_KEY_1="credential.helper", GIT_CONFIG_VALUE_1="")
        command = ["git", "ls-remote", args.base.rstrip("/") + "/" + state["仓库"]["full_name"] + ".git"]
        result = subprocess.run(command, env=env, capture_output=True, text=True, timeout=30)
        state["步骤"].append({"操作": "HTTP Git ls-remote", "身份": user, "退出码": result.returncode,
                              "预期可读": expected, "引用数": len(result.stdout.splitlines())})
        write_json(args.state, state)
        assert (result.returncode == 0 and bool(result.stdout.strip())) == expected

    if args.mode == "prepare":
        assert not args.state.exists(), "准备阶段必须使用全新状态文件，避免重复资源"
        name = "phase1-team-delete-" + str(time.time_ns())
        group = check("POST", "/governance/groups", 201,
                      {"path": name, "name": "非空团队删除验收", "visibility": 2})
        state["群组"] = group
        repo = check("POST", "/orgs/" + name + "/repos", 201,
                     {"name": "team-target", "private": True, "auto_init": True, "default_branch": "main"})
        state["仓库"] = {"id": repo["id"], "full_name": repo["full_name"]}
        state["团队"] = []
        for name, members, permission in (("alpha", [args.team_only, args.two_teams], "write"),
                                           ("beta", [args.two_teams], "read")):
            team = check("POST", "/orgs/" + group["full_path"] + "/teams", 201,
                         {"name": name, "permission": permission, "units_map": {"repo.code": permission, "repo.issues": permission}})
            state["团队"].append({"id": team["id"], "name": name})
            check("PUT", f"/teams/{team['id']}/repos/{repo['full_name']}", 204)
            for user in members:
                check("PUT", f"/teams/{team['id']}/members/{user}", 204)
        user = check("GET", "/users/" + args.direct)
        direct = check("GET", f"/governance/repositories/{repo['id']}/members")
        check("PUT", f"/governance/repositories/{repo['id']}/members/{user['id']}", 204,
              {"role": 20, "revision": direct["revision"]})
        write_json(args.state, state)
        for user in (args.team_only, args.two_teams, args.direct):
            git_read(user, True)
        check("DELETE", f"/teams/{state['团队'][0]['id']}", 403, user=args.team_only)
        print("夹具已建，请普通Owner从原生UI删除非空alpha团队：" + group["full_path"])
    else:
        alpha, beta = state["团队"]
        check("GET", f"/teams/{alpha['id']}", 404)
        surviving = check("GET", f"/teams/{beta['id']}/members")
        assert args.two_teams in [row["login"] for row in surviving]
        repos = check("GET", f"/teams/{beta['id']}/repos")
        assert state["仓库"]["id"] in [row["id"] for row in repos]
        for user, allowed in ((args.team_only, False), (args.two_teams, True), (args.direct, True)):
            git_read(user, allowed)
        print("非空团队删除已验证：独有来源撤权，另一团队和直接成员仍可读")


if __name__ == "__main__":
    main()
