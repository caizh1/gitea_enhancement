#!/usr/bin/env python3
"""分阶段复验归档恢复后的旧队列、任务令牌和可信人工重跑。"""

import argparse
import base64
import http.server
import json
import os
import pathlib
import secrets
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request

from job_token_boundary import Client


def write_private(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")
    path.chmod(0o600)


def barrier_service(config_path, state_path):
    config = json.loads(config_path.read_text())
    nonce = config["nonce"]
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    ready = threading.Event()
    released = threading.Event()
    task_token = None

    class Handler(http.server.BaseHTTPRequestHandler):
        def answer(self, code, data):
            payload = json.dumps(data, ensure_ascii=False).encode()
            self.send_response(code)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def do_GET(self):
            if self.path != "/state/" + nonce:
                self.send_error(404)
                return
            self.answer(200, {"已到达": ready.is_set(), "已放行": released.is_set()})

        def do_POST(self):
            nonlocal task_token
            if self.path == "/register/" + nonce:
                auth = self.headers.get("Authorization", "")
                if not auth.startswith("Bearer ") or task_token is not None:
                    self.send_error(403)
                    return
                task_token = auth.removeprefix("Bearer ")
                ready.set()
                if not released.wait(900):
                    self.send_error(504)
                    return
                self.answer(200, {"已放行": True})
                return
            if self.path.startswith("/probe/" + nonce + "/"):
                phase = self.path.rsplit("/", 1)[-1]
                if phase not in {"before", "archived", "restored"} or not ready.is_set():
                    self.send_error(409)
                    return
                body = json.dumps({"content": base64.b64encode(("一期 F4 " + phase + "\n").encode()).decode(),
                                   "branch": config["branch"], "message": "test: lifecycle token " + phase}).encode()
                auth = "Basic " + base64.b64encode(("oauth2:" + task_token).encode()).decode()
                request = urllib.request.Request(config["base"] + config["repo"] +
                    "/contents/f4-token-" + phase + ".txt", method="POST", data=body,
                    headers={"Authorization": auth, "Content-Type": "application/json"})
                try:
                    with opener.open(request, timeout=20) as response:
                        code = response.status
                except urllib.error.HTTPError as error:
                    code = error.code
                    error.close()
                self.answer(200, {"HTTP": code})
                return
            if self.path == "/release/" + nonce:
                released.set()
                self.answer(200, {"已放行": True})
                return
            if self.path == "/stop/" + nonce:
                released.set()
                self.answer(200, {"已停止": True})
                threading.Thread(target=server.shutdown, daemon=True).start()
                return
            self.send_error(404)

        def log_message(self, *_):
            pass

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    write_private(state_path, {"端口": server.server_address[1], "已启动": True})
    server.serve_forever(poll_interval=0.2)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("start", "archived", "restored", "finish", "serve"))
    parser.add_argument("--base", default="http://127.0.0.1:13043")
    parser.add_argument("--credentials", type=pathlib.Path)
    parser.add_argument("--owner", default="phase1-v15-owner")
    parser.add_argument("--fixture", type=pathlib.Path)
    parser.add_argument("--state", required=True, type=pathlib.Path)
    parser.add_argument("--barrier-config", type=pathlib.Path)
    args = parser.parse_args()
    if args.mode == "serve":
        barrier_service(args.barrier_config, args.state)
        return
    if args.base.rstrip("/") != "http://127.0.0.1:13043":
        parser.error("只允许本机隔离实例 13043")
    if not args.credentials or not args.fixture:
        parser.error("需指定 --credentials 与 --fixture")
    fixture = json.loads(args.fixture.read_text())
    if args.mode == "start" and args.state.exists():
        parser.error("状态已存在，不覆盖旧运行或证据")
    if args.mode != "start" and not args.state.exists():
        parser.error("先运行 start")
    args.state.parent.mkdir(parents=True, exist_ok=True)
    state = json.loads(args.state.read_text()) if args.state.exists() else {
        "说明": "F4 独立真实 Runner 生命周期验收", "实例": args.base,
        "父组": fixture["父组"], "子组": fixture["子组"], "仓库": fixture["仓库"],
        "Runner": fixture["Runner"], "步骤": [], "阶段": "准备中"}
    api = Client(args.base, args.credentials, args.owner)
    repo = "/api/v1/repos/" + fixture["子组"]["兼容名"] + "/active"
    branch = fixture["仓库"]["active"]["默认分支"]
    group_path = "/api/v1/governance/groups/" + str(fixture["父组"]["ID"])
    pending_path = "/api/v1/governance/repositories/" + str(fixture["仓库"]["pending"]["ID"]) + "/deletion"
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    barrier_config = args.state.parent / "f4-barrier-config.json"
    barrier_state = args.state.parent / "f4-barrier-state.json"

    def save():
        write_private(args.state, state)

    def check(label, actual, expected):
        passed = actual in expected if isinstance(expected, set) else actual == expected
        state["步骤"].append({"时间": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "检查": label, "实际": actual, "期望": sorted(expected) if isinstance(expected, set) else expected,
            "通过": passed})
        save()
        if not passed:
            raise AssertionError(label + "：" + str(actual))

    def call(label, path, method="GET", body=None, expected=200):
        code, result = api.call(path, method, body)
        check(label, code, expected)
        return result

    def run(run_id, attempt=None):
        path = repo + "/actions/runs/" + str(run_id)
        if attempt is not None:
            path += "/attempts/" + str(attempt)
        return call("读取 Run " + str(run_id) + (" Attempt" + str(attempt) if attempt else ""), path)

    def wait_for(label, predicate, seconds=180):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            value = predicate()
            if value:
                return value
            time.sleep(2)
        raise TimeoutError(label)

    def barrier(path):
        details = json.loads(barrier_state.read_text())
        nonce = json.loads(barrier_config.read_text())["nonce"]
        request = urllib.request.Request("http://127.0.0.1:" + str(details["端口"]) + "/" + path + "/" + nonce,
            method="POST", data=b"")
        with opener.open(request, timeout=25) as response:
            return json.loads(response.read())

    def barrier_probe(phase):
        details = json.loads(barrier_state.read_text())
        nonce = json.loads(barrier_config.read_text())["nonce"]
        request = urllib.request.Request("http://127.0.0.1:" + str(details["端口"]) +
            "/probe/" + nonce + "/" + phase, method="POST", data=b"")
        with opener.open(request, timeout=25) as response:
            return json.loads(response.read())["HTTP"]

    def workflow(name, content):
        return call("写入" + name + "工作流", repo + "/contents/.gitea/workflows/" + name,
            "POST", {"content": base64.b64encode(content.encode()).decode(), "branch": branch,
                     "message": "test(actions): F4 生命周期工作流"}, 201)["commit"]["sha"]

    try:
        if args.mode == "start":
            check("普通 Owner 非管理员", call("读取当前身份", "/api/v1/user")["is_admin"], False)
            check("父组尚未归档", call("读取父组", group_path)["archived"], False)
            runners = call("读取专属 Runner", "/api/v1/orgs/" + fixture["子组"]["兼容名"] +
                           "/actions/runners")
            check("专属 Runner 在线", any(r["id"] == fixture["Runner"]["ID"] and r["status"] == "online"
                for r in runners.get("runners", runners.get("entries", []))), True)
            if barrier_config.exists() or barrier_state.exists():
                raise FileExistsError("屏障状态已存在，不复用旧 token")
            write_private(barrier_config, {"nonce": secrets.token_hex(16), "base": args.base,
                "repo": repo, "branch": branch})
            with (args.state.parent / "f4-barrier.log").open("w") as log:
                process = subprocess.Popen([sys.executable, str(pathlib.Path(__file__)), "serve",
                    "--state", str(barrier_state), "--barrier-config", str(barrier_config)],
                    stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            state["屏障PID"] = process.pid
            wait_for("屏障未启动", lambda: barrier_state.exists(), 15)
            details = json.loads(barrier_state.read_text())
            nonce = json.loads(barrier_config.read_text())["nonce"]
            register = "http://127.0.0.1:" + str(details["端口"]) + "/register/" + nonce
            probe = "import os, urllib.request\n" + (
                "request = urllib.request.Request(%r, method='POST', data=b'', "
                "headers={'Authorization': 'Bearer ' + os.environ['GITEA_TOKEN']})\n" % register) + (
                "with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=900) as response:\n"
                "    assert response.status == 200\n")
            workflow("f4-token.yml", "name: F4 旧任务令牌\non:\n  workflow_dispatch:\njobs:\n"
                "  token:\n    runs-on: " + fixture["Runner"]["标签"] + "\n"
                "    permissions:\n      contents: write\n    env:\n"
                "      GITEA_TOKEN: ${{ secrets.GITEA_TOKEN }}\n    steps:\n"
                "      - run: |\n          set +x\n          python3 - <<'PY'\n" +
                "".join("          " + line + "\n" for line in probe.splitlines()) + "          PY\n")
            token_run = call("正式派发已领取任务", repo +
                "/actions/workflows/f4-token.yml/dispatches?return_run_details=true", "POST",
                {"ref": branch})["workflow_run_id"]
            state["旧已领取RunID"] = token_run
            def registered():
                request = urllib.request.Request("http://127.0.0.1:" + str(details["端口"]) +
                    "/state/" + nonce)
                with opener.open(request, timeout=5) as response:
                    return json.loads(response.read())["已到达"]
            wait_for("真 Runner 未到达令牌屏障", registered, 90)
            check("旧任务真实运行中", run(token_run)["status"], "in_progress")
            check("归档前旧 token 可写", barrier_probe("before"), 201)
            call("归档前 token 写入已落盘", repo + "/contents/f4-token-before.txt")

            fresh_probe = ("import base64, json, os, urllib.request\n"
                "token = os.environ['GITEA_TOKEN']\n"
                "body = json.dumps({'content': base64.b64encode('可信新任务令牌\\n'.encode()).decode(), "
                "'branch': %r, 'message': 'test: fresh rerun token'}).encode()\n" % branch +
                "auth = 'Basic ' + base64.b64encode(('oauth2:' + token).encode()).decode()\n"
                "request = urllib.request.Request(%r, method='POST', data=body, " %
                (args.base + repo + "/contents/f4-rerun-new-token.txt") +
                "headers={'Authorization': auth, 'Content-Type': 'application/json'})\n"
                "with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=30) as response:\n"
                "    assert response.status == 201\n")
            workflow("f4-queued.yml", "name: F4 旧排队任务\non:\n  workflow_dispatch:\njobs:\n"
                "  queued:\n    runs-on: " + fixture["Runner"]["标签"] + "\n"
                "    permissions:\n      contents: write\n    env:\n"
                "      GITEA_TOKEN: ${{ secrets.GITEA_TOKEN }}\n    steps:\n"
                "      - run: |\n          set +x\n          python3 - <<'PY'\n" +
                "".join("          " + line + "\n" for line in fresh_probe.splitlines()) + "          PY\n")
            queued_run = call("正式派发旧排队任务", repo +
                "/actions/workflows/f4-queued.yml/dispatches?return_run_details=true", "POST",
                {"ref": branch})["workflow_run_id"]
            state["旧排队RunID"] = queued_run
            check("旧派发Run排队", run(queued_run)["status"], {"queued", "waiting", "pending"})
            jobs = call("旧排队Job未领取", repo + "/actions/runs/" + str(queued_run) + "/jobs")["jobs"]
            check("旧派发Job无Runner", all(job.get("runner_id") is None for job in jobs), True)

            cron_sha = workflow("f4-cron.yml", "name: F4 原生定时旧任务\non:\n  schedule:\n"
                "    - cron: '* * * * *'\njobs:\n  scheduled:\n    runs-on: " +
                fixture["Runner"]["标签"] + "\n    steps:\n      - run: echo F4_CRON\n")
            state["原生定时工作流SHA"] = cron_sha
            def scheduled():
                code, result = api.call(repo + "/actions/runs?limit=50")
                if code != 200:
                    return None
                matches = [entry for entry in result.get("workflow_runs", [])
                    if entry.get("event") == "schedule" and
                    entry.get("path", "").split("@", 1)[0] == "f4-cron.yml" and
                    entry.get("head_sha") == cron_sha]
                return matches[0] if matches else None
            cron_run = wait_for("真调度器未创建原生 cron Run", scheduled, 180)
            state["旧定时RunID"] = cron_run["id"]
            check("旧定时Run排队", cron_run["status"], {"queued", "waiting", "pending"})

            pending = call("待删仓原状态", pending_path)
            check("待删仓起初无计划", pending["pending"], None)
            pending = call("正式安排子仓独立待删", pending_path, "POST",
                {"confirmation_path": pending["full_path"]})
            state["待删"] = {"仓库ID": fixture["仓库"]["pending"]["ID"],
                "路径": pending["full_path"], "到期": pending["pending"]["due_unix"]}
            check("独立待删仓归档", pending["archived"], True)
            state["阶段"] = "等待普通 Owner UI 归档父组"
            print("F4 归档关口：", args.base + "/" + fixture["父组"]["完整路径"],
                  "旧 Run", token_run, queued_run, cron_run["id"], flush=True)

        elif args.mode == "archived":
            check("当前阶段", state["阶段"], "等待普通 Owner UI 归档父组")
            check("父组已归档", call("读取归档父组", group_path)["archived"], True)
            check("活跃仓归档", call("读取活跃仓", repo)["archived"], True)
            for key in ("旧排队RunID", "旧定时RunID"):
                old = run(state[key])
                check(key + "已取消", (old["status"], old["conclusion"]), ("completed", "cancelled"))
            check("归档后旧 token 拒写", barrier_probe("archived"), {401, 403, 404, 423})
            code, _ = api.call(repo + "/contents/f4-token-archived.txt")
            check("归档旧 token 未落盘", code, 404)
            pending = call("归档后待删计划", pending_path)
            check("待删到期不变", pending["pending"]["due_unix"], state["待删"]["到期"])
            state["阶段"] = "等待普通 Owner UI 恢复父组"
            print("F4 恢复关口：", args.base + "/" + fixture["父组"]["完整路径"], flush=True)

        elif args.mode == "restored":
            check("当前阶段", state["阶段"], "等待普通 Owner UI 恢复父组")
            check("父组已恢复", call("读取恢复父组", group_path)["archived"], False)
            check("活跃仓解档", call("读取活跃仓", repo)["archived"], False)
            pending = call("恢复后独立待删计划", pending_path)
            check("独立待删仓仍归档", pending["archived"], True)
            check("独立待删到期未变", pending["pending"]["due_unix"], state["待删"]["到期"])
            check("独立待删路径未变", pending["full_path"], state["待删"]["路径"])
            for key in ("旧排队RunID", "旧定时RunID"):
                old = run(state[key])
                check(key + "恢复后仍取消", (old["status"], old["conclusion"]), ("completed", "cancelled"))
            check("恢复后同一旧 token 永久拒写", barrier_probe("restored"), {401, 403, 404, 423})
            code, _ = api.call(repo + "/contents/f4-token-restored.txt")
            check("恢复后旧 token 未落盘", code, 404)
            state["阶段"] = "恢复复核通过，待放行与人工重跑"
            print("F4 恢复复核通过；可执行 finish", flush=True)

        elif args.mode == "finish":
            check("当前阶段", state["阶段"], "恢复复核通过，待放行与人工重跑")
            barrier("release")
            old_token = wait_for("旧已领取任务未终结", lambda: run(state["旧已领取RunID"])
                if run(state["旧已领取RunID"])["status"] == "completed" else None, 120)
            check("旧已领取任务按取消收尾", old_token["conclusion"], "cancelled")
            queued_before = run(state["旧排队RunID"], 1)
            check("重跑前旧 Attempt1 已取消", queued_before["conclusion"], "cancelled")
            rerun = call("普通 Owner 正式重跑旧取消 Run", repo + "/actions/runs/" +
                str(state["旧排队RunID"]) + "/rerun", "POST", expected=201)
            check("人工重跑新 Attempt", rerun["run_attempt"], 2)
            owner = call("确认重跑发起身份", "/api/v1/user")
            check("新 Attempt 由当前普通人类授权", rerun["trigger_actor"]["id"], owner["id"])
            fresh = wait_for("人工重跑未完成", lambda: run(state["旧排队RunID"])
                if run(state["旧排队RunID"])["status"] == "completed" else None, 120)
            check("新 Attempt 成功", (fresh["run_attempt"], fresh["conclusion"]), (2, "success"))
            call("新任务自身 token 写入成功", repo + "/contents/f4-rerun-new-token.txt")
            check("旧 Attempt1 始终取消", run(state["旧排队RunID"], 1)["conclusion"], "cancelled")
            check("旧原生 cron Run 未复活", run(state["旧定时RunID"])["conclusion"], "cancelled")
            pending = call("最终独立待删计划", pending_path)
            check("待删仍归档", pending["archived"], True)
            check("待删期限未变", pending["pending"]["due_unix"], state["待删"]["到期"])
            barrier("stop")
            state["阶段"] = "通过"
            print("F4 真实 Runner 复验通过：旧 token / queue / cron 已拒，新人工 Attempt 成功", flush=True)
    except Exception as error:
        state["首错"] = str(error)
        state["阶段"] = "失败"
        raise
    finally:
        save()


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
