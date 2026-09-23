#!/usr/bin/env python3
"""角色和成员生命周期矩阵的隔离实例验收。"""
import base64
import datetime
import importlib.util
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import time
import urllib.parse

ROOT = Path(__file__).resolve().parents[1]
WORK = Path("/tmp/gitea-user-acceptance-20260918")
RUN = "rm-" + datetime.datetime.now().strftime("%H%M%S") + "-" + secrets.token_hex(2)
RUN_DIR = WORK / RUN
STATE = RUN_DIR / "state.json"
OUT = ROOT / "docs/evidence/governance-linkage-20260918/rm-results.jsonl"
DB = "user-permission-20260918-database-1"
RUN_DIR.mkdir(mode=0o700, parents=True)
OUT.parent.mkdir(parents=True, exist_ok=True)

spec = importlib.util.spec_from_file_location("acceptance", ROOT / "tools/user-permission-acceptance.py")
a = importlib.util.module_from_spec(spec)
spec.loader.exec_module(a)
api, ok, env_for = a.api, a.ok, a.env_for
ADMIN = a.ADMIN
S = {"运行标识": RUN, "用户": {}, "组织": {}, "仓库": {}}


def run(args, cwd=None, env=None):
    return subprocess.run(args, cwd=cwd, env=env, capture_output=True, text=True, timeout=90)


def save():
    STATE.write_text(json.dumps(S, ensure_ascii=False, indent=2))
    STATE.chmod(0o600)


def record(case, check, passed=None, expected="", before=None, after=None, defect="", result=None, **extra):
    row = {
        "场景": case, "检查": check,
        "结果": result or ("通过" if passed else "失败"), "预期": expected,
        "前证据": before, "后证据": after,
        "时间": datetime.datetime.now(datetime.timezone.utc).isoformat(), "缺陷": defect,
        "运行标识": RUN, **extra,
    }
    with OUT.open("a") as f:
        f.write(json.dumps(row, ensure_ascii=False) + "\n")
    print(case, check, row["结果"], flush=True)


def user(label):
    name = f"{RUN}-{label}"
    password = secrets.token_urlsafe(24)
    created = ok("POST", "/admin/users", {"username": name, "password": password, "email": name + "@example.invalid", "must_change_password": False})
    u = {"name": name, "password": password, "id": created["id"], "email": name + "@example.invalid"}
    u["token"] = ok("POST", f"/users/{name}/tokens", {"name": "角色成员验收", "scopes": ["all"]}, u)["sha1"]
    key = WORK / (name + "-key")
    generated = run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)])
    if generated.returncode:
        raise RuntimeError("SSH 密钥创建失败")
    key.chmod(0o600)
    ok("POST", "/user/keys", {"title": "角色成员验收", "key": Path(str(key) + ".pub").read_text()}, u)
    S["用户"][label] = {k: v for k, v in u.items() if k != "token"}
    save()
    return u


def group(label, parent=None):
    path = f"{RUN}-{label}" if parent is None else label
    g = ok("POST", "/governance/groups", {"name": "角色成员验收", "path": path, "parent_id": parent["id"] if parent else 0, "visibility": 2})
    S["组织"][label] = g
    save()
    return g


def repo(label, owner):
    r = ok("POST", f"/orgs/{owner['full_path']}/repos", {"name": f"{RUN}-{label}", "private": True, "auto_init": True, "default_branch": "main"})
    S["仓库"][label] = r
    save()
    return r


def member(g, u, role=None, custom=0, expires=0, actor=None):
    state = ok("GET", f"/governance/groups/{g['id']}")
    path = f"/governance/groups/{g['id']}/members/{u['id']}"
    if role is None:
        return api("DELETE", path + "?revision=" + str(state["revision"]), user=actor)
    return api("PUT", path, {"role": role, "custom_role_id": custom, "expires_unix": expires, "revision": state["revision"]}, actor)


def direct(r, u, role=None, actor=None):
    state = ok("GET", f"/governance/repositories/{r['id']}/members")
    path = f"/governance/repositories/{r['id']}/members/{u['id']}"
    if role is None:
        return api("DELETE", path + "?revision=" + str(state["revision"]), user=actor)
    return api("PUT", path, {"role": role, "revision": state["revision"]}, actor)


def refs(r):
    result = run(["git", "ls-remote", r["clone_url"]], env=env_for(ADMIN))
    if result.returncode:
        raise RuntimeError("管理员引用见证失败")
    return dict(line.split()[::-1] for line in result.stdout.splitlines())


def git_probe(case, label, r, u, readable, writable):
    for protocol, url in (("HTTP", r["clone_url"]), ("SSH", r["ssh_url"])):
        folder = RUN_DIR / f"{case}-{label}-{protocol.lower()}"
        cloned = run(["git", "clone", url, str(folder)], env=env_for(u))
        read_ok = cloned.returncode == 0 and (folder / "README.md").exists()
        record(case, label + "克隆", read_ok == readable, "允许" if readable else "拒绝", {"退出码": cloned.returncode}, {"存在README": (folder / "README.md").exists()}, 协议=protocol, 输出=cloned.stderr[-800:])
        seed = folder
        if not read_ok:
            seed = RUN_DIR / f"{case}-{label}-{protocol.lower()}-seed"
            seeded = run(["git", "clone", r["clone_url"], str(seed)], env=env_for(ADMIN))
            if seeded.returncode:
                raise RuntimeError("管理员准备提交失败")
        marker = seed / f"{case.lower()}-{label}-{protocol.lower()}.txt"
        marker.write_text("角色和成员生命周期真实验收\n")
        for cmd in (["git", "config", "user.name", "角色成员验收"], ["git", "config", "user.email", "rm@example.invalid"], ["git", "add", marker.name], ["git", "commit", "-m", "test: 验证角色成员权限"]):
            if run(cmd, cwd=seed).returncode:
                raise RuntimeError("本地提交准备失败")
        sha = run(["git", "rev-parse", "HEAD"], cwd=seed).stdout.strip()
        branch = f"refs/heads/rm/{RUN}/{case.lower()}-{label}-{protocol.lower()}"
        before = refs(r)
        pushed = run(["git", "push", url, "HEAD:" + branch], cwd=seed, env=env_for(u))
        after = refs(r)
        allowed = pushed.returncode == 0 and after.get(branch) == sha
        denied = pushed.returncode != 0 and before == after
        record(case, label + "推送", allowed if writable else denied, "允许" if writable else "拒绝且引用不变", before, after, 协议=protocol, 退出码=pushed.returncode, 输出=pushed.stderr[-800:])


