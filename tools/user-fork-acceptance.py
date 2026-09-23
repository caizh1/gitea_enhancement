#!/usr/bin/env python3
"""在本轮隔离实例验证私有 Fork 与上游写权限边界。"""

import base64
import datetime
import json
import os
from pathlib import Path
import secrets
import subprocess
import time
import urllib.error
import urllib.request


ROOT = Path(__file__).resolve().parents[1]
WORK = Path("/tmp/gitea-user-acceptance-20260918")
BASE = "http://127.0.0.1:3538"
OUT = ROOT / "docs/evidence/user-permission-20260918/fork-results.jsonl"
ADMIN = {"name": "acceptance-admin", "password": json.loads((WORK / "secrets.json").read_text())["管理员密码"]}


def api(method, path, data=None, user=None):
    user = user or ADMIN
    if "token" in user:
        auth = "token " + user["token"]
    else:
        auth = "Basic " + base64.b64encode(f"{user['name']}:{user['password']}".encode()).decode()
    request = urllib.request.Request(BASE + "/api/v1" + path, data=json.dumps(data).encode() if data is not None else None, headers={"Authorization": auth, "Content-Type": "application/json"}, method=method)
    try:
        response = urllib.request.urlopen(request, timeout=40)
    except urllib.error.HTTPError as error:
        response = error
    raw = response.read()
    try:
        body = json.loads(raw)
    except ValueError:
        body = raw.decode(errors="replace")
    return response.status, body


def ok(method, path, data=None, user=None):
    status, body = api(method, path, data, user)
    if not 200 <= status < 300:
        raise RuntimeError(f"准备操作失败：{method} {path}，状态 {status}，响应 {body}")
    return body


def run(args, *, env=None, cwd=None):
    return subprocess.run(args, env=env, cwd=cwd, capture_output=True, text=True, timeout=90)


def git_env(user, key):
    env = os.environ.copy()
    for name in list(env):
        if name.startswith("GIT_"):
            del env[name]
    basic = base64.b64encode(f"{user['name']}:{user['token']}".encode()).decode()
    env.update({
        "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_COUNT": "2",
        "GIT_CONFIG_KEY_0": "http.extraHeader", "GIT_CONFIG_VALUE_0": "Authorization: Basic " + basic,
        "GIT_CONFIG_KEY_1": "credential.helper", "GIT_CONFIG_VALUE_1": "", "GIT_TERMINAL_PROMPT": "0",
        "GIT_SSH_COMMAND": f"ssh -i {key} -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile={WORK}/known-hosts",
    })
    return env


def admin_env():
    return git_env({"name": ADMIN["name"], "token": ADMIN["password"]}, WORK / "unused-admin-key")


def refs(repo):
    result = run(["git", "ls-remote", repo["clone_url"]], env=admin_env())
    if result.returncode != 0:
        raise RuntimeError("管理员见证读取引用失败")
    return dict(line.split()[::-1] for line in result.stdout.splitlines())


def record(check, passed, **evidence):
    row = {"场景": "E09", "检查": check, "结果": "通过" if passed else "失败", "时间": datetime.datetime.now(datetime.timezone.utc).isoformat(), **evidence}
    with OUT.open("a") as stream:
        stream.write(json.dumps(row, ensure_ascii=False) + "\n")
    print("E09", check, row["结果"], flush=True)


def commit(folder, protocol):
    filename = "fork-" + protocol.lower() + ".txt"
    (folder / filename).write_text("私有 Fork 开发验收：" + protocol + "\n")
    for command in (["git", "config", "user.name", "Fork验收用户"], ["git", "config", "user.email", "fork-user@example.invalid"], ["git", "add", filename], ["git", "commit", "-m", "test: 验证私有 Fork 权限边界"]):
        result = run(command, cwd=folder)
        if result.returncode != 0:
            raise RuntimeError("准备 Fork 新提交失败")
    return run(["git", "rev-parse", "HEAD"], cwd=folder).stdout.strip()


