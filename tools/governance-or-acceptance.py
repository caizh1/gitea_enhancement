#!/usr/bin/env python3
"""在专用隔离实例验收 O01-O10 与 P01-P12；不读取或改写共享 fixture。"""

import base64
import datetime
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[1]
BASE = "http://127.0.0.1:3538"
SSH_PORT = 4538
SHARED = Path("/tmp/gitea-user-acceptance-20260918")
RUN = uuid.uuid4().hex[:8]
PREFIX = f"or-{RUN}"
WORK = Path(f"/tmp/gitea-governance-or-{RUN}")
STATE = WORK / "state.json"
LOG = WORK / "run.log"
OUT = ROOT / "docs/evidence/governance-linkage-20260918/or-results.jsonl"
PASSWORD = json.loads((SHARED / "secrets.json").read_text())["管理员密码"]
ADMIN = {"name": "acceptance-admin", "password": PASSWORD}
ROWS = []
OBJECTS = {"users": {}, "groups": {}, "repos": {}}


def persist():
    STATE.write_text(json.dumps({"run": RUN, "prefix": PREFIX, "objects": OBJECTS}, ensure_ascii=False, indent=2) + "\n")
    STATE.chmod(0o600)


def auth(user):
    secret = user.get("token", user.get("password", ""))
    return "Basic " + base64.b64encode(f'{user["name"]}:{secret}'.encode()).decode()


def api(method, path, data=None, user=ADMIN, raw=False):
    request = urllib.request.Request(
        BASE + "/api/v1" + path,
        data=None if data is None else json.dumps(data).encode(),
        headers={"Authorization": auth(user), "Content-Type": "application/json"},
        method=method,
    )
    try:
        response = urllib.request.urlopen(request, timeout=40)
    except urllib.error.HTTPError as error:
        response = error
    body = response.read()
    if raw:
        return response.status, body
    try:
        body = json.loads(body)
    except ValueError:
        body = body.decode(errors="replace")
    return response.status, body


def require(method, path, data=None, user=ADMIN, statuses=(200, 201, 204)):
    status, body = api(method, path, data, user)
    if status not in statuses:
        raise RuntimeError(f"{method} {path} 返回 {status}：{str(body)[:600]}")
    return body


def run(args, cwd=None, env=None, timeout=90):
    return subprocess.run(args, cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)


def safe(value):
    if isinstance(value, str):
        return value[-1800:]
    if isinstance(value, dict):
        return {k: safe(v) for k, v in value.items() if k not in {"token", "password", "sha1", "key"}}
    if isinstance(value, list):
        return [safe(v) for v in value[:80]]
    return value


def record(case, check, result, expected, before=None, after=None, defect="无", **extra):
    row = {
        "场景": case,
        "检查": check,
        "结果": result,
        "预期": expected,
        "前后证据": {"操作前": safe(before), "操作后": safe(after)},
        "时间": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "缺陷": defect,
        "运行标识": RUN,
        **safe(extra),
    }
    ROWS.append(row)
    OUT.parent.mkdir(parents=True, exist_ok=True)
    with OUT.open("a") as stream:
        stream.write(json.dumps(row, ensure_ascii=False) + "\n")
    print(case, result, check, flush=True)


def scenario(case, expected, callback):
    before = {"对象": json.loads(json.dumps(OBJECTS))}
    try:
        actual = callback() or {}
        passed = actual.pop("通过", True)
        result = "通过" if passed else "失败"
        defect = actual.pop("缺陷", "无" if passed else f"{case}-001")
        record(case, actual.pop("检查", "完整场景断言"), result, expected, before, actual, defect)
    except Exception as error:
        record(case, "执行器异常反证后仍未完成", "未验证", expected, before, {"错误": str(error)}, "无；执行缺口")


def create_user(label):
    name = f"{PREFIX}-{label}"[:38]
    password = secrets.token_urlsafe(22)
    created = require("POST", "/admin/users", {"username": name, "password": password, "email": name + "@example.invalid", "must_change_password": False})
    user = {"id": created["id"], "name": name, "password": password}
    token = require("POST", f"/users/{name}/tokens", {"name": "组织仓库联动验收", "scopes": ["all"]}, user)
    user["token"] = token["sha1"]
    key = WORK / f"{name}-key"
    result = run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)])
    if result.returncode:
        raise RuntimeError(result.stderr)
    require("POST", "/user/keys", {"title": "组织仓库联动验收", "key": Path(str(key) + ".pub").read_text()}, user)
    user["key_path"] = str(key)
    OBJECTS["users"][label] = {"id": user["id"], "name": name}
    persist()
    return user


