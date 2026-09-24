#!/usr/bin/env python3
"""同名祖先标签的隔离夹具与实际接口核对，标签选择和转移保留浏览器操作。"""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import time

from capacity_probe import API, write_json


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("mode", choices=("prepare", "selected", "history", "transferred", "revoke"))
    p.add_argument("--base", required=True)
    p.add_argument("--credentials", type=Path, required=True)
    p.add_argument("--state", type=Path, required=True)
    p.add_argument("--owner", required=True)
    p.add_argument("--child-owner", required=True)
    p.add_argument("--source", required=True)
    p.add_argument("--isolated-instance", required=True, action="store_true")
    a = p.parse_args()
    passwords = json.loads(a.credentials.read_text())
    api = API(a.base, a.owner, passwords.get(a.owner, passwords.get("password")))
    state = json.loads(a.state.read_text()) if a.state.exists() else {
        "实例": a.base, "源码": a.source, "步骤": [],
        "脚本SHA256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    assert state["实例"] == a.base and state["源码"] == a.source

    def check(method, path, expected=200, data=None, user=a.owner):
        actor = (user, passwords.get(user, passwords.get("password")))
        code, body, _, _ = api.call(method, path, data, actor)
        state["步骤"].append({"操作": method + " " + path, "身份": user, "HTTP": code, "预期": expected})
        write_json(a.state, state)
        assert code == expected, f"{method} {path} HTTP {code}，预期{expected}"
        return body

    if a.mode == "prepare":
        assert not a.state.exists(), "创建夹具需使用全新状态文件"
        assert not check("GET", "/user")["is_admin"]
        groups = []
        root_path = "phase1-labels-" + str(time.time_ns())
        for slug, parent in ((root_path, 0), ("child", 0), ("destination", 1), (root_path + "-outside", -1)):
            parent_id = groups[parent]["id"] if groups and parent >= 0 else 0
            groups.append(check("POST", "/governance/groups", 201,
                                {"path": slug, "name": "同名标签验收 " + slug, "parent_id": parent_id, "visibility": 2}))
        state["群组"] = groups
        user = check("GET", "/users/" + a.child_owner)
        scope = f"/governance/groups/{groups[1]['id']}/members"
        version = check("GET", f"/governance/groups/{groups[1]['id']}")["revision"]
        check("PUT", scope + "/" + str(user["id"]), 204, {"role": 50, "revision": version})
        org = groups[1]["compatibility_name"]
        repo = check("POST", "/orgs/" + org + "/repos", 201,
                     {"name": "label-target", "private": True, "auto_init": True, "default_branch": "main"})
        state["仓库"] = {k: repo[k] for k in ("id", "full_name", "html_url")}
        labels = []
        paths = ["/orgs/" + groups[i]["compatibility_name"] + "/labels" for i in (0, 1)]
        paths += ["/repos/" + repo["full_name"] + "/labels", "/orgs/" + groups[3]["compatibility_name"] + "/labels"]
        for path, desc, color in zip(paths, ("父群组来源", "子群组来源", "仓库来源", "外部来源"), ("112233", "445566", "778899", "AABBCC")):
            labels.append(check("POST", path, 201, {"name": "same-name", "description": desc, "color": color}))
        state["标签"] = labels
        issue = check("POST", "/repos/" + repo["full_name"] + "/issues", 201,
                      {"title": "同名来源标签与历史验收", "body": "普通用户按独立ID选择三个同名标签。"})
        state["Issue"] = issue["number"]
        check("POST", "/repos/" + repo["full_name"] + "/branches", 201, {"new_branch_name": "label-pr", "old_branch_name": "main"})
        check("POST", "/repos/" + repo["full_name"] + "/contents/label-probe.txt", 201,
              {"branch": "label-pr", "message": "test(labels): 标签历史样本", "content": base64.b64encode("标签验收样本\n".encode()).decode()})
        pull = check("POST", "/repos/" + repo["full_name"] + "/pulls", 201,
                     {"title": "拉取请求同名标签历史", "head": "label-pr", "base": "main"})
        state["PR"] = pull["number"]
        check("PUT", "/repos/" + repo["full_name"] + f"/issues/{pull['number']}/labels", 200,
              {"labels": [label["id"] for label in labels[:3]]})
        write_json(a.state, state)
        print("夹具完成；请在Issue页面选择三个同名标签：" + repo["html_url"] + "/issues/" + str(issue["number"]))
        return

    groups, labels, repo = state["群组"], state["标签"], state["仓库"]
    if a.mode == "revoke":
        user = check("GET", "/users/" + a.child_owner)
        group = check("GET", f"/governance/groups/{groups[1]['id']}")
        check("DELETE", f"/governance/groups/{group['id']}/members/{user['id']}?revision={group['revision']}", 204)
        for name in (repo["full_name"], groups[2]["compatibility_name"] + "/label-target"):
            check("GET", "/repos/" + name + "/labels", 404, user=a.child_owner)
            check("GET", "/repos/" + name + f"/issues/{state['Issue']}", 404, user=a.child_owner)
        state["阶段"] = a.mode
        write_json(a.state, state)
        print("撤权后新旧路径均拒绝标签与Issue读取")
        return
    current = groups[2]["compatibility_name"] + "/label-target" if a.mode == "transferred" else repo["full_name"]
    ids = {label["id"] for label in labels[:3]}
    available = check("GET", "/repos/" + current + "/labels", user=a.child_owner)
    assert ids.issubset({label["id"] for label in available})
    for number in (state["Issue"], state["PR"]):
        if a.mode == "history":
            check("DELETE", "/repos/" + current + f"/issues/{number}/labels/{labels[0]['id']}", 204)
        applied = check("GET", "/repos/" + current + f"/issues/{number}/labels")
        expected_ids = ids if a.mode == "selected" else ids - {labels[0]["id"]}
        assert {label["id"] for label in applied} == expected_ids
        for label_id in expected_ids:
            found = check("GET", "/repos/" + current + "/issues?state=all&labels=" + str(label_id))
            assert number in [row["number"] for row in found]
    check("PATCH", "/orgs/" + groups[0]["compatibility_name"] + f"/labels/{labels[0]['id']}", 404,
          {"name": "不允许修改来源"}, user=a.child_owner)
    assert check("GET", "/orgs/" + groups[0]["compatibility_name"] + f"/labels/{labels[0]['id']}")["name"] == "same-name"
    check("POST", "/repos/" + current + f"/issues/{state['Issue']}/labels", 404,
          {"labels": [labels[3]["id"]]})
    after_rejected = check("GET", "/repos/" + current + f"/issues/{state['Issue']}/labels")
    assert {label["id"] for label in after_rejected} == expected_ids
    current_repo = check("GET", "/repos/" + current)
    assert current_repo["id"] == repo["id"]
    if a.mode == "transferred":
        old = check("GET", "/repos/" + repo["full_name"])
        assert old["id"] == repo["id"]
    state["阶段"] = a.mode
    write_json(a.state, state)
    print("已核对阶段：" + a.mode)


if __name__ == "__main__":
    main()