def main():
    OUT.parent.mkdir(parents=True, exist_ok=True)
    if OUT.exists():
        raise RuntimeError("证据文件已存在，拒绝覆盖本轮结果")
    suffix = secrets.token_hex(4)
    username = "fork-user-" + suffix
    password = secrets.token_urlsafe(24)
    created = ok("POST", "/admin/users", {"username": username, "password": password, "email": username + "@example.invalid", "must_change_password": False})
    user = {"name": username, "password": password, "id": created["id"]}
    token = ok("POST", f"/users/{username}/tokens", {"name": "E09隔离验收", "scopes": ["all"]}, user)
    user["token"] = token["sha1"]
    key = WORK / (username + "-key")
    result = run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)])
    if result.returncode != 0:
        raise RuntimeError("生成普通用户 SSH 密钥失败")
    ok("POST", "/user/keys", {"title": "E09隔离验收", "key": Path(str(key) + ".pub").read_text()}, user)

    upstream = ok("POST", "/orgs/alpha/repos", {"name": "fork-upstream-" + suffix, "private": True, "auto_init": True, "default_branch": "main"})
    ok("PUT", f"/repos/{upstream['full_name']}/collaborators/{username}", {"permission": "read"})
    permission = ok("GET", f"/repos/{upstream['full_name']}/collaborators/{username}/permission")
    record("普通用户仅有上游读取权限", permission.get("permission") == "read", 用户=username, 上游=upstream["full_name"], 权限=permission.get("permission"))

    fork_name = "private-fork-" + suffix
    status, body = api("POST", f"/repos/{upstream['full_name']}/forks", {"name": fork_name}, user)
    if status != 202:
        record("通过API创建用户私有Fork", False, HTTP状态=status, 响应=str(body)[:1000])
        return
    fork = None
    for _ in range(60):
        check_status, candidate = api("GET", f"/repos/{username}/{fork_name}", user=user)
        if check_status == 200:
            fork = candidate
            break
        time.sleep(0.5)
    record("通过API创建用户私有Fork", fork is not None and fork.get("private") is True and fork.get("fork") is True, HTTP状态=status, 用户=username, Fork仓库=f"{username}/{fork_name}", 是否私有=fork.get("private") if fork else None, 是否Fork=fork.get("fork") if fork else None)
    if fork is None:
        return

    for protocol, clone_url in (("HTTP", fork["clone_url"]), ("SSH", fork["ssh_url"])):
        folder = WORK / ("fork-e09-" + suffix + "-" + protocol.lower())
        result = run(["git", "clone", clone_url, str(folder)], env=git_env(user, key))
        record("克隆本人私有Fork", result.returncode == 0 and (folder / "README.md").exists(), 协议=protocol, 退出码=result.returncode, 输出=result.stderr[-1400:])
        if result.returncode != 0:
            continue
        sha = commit(folder, protocol)
        branch = "feature/e09-" + protocol.lower()
        result = run(["git", "push", clone_url, "HEAD:refs/heads/" + branch], env=git_env(user, key), cwd=folder)
        fork_refs = refs(fork)
        record("向本人私有Fork推送真实新提交", result.returncode == 0 and fork_refs.get("refs/heads/" + branch) == sha, 协议=protocol, 退出码=result.returncode, 本地SHA=sha, Fork引用=fork_refs, 输出=result.stderr[-1400:])

        pr_status, pr = api("POST", f"/repos/{upstream['full_name']}/pulls", {"title": "E09 私有 Fork " + protocol, "head": username + ":" + branch, "base": "main", "body": "私有 Fork 权限边界验收"}, user)
        record("由Fork向上游创建PR", pr_status == 201 and pr.get("head", {}).get("sha") == sha and pr.get("base", {}).get("repo", {}).get("full_name") == upstream["full_name"], 协议=protocol, HTTP状态=pr_status, PR编号=pr.get("number") if isinstance(pr, dict) else None, 本地SHA=sha)

        upstream_url = upstream["clone_url"] if protocol == "HTTP" else upstream["ssh_url"]
        before = refs(upstream)
        result = run(["git", "push", upstream_url, "HEAD:refs/heads/direct-e09-" + protocol.lower()], env=git_env(user, key), cwd=folder)
        after = refs(upstream)
        record("同一用户直接推送上游拒绝", result.returncode != 0 and before == after, 协议=protocol, 退出码=result.returncode, 本地SHA=sha, 操作前=before, 操作后=after, 输出=result.stderr[-1600:])


if __name__ == "__main__":
    main()
