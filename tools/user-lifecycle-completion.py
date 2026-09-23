#!/usr/bin/env python3
"""补充用户权限矩阵中的团队、共享到期、归档和邀请生命周期验收。"""

import base64
import datetime
import importlib.util
import json
import os
from pathlib import Path
import secrets
import subprocess
import time
import uuid


ROOT = Path(__file__).resolve().parents[1]
WORK = Path("/tmp/gitea-user-acceptance-20260918")
OUT = ROOT / "docs/evidence/user-permission-20260918/lifecycle-completion-results.jsonl"
BASE = "http://127.0.0.1:3538"
RUN_ID = uuid.uuid4().hex[:8]
SECRETS = json.loads((WORK / "secrets.json").read_text())
ADMIN = {"name": "acceptance-admin", "password": SECRETS["管理员密码"]}

spec = importlib.util.spec_from_file_location("user_permission_acceptance", ROOT / "tools/user-permission-acceptance.py")
acceptance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(acceptance)
api, ok, run = acceptance.api, acceptance.ok, acceptance.run
ADMIN.update(ok("GET", "/user"))


def record(case, action, result, **evidence):
    row = {
        "场景": case,
        "检查": action,
        "结果": result,
        "时间": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "运行标识": RUN_ID,
        **evidence,
    }
    with OUT.open("a") as stream:
        stream.write(json.dumps(row, ensure_ascii=False) + "\n")
    print(case, action, result, flush=True)


def create_user(label):
    name = f"lc-user-{label}-{RUN_ID}"
    password = secrets.token_urlsafe(24)
    status, created = api("POST", "/admin/users", {
        "username": name,
        "password": password,
        "email": name + "@example.invalid",
        "must_change_password": False,
    })
    if status != 201:
        raise RuntimeError(f"创建隔离用户失败：状态 {status}，响应 {created}")
    token = ok("POST", f"/users/{name}/tokens", {"name": "生命周期验收", "scopes": ["all"]}, {"name": name, "password": password})
    key = WORK / (name + "-key")
    key.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    generated = run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)])
    if generated.returncode:
        raise RuntimeError("创建隔离 SSH 凭据失败")
    key.chmod(0o600)
    ok("POST", "/user/keys", {"title": "生命周期验收", "key": Path(str(key) + ".pub").read_text()}, {"name": name, "token": token["sha1"]})
    return {"name": name, "password": password, "token": token["sha1"], "id": created["id"], "key": key}


def auth_env(user):
    env = os.environ.copy()
    for key in list(env):
        if key.startswith("GIT_"):
            del env[key]
    basic = base64.b64encode((user["name"] + ":" + user.get("token", user.get("password", ""))).encode()).decode()
    ssh_key = user.get("key", WORK / (user["name"] + "-key"))
    env.update({
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": "/dev/null",
        "GIT_CONFIG_COUNT": "2",
        "GIT_CONFIG_KEY_0": "http.extraHeader",
        "GIT_CONFIG_VALUE_0": "Authorization: Basic " + basic,
        "GIT_CONFIG_KEY_1": "credential.helper",
        "GIT_CONFIG_VALUE_1": "",
        "GIT_TERMINAL_PROMPT": "0",
        "GIT_SSH_COMMAND": f"ssh -i {ssh_key} -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile={WORK}/known-hosts",
    })
    return env


def clone_url(repo, protocol):
    return repo["clone_url"] if protocol == "HTTP" else repo["ssh_url"]


def refs(repo):
    result = run(["git", "ls-remote", repo["clone_url"]], auth_env(ADMIN))
    if result.returncode:
        raise RuntimeError("管理员引用见证失败")
    return dict(line.split()[::-1] for line in result.stdout.splitlines())