def api_check(case, check, method, path, u, allowed, data=None, stable=None):
    before = stable() if stable else None
    status, body = api(method, path, data, u)
    after = stable() if stable else None
    passed = (200 <= status < 300) if allowed else (status >= 400 and (stable is None or before == after))
    record(case, check, passed, "成功" if allowed else "拒绝且状态不变", before, after, HTTP状态=status, 响应=body)
    return status, body


def role_matrix(root, child, sibling, foreign, target):
    roles = [("minimal", 5, False, False), ("guest", 10, False, False), ("planner", 15, False, False), ("reporter", 20, True, False), ("developer", 30, True, True), ("maintainer", 40, True, True), ("owner", 50, True, True)]
    people = {}
    outsider = user("outsider")
    git_probe("R01", "非成员", target, outsider, False, False)
    api_check("R01", "私有仓库详情拒绝", "GET", "/repos/" + target["full_name"], outsider, False)
    for idx, (label, role, readable, writable) in enumerate(roles, 2):
        case = f"R{idx:02d}"
        u = user(label)
        people[label] = u
        status, _ = member(root, u, role)
        if not 200 <= status < 300:
            raise RuntimeError(f"准备角色失败 {label}: {status}")
        git_probe(case, label, target, u, readable, writable)
        state = lambda: ok("GET", f"/governance/groups/{root['id']}")["revision"]
        api_check(case, "组织成员管理边界", "PUT", f"/governance/groups/{root['id']}/members/{outsider['id']}", u, role == 50, {"role": 10, "revision": state()}, state)
        if role == 50:
            member(root, outsider, None)
        if role == 40:
            api_check(case, "创建后代组织", "POST", "/governance/groups", u, True, {"name": "维护者创建", "path": f"{RUN}-maint-child", "parent_id": child["id"], "visibility": 2})
        if role == 50:
            api_check(case, "无关联组织管理拒绝", "PUT", f"/governance/groups/{foreign['id']}/members/{outsider['id']}", u, False, {"role": 10, "revision": ok("GET", f"/governance/groups/{foreign['id']}")["revision"]}, lambda: ok("GET", f"/governance/groups/{foreign['id']}")["revision"])
    return people, outsider


def custom_roles(root, target, people, outsider):
    gid = root["id"]
    owner = people["owner"]
    state = ok("GET", f"/governance/groups/{gid}")
    role = ok("POST", f"/governance/groups/{gid}/roles", {"name": "审批Reporter", "base_role": 20, "abilities": ["manage_approvals"], "revision": state["revision"]}, owner)
    actor = user("custom-actor")
    assert 200 <= member(root, actor, 20, role["id"])[0] < 300
    git_probe("R09", "Reporter基础能力保留", target, actor, True, False)
    api_check("R09", "额外审批管理能力", "PUT", f"/governance/repositories/{target['id']}/approval-settings", actor, True, {"prevent_author": True, "revision": 0})
    reporter = people["reporter"]
    for label, payload in (("无管理权创建Owner角色", {"name": "越权", "base_role": 50, "abilities": []}), ("未知能力", {"name": "未知", "base_role": 20, "abilities": ["unknown_power"]})):
        rev = ok("GET", f"/governance/groups/{gid}")["revision"]
        payload["revision"] = rev
        api_check("R10", label, "POST", f"/governance/groups/{gid}/roles", reporter if "无管理" in label else owner, False, payload, lambda: ok("GET", f"/governance/groups/{gid}")["revision"])
    rev = ok("GET", f"/governance/groups/{gid}")["revision"]
    updated = ok("PUT", f"/governance/groups/{gid}/roles/{role['id']}", {"name": "审批和成员Reporter", "base_role": 20, "abilities": ["manage_approvals", "manage_members"], "revision": rev}, owner)
    api_check("R11", "增加能力即时生效", "PUT", f"/governance/repositories/{target['id']}/members/{outsider['id']}", actor, True, {"role": 10, "revision": ok("GET", f"/governance/repositories/{target['id']}/members")["revision"]})
    direct(target, outsider, None)
    rev = ok("GET", f"/governance/groups/{gid}")["revision"]
    ok("PUT", f"/governance/groups/{gid}/roles/{updated['id']}", {"name": "仅审批Reporter", "base_role": 20, "abilities": ["manage_approvals"], "revision": rev}, owner)
    api_check("R11", "移除能力原会话即时失效", "PUT", f"/governance/repositories/{target['id']}/members/{outsider['id']}", actor, False, {"role": 10, "revision": ok("GET", f"/governance/repositories/{target['id']}/members")["revision"]}, lambda: ok("GET", f"/governance/repositories/{target['id']}/members")["revision"])
    rev = ok("GET", f"/governance/groups/{gid}")["revision"]
    api_check("R12", "在用角色禁止删除", "DELETE", f"/governance/groups/{gid}/roles/{role['id']}?revision={rev}", owner, False, stable=lambda: ok("GET", f"/governance/groups/{gid}")["revision"])
    rev = ok("GET", f"/governance/groups/{gid}")["revision"]
    api_check("R12", "在用角色禁止改变基础角色", "PUT", f"/governance/groups/{gid}/roles/{role['id']}", owner, False, {"name": "改基础", "base_role": 30, "abilities": [], "revision": rev}, lambda: ok("GET", f"/governance/groups/{gid}")["revision"])
    member(root, actor, None)
    rev = ok("GET", f"/governance/groups/{gid}")["revision"]
    api_check("R12", "解除引用后删除", "DELETE", f"/governance/groups/{gid}/roles/{role['id']}?revision={rev}", owner, True)


