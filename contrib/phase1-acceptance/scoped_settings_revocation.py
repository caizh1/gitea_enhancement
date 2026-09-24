#!/usr/bin/env python3
"""为统一工作流设置的旧页面撤权验收准备独立群组；页面提交由浏览器执行。"""

import argparse
import base64
import json
import pathlib
import time

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=("prepare", "grant", "revoke", "enable-pr", "prepare-move", "resume-move", "move-away", "inspect"))
    parser.add_argument("--credentials", required=True, type=pathlib.Path)
    parser.add_argument("--state", required=True, type=pathlib.Path)
    parser.add_argument("--base", default="http://127.0.0.1:13043")
    parser.add_argument("--owner", default="phase1-v15-owner")
    parser.add_argument("--subject", default="phase1-v15-outsider")
    args = parser.parse_args()
    if args.base != "http://127.0.0.1:13043":
        parser.error("只允许本次授权的回环隔离实例")
    if args.stage == "prepare" and args.state.exists():
        parser.error("不覆盖已有夹具")
    state = json.loads(args.state.read_text()) if args.state.exists() else {"说明": "S20旧页面提交撤权", "步骤": []}
    client = Client(args.base, args.credentials, args.owner)

    def save():
        args.state.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")
        args.state.chmod(0o600)

    def api(path, method="GET", body=None, expected=200, user=None):
        code, value = client.call("/api/v1" + path, method, body, user)
        state["步骤"].append({"请求": method + " " + path, "实际": code, "期望": expected, "通过": code == expected})
        save()
        if code != expected:
            raise AssertionError("正式请求失败，原始状态已保留")
        return value

    if args.stage == "prepare":
        for user in (args.owner, args.subject):
            identity = api("/user", user=user)
            if identity["is_admin"]:
                raise AssertionError("必须使用普通账号")
        state["主体ID"] = identity["id"]
        group = api("/governance/groups", "POST", {"name": "一期旧设置页撤权验收", "path": "phase1-settings-" + str(time.time_ns()), "visibility": 2}, 201)
        state["群组"] = group
        save()
        repo = api("/orgs/" + group["compatibility_name"] + "/repos", "POST", {"name": "source", "private": True, "auto_init": True, "default_branch": "main"}, 201)
        state["来源仓库"] = {k: repo[k] for k in ("id", "full_name", "html_url")}
        save()
        content = "name: 一期设置边界\non:\n  workflow_dispatch:\njobs:\n  check:\n    runs-on: phase1-settings-no-runner\n    steps:\n      - run: echo SETTINGS_SCOPE_OK\n"
        result = api("/repos/" + repo["full_name"] + "/contents/.gitea/scoped_workflows/check.yml", "POST", {"content": base64.b64encode(content.encode()).decode(), "branch": "main", "message": "test(actions): 一期旧设置页面夹具"}, 201)
        state["来源提交"] = result["commit"]["sha"]
        state["页面"] = args.base + "/org/" + group["compatibility_name"] + "/settings/actions/scoped-workflows"
        save()
    if args.stage in ("prepare", "grant", "revoke"):
        group = api("/governance/groups/" + str(state["群组"]["id"]))
        path = "/governance/groups/" + str(group["id"]) + "/members/" + str(state["主体ID"])
        if args.stage == "revoke":
            api(path + "?revision=" + str(group["revision"]), "DELETE", expected=204)
        else:
            api(path, "PUT", {"revision": group["revision"], "role": 50}, 204)
    if args.stage == "enable-pr":
        path = "/repos/" + state["来源仓库"]["full_name"] + "/contents/.gitea/scoped_workflows/check.yml"
        previous = api(path)
        content = base64.b64decode(previous["content"]).decode().replace("  workflow_dispatch:", "  pull_request:")
        result = api(path, "PUT", {"sha": previous["sha"], "content": base64.b64encode(content.encode()).decode(), "branch": "main", "message": "test(actions): 配置可产生PR状态的设置正例"})
        state["PR来源提交"] = result["commit"]["sha"]
    if args.stage == "prepare-move":
        if "移动父组" in state:
            raise RuntimeError("移动夹具已建立，不覆盖")
        parents = []
        for suffix in ("a", "b"):
            parent = api("/governance/groups", "POST", {"name": "一期设置移动父组" + suffix, "path": "phase1-settings-parent-" + suffix + "-" + str(time.time_ns()), "visibility": 2}, 201)
            parents.append(parent)
        state["移动父组"] = parents
        save()
        api("/governance/groups/" + str(parents[0]["id"]) + "/members/" + str(state["主体ID"]), "PUT", {"revision": parents[0]["revision"], "role": 50}, 204)
    if args.stage in ("prepare-move", "resume-move", "move-away"):
        group = api("/governance/groups/" + str(state["群组"]["id"]))
        parent = state["移动父组"][1 if args.stage == "move-away" else 0]
        if args.stage == "resume-move" and (group["parent_id"] != 0 or state.get("移动结果")):
            raise RuntimeError("只允许恢复尚未移动的已建夹具")
        moved = api("/governance/groups/" + str(group["id"]) + "/move", "POST", {"parent_id": parent["id"], "path": group["path"], "revision": group["revision"]})
        state.setdefault("移动结果", []).append({"阶段": args.stage, "源路径": group["full_path"], "目标路径": moved["full_path"], "父组": parent["id"]})
        state["群组"] = moved
        state["页面"] = args.base + "/org/" + moved["compatibility_name"] + "/settings/actions/scoped-workflows"
        save()
        if args.stage in ("prepare-move", "resume-move"):
            extra = api("/orgs/" + moved["compatibility_name"] + "/repos", "POST", {"name": "other-source", "private": True, "auto_init": True}, 201)
            state["其他来源ID"] = extra["id"]
            save()
    current = api("/governance/groups/" + str(state["群组"]["id"]))
    state["最新修订"] = current["revision"]
    state["阶段"] = args.stage
    save()
    print(json.dumps({"阶段": args.stage, "群组ID": current["id"], "页面": state["页面"]}, ensure_ascii=False))


if __name__ == "__main__":
    main()
