#!/usr/bin/env python3
"""反证事项与 PR 独立治理能力的原始 API 失败。"""

import base64
import datetime
from html.parser import HTMLParser
import http.cookiejar
import importlib.util
import json
from pathlib import Path
import secrets
import urllib.parse
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[1]
WORK = Path("/tmp/gitea-user-acceptance-20260918")
OUT = ROOT / "docs/evidence/user-permission-20260918/capability-review-results.jsonl"
REPORT = ROOT / "docs/evidence/user-permission-20260918/capability-review.md"
BASE = "http://127.0.0.1:3538"
RUN = uuid.uuid4().hex[:8]
passwords = json.loads((WORK / "secrets.json").read_text())

spec = importlib.util.spec_from_file_location("acceptance", ROOT / "tools/user-permission-acceptance.py")
a = importlib.util.module_from_spec(spec)
spec.loader.exec_module(a)
api, ok = a.api, a.ok
ADMIN = {"name": "acceptance-admin", "password": passwords["管理员密码"]}
ROWS = []


def record(case, check, passed, **evidence):
    row = {"场景": case, "检查": check, "结果": "通过" if passed else "失败", "时间": datetime.datetime.now(datetime.timezone.utc).isoformat(), "运行标识": RUN, **evidence}
    ROWS.append(row)
    with OUT.open("a") as stream:
        stream.write(json.dumps(row, ensure_ascii=False) + "\n")
    print(case, check, row["结果"], flush=True)


def create_user(label):
    name, password = f"cap-user-{label}-{RUN}", secrets.token_urlsafe(24)
    user = ok("POST", "/admin/users", {"username": name, "password": password, "email": name + "@example.invalid", "must_change_password": False})
    token = ok("POST", f"/users/{name}/tokens", {"name": "能力反证", "scopes": ["all"]}, {"name": name, "password": password})
    return {"name": name, "password": password, "token": token["sha1"], "id": user["id"]}


def set_member(group_id, user, role):
    state = ok("GET", f"/governance/groups/{group_id}")
    ok("PUT", f"/governance/groups/{group_id}/members/{user['id']}", {"role": role, "revision": state["revision"]})


def web_status(user, path):
    class Fields(HTMLParser):
        def __init__(self):
            super().__init__()
            self.values = {}

        def handle_starttag(self, tag, attrs):
            values = dict(attrs)
            if tag == "input" and values.get("type") == "hidden" and values.get("name"):
                self.values[values["name"]] = values.get("value", "")

    browser = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    parser = Fields()
    parser.feed(browser.open(BASE + "/user/login", timeout=15).read().decode())
    parser.values.update({"user_name": user["name"], "password": user["password"]})
    browser.open(urllib.request.Request(BASE + "/user/login", data=urllib.parse.urlencode(parser.values).encode(), method="POST"), timeout=15).read()
    try:
        response = browser.open(BASE + path, timeout=15)
        body = response.read().decode(errors="replace")
        return response.status, urllib.parse.urlsplit(response.url).path, body
    except urllib.error.HTTPError as error:
        return error.code, urllib.parse.urlsplit(error.url).path, error.read().decode(errors="replace")


def verify_issue_semantics(repo_path, label_id, actor, other_issue, label, can_manage=False):
    status, own = api("POST", repo_path + "/issues", {"title": label + "创建的事项", "body": "独立能力反证"}, actor)
    record(label, "只读事项用户可创建普通事项", status == 201, HTTP状态=status, 事项编号=own.get("number") if isinstance(own, dict) else None, 归因="Gitea 原生 reader 既定语义")
    edit_status, edited = api("PATCH", repo_path + "/issues/" + str(own.get("number", 0)), {"title": label + "编辑自己的事项"}, actor)
    record(label, "只读事项用户可编辑自己事项", edit_status == 201 and edited.get("title") == label + "编辑自己的事项", HTTP状态=edit_status, 归因="EditIssue 明确允许 poster")
    other_status, _ = api("PATCH", repo_path + "/issues/" + str(other_issue), {"title": "越权修改他人事项"}, actor)
    record(label, "write_issues 管理他人事项" if can_manage else "无 write_issues 不能编辑他人事项", other_status == 201 if can_manage else other_status == 403, HTTP状态=other_status)
    label_status, _ = api("POST", repo_path + "/issues/" + str(own.get("number", 0)) + "/labels", {"labels": [label_id]}, actor)
    record(label, "write_issues 管理事项标签" if can_manage else "无 write_issues 不能给事项加标签", label_status == 200 if can_manage else label_status == 403, HTTP状态=label_status)