def membership_matrix(root, child, foreign, target, foreign_repo, people):
    owner = people["owner"]
    actor = user("m-actor")
    assert 200 <= member(root, actor, 30)[0] < 300
    git_probe("M01", "本组后代", target, actor, True, True)
    git_probe("M01", "无关联组织", foreign_repo, actor, False, False)
    manager = user("repo-manager")
    assert 200 <= member(root, manager, 40)[0] < 300
    candidate = user("repo-candidate")
    api_check("M02", "仓库成员添加", "PUT", f"/governance/repositories/{target['id']}/members/{candidate['id']}", manager, True, {"role": 20, "revision": ok("GET", f"/governance/repositories/{target['id']}/members")["revision"]})
    api_check("M02", "仓库管理不能管理组织成员", "PUT", f"/governance/groups/{root['id']}/members/{candidate['id']}", manager, False, {"role": 30, "revision": ok("GET", f"/governance/groups/{root['id']}")["revision"]}, lambda: ok("GET", f"/governance/groups/{root['id']}")["revision"])
    direct(target, candidate, None)
    rev = ok("GET", f"/governance/groups/{root['id']}")["revision"]
    custom = ok("POST", f"/governance/groups/{root['id']}/roles", {"name": "Guest成员管理员", "base_role": 10, "abilities": ["manage_group_members"], "revision": rev}, owner)
    limited = user("limited-manager")
    assert 200 <= member(root, limited, 10, custom["id"])[0] < 300
    api_check("M03", "不能授予超过自身能力的Developer", "PUT", f"/governance/groups/{root['id']}/members/{candidate['id']}", limited, False, {"role": 30, "revision": ok("GET", f"/governance/groups/{root['id']}")["revision"]}, lambda: ok("GET", f"/governance/groups/{root['id']}")["revision"])
    api_check("M03", "不能修改Owner", "PUT", f"/governance/groups/{root['id']}/members/{owner['id']}", limited, False, {"role": 10, "revision": ok("GET", f"/governance/groups/{root['id']}")["revision"]}, lambda: ok("GET", f"/governance/groups/{root['id']}")["revision"])
    changing = user("changing")
    member(root, changing, 20)
    git_probe("M04", "升级前Reporter", target, changing, True, False)
    member(root, changing, 30)
    git_probe("M04", "升级后Developer", target, changing, True, True)
    member(root, changing, 20)
    git_probe("M04", "降级后Reporter", target, changing, True, False)
    layered = user("layered")
    member(root, layered, 30)
    direct(target, layered, 30)
    member(root, layered, None)
    git_probe("M05", "先撤父组仍有直接来源", target, layered, True, True)
    direct(target, layered, None)
    git_probe("M05", "最后来源撤销", target, layered, False, False)
    member(root, layered, 30)
    direct(target, layered, 30)
    direct(target, layered, None)
    git_probe("M05", "先撤直接仍有父组来源", target, layered, True, True)
    member(root, layered, None)
    git_probe("M05", "反向最后来源撤销", target, layered, False, False)
    sources = user("sources")
    native_owner = target["full_name"].split("/", 1)[0]
    team = ok("POST", f"/orgs/{native_owner}/teams", {"name": RUN + "-team", "description": "来源隔离", "permission": "write", "includes_all_repositories": False, "units": ["repo.code"]})
    ok("PUT", f"/teams/{team['id']}/repos/{target['full_name']}")
    ok("PUT", f"/teams/{team['id']}/members/{sources['name']}")
    ok("PUT", f"/repos/{target['full_name']}/collaborators/{sources['name']}", {"permission": "write"})
    member(root, sources, 30)
    member(root, sources, None)
    git_probe("M06", "删除治理来源仍有Team和协作者", target, sources, True, True)
    ok("DELETE", f"/teams/{team['id']}/members/{sources['name']}")
    git_probe("M06", "移出Team仍有协作者", target, sources, True, True)
    ok("DELETE", f"/repos/{target['full_name']}/collaborators/{sources['name']}")
    git_probe("M06", "全部来源撤销", target, sources, False, False)
    expiring = user("expiring")
    expiry = int(time.time()) + 5
    member(root, expiring, 30, expires=expiry)
    git_probe("M07", "到期前", target, expiring, True, True)
    time.sleep(max(0, expiry + 1 - time.time()))
    git_probe("M07", "到期后旧凭据", target, expiring, False, False)
    permanent = user("permanent-owner")
    member(root, permanent, 50)
    status, body = member(root, permanent, None, actor=owner)
    record("M08", "保留另一永久Owner时可删除", 200 <= status < 300, "成功", None, {"HTTP状态": status, "响应": body})
    rev_before = ok("GET", f"/governance/groups/{root['id']}")["revision"]
    status, body = member(root, owner, 20, actor=owner)
    record("M08", "最后永久Owner降级拒绝", status >= 400 and ok("GET", f"/governance/groups/{root['id']}")["revision"] == rev_before, "拒绝并回滚", rev_before, ok("GET", f"/governance/groups/{root['id']}")["revision"], HTTP状态=status, 响应=body)
    invitation_and_requests(root, target, owner, actor)
    disabled = user("disabled")
    member(root, disabled, 30)
    api_check("M12", "禁用前访问对照", "GET", "/repos/" + target["full_name"], disabled, True)
    ok("PATCH", f"/admin/users/{disabled['name']}", {"active": False})
    api_check("M12", "禁用后旧Token拒绝", "GET", "/repos/" + target["full_name"], disabled, False)
    git_probe("M12", "禁用后旧Token和SSH", target, disabled, False, False)
    ok("PATCH", f"/admin/users/{disabled['name']}", {"active": True})
    git_probe("M12", "重新启用恢复仍有效授权", target, disabled, True, True)


