#!/usr/bin/env python3
"""在隔离实例运行独立作业令牌的 Git、Generic 和撤权屏障验收。"""

import argparse
import base64
import http.server
import json
import pathlib
import re
import sys
import threading
import time
import urllib.error
import urllib.request


class Client:
    """用私有凭据调用正式 HTTP API，结果中不保存认证头。"""

    def __init__(self, base, credentials, owner):
        self.base = base.rstrip("/")
        self.credentials = json.loads(pathlib.Path(credentials).read_text())
        self.owner = owner
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def call(self, path, method="GET", body=None, user=None):
        user = user or self.owner
        password = self.credentials.get(user, self.credentials.get("password"))
        if not isinstance(password, str):
            raise ValueError("私有凭据缺少账号 " + user)
        auth = base64.b64encode((user + ":" + password).encode()).decode()
        raw_body = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.base + path, method=method, data=raw_body,
            headers={"Authorization": "Basic " + auth, "Content-Type": "application/json"})
        try:
            with self.opener.open(req, timeout=30) as response:
                code, raw, content_type = response.status, response.read(), response.headers.get("Content-Type", "")
        except urllib.error.HTTPError as error:
            code, raw, content_type = error.code, error.read(), error.headers.get("Content-Type", "")
            error.close()
        return code, json.loads(raw) if raw and "json" in content_type else raw


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--fixture", type=pathlib.Path, required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    args = parser.parse_args()
    state_path = args.state
    if state_path.exists():
        raise RuntimeError("结果已存在；不得重复调度同一验收任务")
    state_path.parent.mkdir(parents=True, exist_ok=True)
    fixture = json.loads(args.fixture.read_text())
    api = Client(args.base, args.credentials, fixture["identities"]["owner"])
    owner = fixture["owner"]
    foreign = fixture["foreign"]
    repos = fixture["repos"]
    state = {"说明": "独立 Runner jobToken 真协议验收", "fixture": fixture, "步骤": []}

    def save():
        state_path.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def check(label, actual, expected):
        passed = actual in expected if isinstance(expected, (set, list)) else actual == expected
        state["步骤"].append({"检查": label, "实际": actual, "期望": sorted(expected) if isinstance(expected, set) else expected, "通过": passed})
        save()
        if not passed:
            raise AssertionError(label + "：" + str(actual))

    def request(label, path, method="GET", body=None, user=None, expected=200):
        code, result = api.call(path, method, body, user)
        check(label, code, expected)
        return result

    released = threading.Event()
    ready = threading.Event()

    class Barrier(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path != "/ready":
                self.send_error(404)
                return
            ready.set()
            if not released.wait(120):
                self.send_error(504)
                return
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"go")

        def log_message(self, *_):
            pass

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Barrier)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    state["屏障"] = {"本机端口": server.server_address[1]}
    save()

    probe = r'''import base64, hashlib, os, pathlib, subprocess, tempfile, urllib.error, urllib.request
token = os.environ["GITEA_TOKEN"]
base = CONFIG["base"]
owner = CONFIG["owner"]
foreign = CONFIG["foreign"]
client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
basic = "Basic " + base64.b64encode(("oauth2:" + token).encode()).decode()
def http(label, path, expected, digest=None, bearer=False):
    header = {"Authorization": ("token " + token) if bearer else basic}
    request = urllib.request.Request(path if path.startswith("http://") else base + path, headers=header)
    try:
        with client.open(request, timeout=20) as response:
            code, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        code, raw = error.code, error.read(1024)
        error.close()
    print("BOUNDARY %s=%s" % (label, code), flush=True)
    assert code in expected, (label, code, expected)
    if digest:
        actual = hashlib.sha256(raw).hexdigest()
        print("BOUNDARY %s_sha=%s" % (label, actual), flush=True)
        assert actual == digest, (label, actual)
def git(label, target_owner, repo, should_succeed):
    with tempfile.TemporaryDirectory() as directory:
        askpass = pathlib.Path(directory) / "askpass.sh"
        askpass.write_text('#!/bin/sh\ncase "$1" in *Username*) printf "oauth2\\n";; *) printf "%s\\n" "$GITEA_TOKEN";; esac\n')
        askpass.chmod(0o700)
        env = os.environ.copy()
        env.update({"GIT_ASKPASS": str(askpass), "GIT_TERMINAL_PROMPT": "0", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null"})
        url = base + "/" + target_owner + "/" + repo + ".git"
        result = subprocess.run(["git", "-c", "credential.helper=", "ls-remote", url], env=env, capture_output=True, timeout=25)
    ok = result.returncode == 0 and bool(result.stdout.strip())
    print("BOUNDARY %s_exit=%s" % (label, result.returncode), flush=True)
    print("BOUNDARY %s_refs=%s" % (label, len(result.stdout.splitlines()) if ok else 0), flush=True)
    assert ok == should_succeed, (label, result.returncode, ok)
def phase(prefix, authorized):
    git(prefix + "_git_self", owner, CONFIG["sourceRepo"], authorized)
    git(prefix + "_git_allowed", owner, CONFIG["allowedRepo"], authorized)
    git(prefix + "_git_denied", owner, CONFIG["deniedRepo"], False)
    git(prefix + "_git_foreign", foreign["owner"], foreign["repoName"], False)
    http(prefix + "_generic_same", "/api/packages/" + owner + "/generic/" + CONFIG["samePackage"] + "/" + CONFIG["sameVersion"] + "/payload.txt", {200} if authorized else {401,403,404}, CONFIG["sameSHA"] if authorized else None)
    http(prefix + "_generic_foreign", "/api/packages/" + foreign["owner"] + "/generic/" + foreign["packageName"] + "/1.0.0/payload.txt", {401,403,404})
    http(prefix + "_actions_cross", "/api/v1/repos/" + owner + "/" + CONFIG["allowedRepo"] + "/actions/artifacts", {401,403,404}, bearer=True)
phase("before", True)
http("barrier", "/ready", {200})
phase("after", False)
'''
    config = {
        "base": args.base.rstrip("/"), "owner": owner,
        "foreign": {"owner": foreign["owner"], "repoName": foreign["repoName"], "packageName": foreign["packageName"]},
        "sourceRepo": repos["token-source"]["name"], "allowedRepo": repos["token-allowed"]["name"],
        "deniedRepo": repos["token-denied"]["name"],
        "samePackage": fixture["samePackage"]["name"], "sameVersion": fixture["samePackage"]["version"],
        "sameSHA": fixture["samePackage"]["sha256"],
    }
    probe = probe.replace("CONFIG", repr(config))
    probe = probe.replace('http("barrier", "/ready", {200})', f'http("barrier", "http://127.0.0.1:{server.server_address[1]}/ready", {{200}})')
    workflow = "name: job token boundary\non:\n  workflow_dispatch:\njobs:\n  probe:\n    runs-on: " + fixture["runner"]["labelName"] + "\n    permissions:\n      contents: read\n      packages: read\n      actions: read\n    env:\n      GITEA_TOKEN: ${{ secrets.GITEA_TOKEN }}\n    steps:\n      - name: Real Git and Generic client probes\n        run: |\n          set +x\n          python3 - <<'PY'\n" + "".join("          " + line + "\n" for line in probe.splitlines()) + "          PY\n"

    try:
        for name in ("token-source", "token-allowed", "token-denied"):
            repo = repos[name]
            path = "/api/v1/repos/" + owner + "/" + repo["name"] + "/contents/boundary.txt"
            body = {"content": base64.b64encode(("一期令牌边界 " + name + "\n").encode()).decode(), "message": "加入令牌测试数据", "branch": repo["default_branch"]}
            request("新建数据 " + name, path, "POST", body, expected=201)
        path = "/api/v1/repos/" + foreign["owner"] + "/" + foreign["repoName"] + "/contents/boundary.txt"
        body = {"content": base64.b64encode("一期不同 Owner 私有 Git\n".encode()).decode(), "message": "加入跨 Owner 对照数据", "branch": foreign["defaultBranch"]}
        request("新建不同 Owner 数据", path, "POST", body, expected=201)
        actor_name = fixture["identities"]["actor"]
        actor = request("确认触发者身份", "/api/v1/user", user=actor_name)
        check("触发者非实例管理员", actor["is_admin"], False)
        request("确认触发者有源仓权限", "/api/v1/repos/" + owner + "/" + repos["token-source"]["name"], user=actor_name)
        request("确认触发者可读不同 Owner 仓", "/api/v1/repos/" + foreign["owner"] + "/" + foreign["repoName"], user=actor_name)
        path = "/api/v1/repos/" + owner + "/" + repos["token-source"]["name"] + "/contents/.gitea/workflows/token-boundary.yml"
        body = {"content": base64.b64encode(workflow.encode()).decode(), "message": "加入独立令牌真实验收", "branch": repos["token-source"]["default_branch"]}
        request("写入固定工作流", path, "POST", body, expected=201)
        dispatch = "/api/v1/repos/" + owner + "/" + repos["token-source"]["name"] + "/actions/workflows/token-boundary.yml/dispatches?return_run_details=true"
        response = request("普通 Developer 触发 Run", dispatch, "POST", {"ref": repos["token-source"]["default_branch"]}, user=actor_name, expected=200)
        run_id = response["workflow_run_id"]
        state["RunID"] = run_id
        save()
        if not ready.wait(120):
            raise TimeoutError("真实 Job 未到达授权撤销屏障")
        state["屏障"]["到达"] = True
        save()
        group_id = fixture["ownerID"]
        group = request("读来源当前修订", "/api/v1/governance/groups/" + str(group_id))
        request("任务中撤销触发者来源", f"/api/v1/governance/groups/{group_id}/members/{fixture['triggerActor']['userID']}?revision={group['revision']}", "DELETE", expected=204)
        state["屏障"]["撤权后放行"] = True
        save()
        released.set()
        path = "/api/v1/repos/" + owner + "/" + repos["token-source"]["name"] + "/actions/runs/" + str(run_id)
        for _ in range(120):
            result = request("轮询 Run", path)
            if result["status"] == "completed":
                state["Run"] = {"status": result["status"], "conclusion": result["conclusion"]}
                break
            time.sleep(2)
        else:
            raise TimeoutError("Run 未结束")
        check("Run 成功", state["Run"]["conclusion"], "success")
        jobs = request("读取 Jobs", path + "/jobs")["jobs"]
        check("Job 数量", len(jobs), 1)
        job = jobs[0]
        state["Job"] = {"id": job["id"], "status": job["status"], "conclusion": job["conclusion"]}
        check("Job 成功", job["conclusion"], "success")
        log_path = "/api/v1/repos/" + owner + "/" + repos["token-source"]["name"] + "/actions/jobs/" + str(job["id"]) + "/logs"
        code, raw = api.call(log_path)
        check("日志可读", code, 200)
        lines = raw.decode("utf-8", "replace") if isinstance(raw, bytes) else ""
        state["真实客户端状态"] = {key: value for key, value in re.findall(r"BOUNDARY ([a-z_]+)=([A-Za-z0-9]+)", lines)}
        check("真实客户端断言行数", len(state["真实客户端状态"]), 24)
        state["结果"] = "通过"
    except Exception as error:
        state["结果"] = "失败"
        state["首错"] = str(error)
        raise
    finally:
        released.set()
        server.shutdown()
        save()
    print("通过：Run", state["RunID"], "Job", state["Job"]["id"], "结果", state_path)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