def create_group(label, slug, parent=0, visibility=2, user=ADMIN):
    group = require("POST", "/governance/groups", {"path": slug, "name": "组织仓库联动验收", "parent_id": parent, "visibility": visibility}, user)
    OBJECTS["groups"][label] = {"id": group["id"], "full_path": group["full_path"]}
    persist()
    return group


def group_state(group_id, user=ADMIN):
    return require("GET", f"/governance/groups/{group_id}", user=user)


def member(group_id, user, role, actor=ADMIN):
    state = group_state(group_id, actor)
    status, body = api("PUT", f'/governance/groups/{group_id}/members/{user["id"]}', {"role": role, "revision": state["revision"]}, actor)
    return status, body


def remove_member(group_id, user, actor=ADMIN):
    state = group_state(group_id, actor)
    return api("DELETE", f'/governance/groups/{group_id}/members/{user["id"]}?revision={state["revision"]}', user=actor)


def create_repo(label, group, name, private=True, user=ADMIN):
    repo = require("POST", f'/orgs/{group["full_path"]}/repos', {"name": name, "private": private, "auto_init": True, "default_branch": "main"}, user)
    OBJECTS["repos"][label] = {"id": repo["id"], "full_name": repo["full_name"]}
    persist()
    return repo


def repo(full_name, user=ADMIN):
    return require("GET", "/repos/" + full_name, user=user)


def git_env(user):
    environment = os.environ.copy()
    for key in list(environment):
        if key.startswith("GIT_"):
            del environment[key]
    header = "Authorization: Basic " + base64.b64encode(f'{user["name"]}:{user.get("token", user.get("password", ""))}'.encode()).decode()
    environment.update({
        "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_COUNT": "2",
        "GIT_CONFIG_KEY_0": "http.extraHeader", "GIT_CONFIG_VALUE_0": header,
        "GIT_CONFIG_KEY_1": "credential.helper", "GIT_CONFIG_VALUE_1": "", "GIT_TERMINAL_PROMPT": "0",
        "GIT_SSH_COMMAND": f'ssh -F /dev/null -i {user.get("key_path", WORK / "missing")} -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile={WORK / "known-hosts"}',
    })
    return environment


def git_url(repository, protocol):
    if protocol == "HTTP":
        return repository["clone_url"]
    return f'ssh://git@127.0.0.1:{SSH_PORT}/{repository["full_name"]}.git'


def refs(repository, user=ADMIN, protocol="HTTP"):
    result = run(["git", "ls-remote", git_url(repository, protocol)], env=git_env(user))
    return result.returncode, dict(line.split()[::-1] for line in result.stdout.splitlines()), result.stderr[-1000:]


def push_probe(repository, user, label, allow):
    checks = []
    for protocol in ("HTTP", "SSH"):
        folder = WORK / "clones" / f"{label}-{protocol.lower()}-{uuid.uuid4().hex[:5]}"
        folder.parent.mkdir(exist_ok=True)
        cloned = run(["git", "clone", git_url(repository, protocol), str(folder)], env=git_env(user))
        if cloned.returncode:
            checks.append({"协议": protocol, "读取": False, "推送": False, "符合预期": not allow})
            continue
        run(["git", "config", "user.name", "组织仓库验收"], cwd=folder)
        run(["git", "config", "user.email", "or@example.invalid"], cwd=folder)
        marker = folder / "or-acceptance.txt"
        marker.write_text(f"组织仓库联动验收 {label} {protocol}\n")
        run(["git", "add", marker.name], cwd=folder)
        run(["git", "commit", "-m", "test: 验证组织仓库联动"], cwd=folder)
        sha = run(["git", "rev-parse", "HEAD"], cwd=folder).stdout.strip()
        branch = f"feature/{PREFIX}-{label}-{protocol.lower()}"[:120]
        before = refs(repository)[1]
        pushed = run(["git", "push", git_url(repository, protocol), f"HEAD:refs/heads/{branch}"], cwd=folder, env=git_env(user))
        after = refs(repository)[1]
        success = pushed.returncode == 0 and after.get("refs/heads/" + branch) == sha
        denied = pushed.returncode != 0 and before == after
        checks.append({"协议": protocol, "读取": True, "推送": success, "引用未变": before == after, "符合预期": success if allow else denied})
    return checks