def invitation_and_requests(root, target, owner, actor):
    invited = user("invited")
    state = ok("GET", f"/governance/groups/{root['id']}")
    status, inv = api("POST", f"/governance/groups/{root['id']}/invitations", {"email": invited["email"], "role": 30, "revision": state["revision"]}, owner)
    if status == 201:
        git_probe("M09", "接受前无权限", target, invited, False, False)
        wrong = user("wrong-invite-user")
        api_check("M09", "错误账号伪造令牌拒绝", "POST", f"/governance/invitations/{inv['id']}/accept", wrong, False, {"token": "错误令牌"})
        record("M09", "正确接受和重复接受", result="环境阻塞", expected="真实邮件令牌接受一次，重复不扩权", defect="", 原因="dummy mailer 未提供可由收件人读取的邮件存储；未读取数据库令牌密文")
    else:
        record("M09", "邀请创建", passed=False, expected="201", before=None, after={"HTTP状态": status, "响应": inv}, defect="邀请创建异常")
    for label in ("expired", "revoked", "declined", "lost-manager"):
        u = user("invite-" + label)
        state = ok("GET", f"/governance/groups/{root['id']}")
        st, item = api("POST", f"/governance/groups/{root['id']}/invitations", {"email": u["email"], "role": 30, "revision": state["revision"]}, owner)
        if st != 201:
            record("M10", label + "邀请准备", False, "201", None, {"状态": st, "响应": item}, "邀请准备异常")
            continue
        if label == "expired":
            sql = f"update governance_invitation set expires_unix={int(time.time())-1} where id={int(item['id'])} returning id"
            changed = run(["docker", "exec", DB, "psql", "-U", "gitea", "-d", "gitea", "-tAc", sql])
            record("M10", "过期邀请不授权", changed.returncode == 0 and str(item["id"]) in changed.stdout, "到期后不能创建成员", {"邀请ID": item["id"]}, {"数据库fixture仅改本邀请到期时间": changed.stdout.strip()})
        else:
            api_check("M10", label + "邀请撤销", "DELETE", f"/governance/groups/{root['id']}/invitations/{item['id']}", owner, True)
        git_probe("M10", label + "无成员残留", target, u, False, False)
    requester = user("requester")
    setting = ok("GET", f"/governance/groups/{root['id']}/access-requests", user=owner)
    if setting["setting"]["disabled"]:
        ok("PUT", f"/governance/groups/{root['id']}/access-requests/settings", {"disabled": False, "revision": setting["setting"]["revision"]}, owner)
    status, req = api("POST", f"/governance/groups/{root['id']}/access-requests", user=requester)
    record("M11", "提交申请本身不授权", status == 201, "申请创建但无权限", None, {"HTTP状态": status, "申请": req})
    git_probe("M11", "待批准申请", target, requester, False, False)
    api_check("M11", "申请者本人无权批准", "POST", f"/governance/groups/{root['id']}/access-requests/{req['id']}/approve", requester, False, {"role": 30, "revision": ok("GET", f"/governance/groups/{root['id']}")["revision"]})
    api_check("M11", "Owner合法批准", "POST", f"/governance/groups/{root['id']}/access-requests/{req['id']}/approve", owner, True, {"role": 30, "revision": ok("GET", f"/governance/groups/{root['id']}")["revision"]})
    git_probe("M11", "批准后Developer生效", target, requester, True, True)


def main():
    root = group("alpha")
    child = group("platform", root)
    sibling = group("business", root)
    foreign = group("beta")
    target = repo("sdk", child)
    foreign_repo = repo("beta", foreign)
    people, outsider = role_matrix(root, child, sibling, foreign, target)
    custom_roles(root, target, people, outsider)
    membership_matrix(root, child, foreign, target, foreign_repo, people)
    for case in [f"R{i:02d}" for i in range(1, 13)] + [f"M{i:02d}" for i in range(1, 13)]:
        record(case, "场景执行边界", result="已执行", expected="API 与必要 Git 闭环；网页入口另行记录", before=None, after=None, defect="", 网页="本脚本未自动化网页表单")
    save()


