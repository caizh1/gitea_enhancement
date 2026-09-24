#!/usr/bin/env python3
"""在隔离实例用正式 API 建立作业令牌边界夹具和专用 Runner。"""

import argparse
import base64
import hashlib
import http.cookiejar
import json
import pathlib
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--fixture", type=pathlib.Path, required=True)
    parser.add_argument("--owner-user", required=True)
    parser.add_argument("--actor-user", required=True)
    parser.add_argument("--runner-binary", type=pathlib.Path, required=True)
    parser.add_argument("--runner-label", default="phase1-token-closure:host")
    args = parser.parse_args()
    if args.fixture.exists():
        raise RuntimeError("夹具已存在；不得覆盖或复用同一组对象")
    args.fixture.parent.mkdir(parents=True, exist_ok=True)
    api = Client(args.base, args.credentials, args.owner_user)
    suffix = str(time.time_ns())
    state = {"说明": "独立真实任务令牌夹具；凭据另存私有目录", "实例": args.base,
        "identities": {"owner": args.owner_user, "actor": args.actor_user}, "repos": {}, "步骤": []}

    def save():
        args.fixture.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def call(label, path, method="GET", body=None, user=None, expected=200):
        status, result = api.call(path, method, body, user)
        state["步骤"].append({"检查": label, "实际": status, "期望": expected, "通过": status == expected})
        save()
        if status != expected:
            raise AssertionError(label + "：HTTP " + str(status))
        return result

    owner_user = call("普通 Owner 身份", "/api/v1/user")
    actor_user = call("普通触发者身份", "/api/v1/user", user=args.actor_user)
    if owner_user["is_admin"] or actor_user["is_admin"]:
        raise AssertionError("验收账号不能是实例管理员")
    state["triggerActor"] = {"userID": actor_user["id"], "sourceGroupRole": 30}
    group = call("建私有源群组", "/api/v1/governance/groups", "POST",
        {"name": "一期令牌独立验收", "path": "phase1-token-" + suffix, "parent_id": 0, "visibility": 2}, expected=201)
    state["ownerID"], state["owner"] = group["id"], group["compatibility_name"]
    save()
    for name in ("token-source", "token-allowed", "token-denied"):
        repo = call("建私有仓 " + name, "/api/v1/orgs/" + state["owner"] + "/repos", "POST",
            {"name": name, "private": True, "auto_init": True}, expected=201)
        state["repos"][name] = {"id": repo["id"], "name": name, "default_branch": repo["default_branch"], "private": True}
        save()
    group = call("读源群组修订", "/api/v1/governance/groups/" + str(state["ownerID"]))
    call("触发者临时 Developer", f"/api/v1/governance/groups/{state['ownerID']}/members/{actor_user['id']}",
        "PUT", {"role": 30, "revision": group["revision"]}, expected=204)

    foreign = call("建不同 Owner 私有群组", "/api/v1/governance/groups", "POST",
        {"name": "一期令牌不同 Owner 对照", "path": "phase1-token-foreign-" + suffix, "parent_id": 0, "visibility": 2}, expected=201)
    state["foreign"] = {"groupID": foreign["id"], "owner": foreign["compatibility_name"],
        "repoName": "foreign-private", "packageName": "token-foreign-" + suffix,
        "defaultBranch": "main"}
    save()
    foreign = call("读不同 Owner 群组修订", "/api/v1/governance/groups/" + str(foreign["id"]))
    call("触发者在不同 Owner 有 Reporter", f"/api/v1/governance/groups/{foreign['id']}/members/{actor_user['id']}",
        "PUT", {"role": 20, "revision": foreign["revision"]}, expected=204)
    repo = call("建不同 Owner 私有仓", "/api/v1/orgs/" + state["foreign"]["owner"] + "/repos",
        "POST", {"name": "foreign-private", "private": True, "auto_init": True}, expected=201)
    state["foreign"]["repoID"] = repo["id"]
    state["foreign"]["defaultBranch"] = repo["default_branch"]
    save()

    def put_package(owner, name, payload):
        password = api.credentials.get(args.owner_user, api.credentials.get("password"))
        auth = base64.b64encode((args.owner_user + ":" + password).encode()).decode()
        path = "/api/packages/" + owner + "/generic/" + name + "/1.0.0/payload.txt"
        request = urllib.request.Request(args.base.rstrip("/") + path, data=payload, method="PUT",
            headers={"Authorization": "Basic " + auth, "Content-Type": "application/octet-stream"})
        with api.opener.open(request, timeout=30) as response:
            status = response.status
        state["步骤"].append({"检查": "Owner 发布私有 Generic 包", "实际": status, "期望": 201, "通过": status == 201})
        save()
        if status != 201:
            raise AssertionError("Generic 发布 HTTP " + str(status))
        return hashlib.sha256(payload).hexdigest()

    same_name = "token-same-" + suffix
    state["samePackage"] = {"name": same_name, "version": "1.0.0",
        "sha256": put_package(state["owner"], same_name, ("一期同 Owner 私有包 " + suffix + "\n").encode())}
    state["foreign"]["packageSHA256"] = put_package(state["foreign"]["owner"], state["foreign"]["packageName"],
        ("一期不同 Owner 私有包 " + suffix + "\n").encode())
    save()

    jar = http.cookiejar.CookieJar()
    web = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(jar))
    def web_request(path, fields=None):
        data = urllib.parse.urlencode(fields).encode() if fields is not None else None
        request = urllib.request.Request(args.base.rstrip("/") + path, data=data,
            headers={"Content-Type": "application/x-www-form-urlencoded"})
        with web.open(request, timeout=30) as response:
            return response.status
    password = api.credentials.get(args.owner_user, api.credentials.get("password"))
    if web_request("/user/login", {"user_name": args.owner_user, "password": password}) != 200:
        raise AssertionError("Owner Web 登录失败")
    settings = "/org/" + state["owner"] + "/settings/actions/general"
    if web_request(settings) != 200 or web_request(settings,
        {"cross_repo_add_target": "true", "cross_repo_add_target_name": "token-allowed"}) != 200:
        raise AssertionError("同 Owner 目标 allow-list 设置失败")
    state["allowlist"] = {"ownerID": state["ownerID"], "targetRepoID": state["repos"]["token-allowed"]["id"],
        "targetRepoName": "token-allowed", "settingsHTTP": 200}
    save()

    token = call("获取群组 Runner 注册令牌", "/api/v1/orgs/" + state["owner"] + "/actions/runners/registration-token", "POST")["token"]
    runner_dir = args.fixture.parent / "runner"
    runner_dir.mkdir(mode=0o700, exist_ok=False)
    secret = runner_dir / "registration-token.json"
    secret.write_text(json.dumps({"token": token}) + "\n")
    secret.chmod(0o600)
    config = runner_dir / "runner.yaml"
    config.write_text(f"log:\n  level: info\nrunner:\n  file: {runner_dir}/.runner\n  capacity: 1\n  fetch_interval: 2s\n  labels:\n    - {args.runner_label}\ncache:\n  enabled: false\nhost:\n  workdir_parent: {runner_dir}/jobs\n")
    config.chmod(0o600)
    runner_name = "phase1-token-closure-" + suffix
    with (runner_dir / "registration.log").open("w") as log:
        registration = subprocess.run([str(args.runner_binary), "register", "--no-interactive", "--config", str(config),
            "--instance", args.base, "--token", token, "--name", runner_name, "--labels", args.runner_label],
            stdout=log, stderr=subprocess.STDOUT, timeout=30)
    if registration.returncode != 0:
        raise RuntimeError("Runner 注册失败，退出码 " + str(registration.returncode))
    with (runner_dir / "daemon.log").open("ab") as log:
        daemon = subprocess.Popen([str(args.runner_binary), "daemon", "--config", str(config)],
            stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    (runner_dir / "daemon.pid").write_text(str(daemon.pid) + "\n")
    runner_id = None
    for _ in range(30):
        listing = call("查询群组 Runner", "/api/v1/orgs/" + state["owner"] + "/actions/runners")
        matches = [entry for entry in listing.get("runners", listing.get("entries", [])) if entry["name"] == runner_name]
        if len(matches) == 1 and matches[0]["status"] == "online":
            runner_id = matches[0]["id"]
            break
        time.sleep(1)
    if runner_id is None:
        raise TimeoutError("专用 Runner 未上线")
    state["runner"] = {"id": runner_id, "name": runner_name,
        "labelName": args.runner_label.split(":", 1)[0], "labelSpec": args.runner_label,
        "daemonPID": daemon.pid, "privateConfig": str(config)}
    state["结果"] = "夹具可用"
    save()
    print("夹具可用：群组", state["ownerID"], "Runner", runner_id, "详情", args.fixture)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