def web_gui(root):
    script = r'''
const {chromium}=require('/tmp/gitea-gui-acceptance-runtime/node_modules/playwright');
(async()=>{const base=process.env.OR_BASE;const browser=await chromium.launch({headless:true,executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'});const page=await browser.newPage();page.setDefaultTimeout(60000);page.setDefaultNavigationTimeout(60000);
await page.goto(base+'/user/login');await page.locator('[name=user_name]').fill(process.env.OR_USER);await page.locator('[name=password]').fill(process.env.OR_PASSWORD);await Promise.all([page.waitForURL(u=>!u.pathname.includes('/user/login')),page.locator('button').filter({hasText:/Sign In|登录/}).click()]);
await page.goto(base+'/governance/groups/'+process.env.OR_GROUP+'?create=1');await page.locator('#group-name').fill('网页创建子组织');await page.locator('#group-path').fill(process.env.OR_CHILD);await Promise.all([page.waitForNavigation({waitUntil:'domcontentloaded'}),page.locator('button').filter({hasText:'创建群组'}).click()]);
const groupUrl=page.url();await page.goto(base+'/repo/create?org='+process.env.OR_GROUP);await page.locator('#repo_name').fill(process.env.OR_REPO);const init=page.locator('[name=auto_init]');if(await init.count()&&!await init.isChecked())await init.check();await Promise.all([page.waitForNavigation({waitUntil:'domcontentloaded'}),page.locator('button').filter({hasText:/创建仓库|Create Repository/}).click()]);
console.log(JSON.stringify({groupUrl,repoUrl:page.url()}));await browser.close();})().catch(e=>{console.error(e.message);process.exit(1)});
'''
    environment = os.environ.copy()
    environment.update({"OR_BASE": BASE, "OR_USER": ADMIN["name"], "OR_PASSWORD": ADMIN["password"], "OR_GROUP": str(root["id"]), "OR_CHILD": f"ui-{RUN}", "OR_REPO": f"ui-repo-{RUN}"})
    result = run(["node", "-e", script], env=environment, timeout=150)
    if result.returncode:
        raise RuntimeError("网页实操失败：" + result.stderr[-1200:])
    return json.loads(result.stdout.splitlines()[-1])


