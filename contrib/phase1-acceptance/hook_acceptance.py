#!/usr/bin/env python3
"""一期 H01/H02/H03 独立 Hook 夹具与真实事件入口。"""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import time

from capacity_probe import API, write_json


EVENTS = ["push", "issues", "issue_comment", "pull_request", "pull_request_comment",
          "workflow_run", "workflow_job"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=("prepare", "restore-events", "events", "disable", "verify"))
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", required=True, type=Path)
    parser.add_argument("--work", required=True, type=Path)
    parser.add_argument("--owner", required=True)
    parser.add_argument("--child-owner", required=True)
    parser.add_argument("--receiver-port", type=int, required=True)
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    args = parser.parse_args()
    args.work.mkdir(parents=True, exist_ok=True)
    os.chmod(args.work, 0o700)
    state_path = args.work / "state.json"
    credentials = json.loads(args.credentials.read_text())
    password = credentials["password"]
    api = API(args.base, args.owner, password)
    state = json.loads(state_path.read_text()) if state_path.exists() else {
        "实例": args.base, "根Owner": args.owner, "子Owner": args.child_owner,
        "接收端端口": args.receiver_port, "步骤": [],
    }
    if (state["实例"], state["根Owner"], state["子Owner"], state["接收端端口"]) != (
            args.base, args.owner, args.child_owner, args.receiver_port):
        raise SystemExit("状态文件与固定输入不符")

    def save():
        write_json(state_path, state)

    def check(label, method, path, expected, body=None, actor=args.owner):
        status, value, size, elapsed = api.call(method, path, body, (actor, password))
        item = {"检查": label, "身份": actor, "方法": method, "路径": path,
                "状态": status, "期望": sorted(expected) if isinstance(expected, set) else expected,
                "响应字节": size, "毫秒": round(elapsed, 1)}
        state["步骤"].append(item)
        save()
        if status not in (expected if isinstance(expected, set) else {expected}):
            raise RuntimeError(f"{label}：HTTP {status}；响应体不写入记录")
        return value

    if args.phase == "prepare":
        if state_path.exists():
            raise SystemExit("准备阶段需要全新工作目录")
        save()
        me = check("根Owner身份", "GET", "/user", 200)
        other = check("子Owner身份", "GET", "/user", 200, actor=args.child_owner)
        if me["is_admin"] or other["is_admin"]:
            raise RuntimeError("本夹具要求两个普通账号")
        slug = "phase1-hook-" + str(time.time_ns())
        root = check("建立隔离根群组", "POST", "/governance/groups", 201,
                     {"path": slug, "name": "一期 Hook 隔离验收", "visibility": 2})
        child = check("建立隔离子群组", "POST", "/governance/groups", 201,
                      {"path": "child", "name": "一期 Hook 子来源", "parent_id": root["id"], "visibility": 2})
        repo = check("建立隔离仓库", "POST", "/orgs/" + child["compatibility_name"] + "/repos", 201,
                     {"name": "hook-target", "private": True, "auto_init": True, "default_branch": "main"})
        state.update({"根群组": root, "子群组": child,
                      "仓库": {"id": repo["id"], "full_name": repo["full_name"], "default_branch": repo["default_branch"]}})
        save()
        for label in ("root", "child"):
            key = secrets.token_hex(24)
            path = args.work / ("signing-key-" + label)
            path.write_text(key)
            os.chmod(path, 0o600)
        (args.work / "response-status").write_text("200\n")
        os.chmod(args.work / "response-status", 0o600)
        hook_url = f"http://127.0.0.1:{args.receiver_port}/phase1-hook"
        root_path = "/orgs/" + root["compatibility_name"] + "/hooks"
        child_path = "/orgs/" + child["compatibility_name"] + "/hooks"
        check("无权人拒建根Hook", "POST", root_path, {403, 404},
              {"type": "gitea", "config": {"url": hook_url, "content_type": "json"}, "active": True},
              actor=args.child_owner)
        hooks = {}
        for label, path in (("root", root_path), ("child", child_path)):
            key = (args.work / ("signing-key-" + label)).read_text()
            hook = check("建立" + label + " Hook", "POST", path, 201, {
                "type": "gitea", "name": "一期隔离" + label,
                "config": {"url": hook_url, "content_type": "json", "secret": key},
                "events": EVENTS, "active": True,
            })
            hooks[label] = hook["id"]
        state["Hook编号"] = hooks
        save()
        check("无权人拒读根Hook", "GET", root_path + "/" + str(hooks["root"]), {403, 404}, actor=args.child_owner)
        check("无权人拒改子Hook", "PATCH", child_path + "/" + str(hooks["child"]),
              {403, 404}, {"name": "越权改名"}, actor=args.child_owner)
        current = check("读取子群组修订", "GET", "/governance/groups/" + str(child["id"]), 200)
        check("授予子群组Owner", "PUT", f"/governance/groups/{child['id']}/members/{other['id']}", 204,
              {"role": 50, "revision": current["revision"]})
        check("子Owner拒改祖先Hook", "PATCH", root_path + "/" + str(hooks["root"]),
              {403, 404}, {"name": "越权祖先改名"}, actor=args.child_owner)
        check("子Owner拒删祖先Hook", "DELETE", root_path + "/" + str(hooks["root"]),
              {403, 404}, actor=args.child_owner)
        check("子Owner修改本级Hook", "PATCH", child_path + "/" + str(hooks["child"]),
              200, {"name": "一期隔离子来源已修改"}, actor=args.child_owner)
        check("根Owner修改本级Hook", "PATCH", root_path + "/" + str(hooks["root"]),
              200, {"name": "一期隔离根来源已修改"})
        for label, path in (("root", root_path), ("child", child_path)):
            hook = check("回读" + label + " Hook", "GET", path + "/" + str(hooks[label]), 200)
            if hook["id"] != hooks[label] or hook["config"].get("url") != hook_url:
                raise RuntimeError("Hook 修改后来源或端点不一致")
        print("准备完成：" + repo["full_name"])

    elif args.phase == "restore-events":
        for label, group, actor in (("root", state["根群组"], args.owner),
                                    ("child", state["子群组"], args.child_owner)):
            path = f"/orgs/{group['compatibility_name']}/hooks/{state['Hook编号'][label]}"
            check("显式恢复" + label + "事件目录", "PATCH", path, 200,
                  {"events": EVENTS, "name": "一期隔离" + label + "事件已恢复"}, actor=actor)
            hook = check("回读" + label + "事件目录", "GET", path, 200, actor=actor)
            if not set(EVENTS).issubset(set(hook["events"])):
                raise RuntimeError(label + " Hook 事件订阅未恢复")
        print("自建 Hook 的事件目录已显式恢复；首错记录保留")

    elif args.phase == "events":
        repo = state["仓库"]["full_name"]
        path = "/repos/" + repo
        branch = state["仓库"]["default_branch"]
        marker = str(time.time_ns())
        check("实际push事件", "PUT", path + "/contents/hook-push-" + marker + ".txt", 201,
              {"branch": branch, "message": "一期 Hook push 事件", "content": base64.b64encode(marker.encode()).decode()})
        issue = check("实际Issue事件", "POST", path + "/issues", 201,
                      {"title": "一期 Hook Issue " + marker, "body": "隔离事件"})
        check("实际评论事件", "POST", path + f"/issues/{issue['number']}/comments", 201,
              {"body": "一期 Hook 评论 " + marker})
        feature = "hook-pr-" + marker
        check("建立PR分支", "POST", path + "/branches", 201,
              {"new_branch_name": feature, "old_branch_name": branch})
        check("写入PR分支", "PUT", path + "/contents/hook-pr-" + marker + ".txt", 201,
              {"branch": feature, "message": "一期 Hook PR 事件", "content": base64.b64encode(marker.encode()).decode()})
        pr = check("实际PR事件", "POST", path + "/pulls", 201,
                   {"head": feature, "base": branch, "title": "一期 Hook PR " + marker, "body": "隔离事件"})
        check("实际PR评论事件", "POST", path + f"/issues/{pr['number']}/comments", 201,
              {"body": "一期 Hook PR 评论 " + marker})
        state["事件编号"] = {"Issue": issue["number"], "PR": pr["number"], "标记": marker}
        save()
        print("日常事件已触发，请执行 verify 核对真实接收")

    elif args.phase == "disable":
        root = state["根群组"]["compatibility_name"]
        path = f"/orgs/{root}/hooks/{state['Hook编号']['root']}"
        check("停用隔离根Hook", "PATCH", path, 200, {"active": False})
        check("停用后回读", "GET", path, 200)
        print("隔离根 Hook 已停用")

    else:
        receipts = args.work / "receipts.jsonl"
        if not receipts.exists():
            raise RuntimeError("尚无真实接收记录")
        rows = [json.loads(line) for line in receipts.read_text().splitlines()]
        wanted = {"push", "issues", "issue_comment", "pull_request"}
        by_event = {event: [row for row in rows if row["事件"] == event and row["仓库编号"] == state["仓库"]["id"]]
                    for event in wanted}
        both = {k: {"signing-key-root", "signing-key-child"}.issubset(
            {name for row in values for name in row["签名密钥标签"]}) for k, values in by_event.items()}
        result = {"仓库": state["仓库"], "Hook编号": state["Hook编号"],
                  "事件数": {k: len(v) for k, v in by_event.items()},
                  "有效双来源": both, "投递总数": len(rows)}
        write_json(args.work / "summary.json", result)
        if not all(result["有效双来源"].values()):
            raise RuntimeError("事件缺少两个有效签名来源；见私有 summary.json")
        print("push、Issue、评论、PR 的双来源签名接收均通过")


if __name__ == "__main__":
    main()