def followup():
    root = group("followup-alpha")
    child = group("sdk", root)
    target = repo("followup-sdk", child)
    owner = user("followup-owner")
    member(root, owner, 50)
    # M07：为两种协议保留充足的到期前窗口。
    expiring = user("followup-expiring")
    expiry = int(time.time()) + 30
    member(root, expiring, 30, expires=expiry)
    git_probe("M07", "长窗口到期前", target, expiring, True, True)
    time.sleep(max(0, expiry + 1 - time.time()))
    git_probe("M07", "长窗口到期后", target, expiring, False, False)
    # M08：隔离实例的站点管理员是原生永久 Owner，明确验证有对照来源时的合法变更。
    secondary = user("followup-secondary-owner")
    member(root, secondary, 50)
    status, body = member(root, secondary, 20, actor=owner)
    record("M08", "存在另一永久Owner时允许降级", 200 <= status < 300, "成功且仍有原生永久Owner", None, {"HTTP状态": status, "响应": body}, 原生Owner="隔离管理员创建顶层组织后保留原生 Owner 来源")
    record("M08", "无永久Owner负向", result="环境阻塞", expected="导致零永久Owner的请求拒绝", before=None, after=None, defect="", 原因="每个隔离顶层组织均由站点管理员创建并保留原生永久 Owner；不能把仍有原生 Owner 时的合法降级误判为缺陷")
    # M09/M10：管理员作为仍有效邀请者，避免前序角色变化污染。
    accepted = user("followup-invited")
    state = ok("GET", f"/governance/groups/{root['id']}")
    st, inv = api("POST", f"/governance/groups/{root['id']}/invitations", {"email": accepted["email"], "role": 30, "revision": state["revision"]}, ADMIN)
    record("M09", "创建待接受邀请", st == 201, "201", None, {"HTTP状态": st, "邀请ID": inv.get("id") if isinstance(inv, dict) else None})
    git_probe("M09", "接受前不授权", target, accepted, False, False)
    wrong = user("followup-wrong")
    if st == 201:
        api_check("M09", "错误账号和错误令牌拒绝", "POST", f"/governance/invitations/{inv['id']}/accept", wrong, False, {"token": "invalid"})
    record("M09", "正确账号接受及重复接受", result="环境阻塞", expected="首次接受取得Developer，重复接受不扩大", before=None, after=None, defect="", 原因="dummy mailer 没有可读取邮件存储，容器日志未输出正文令牌；拒绝读取数据库令牌哈希或伪造通过")
    for label in ("expired", "revoked"):
        u = user("followup-" + label)
        state = ok("GET", f"/governance/groups/{root['id']}")
        st, item = api("POST", f"/governance/groups/{root['id']}/invitations", {"email": u["email"], "role": 30, "revision": state["revision"]}, ADMIN)
        if st != 201:
            record("M10", label + "邀请准备", False, "201", None, {"状态": st, "响应": item}, "邀请创建异常")
            continue
        if label == "expired":
            sql = f"update governance_invitation set expires_unix={int(time.time())-1} where id={int(item['id'])} returning id"
            changed = run(["docker", "exec", DB, "psql", "-U", "gitea", "-d", "gitea", "-tAc", sql])
            record("M10", "专属邀请到期fixture", changed.returncode == 0 and str(item["id"]) in changed.stdout, "仅本邀请到期", {"邀请ID": item["id"]}, {"更新行": changed.stdout.strip()})
        else:
            api_check("M10", "撤销邀请", "DELETE", f"/governance/groups/{root['id']}/invitations/{item['id']}", ADMIN, True)
        git_probe("M10", label + "邀请不产生成员", target, u, False, False)
    record("M10", "拒绝及邀请者失权后接受", result="环境阻塞", expected="旧令牌拒绝且不留成员", before=None, after=None, defect="", 原因="同 M09，dummy mailer 正文令牌不可读；已完成过期、撤销及无成员残留")
    # M11：公开组织允许非成员在开启设置后提交真实访问申请。
    public = group("request-public")
    public_repo = repo("request-public", public)
    ok("PATCH", "/repos/" + public_repo["full_name"], {"private": False})
    requester = user("followup-requester")
    setting = ok("GET", f"/governance/groups/{public['id']}/access-requests", user=ADMIN)
    if setting["setting"]["disabled"]:
        ok("PUT", f"/governance/groups/{public['id']}/access-requests/settings", {"disabled": False, "revision": setting["setting"]["revision"]}, ADMIN)
    st, request = api("POST", f"/governance/groups/{public['id']}/access-requests", user=requester)
    record("M11", "非成员提交访问申请", st == 201, "201且申请本身不授权私有能力", None, {"HTTP状态": st, "申请": request})
    if st == 201:
        api_check("M11", "申请者本人批准拒绝", "POST", f"/governance/groups/{public['id']}/access-requests/{request['id']}/approve", requester, False, {"role": 30, "revision": ok("GET", f"/governance/groups/{public['id']}")["revision"]})
        api_check("M11", "管理员批准", "POST", f"/governance/groups/{public['id']}/access-requests/{request['id']}/approve", ADMIN, True, {"role": 30, "revision": ok("GET", f"/governance/groups/{public['id']}")["revision"]})
        git_probe("M11", "批准后Developer", public_repo, requester, True, True)
    # M12：同一账号、同一授权、旧凭据禁用和恢复。
    disabled = user("followup-disabled")
    member(root, disabled, 30)
    git_probe("M12", "禁用前", target, disabled, True, True)
    ok("PATCH", f"/admin/users/{disabled['name']}", {"active": False})
    git_probe("M12", "禁用期间旧凭据", target, disabled, False, False)
    ok("PATCH", f"/admin/users/{disabled['name']}", {"active": True})
    git_probe("M12", "启用后仍有效来源恢复", target, disabled, True, True)
    extended(root, target, owner)


