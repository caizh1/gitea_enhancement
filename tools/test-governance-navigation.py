#!/usr/bin/env python3
"""仅在指定的本机隔离实例创建导航样本并验证权限，凭据不进入报告。"""

import base64
import json
import secrets
import urllib.parse
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WORK = ROOT / "work/navigation-review"
BASE = "http://127.0.0.1:3510"
ADMIN = ("nav-admin", (WORK / "admin-password").read_text().strip())
RESULTS = []


def request(method, path, data=None, auth=ADMIN, expected=200):
    headers = {"Content-Type": "application/json"}
    if auth:
        headers["Authorization"] = "Basic " + base64.b64encode(":".join(auth).encode()).decode()
    req = urllib.request.Request(BASE + "/api/v1" + path, json.dumps(data).encode() if data is not None else None, headers, method=method)
    try:
        response = urllib.request.urlopen(req, timeout=30)
    except urllib.error.HTTPError as error:
        response = error
    body = response.read()
    if response.code != expected:
        raise AssertionError(f"{method} {path}：预期 {expected}，实际 {response.code}，{body[:400].decode(errors='replace')}")
    return json.loads(body) if body else None


def check(name, condition):
    RESULTS.append({"用例": name, "结果": "通过" if condition else "失败"})
    if not condition:
        raise AssertionError(name)


def group(path, name, parent=0):
    return request("POST", "/governance/groups", {"path": path, "name": name, "parent_id": parent, "visibility": 2}, expected=201)


def grant(group_id, user_id, role):
    current = request("GET", f"/governance/groups/{group_id}")
    request("PUT", f"/governance/groups/{group_id}/members/{user_id}", {"role": role, "revision": current["revision"]}, expected=204)


def setup():
    fixture_file = WORK / "fixtures.json"
    if fixture_file.exists():
        return json.loads(fixture_file.read_text())
    root = request("GET", "/orgs/rd")
    storage = group("storage", "存储研发", root["id"])
    firmware = group("firmware", "固件团队", storage["id"])
    validation = group("validation", "验证团队", storage["id"])
    platform = group("platform", "平台研发", root["id"])
    repo = request("POST", "/orgs/rd/storage/firmware/repos", {"name": "firmware-core", "private": True, "auto_init": True}, expected=201)
    request("POST", "/orgs/rd/storage/repos", {"name": "storage-sdk", "private": True, "auto_init": True}, expected=201)
    users = {}
    for name, display in [("nav-inherited", "继承用户"), ("nav-child", "子组用户"), ("nav-repository", "仓库用户")]:
        password = secrets.token_urlsafe(22)
        user = request("POST", "/admin/users", {"username": name, "full_name": display, "password": password, "email": name + "@example.invalid", "must_change_password": False}, expected=201)
        users[name] = {"id": user["id"], "password": password}
    grant(root["id"], users["nav-inherited"]["id"], 20)
    grant(firmware["id"], users["nav-child"]["id"], 30)
    repository_state=request("GET", f'/governance/repositories/{repo["id"]}/members')
    request("PUT", f'/governance/repositories/{repo["id"]}/members/{users["nav-repository"]["id"]}', {"role": 20, "revision":repository_state["revision"]}, expected=204)
    fixture = {"root": root["id"], "storage": storage["id"], "firmware": firmware["id"], "validation": validation["id"], "platform": platform["id"], "repo": repo["id"], "users": users}
    fixture_file.write_text(json.dumps(fixture, ensure_ascii=False, indent=2))
    fixture_file.chmod(0o600)
    return fixture


def run():
    fixture = setup()
    auth = lambda name: (name, fixture["users"][name]["password"])
    base = "/governance/navigation/groups"
    inherited = request("GET", base, auth=auth("nav-inherited"))
    check("首页继承群组不重复平铺", [n["id"] for n in inherited["items"]] == [fixture["root"]])
    child = request("GET", base, auth=auth("nav-child"))
    check("仅子组授权保留独立入口", [n["id"] for n in child["items"] if n["full_path"].startswith("rd/")] == [fixture["firmware"]])
    request("GET", f'{base}/{fixture["root"]}', auth=auth("nav-child"), expected=404)
    request("GET", f'{base}/{fixture["validation"]}', auth=auth("nav-child"), expected=404)
    check("隐藏父组与兄弟组直接访问被拒绝", True)
    detail = request("GET", f'{base}/{fixture["firmware"]}', auth=auth("nav-child"))
    check("不可见祖先没有可点击入口", all(not b["url"] for b in detail["breadcrumbs"][:-1]))
    restricted = request("GET", f'{base}/{fixture["firmware"]}', auth=auth("nav-repository"))
    check("仅仓库授权使用受限导航", restricted["restricted_navigation"] and not restricted["allowed_actions"])
    check("受限导航只返回已授权仓库", [n["id"] for n in restricted["items"]] == [fixture["repo"]])
    request("GET", f'/governance/groups/{fixture["firmware"]}', auth=auth("nav-repository"), expected=404)
    check("受限导航未获得群组权限", True)
    mixed = request("GET", f'{base}/{fixture["storage"]}')
    check("群组在前且仓库仅在直属层显示", [n["type"] for n in mixed["items"]] == ["group", "group", "repository"])
    check("只有仓库的子群组可以展开", next(n for n in mixed["items"] if n["id"] == fixture["firmware"] and n["type"] == "group")["expandable"])
    page = request("GET", f'{base}/{fixture["storage"]}?limit=1')
    cursor = urllib.parse.quote(page["next_cursor"])
    request("GET", f'{base}/{fixture["storage"]}?limit=1&cursor={cursor}', auth=auth("nav-inherited"), expected=400)
    check("分页游标拒绝跨身份使用", True)
    own = request("GET", f'/governance/groups/{fixture["firmware"]}/members?own=true', auth=auth("nav-child"))
    check("普通用户只能读取自己的授权来源", len(own) == 1 and own[0]["username"] == "nav-child")
    request("GET", f'/governance/groups/{fixture["firmware"]}/members', auth=auth("nav-child"), expected=404)
    check("普通用户不能读取完整成员列表", True)
    current = request("GET", f'/governance/groups/{fixture["firmware"]}')
    request("DELETE", f'/governance/groups/{fixture["firmware"]}/members/{fixture["users"]["nav-child"]["id"]}?revision={current["revision"]}', expected=204)
    request("GET", f'{base}/{fixture["firmware"]}', auth=auth("nav-child"), expected=404)
    check("撤权后的新导航请求立即拒绝", True)
    grant(fixture["firmware"], fixture["users"]["nav-child"]["id"], 30)


if __name__ == "__main__":
    try:
        run()
    except Exception as error:
        RESULTS.append({"结果": "失败", "原因": str(error)})
        raise
    finally:
        (WORK / "api-results.json").write_text(json.dumps(RESULTS, ensure_ascii=False, indent=2))
        print(json.dumps(RESULTS, ensure_ascii=False, indent=2))
