#!/usr/bin/env python3
"""在本轮隔离实例验证部署密钥边界，凭据和私钥只保存在临时目录。"""

import base64
import datetime
import json
import os
from pathlib import Path
import secrets
import subprocess
import urllib.error
import urllib.request


ROOT = Path(__file__).resolve().parents[1]
WORK = Path("/tmp/gitea-user-acceptance-20260918")
BASE = "http://127.0.0.1:3538"
OUT = ROOT / "docs/evidence/user-permission-20260918/deploy-key-results.jsonl"
ADMIN = {"name": "acceptance-admin", "password": json.loads((WORK / "secrets.json").read_text())["管理员密码"]}


def api(method, path, data=None):
    auth = base64.b64encode(f"{ADMIN['name']}:{ADMIN['password']}".encode()).decode()
    request = urllib.request.Request(
        BASE + "/api/v1" + path,
        data=json.dumps(data).encode() if data is not None else None,
        headers={"Authorization": "Basic " + auth, "Content-Type": "application/json"},
        method=method,
    )
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


def ok(method, path, data=None):
    status, body = api(method, path, data)
    if not 200 <= status < 300:
        raise RuntimeError(f"准备操作失败：{method} {path}，状态 {status}，响应 {body}")
    return body


def run(args, *, env=None, cwd=None):
    return subprocess.run(args, env=env, cwd=cwd, capture_output=True, text=True, timeout=90)


def ssh_env(key):
    env = os.environ.copy()
    for name in list(env):
        if name.startswith("GIT_"):
            del env[name]
    env.update({
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": "/dev/null",
        "GIT_TERMINAL_PROMPT": "0",
        "GIT_SSH_COMMAND": f"ssh -i {key} -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile={WORK}/known-hosts",
    })
    return env


def admin_env():
    env = os.environ.copy()
    for name in list(env):
        if name.startswith("GIT_"):
            del env[name]
    auth = base64.b64encode(f"{ADMIN['name']}:{ADMIN['password']}".encode()).decode()
    env.update({
        "GIT_CONFIG_NOSYSTEM": "1",
        "GIT_CONFIG_GLOBAL": "/dev/null",
        "GIT_CONFIG_COUNT": "2",
        "GIT_CONFIG_KEY_0": "http.extraHeader",
        "GIT_CONFIG_VALUE_0": "Authorization: Basic " + auth,
        "GIT_CONFIG_KEY_1": "credential.helper",
        "GIT_CONFIG_VALUE_1": "",
        "GIT_TERMINAL_PROMPT": "0",
    })
    return env


def refs(repo):
    result = run(["git", "ls-remote", repo["clone_url"]], env=admin_env())
    if result.returncode != 0:
        raise RuntimeError("管理员见证读取引用失败")
    return dict(line.split()[::-1] for line in result.stdout.splitlines())


def record(check, passed, **evidence):
    row = {
        "场景": "D05",
        "检查": check,
        "结果": "通过" if passed else "失败",
        "时间": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        **evidence,
    }
    with OUT.open("a") as stream:
        stream.write(json.dumps(row, ensure_ascii=False) + "\n")
    print("D05", check, row["结果"], flush=True)


def make_key(folder, name):
    path = folder / name
    result = run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(path)])
    if result.returncode != 0:
        raise RuntimeError("生成隔离部署密钥失败")
    return path


def commit(folder, filename, content):
    (folder / filename).write_text(content)
    commands = [
        ["git", "config", "user.name", "部署密钥验收"],
        ["git", "config", "user.email", "deploy-key@example.invalid"],
        ["git", "add", filename],
        ["git", "commit", "-m", "test: 验证部署密钥边界"],
    ]
    for command in commands:
        result = run(command, cwd=folder)
        if result.returncode != 0:
            raise RuntimeError("准备真实新提交失败")
    return run(["git", "rev-parse", "HEAD"], cwd=folder).stdout.strip()