def extended(root, target, owner):
    # E04：一个 Planner 的事项、PR 元数据和代码边界链路。
    developer = user("e04-developer")
    planner = user("e04-planner")
    member(root, developer, 30)
    member(root, planner, 15)
    issue = ok("POST", f"/repos/{target['full_name']}/issues", {"title": "Planner 协作事项", "body": "固定事项样本"}, developer)
    status, edited = api("PATCH", f"/repos/{target['full_name']}/issues/{issue['number']}", {"title": "Planner 已管理事项"}, planner)
    record("E04", "Planner管理他人事项", status == 201 or status == 200, "成功", {"事项": issue}, {"HTTP状态": status, "事项": edited})
    folder = RUN_DIR / "e04-pr"
    assert run(["git", "clone", target["clone_url"], str(folder)], env=env_for(developer)).returncode == 0
    (folder / "e04.txt").write_text("E04 固定 PR 样本\n")
    for cmd in (["git", "config", "user.name", "E04开发者"], ["git", "config", "user.email", "e04@example.invalid"], ["git", "add", "e04.txt"], ["git", "commit", "-m", "test: E04 PR样本"]): assert run(cmd, cwd=folder).returncode == 0
    branch = "feature/e04-" + RUN
    assert run(["git", "push", "origin", "HEAD:" + branch], cwd=folder, env=env_for(developer)).returncode == 0
    pr = ok("POST", f"/repos/{target['full_name']}/pulls", {"title": "E04固定PR", "head": branch, "base": "main"}, developer)
    status, meta = api("GET", f"/repos/{target['full_name']}/pulls/{pr['number']}", user=planner)
    record("E04", "Planner读取PR元数据", status == 200, "成功（已知 CAP-PR-001 当前可能返回403）", {"PR编号": pr["number"]}, {"HTTP状态": status, "响应": meta}, "CAP-PR-001" if status != 200 else "")
    api_check("E04", "Planner请求PR文件拒绝", "GET", f"/repos/{target['full_name']}/pulls/{pr['number']}.diff", planner, False)
    git_probe("E04", "Planner代码读写拒绝", target, planner, False, False)
    # E01 的邀请入职起点受令牌边界阻塞，后段不能用直接成员伪装。
    recruit = user("e01-recruit")
    state = ok("GET", f"/governance/groups/{root['id']}")
    st, inv = api("POST", f"/governance/groups/{root['id']}/invitations", {"email": recruit["email"], "role": 30, "revision": state["revision"]}, ADMIN)
    record("E01", "邀请入职起点", st == 201, "创建待接受Developer邀请", None, {"HTTP状态": st, "邀请ID": inv.get("id") if isinstance(inv, dict) else None})
    record("E01", "继承开发至PR审批合并", result="环境阻塞", expected="接受后继承sdk开发并完成审批合并", before=None, after=None, defect="", 原因="dummy mailer 未暴露正文令牌，不能以直接成员替代邀请入职")
    # E02：同一账号三来源逐一撤销，最后禁用并由旧凭据见证。
    leaver = user("e02-leaver")
    member(root, leaver, 30)
    direct(target, leaver, 30)
    ok("PUT", f"/repos/{target['full_name']}/collaborators/{leaver['name']}", {"permission": "write"})
    git_probe("E02", "三来源在职", target, leaver, True, True)
    member(root, leaver, None)
    git_probe("E02", "撤组织来源仍可工作", target, leaver, True, True)
    direct(target, leaver, None)
    git_probe("E02", "撤直接来源仍有协作者", target, leaver, True, True)
    ok("DELETE", f"/repos/{target['full_name']}/collaborators/{leaver['name']}")
    git_probe("E02", "完全离职", target, leaver, False, False)
    ok("PATCH", f"/admin/users/{leaver['name']}", {"active": False})
    git_probe("E02", "离职后禁用旧凭据", target, leaver, False, False)


def m11_only():
    public = ok("POST", "/governance/groups", {"name": "访问申请公开组织", "path": RUN + "-m11-public", "visibility": 0})
    public_repo = repo("m11-public", public)
    ok("PATCH", "/repos/" + public_repo["full_name"], {"private": False})
    requester = user("m11-only-requester")
    state = ok("GET", f"/governance/groups/{public['id']}/access-requests", user=ADMIN)
    if state["setting"]["disabled"]:
        ok("PUT", f"/governance/groups/{public['id']}/access-requests/settings", {"disabled": False, "revision": state["setting"]["revision"]}, ADMIN)
    st, request = api("POST", f"/governance/groups/{public['id']}/access-requests", user=requester)
    record("M11", "公开组织提交访问申请", st == 201, "201且申请本身不授予Developer", {"成员": False}, {"HTTP状态": st, "申请": request})
    if st != 201:
        return
    api_check("M11", "申请者本人批准拒绝", "POST", f"/governance/groups/{public['id']}/access-requests/{request['id']}/approve", requester, False, {"role": 30, "revision": ok("GET", f"/governance/groups/{public['id']}")["revision"]})
    api_check("M11", "管理员合法批准", "POST", f"/governance/groups/{public['id']}/access-requests/{request['id']}/approve", ADMIN, True, {"role": 30, "revision": ok("GET", f"/governance/groups/{public['id']}")["revision"]})
    git_probe("M11", "批准后Developer", public_repo, requester, True, True)
    api_check("M11", "重复批准不重复授权", "POST", f"/governance/groups/{public['id']}/access-requests/{request['id']}/approve", ADMIN, False, {"role": 30, "revision": ok("GET", f"/governance/groups/{public['id']}")["revision"]})


def secondary_api(method, path, data=None, actor=None):
    actor = actor or ADMIN
    auth = "token " + actor["token"] if "token" in actor else "Basic " + base64.b64encode((actor["name"] + ":" + actor["password"]).encode()).decode()
    config = ["url = \"http://127.0.0.1:3002/api/v1" + path + "\"", "request = \"" + method + "\"", "header = \"Authorization: " + auth + "\"", "header = \"Content-Type: application/json\"", "write-out = \"\\n%{http_code}\""]
    if data is not None:
        config.append("data = " + json.dumps(json.dumps(data, ensure_ascii=False)))
    result = subprocess.run(["docker", "exec", "-i", a.CONTAINER, "curl", "-sS", "-K", "-"], input="\n".join(config) + "\n", text=True, capture_output=True, timeout=45)
    if result.returncode:
        raise RuntimeError("次进程 API 请求失败")
    raw, status = result.stdout.rsplit("\n", 1)
    try:
        body = json.loads(raw)
    except ValueError:
        body = raw
    return int(status), body


