#!/usr/bin/env python3
"""在授权的隔离实例建立审批和必选工作流专用样本。"""

import argparse
import base64
import hashlib
import json
import pathlib
import time
import urllib.error
import urllib.parse
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("prepare", "open-pr", "inspect"))
    parser.add_argument("--base", required=True)
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    parser.add_argument("--credentials", required=True, type=pathlib.Path)
    parser.add_argument("--state", required=True, type=pathlib.Path)
    parser.add_argument("--parent-id", required=True, type=int)
    parser.add_argument("--owner", required=True)
    parser.add_argument("--reviewer", required=True)
    parser.add_argument("--runner-label", default="phase1-v15")
    parser.add_argument("--source-commit", required=True)
    args = parser.parse_args()
    base = args.base.rstrip("/")
    if urllib.parse.urlsplit(base).scheme not in ("http", "https"):
        parser.error("实例地址必须为 HTTP(S)")
    credentials = json.loads(args.credentials.read_text())
    script_hash = hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()
    state = json.loads(args.state.read_text()) if args.state.exists() else {
        "实例": base, "父组ID": args.parent_id, "普通Owner": args.owner,
        "审查人": args.reviewer, "Runner标签": args.runner_label,
        "源码提交": args.source_commit, "脚本SHA256": script_hash, "步骤": [],
    }
    for key, expected in (("实例", base), ("父组ID", args.parent_id),
                          ("普通Owner", args.owner), ("审查人", args.reviewer),
                          ("Runner标签", args.runner_label), ("源码提交", args.source_commit),
                          ("脚本SHA256", script_hash)):
        if state.get(key) != expected:
            parser.error("已有样本的" + key + "与本次参数不一致")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def save():
        args.state.parent.mkdir(parents=True, exist_ok=True)
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def request(path, method="GET", body=None, user=args.owner, expected=(200,)):
        password = credentials.get(user, credentials.get("password"))
        if not isinstance(password, str):
            raise ValueError("私有凭据文件缺少账号密码")
        authorization = base64.b64encode((user + ":" + password).encode()).decode()
        payload = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(base + path, method=method, data=payload, headers={
            "Authorization": "Basic " + authorization,
            "Content-Type": "application/json", "Accept": "application/json",
        })
        try:
            with opener.open(req, timeout=30) as response:
                code, raw = response.status, response.read()
        except urllib.error.HTTPError as error:
            code, raw = error.code, error.read()
        state["步骤"].append({"时间": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                          "请求": method + " " + path, "实际": code,
                          "预期": list(expected), "通过": code in expected})
        save()
        if code not in expected:
            raise AssertionError(f"{method} {path} 返回 {code}，预期 {expected}；已停在首错")
        return json.loads(raw) if raw else None

    def file(repo, path, content, branch):
        return request("/api/v1/repos/" + repo + "/contents/" + path, "POST", {
            "content": base64.b64encode(content.encode()).decode(),
            "branch": branch, "message": "test(governance): 建立独立审批门禁样本",
        }, expected=(201,))["commit"]["sha"]

    if args.mode == "prepare":
        if "群组ID" in state:
            parser.error("样本已建立；请使用 inspect 或 open-pr")
        parent = request("/api/v1/governance/groups/" + str(args.parent_id))
        if parent["id"] != args.parent_id:
            raise AssertionError("父组 ID 与读取结果不一致")
        owner = request("/api/v1/user")
        reviewer = request("/api/v1/user", user=args.reviewer)
        if owner["is_admin"] or reviewer["is_admin"]:
            raise AssertionError("验收必须使用普通账号")
        state["身份ID"] = {"Owner": owner["id"], "审查人": reviewer["id"]}
        suffix = str(int(time.time()))
        group = request("/api/v1/governance/groups", "POST", {
            "name": "一期审批与必选工作流专用子组",
            "path": "phase1-pr-gate-" + suffix,
            "parent_id": args.parent_id, "visibility": 2,
        }, expected=(201,))
        state["群组ID"] = group["id"]
        state["群组兼容名"] = group["compatibility_name"]
        state["群组全路径"] = group["full_path"]
        save()
        repos = {}
        for key, name in (("来源", "required-source"), ("消费", "approval-consumer")):
            repo = request("/api/v1/orgs/" + state["群组兼容名"] + "/repos", "POST", {
                "name": name, "private": True, "auto_init": True,
                "default_branch": "main", "description": "一期审批与必选工作流隔离样本",
            }, expected=(201,))
            repos[key] = {"ID": repo["id"], "名称": name, "页面": repo["html_url"],
                          "默认分支": repo["default_branch"]}
            state["仓库"] = repos
            save()
        source = state["群组兼容名"] + "/" + repos["来源"]["名称"]
        workflow = ("name: 一期必选工作流\n"
                    "on:\n  pull_request:\n"
                    "jobs:\n  required-check:\n    runs-on: " + args.runner_label + "\n"
                    "    steps:\n      - run: echo P03_REQUIRED_OK\n")
        state["来源提交"] = file(source, ".gitea/scoped_workflows/required.yml",
                                 workflow, repos["来源"]["默认分支"])
        group = request("/api/v1/governance/groups/" + str(state["群组ID"]))
        request(f"/api/v1/governance/groups/{state['群组ID']}/members/{reviewer['id']}",
                "PUT", {"role": 30, "revision": group["revision"]}, expected=(204,))
        consumer_id = repos["消费"]["ID"]
        rule = request(f"/api/v1/governance/repositories/{consumer_id}/approval-rules", "POST", {
            "rule": {"name": "专人一票审批", "required": 1,
                     "user_ids": [reviewer["id"]], "branch_mode": "all", "enabled": True},
        }, expected=(201,))
        state["审批规则ID"] = rule["id"]
        consumer = state["群组兼容名"] + "/" + repos["消费"]["名称"]
        request("/api/v1/repos/" + consumer + "/branches", "POST", {
            "new_branch_name": "review-change", "old_ref_name": repos["消费"]["默认分支"],
        }, expected=(201,))
        state["PR分支提交"] = file(consumer, "review.txt", "一期审批与必选来源样本\n", "review-change")
        save()
        print("样本已准备；先在子组 UI 登记来源并设必选，再执行 open-pr。")

    elif args.mode == "open-pr":
        if "群组ID" not in state or "PR编号" in state:
            parser.error("先 prepare，且同一样本只能创建一次 PR")
        group = state["群组兼容名"]
        source_id = state["仓库"]["来源"]["ID"]
        # 必须由操作员先在原生页面设置必选；只读检查阻止提前创建 PR。
        page = request("/api/v1/repos/" + group + "/" + state["仓库"]["来源"]["名称"])
        if page["id"] != source_id or page["archived"]:
            raise AssertionError("来源仓库身份或状态不符合预期")
        consumer = group + "/" + state["仓库"]["消费"]["名称"]
        pr = request("/api/v1/repos/" + consumer + "/pulls", "POST", {
            "head": "review-change", "base": state["仓库"]["消费"]["默认分支"],
            "title": "一期审批与必选工作流联合验收",
            "body": "仅使用本专用子组。审批资格、来源修订和自动合并由 UI 验收。",
        }, expected=(201,))
        state["PR编号"] = pr["number"]
        state["PR页面"] = pr["html_url"]
        save()
        print("PR 已创建：" + pr["html_url"])

    else:
        if "群组ID" not in state:
            parser.error("先 prepare")
        group = request("/api/v1/governance/groups/" + str(state["群组ID"]))
        if group["parent_id"] != args.parent_id:
            raise AssertionError("样本已移出专用父组")
        for repo in state["仓库"].values():
            current = request("/api/v1/repos/" + state["群组兼容名"] + "/" + repo["名称"])
            if current["id"] != repo["ID"]:
                raise AssertionError("仓库身份发生变化")
        print("独立群组和仓库身份仍符合准备记录。")


if __name__ == "__main__":
    main()