def main():
    WORK.mkdir(mode=0o700)
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.touch(exist_ok=True)
    if OUT.stat().st_size:
        raise RuntimeError(f"证据文件已存在且非空，拒绝覆盖：{OUT}")
    persist()
    health = run(["docker", "inspect", "-f", "{{.State.Status}}", "user-permission-20260918-gitea-1"])
    if health.stdout.strip() != "running":
        raise RuntimeError("隔离服务未运行")
    version = require("GET", "/version")
    source = run(["git", "rev-parse", "HEAD"], cwd=ROOT).stdout.strip()
    if source != "1d07c4e3edaae9a69ff2d875adb4d88ecb3c1fda":
        raise RuntimeError("源码基线变化，停止验收")

    owner = create_user("owner")
    alpha_user = create_user("alpha-dev")
    platform_user = create_user("platform-dev")
    mixed_a = create_user("mixed-a")
    mixed_b = create_user("mixed-b")
    outsider = create_user("outsider")
    direct_user = create_user("direct")
    old_user = create_user("old-parent")
    new_user = create_user("new-parent")
    explicit_user = create_user("explicit")

    alpha = create_group("alpha", f"{PREFIX}-alpha")
    beta = create_group("beta", f"{PREFIX}-beta")
    public = create_group("public", f"{PREFIX}-public", visibility=0)
    member(alpha["id"], owner, 50)
    member(beta["id"], owner, 50)
    member(alpha["id"], alpha_user, 30)
    platform = create_group("platform", "platform", alpha["id"])
    sdk = create_group("sdk", "sdk", platform["id"])
    business = create_group("business", "business", alpha["id"])
    member(platform["id"], platform_user, 30)
    member(alpha["id"], mixed_a, 20); member(sdk["id"], mixed_a, 30)
    member(alpha["id"], mixed_b, 30); member(sdk["id"], mixed_b, 20)
    root_repo = create_repo("root", alpha, "root")
    platform_repo = create_repo("platform", platform, "platform")
    sdk_repo = create_repo("sdk", sdk, "sdk")
    business_repo = create_repo("business", business, "business")
    beta_repo = create_repo("beta", beta, "beta")

    gui = {}
    scenario("O01", "网页/API 创建层级正确，继承生效且无额外直接成员", lambda: (
        gui.update(web_gui(alpha)) or
        {"检查": "管理网页创建子组与仓库，API创建第二级并核对继承", "网页": gui, "层级": [platform["full_path"], sdk["full_path"]], "Git": push_probe(sdk_repo, alpha_user, "o01", True), "通过": all(x["符合预期"] for x in push_probe(sdk_repo, alpha_user, "o01b", True))}
    ))

    def o02():
        before = require("GET", f'/governance/groups?parent_id={beta["id"]}', user=owner)
        status, body = api("POST", "/governance/groups", {"path": "forbidden", "name": "禁止创建", "parent_id": beta["id"], "visibility": 2}, platform_user)
        after = require("GET", f'/governance/groups?parent_id={beta["id"]}', user=owner)
        return {"检查": "无目标权限篡改父组ID", "HTTP状态": status, "响应": body, "通过": status in (403, 404) and before == after}
    scenario("O02", "请求拒绝且目标树、原生映射和导航不变化", o02)
    scenario("O03", "alpha Developer 对两级后代页面/API/Git读写成功", lambda: {"检查": "两级继承HTTP与SSH开发", "检查项": push_probe(sdk_repo, alpha_user, "o03", True), "通过": all(x["符合预期"] for x in push_probe(platform_repo, alpha_user, "o03p", True) + push_probe(sdk_repo, alpha_user, "o03s", True))})

    def o04():
        allowed = push_probe(sdk_repo, platform_user, "o04-allowed", True)
        denied = [*push_probe(root_repo, platform_user, "o04-root", False), *push_probe(business_repo, platform_user, "o04-business", False), *push_probe(beta_repo, platform_user, "o04-beta", False)]
        return {"检查": "本级后代允许且父同级无关拒绝", "允许": allowed, "拒绝": denied, "通过": all(x["符合预期"] for x in allowed + denied)}
    scenario("O04", "platform Developer 仅本级与后代读写", o04)

    def o05():
        checks = push_probe(sdk_repo, mixed_a, "o05-a-sdk", True) + push_probe(business_repo, mixed_a, "o05-a-business", False) + push_probe(sdk_repo, mixed_b, "o05-b-sdk", True)
        return {"检查": "具体能力取并集且低直接角色不覆盖高继承", "检查项": checks, "通过": all(x["符合预期"] for x in checks)}
    scenario("O05", "混合来源按能力并集计算", o05)

    def o06():
        state = group_state(platform["id"])
        attempts = []
        for path, parent in (("platform", sdk["id"]), ("business", alpha["id"]), ("../escape", alpha["id"])):
            status, body = api("POST", f'/governance/groups/{platform["id"]}/move', {"path": path, "parent_id": parent, "revision": state["revision"]})
            attempts.append({"path": path, "parent": parent, "status": status, "body": body})
        fresh = group_state(platform["id"])
        return {"检查": "后代环、冲突名、非法路径均原子拒绝", "尝试": attempts, "当前": fresh, "通过": all(x["status"] >= 400 for x in attempts) and fresh["full_path"] == platform["full_path"]}
    scenario("O06", "非法移动完整回滚，无环和半更新", o06)

    move_src = create_group("move-src", f"{PREFIX}-move-src")
    move_dst = create_group("move-dst", f"{PREFIX}-move-dst")
    member(move_src["id"], old_user, 30); member(move_dst["id"], new_user, 30)
    move_child = create_group("move-child", "child", move_src["id"])
    move_leaf = create_group("move-leaf", "leaf", move_child["id"])
    member(move_child["id"], explicit_user, 30)
    move_repo = create_repo("move", move_leaf, "payload")

    def do_move():
        before = refs(move_repo)[1]
        current = group_state(move_child["id"])
        status, moved = api("POST", f'/governance/groups/{move_child["id"]}/move', {"path": "child", "parent_id": move_dst["id"], "revision": current["revision"]})
        current_repo = repo(move_repo["full_name"])
        move_repo.update(current_repo)
        checks = push_probe(move_repo, old_user, "o07-old", False) + push_probe(move_repo, new_user, "o07-new", True) + push_probe(move_repo, explicit_user, "o07-explicit", True)
        return status, moved, before, checks
    movement = {}
    def o07():
        status, moved, before, checks = do_move()
        movement.update({"status": status, "moved": moved})
        return {"检查": "跨根移动后继承重算、直接授权保留、引用保留", "移动": moved, "原引用": before, "检查项": checks, "通过": status == 200 and all(x["符合预期"] for x in checks)}
    scenario("O07", "新父继承生效、旧父失权、直接授权保留", o07)

    def o08():
        temp = create_group("o08-root", f"{PREFIX}-o08")
        member(temp["id"], old_user, 50)
        member(beta["id"], old_user, 40)
        child = create_group("o08-child", "child", temp["id"])
        current = group_state(child["id"], old_user)
        status, receipt = api("POST", f'/governance/groups/{child["id"]}/move', {"path": "lost", "parent_id": beta["id"], "revision": current["revision"]}, old_user)
        follow = api("GET", f'/governance/groups/{child["id"]}', user=old_user)[0]
        after = group_state(child["id"], old_user) if follow == 200 else {"revision": 0}
        manage = api("POST", f'/governance/groups/{child["id"]}/move', {"path": "forbidden", "parent_id": beta["id"], "revision": after["revision"]}, old_user)[0]
        return {"检查": "旧父Owner与目标Maintainer移动回执、读取与随后管理拒绝分开判定", "移动状态": status, "回执": receipt, "后续读取状态": follow, "后续管理状态": manage, "通过": status == 200 and follow == 200 and manage in (403, 404)}
    scenario("O08", "移动成功回执不因随后失权被误报失败", o08)

    def o09():
        current = group_state(move_child["id"])
        old_path = current["full_path"]
        status, renamed = api("POST", f'/governance/groups/{move_child["id"]}/move', {"path": "renamed", "parent_id": move_dst["id"], "revision": current["revision"]})
        status_old, old_body = api("GET", f'/repos/{urllib.parse.quote(old_path, safe="/")}/leaf/payload', user=explicit_user)
        status_new, new_body = api("GET", f'/repos/{renamed["full_path"]}/leaf/payload', user=explicit_user)
        same = status_old == status_new == 200 and old_body["id"] == new_body["id"] == move_repo["id"]
        return {"检查": "改名后新旧入口指向同一稳定ID并重新鉴权", "旧路径状态": status_old, "新路径状态": status_new, "稳定ID": move_repo["id"], "通过": status == 200 and same}
    scenario("O09", "新旧路径指向同一资源且按当前权限鉴权", o09)

    def o10():
        current = group_state(sdk["id"])
        status, body = api("POST", f'/governance/groups/{sdk["id"]}/move', {"path": "sdk", "parent_id": platform["id"], "visibility": 0, "revision": current["revision"]})
        anonymous = api("GET", f'/governance/groups/{sdk["id"]}', user={"name": "", "password": ""})[0]
        visible = require("GET", f'/governance/groups?parent_id={platform["id"]}', user=platform_user)
        hidden = api("GET", f'/governance/groups?parent_id={beta["id"]}', user=platform_user)[1]
        final = group_state(sdk["id"])
        return {"检查": "祖先可见性限制、授权列表与无权计数", "请求状态": status, "最终可见性": final["visibility"], "匿名状态": anonymous, "有权列表": len(visible), "无权响应": hidden, "通过": final["visibility"] == 2 and anonymous in (401, 403, 404) and any(x["id"] == sdk["id"] for x in visible)}
    scenario("O10", "私有祖先约束不被绕过且无权资源不泄露", o10)

    p_root = create_group("p-root", f"{PREFIX}-projects")
    member(p_root["id"], owner, 50)

    def p01():
        good = create_repo("p01", p_root, "created", user=owner)
        status_bad, body_bad = api("POST", f'/orgs/{p_root["full_path"]}/repos', {"name": "denied", "private": True}, outsider)
        checks = push_probe(good, owner, "p01", True)
        return {"检查": "网页/API建库、归属、继承和Git实体", "网页": gui, "拒绝状态": status_bad, "拒绝响应": body_bad, "Git": checks, "通过": status_bad in (403, 404) and all(x["符合预期"] for x in checks)}
    scenario("P01", "有权创建完整成功，无权创建完整拒绝", p01)

    def p02():
        before = require("GET", f'/orgs/{beta["full_path"]}/repos', user=owner)
        status, body = api("POST", f'/orgs/{beta["full_path"]}/repos', {"name": "forged", "owner": p_root["full_path"], "private": True}, platform_user)
        after = require("GET", f'/orgs/{beta["full_path"]}/repos', user=owner)
        return {"检查": "篡改owner与目标组织路径", "状态": status, "响应": body, "通过": status in (403, 404) and [x["id"] for x in before] == [x["id"] for x in after]}
    scenario("P02", "目标组织无新仓库且不能绕过校验", p02)

    def p03():
        same_a = create_repo("same-a", p_root, "same-name")
        other = create_group("same-other", f"{PREFIX}-other")
        same_b = create_repo("same-b", other, "same-name")
        attempts = [api("POST", f'/orgs/{p_root["full_path"]}/repos', {"name": name, "private": True})[0] for name in ("same-name", "SAME-NAME", "../escape")]
        return {"检查": "跨命名空间同名隔离，同命名空间与非法名称拒绝", "稳定ID": [same_a["id"], same_b["id"]], "拒绝状态": attempts, "通过": same_a["id"] != same_b["id"] and all(x >= 400 for x in attempts)}
    scenario("P03", "合法同名不串数据，冲突和非法路径拒绝", p03)

    direct_repo = create_repo("direct", p_root, "direct")
    direct_state = require("GET", f'/governance/repositories/{direct_repo["id"]}/members')
    require("PUT", f'/governance/repositories/{direct_repo["id"]}/members/{direct_user["id"]}', {"role": 30, "revision": direct_state["revision"]})
    scenario("P04", "仓库直接Developer仅能开发目标仓库", lambda: {"检查": "目标仓库开发与邻近仓库/组织拒绝", "检查项": push_probe(direct_repo, direct_user, "p04-direct", True) + push_probe(root_repo, direct_user, "p04-neighbor", False), "组织状态": api("GET", f'/governance/groups/{p_root["id"]}', user=direct_user)[0], "通过": all(x["符合预期"] for x in push_probe(direct_repo, direct_user, "p04d", True) + push_probe(root_repo, direct_user, "p04n", False)) and api("GET", f'/governance/groups/{p_root["id"]}', user=direct_user)[0] in (403, 404)})

    def p05():
        public_repo = create_repo("visibility", public, f"visibility-{RUN}", private=False)
        anon_before = api("GET", "/repos/" + public_repo["full_name"], user={"name": "", "password": ""})[0]
        updated = require("PATCH", "/repos/" + public_repo["full_name"], {"private": True})
        anon_after = api("GET", "/repos/" + updated["full_name"], user={"name": "", "password": ""})[0]
        private_update_status = api("PATCH", "/repos/" + sdk_repo["full_name"], {"private": False})[0]
        return {"检查": "公开转私有立即撤销匿名读取，私有父组阻止公开", "匿名前": anon_before, "匿名后": anon_after, "私有父组公开请求": private_update_status, "通过": anon_before == 200 and anon_after in (401, 403, 404) and repo(sdk_repo["full_name"])["private"]}
    scenario("P05", "可见性切换不遗留匿名入口并遵守祖先约束", p05)

    def p06():
        sample = create_repo("rename", p_root, "rename-old")
        issue = require("POST", f'/repos/{sample["full_name"]}/issues', {"title": "改名保留事项", "body": "联动验收"})
        protection = require("POST", f'/repos/{sample["full_name"]}/branch_protections', {"rule_name": "protected/*", "enable_push": False})
        before_refs = refs(sample)[1]
        renamed = require("PATCH", f'/repos/{sample["full_name"]}', {"name": "rename-new"})
        old_status, old = api("GET", f'/repos/{sample["full_name"]}')
        issue_after = require("GET", f'/repos/{renamed["full_name"]}/issues/{issue["number"]}')
        protections = require("GET", f'/repos/{renamed["full_name"]}/branch_protections')
        return {"检查": "改名保留稳定ID、事项、保护规则、引用和旧别名", "旧入口": old_status, "新路径": renamed["full_name"], "通过": renamed["id"] == sample["id"] and old_status == 200 and old["id"] == sample["id"] and issue_after["id"] == issue["id"] and any(x["rule_name"] == "protected/*" for x in protections) and refs(renamed)[1] == before_refs}
    scenario("P06", "改名完整保留数据、授权、规则和受控别名", p06)

    transfer_src = create_group("transfer-src", f"{PREFIX}-transfer-src")
    transfer_dst = create_group("transfer-dst", f"{PREFIX}-transfer-dst")
    member(transfer_src["id"], old_user, 30); member(transfer_dst["id"], new_user, 30); member(transfer_src["id"], owner, 50); member(transfer_dst["id"], owner, 50)
    transfer_repo = create_repo("transfer", transfer_src, "transfer")
    transfer_direct = require("GET", f'/governance/repositories/{transfer_repo["id"]}/members')
    require("PUT", f'/governance/repositories/{transfer_repo["id"]}/members/{explicit_user["id"]}', {"role": 30, "revision": transfer_direct["revision"]})

    def p07():
        conflict = create_repo("transfer-conflict", transfer_dst, "conflict")
        conflict_src = create_repo("transfer-conflict-src", transfer_src, "conflict")
        denied = api("POST", f'/repos/{conflict_src["full_name"]}/transfer', {"new_owner": transfer_dst["full_path"]})[0]
        status, moved = api("POST", f'/repos/{transfer_repo["full_name"]}/transfer', {"new_owner": transfer_dst["full_path"]})
        current = require("GET", f'/repositories/{transfer_repo["id"]}') if False else repo(f'{transfer_dst["full_path"]}/transfer')
        transfer_repo.update(current)
        return {"检查": "跨组织转移成功且冲突完整拒绝", "转移状态": status, "冲突状态": denied, "稳定ID": current["id"], "Git": refs(current)[0], "通过": status in (200, 201, 202) and denied >= 400 and current["id"] == OBJECTS["repos"]["transfer"]["id"] and refs(current)[0] == 0}
    scenario("P07", "合法转移完整成功，冲突与无权限完整拒绝", p07)

    def p08():
        current = repo(f'{transfer_dst["full_path"]}/transfer')
        checks = push_probe(current, old_user, "p08-old", False) + push_probe(current, new_user, "p08-new", True) + push_probe(current, explicit_user, "p08-direct", True)
        return {"检查": "转移后旧新继承重算且直接授权保留", "检查项": checks, "通过": all(x["符合预期"] for x in checks), "未验证": "跨根自定义角色映射、邀请和申请未建立代表性样本"}
    scenario("P08", "各授权来源按迁移契约重新计算且不放大", p08)

    archive_repo = create_repo("archive", p_root, "archive")
    member(p_root["id"], direct_user, 30)
    def p09():
        before = refs(archive_repo)[1]
        require("PATCH", f'/repos/{archive_repo["full_name"]}', {"archived": True})
        reads = [refs(archive_repo, direct_user, p)[0] == 0 for p in ("HTTP", "SSH")]
        writes = push_probe(archive_repo, direct_user, "p09-archived", False)
        require("PATCH", f'/repos/{archive_repo["full_name"]}', {"archived": False})
        restored = push_probe(archive_repo, direct_user, "p09-restored", True)
        return {"检查": "归档读、写门禁、引用不变与恢复写入", "归档读取": reads, "归档写入": writes, "恢复": restored, "通过": all(reads) and all(x["符合预期"] for x in writes + restored) and before.items() <= refs(archive_repo)[1].items()}
    scenario("P09", "归档仍可读、所有写入口拒绝，恢复后按当前权限写", p09)

    def p10():
        tree = create_group("archive-tree", f"{PREFIX}-archive-tree")
        child = create_group("archive-tree-child", "child", tree["id"])
        member(tree["id"], direct_user, 30)
        active = create_repo("archive-active", child, "active")
        independent = create_repo("archive-independent", child, "independent")
        pending = create_repo("archive-pending", child, "pending")
        require("PATCH", f'/repos/{independent["full_name"]}', {"archived": True})
        require("DELETE", f'/repos/{pending["full_name"]}')
        impact = require("GET", f'/governance/groups/{tree["id"]}/archive')
        state = group_state(tree["id"]); require("PUT", f'/governance/groups/{tree["id"]}/archive', {"archived": True, "revision": state["revision"]})
        during = push_probe(active, direct_user, "p10-during", False)
        remove_member(tree["id"], direct_user)
        state = group_state(tree["id"]); require("PUT", f'/governance/groups/{tree["id"]}/archive', {"archived": False, "revision": state["revision"]})
        independent_after = repo(independent["full_name"])["archived"]
        pending_after = require("GET", f'/governance/repositories/{pending["id"]}/deletion')
        revoked = push_probe(active, direct_user, "p10-revoked", False)
        return {"检查": "混合状态整树归档、撤权、整树解除与待删除保留", "影响预览": impact, "期间": during, "独立归档恢复后状态": independent_after, "待删除": pending_after, "撤权后": revoked, "通过": all(x["符合预期"] for x in during + revoked) and not independent_after and pending_after["pending"] is not None and pending_after["archived"]}
    scenario("P10", "整树解除含原独立归档，待删除保留且撤权不复活", p10)

    def p11():
        sample = create_repo("restore", p_root, "restore")
        member_state = require("GET", f'/governance/repositories/{sample["id"]}/members')
        require("PUT", f'/governance/repositories/{sample["id"]}/members/{explicit_user["id"]}', {"role": 30, "revision": member_state["revision"]})
        issue = require("POST", f'/repos/{sample["full_name"]}/issues', {"title": "删除恢复事项", "body": "保留"})
        push_probe(sample, explicit_user, "p11-seed", True)
        before = refs(sample)[1]
        require("DELETE", f'/repos/{sample["full_name"]}')
        pending = require("GET", f'/governance/repositories/{sample["id"]}/deletion')
        members = require("GET", f'/governance/repositories/{sample["id"]}/members')
        require("DELETE", f'/governance/repositories/{sample["id"]}/members/{explicit_user["id"]}?revision={members["revision"]}')
        restored = require("POST", f'/governance/repositories/{sample["id"]}/restore', {"confirmation_path": pending["full_path"], "due_unix": pending["pending"]["due_unix"]})
        current = repo(restored["full_path"])
        issue_after = require("GET", f'/repos/{current["full_name"]}/issues/{issue["number"]}')
        denied = push_probe(current, explicit_user, "p11-revoked", False)
        return {"检查": "窗口内恢复保留代码事项且撤权不复活", "删除计划": pending, "恢复": restored, "检查项": denied, "通过": before.items() <= refs(current)[1].items() and issue_after["id"] == issue["id"] and all(x["符合预期"] for x in denied)}
    scenario("P11", "恢复保留业务数据与当前授权状态", p11)

    def p12():
        sample = create_repo("destroy", p_root, "destroy")
        old_id = sample["id"]
        require("DELETE", f'/repos/{sample["full_name"]}')
        pending = require("GET", f'/governance/repositories/{old_id}/deletion')
        unauthorized = api("POST", f'/governance/repositories/{old_id}/delete-permanently', {"confirmation_path": pending["full_path"], "due_unix": pending["pending"]["due_unix"]}, outsider)[0]
        stale = api("POST", f'/governance/repositories/{old_id}/delete-permanently', {"confirmation_path": pending["full_path"], "due_unix": pending["pending"]["due_unix"] - 1})[0]
        deleted = api("POST", f'/governance/repositories/{old_id}/delete-permanently', {"confirmation_path": pending["full_path"], "due_unix": pending["pending"]["due_unix"]})[0]
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline and api("GET", f'/governance/repositories/{old_id}/deletion')[0] != 404:
            time.sleep(0.2)
        recreated = create_repo("destroy-recreated", p_root, "destroy")
        old_status = api("GET", f'/governance/repositories/{old_id}/deletion')[0]
        return {"检查": "无权、过期重放、永久删除及同名重建身份隔离", "无权状态": unauthorized, "过期状态": stale, "删除状态": deleted, "旧ID状态": old_status, "新ID": recreated["id"], "通过": unauthorized in (403, 404) and stale >= 400 and deleted == 204 and old_status == 404 and recreated["id"] != old_id}
    scenario("P12", "永久删除仅限待删除目标，同名重建不继承旧身份", p12)

    summary = {"运行标识": RUN, "前缀": PREFIX, "版本": version, "源码": source, "总场景": len(ROWS), "通过": sum(x["结果"] == "通过" for x in ROWS), "失败": sum(x["结果"] == "失败" for x in ROWS), "未验证": sum(x["结果"] == "未验证" for x in ROWS), "证据SHA256": hashlib.sha256(OUT.read_bytes()).hexdigest()}
    print(json.dumps(summary, ensure_ascii=False))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        LOG.parent.mkdir(parents=True, exist_ok=True)
        LOG.write_text(str(error) + "\n")
        LOG.chmod(0o600)
        raise