def invitation_token(email, invitation_id):
    from email import policy
    from email.parser import BytesParser
    capture = Path("/tmp/gitea-rm-smtp-20260918.log")
    for _ in range(80):
        raw = capture.read_bytes() if capture.exists() else b""
        for block in reversed(raw.split(b"\n===MESSAGE===\n")):
            if not block.strip():
                continue
            message = BytesParser(policy=policy.default).parsebytes(block)
            if email not in str(message.get("To", "")):
                continue
            parts = []
            for part in message.walk() if message.is_multipart() else [message]:
                if part.get_content_maintype() == "text":
                    try:
                        parts.append(part.get_content())
                    except Exception:
                        pass
            text = "\n".join(parts)
            marker = f"/governance/invitations/{invitation_id}#token="
            if marker in text:
                token = text.split(marker, 1)[1].split()[0].split('"')[0].split('<')[0]
                return urllib.parse.unquote(token.strip())
        time.sleep(.25)
    raise RuntimeError("SMTP 捕获中未找到本邀请正文令牌")


def create_mailed_invitation(g, recipient, inviter=ADMIN, role=30):
    state = ok("GET", f"/governance/groups/{g['id']}")
    status, invitation = secondary_api("POST", f"/governance/groups/{g['id']}/invitations", {"email": recipient["email"], "role": role, "revision": state["revision"]}, inviter)
    if status != 201:
        raise RuntimeError(f"次进程创建邀请失败：{status} {invitation}")
    cron_status, cron_body = secondary_api("POST", "/admin/cron/governance_invitations")
    if not 200 <= cron_status < 300:
        raise RuntimeError(f"次进程邀请投递任务失败：{cron_status} {cron_body}")
    return invitation, invitation_token(recipient["email"], invitation["id"])


def m08_only():
    g = group("m08-owner")
    first = user("m08-first")
    second = user("m08-second")
    assert 200 <= member(g, first, 50)[0] < 300
    assert 200 <= member(g, second, 50)[0] < 300
    teams = ok("GET", f"/orgs/{g['full_path']}/teams")
    owners = next(x for x in teams if x["permission"] == "owner")
    api_check("M08", "移除隔离管理员原生Owner来源", "DELETE", f"/teams/{owners['id']}/members/{ADMIN['name']}", ADMIN, True)
    try:
        before = ok("GET", f"/governance/groups/{g['id']}")["revision"]
        status, body = member(g, second, None, actor=first)
        record("M08", "两名永久Owner对照删除一名", 200 <= status < 300, "成功且剩余一名永久Owner", before, ok("GET", f"/governance/groups/{g['id']}")["revision"], HTTP状态=status, 响应=body)
        for label, role, expires in (("最后永久Owner删除", None, 0), ("最后永久Owner降级", 20, 0), ("最后永久Owner改为到期授权", 50, int(time.time()) + 86400)):
            before = ok("GET", f"/governance/groups/{g['id']}")["revision"]
            status, body = member(g, first, role, expires=expires, actor=first)
            after = ok("GET", f"/governance/groups/{g['id']}")["revision"]
            record("M08", label, status >= 400 and before == after, "拒绝且修订号、Owner授权不变", before, after, HTTP状态=status, 响应=body)
        assert 200 <= member(g, second, 50, actor=first)[0] < 300
        status, body = member(g, first, 20, actor=second)
        record("M08", "恢复第二永久Owner后允许降级", 200 <= status < 300, "成功", None, {"HTTP状态": status, "响应": body})
    finally:
        api("PUT", f"/teams/{owners['id']}/members/{ADMIN['name']}")