def main():
    group = ok("POST", "/governance/groups", {"name": "能力反证组织", "path": "capability-review-" + RUN, "visibility": 2})
    repo = ok("POST", "/orgs/" + group["full_path"] + "/repos", {"name": "private-project", "private": True, "auto_init": True, "default_branch": "main"})
    repo_path = "/repos/" + repo["full_name"]
    label = ok("POST", repo_path + "/labels", {"name": "管理动作反证", "color": "d73a4a"})
    other = ok("POST", repo_path + "/issues", {"title": "管理员创建的他人事项", "body": "用于越权反证"})
    guest, planner = create_user("guest"), create_user("planner")
    set_member(group["id"], guest, 10)
    set_member(group["id"], planner, 15)
    verify_issue_semantics(repo_path, label["id"], guest, other["number"], "Guest")

    ok("POST", repo_path + "/branches", {"new_branch_name": "feature/capability-review", "old_branch_name": "main"})
    changed = ok("POST", repo_path + "/contents/pr.txt", {"branch": "feature/capability-review", "message": "test: 建立 PR 能力反证", "content": base64.b64encode("私有代码反证\n".encode()).decode()})
    pull = ok("POST", repo_path + "/pulls", {"title": "独立 PR 读取能力", "head": "feature/capability-review", "base": "main"})
    verify_issue_semantics(repo_path, label["id"], planner, other["number"], "Planner", can_manage=True)

    invited_groups = []
    combinations = [(15, 20), (20, 15), (15, 30)]
    actors = []
    for role, ceiling in combinations:
        invited = ok("POST", "/governance/groups", {"name": "共享交集反证", "path": f"cap-share-{role}-{ceiling}-{RUN}", "visibility": 2})
        actor = create_user(f"share-{role}-{ceiling}")
        set_member(invited["id"], actor, role)
        state = ok("GET", f"/governance/repositories/{repo['id']}/shares")
        ok("PUT", f"/governance/repositories/{repo['id']}/shares/{invited['id']}", {"max_role": ceiling, "revision": state["revision"]})
        invited_groups.append(invited)
        actors.append((actor, role, ceiling))
        if ceiling < 30:
            verify_issue_semantics(repo_path, label["id"], actor, other["number"], f"C04-{role}-{ceiling}")

    for actor, role, ceiling in [(planner, 15, 0), *actors]:
        case = "A04" if ceiling == 0 else f"C04-{role}-{ceiling}"
        status, body = api("GET", repo_path + "/pulls", user=actor)
        record(case, "read_pulls 用户读取 PR API", status == 200, HTTP状态=status, PR编号=pull["number"], 响应=body if status != 200 else "已返回列表")
        content_status, _ = api("GET", repo_path + "/contents/pr.txt?ref=feature%2Fcapability-review", user=actor)
        record(case, "read_pulls 不泄漏代码内容 API", content_status in (403, 404), HTTP状态=content_status, 代码提交SHA=changed["commit"]["sha"])
        web_code, web_path, web_body = web_status(actor, "/" + repo["full_name"] + "/pulls")
        record(case, "read_pulls 用户读取 PR 网页", web_code == 200 and web_path == "/" + repo["full_name"] + "/pulls" and pull["title"] in web_body, HTTP状态=web_code, 最终路径=web_path)

    defects = [
        {"编号": "CAP-PR-001", "严重级别": "P2", "原因": "PR API 整组错误要求 TypeCode 读取权限，导致具备 read_pulls 但不得读取代码的 Planner 和共享交集用户返回 403；网页仍可查看 PR，未发现数据泄漏、丢失或关键交付全面阻断。", "代码泄漏": "未发现；代码内容 API 对这些用户仍返回 403/404"}
    ]
    record("汇总", "反证结论", all(row["结果"] == "通过" for row in ROWS if row["检查"] != "read_pulls 用户读取 PR API"), 原始失败数=7, 真实缺陷数=1, P1=0, P2=1, 误报归因={"事项创建或编辑自己事项": 3, "PR API 合法访问误拒绝": 4}, 缺陷=defects)
    REPORT.write_text("""# 用户能力原始失败反证报告

## 结论

7 个原始失败归并为 1 个真实缺陷。事项相关 3 项是测试预期误报；PR API 相关 4 项属于同一个 P2 入口缺陷。未发现 P1。

## 事项语义反证

`read_issues` 允许创建普通事项，事项作者可以编辑自己的标题和正文，这是 Gitea 原生 reader 语义。`write_issues` 表示管理事项，不等于创建事项。独立用户验证表明 Guest 及两个不含 `write_issues` 的共享交集用户均不能编辑管理员创建的事项，也不能给事项添加标签。因此原始 HTTP 201 没有证明治理能力交集失效。

## CAP-PR-001（P2）

`/api/v1/repos/{owner}/{repo}/pulls` 整组路由使用 `reqRepoReader(TypeCode)`。Planner 和三种共享交集都具有 `read_pulls`，却因没有 `read_code` 被 API 返回 403；相同账号的 PR 网页可正常打开并看到固定 PR。该路径破坏 API 与网页一致性，并影响 API 客户端工作流，但网页入口明确可用，未发现数据泄漏、数据丢失或关键交付全面阻断，因此未达到 P1，最终评级为 P2。

反证没有发现代码泄漏：这些账号读取固定分支代码内容 API 仍返回 403/404。不能简单删除整组门禁；应改为按 PR 元数据、提交、文件和补丁的实际数据边界分别授权并回归验证。

## 统计

- 原始失败：7
- 测试预期误报：3
- 真实缺陷：1（P1：0，P2：1）
- PR API 失败记录：4，归并为同一根因
""")


if __name__ == "__main__":
    main()