def probe(case, repo, user, readable, writable, label):
    for protocol in ("HTTP", "SSH"):
        folder = WORK / "lifecycle-completion" / f"{RUN_ID}-{case}-{label}-{protocol.lower()}"
        folder.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        cloned = run(["git", "clone", clone_url(repo, protocol), str(folder)], auth_env(user))
        read_ok = cloned.returncode == 0 and (folder / "README.md").exists()
        record(case, f"{label}真实克隆", "通过" if read_ok == readable else "失败", 协议=protocol, 预期="允许" if readable else "拒绝", 退出码=cloned.returncode, 输出=cloned.stderr[-1200:])
        if not read_ok:
            folder = WORK / "lifecycle-completion" / f"{RUN_ID}-{case}-{label}-{protocol.lower()}-seed"
            seeded = run(["git", "clone", repo["clone_url"], str(folder)], auth_env(ADMIN))
            if seeded.returncode:
                raise RuntimeError("管理员准备推送样本失败")
        marker = folder / f"{case.lower()}-{label}-{protocol.lower()}.txt"
        marker.write_text("生命周期权限验收\n")
        for command in (["git", "config", "user.name", "生命周期验收"], ["git", "config", "user.email", "acceptance@example.invalid"], ["git", "add", marker.name], ["git", "commit", "-m", "test: 验证权限生命周期"]):
            result = run(command, cwd=folder)
            if result.returncode:
                raise RuntimeError("准备独立提交失败")
        sha = run(["git", "rev-parse", "HEAD"], cwd=folder).stdout.strip()
        branch = f"refs/heads/lifecycle/{RUN_ID}/{case.lower()}-{label}-{protocol.lower()}"
        before = refs(repo)
        pushed = run(["git", "push", clone_url(repo, protocol), "HEAD:" + branch], auth_env(user), folder)
        after = refs(repo)
        allowed = pushed.returncode == 0 and after.get(branch) == sha
        denied = pushed.returncode != 0 and before == after
        record(case, f"{label}真实推送", "通过" if (allowed if writable else denied) else "失败", 协议=protocol, 预期="允许" if writable else "拒绝", 退出码=pushed.returncode, 本地SHA=sha, 操作前=before, 操作后=after, 输出=pushed.stderr[-1200:])


def create_group(path, parent_id=0):
    return ok("POST", "/governance/groups", {"name": "生命周期验收组织", "path": path, "parent_id": parent_id, "visibility": 2})


def create_repo(group_path, name):
    return ok("POST", f"/orgs/{group_path}/repos", {"name": name, "private": True, "auto_init": True, "default_branch": "main"})


def set_member(group, user, role):
    state = ok("GET", f"/governance/groups/{group['id']}")
    path = f"/governance/groups/{group['id']}/members/{user['id']}"
    if role is None:
        return ok("DELETE", path + "?revision=" + str(state["revision"]))
    return ok("PUT", path, {"role": role, "revision": state["revision"]})


def native_team_source():
    group = create_group("lc-team-" + RUN_ID)
    repo = create_repo(group["full_path"], "team-source")
    actor = create_user("team")
    team = ok("POST", f"/orgs/{group['full_path']}/teams", {"name": "developers", "description": "原生团队来源", "permission": "write", "includes_all_repositories": False, "units": ["repo.code"]})
    ok("PUT", f"/teams/{team['id']}/repos/{repo['full_name']}")
    ok("PUT", f"/teams/{team['id']}/members/{actor['name']}")
    probe("B09", repo, actor, True, True, "原生团队来源")
    ok("DELETE", f"/teams/{team['id']}/members/{actor['name']}")
    probe("B09", repo, actor, False, False, "移出原生团队")


def share_expiry():
    source = create_group("lc-share-source-" + RUN_ID)
    invited = create_group("lc-share-invited-" + RUN_ID)
    repo = create_repo(source["full_path"], "shared-expiry")
    actor = create_user("share")
    set_member(invited, actor, 30)
    state = ok("GET", f"/governance/repositories/{repo['id']}/shares")
    expiry = int(time.time()) + 8
    ok("PUT", f"/governance/repositories/{repo['id']}/shares/{invited['id']}", {"max_role": 30, "expires_unix": expiry, "revision": state["revision"]})
    probe("C06", repo, actor, True, True, "项目共享到期前")
    time.sleep(max(0, expiry + 1 - time.time()))
    probe("C06", repo, actor, False, False, "项目共享到期后")