def main():
    OUT.parent.mkdir(parents=True, exist_ok=True)
    if OUT.exists():
        raise RuntimeError("证据文件已存在，拒绝覆盖本轮结果")
    suffix = secrets.token_hex(4)
    target = ok("POST", "/orgs/alpha/repos", {"name": "deploy-key-target-" + suffix, "private": True, "auto_init": True, "default_branch": "main"})
    other = ok("POST", "/orgs/alpha/repos", {"name": "deploy-key-other-" + suffix, "private": True, "auto_init": True, "default_branch": "main"})
    ok("POST", f"/repos/{target['full_name']}/branch_protections", {"rule_name": "main", "enable_push": False, "enable_force_push": False})

    private = WORK / ("deploy-key-" + suffix)
    private.mkdir(mode=0o700)
    read_key = make_key(private, "read-only")
    write_key = make_key(private, "write")
    ok("POST", f"/repos/{target['full_name']}/keys", {"title": "D05隔离只读密钥", "key": Path(str(read_key) + ".pub").read_text(), "read_only": True})
    ok("POST", f"/repos/{target['full_name']}/keys", {"title": "D05隔离可写密钥", "key": Path(str(write_key) + ".pub").read_text(), "read_only": False})

    read_dir = private / "read-clone"
    result = run(["git", "clone", target["ssh_url"], str(read_dir)], env=ssh_env(read_key))
    record("只读部署密钥读取目标仓库", result.returncode == 0 and (read_dir / "README.md").exists(), 协议="SSH", 退出码=result.returncode, 仓库=target["full_name"], 输出=result.stderr[-1600:])
    read_sha = commit(read_dir, "read-key-test.txt", "只读部署密钥不得写入\n")
    before = refs(target)
    result = run(["git", "push", target["ssh_url"], "HEAD:refs/heads/feature/read-key-denied"], env=ssh_env(read_key), cwd=read_dir)
    after = refs(target)
    record("只读部署密钥真实新提交推送拒绝", result.returncode != 0 and before == after, 协议="SSH", 退出码=result.returncode, 本地SHA=read_sha, 操作前=before, 操作后=after, 输出=result.stderr[-1600:])

    write_dir = private / "write-clone"
    result = run(["git", "clone", target["ssh_url"], str(write_dir)], env=ssh_env(write_key))
    if result.returncode != 0:
        raise RuntimeError("可写部署密钥无法建立目标仓库工作副本")
    write_sha = commit(write_dir, "write-key-test.txt", "可写部署密钥普通分支提交\n")
    result = run(["git", "push", target["ssh_url"], "HEAD:refs/heads/feature/write-key-allowed"], env=ssh_env(write_key), cwd=write_dir)
    after = refs(target)
    record("可写部署密钥普通分支推送", result.returncode == 0 and after.get("refs/heads/feature/write-key-allowed") == write_sha, 协议="SSH", 退出码=result.returncode, 本地SHA=write_sha, 操作后=after, 输出=result.stderr[-1600:])

    before = refs(target)
    result = run(["git", "push", target["ssh_url"], "HEAD:refs/heads/main"], env=ssh_env(write_key), cwd=write_dir)
    after = refs(target)
    record("可写部署密钥保护主分支推送拒绝", result.returncode != 0 and before == after, 协议="SSH", 退出码=result.returncode, 本地SHA=write_sha, 操作前=before, 操作后=after, 输出=result.stderr[-1600:])

    result = run(["git", "ls-remote", other["ssh_url"]], env=ssh_env(write_key))
    record("目标仓库可写部署密钥跨仓库读取拒绝", result.returncode != 0, 协议="SSH", 退出码=result.returncode, 目标仓库=other["full_name"], 输出=result.stderr[-1600:])
    before = refs(other)
    result = run(["git", "push", other["ssh_url"], "HEAD:refs/heads/feature/cross-repo-denied"], env=ssh_env(write_key), cwd=write_dir)
    after = refs(other)
    record("目标仓库可写部署密钥跨仓库推送拒绝", result.returncode != 0 and before == after, 协议="SSH", 退出码=result.returncode, 本地SHA=write_sha, 操作前=before, 操作后=after, 目标仓库=other["full_name"], 输出=result.stderr[-1600:])


if __name__ == "__main__":
    main()