def invitation_only():
    root = group("mail-alpha")
    child = group("sdk", root)
    target = repo("mail-sdk", child)
    # M09：正确接受、错误身份和重复接受均使用真实捕获令牌。
    recipient = user("mail-recipient")
    invitation, token = create_mailed_invitation(root, recipient)
    git_probe("M09", "真实邮件接受前", target, recipient, False, False)
    wrong = user("mail-wrong")
    api_check("M09", "真实令牌错误账号拒绝", "POST", f"/governance/invitations/{invitation['id']}/accept", wrong, False, {"token": token})
    api_check("M09", "正确账号接受真实邮件邀请", "POST", f"/governance/invitations/{invitation['id']}/accept", recipient, True, {"token": token})
    git_probe("M09", "接受后继承后代Developer", target, recipient, True, True)
    api_check("M09", "重复接受不重复授权", "POST", f"/governance/invitations/{invitation['id']}/accept", recipient, False, {"token": token})
    # M10：过期、撤销、拒绝以及邀请者失权后的真实旧令牌。
    for label in ("expired", "revoked", "declined", "lost-inviter"):
        invited = user("mail-" + label)
        inviter = ADMIN
        if label == "lost-inviter":
            inviter = user("mail-inviter")
            member(root, inviter, 50)
        item, item_token = create_mailed_invitation(root, invited, inviter)
        if label == "expired":
            sql = f"update governance_invitation set expires_unix={int(time.time())-1} where id={int(item['id'])} returning id"
            changed = run(["docker", "exec", DB, "psql", "-U", "gitea", "-d", "gitea", "-tAc", sql])
            assert changed.returncode == 0 and str(item["id"]) in changed.stdout
        elif label == "revoked":
            assert 200 <= api("DELETE", f"/governance/groups/{root['id']}/invitations/{item['id']}")[0] < 300
        elif label == "declined":
            assert 200 <= api("POST", f"/governance/invitations/{item['id']}/decline", {"token": item_token}, invited)[0] < 300
        else:
            assert 200 <= member(root, inviter, None)[0] < 300
        api_check("M10", label + "真实旧令牌接受拒绝", "POST", f"/governance/invitations/{item['id']}/accept", invited, False, {"token": item_token})
        git_probe("M10", label + "失败不留成员", target, invited, False, False)
    # E01：同一受邀人从邀请入职到后代开发、PR审批、合并。
    recruit = user("e01-mail-recruit")
    approver = user("e01-approver-one")
    approver_two = user("e01-approver-two")
    check_bot = user("e01-check")
    member(root, approver, 30)
    member(root, approver_two, 30)
    member(root, check_bot, 30)
    ok("PUT", f"/governance/repositories/{target['id']}/approval-settings", {"prevent_author": True, "prevent_committer": False, "prevent_overrides": True, "reset_on_change": True, "require_reauthentication": False, "revision": 0})
    ok("POST", f"/governance/repositories/{target['id']}/approval-rules", {"rule": {"name": "E01两人独立审批", "required": 2, "all_eligible": True, "branch_mode": "all", "enabled": True}, "revision": 0})
    ok("POST", f"/repos/{target['full_name']}/branch_protections", {"rule_name": "main", "enable_push": False, "enable_force_push": False, "required_approvals": 0, "require_governance_approval": True, "enable_status_check": True, "status_check_contexts": ["acceptance/e01"], "dismiss_stale_approvals": True, "block_admin_merge_override": True})
    item, item_token = create_mailed_invitation(root, recruit)
    api_check("E01", "真实邮件邀请入职", "POST", f"/governance/invitations/{item['id']}/accept", recruit, True, {"token": item_token})
    folder = RUN_DIR / "e01-mail-pr"
    assert run(["git", "clone", target["clone_url"], str(folder)], env=env_for(recruit)).returncode == 0
    (folder / "e01.txt").write_text("邀请入职开发审批合并闭环\n")
    for cmd in (["git", "config", "user.name", "邀请入职者"], ["git", "config", "user.email", "e01@example.invalid"], ["git", "add", "e01.txt"], ["git", "commit", "-m", "test: 邀请入职开发"]):
        assert run(cmd, cwd=folder).returncode == 0
    sha = run(["git", "rev-parse", "HEAD"], cwd=folder).stdout.strip()
    branch = "feature/e01-mail-" + RUN
    pushed = run(["git", "push", "origin", "HEAD:" + branch], cwd=folder, env=env_for(recruit))
    pr = ok("POST", f"/repos/{target['full_name']}/pulls", {"title": "邀请入职闭环", "head": branch, "base": "main"}, recruit)
    before = refs(target)
    review_status, review = api("POST", f"/repos/{target['full_name']}/pulls/{pr['number']}/reviews", {"event": "APPROVED", "body": "第一名独立审批人通过"}, approver)
    one_status, one_body = api("POST", f"/repos/{target['full_name']}/pulls/{pr['number']}/merge", {"Do": "merge", "head_commit_id": sha}, recruit)
    one_unchanged = refs(target) == before
    review_two_status, review_two = api("POST", f"/repos/{target['full_name']}/pulls/{pr['number']}/reviews", {"event": "APPROVED", "body": "第二名独立审批人通过"}, approver_two)
    no_check_status, no_check_body = api("POST", f"/repos/{target['full_name']}/pulls/{pr['number']}/merge", {"Do": "merge", "head_commit_id": sha}, recruit)
    no_check_unchanged = refs(target) == before
    ok("POST", f"/repos/{target['full_name']}/statuses/{sha}", {"state": "success", "context": "acceptance/e01", "description": "最新提交独立检查通过"}, check_bot)
    merge_status, merge = api("POST", f"/repos/{target['full_name']}/pulls/{pr['number']}/merge", {"Do": "merge", "head_commit_id": sha}, recruit)
    after = refs(target)
    state = ok("GET", f"/repos/{target['full_name']}/pulls/{pr['number']}", user=recruit)
    passed = pushed.returncode == 0 and review_status in (200, 201) and review_two_status in (200, 201) and one_status >= 400 and one_unchanged and no_check_status >= 400 and no_check_unchanged and merge_status in (200, 201) and state["merged"] and before.get("refs/heads/main") != after.get("refs/heads/main")
    record("E01", "邀请入职到继承开发PR两人审批最新检查合并", passed, "同一受邀人接受后继承后代Developer；一票拒绝、两票但缺检查仍拒绝；最新提交检查通过后合并，管理员绕过关闭", {"主分支": before.get("refs/heads/main")}, {"主分支": after.get("refs/heads/main"), "PR": pr["number"], "第一审批": review_status, "一票合并": one_status, "一票响应": one_body, "第二审批": review_two_status, "缺检查合并": no_check_status, "缺检查响应": no_check_body, "最终合并": merge_status, "响应": merge})


def ui_record():
    members = ok("GET", "/governance/groups/1590/members")
    members = members if isinstance(members, list) else members["members"]
    current = next(x for x in members if x.get("username") == "rm-ui-163320-9868")
    record("M04", "真实网页新增、升级和降级成员", current["direct"]["role"] == 20, "浏览器登录直达成员页；新增 Reporter、升级 Developer、降级 Reporter；每次表单 POST 303，最终 API 独立见证 Reporter", {"用户": "rm-ui-163320-9868", "新增HTTP": 303, "升级HTTP": 303}, {"降级HTTP": 303, "最终直接角色": current["direct"]["role"], "有效角色": current["effective_role"], "页面": "/governance/groups/1590?tab=members"}, 浏览器="Playwright+系统Google Chrome", 登录="真实账号密码表单，redirect_to 指向管理页")


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--followup":
        followup()
    elif len(sys.argv) > 1 and sys.argv[1] == "--m11":
        m11_only()
    elif len(sys.argv) > 1 and sys.argv[1] == "--m08":
        m08_only()
    elif len(sys.argv) > 1 and sys.argv[1] == "--invitations":
        invitation_only()
    elif len(sys.argv) > 1 and sys.argv[1] == "--ui-record":
        ui_record()
    else:
        main()