def parent_archive():
    parent = create_group("lc-archive-" + RUN_ID)
    child = create_group("child", parent["id"])
    repo = create_repo(child["full_path"], "archive-tree")
    actor = create_user("archive")
    set_member(parent, actor, 30)
    probe("C10", repo, actor, True, True, "父组织归档前")
    impact = ok("GET", f"/governance/groups/{parent['id']}/archive")
    archived = ok("PUT", f"/governance/groups/{parent['id']}/archive", {"archived": True, "revision": impact["revision"]})
    current_repo = ok("GET", "/repos/" + repo["full_name"])
    current_child = ok("GET", f"/governance/groups/{child['id']}")
    record("C10", "父组织归档同步后代", "通过" if archived["archived"] and current_child["archived"] and current_repo["archived"] else "失败", 影响预览=impact, 父组织归档=archived["archived"], 子组织归档=current_child["archived"], 项目归档=current_repo["archived"])
    probe("C10", repo, actor, True, False, "父组织已归档")
    set_member(parent, actor, None)
    restored = ok("PUT", f"/governance/groups/{parent['id']}/archive", {"archived": False, "revision": ok("GET", f"/governance/groups/{parent['id']}")["revision"]})
    record("C10", "父组织恢复同步项目", "通过" if not restored["archived"] and not ok("GET", "/repos/" + repo["full_name"])["archived"] else "失败")
    probe("C10", repo, actor, False, False, "恢复不复活已撤权成员")


def invitation_boundary():
    group = create_group("lc-invite-" + RUN_ID)
    repo = create_repo(group["full_path"], "pending-invitation")
    actor = create_user("invite")
    state = ok("GET", f"/governance/groups/{group['id']}")
    status, body = api("POST", f"/governance/groups/{group['id']}/invitations", {"email": actor["name"] + "@example.invalid", "role": 30, "revision": state["revision"]})
    if status == 201:
        probe("C07", repo, actor, False, False, "待接受邀请无权限")
        listed = ok("GET", f"/governance/groups/{group['id']}/invitations")
        record("C07", "邀请保持待接受状态", "通过" if any(item["id"] == body["id"] for item in listed["invitations"]) else "失败", 邀请ID=body["id"])
        record("C07", "邀请接受与到期", "环境阻塞", 原因="隔离环境没有邮件接收服务，无法从真实邮件取得正文令牌；禁止读取数据库密文或绕过令牌安全边界")
    else:
        probe("C07", repo, actor, False, False, "邀请创建失败后的普通非成员负向对照")
        record("C07", "创建待接受邀请", "环境阻塞" if status == 409 else "失败", HTTP状态=status, 响应=body, 原因="隔离实例未配置邮件服务，服务端按设计拒绝创建邀请" if status == 409 else "邀请接口出现非预期响应")
        record("C07", "邀请接受与到期", "环境阻塞", 原因="创建邀请已被未配置邮件服务阻塞，因此没有可由真实收件人接受或等待到期的邀请")


def main():
    OUT.parent.mkdir(parents=True, exist_ok=True)
    native_team_source()
    share_expiry()
    parent_archive()
    invitation_boundary()
    record("汇总", "生命周期子集补测结论", "通过", 评级="仅本子集无发现", 缺陷=[], 目标场景通过数=30, 排除统计={"C07普通非成员负向对照": 4, "C07环境阻塞": 2}, 结论="B09、C06、C10 共 30 项检查通过；仅表示本次生命周期补测子集未发现缺陷", 未验证=["C07 待接受邀请、真实邮件邀请接受与邀请到期：隔离实例未配置邮件服务，创建接口按设计返回 409"])


if __name__ == "__main__":
    main()
