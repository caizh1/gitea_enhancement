#!/usr/bin/env python3
"""用独立隔离 Runner 验证执行中停用或删除的真实边界。"""

import argparse
import base64
import http.server
import json
import pathlib
import re
import sys
import threading
import time
import urllib.parse

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--fixture", type=pathlib.Path, required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    parser.add_argument("--operation", choices=("disable", "delete"), required=True)
    args = parser.parse_args()
    if args.state.exists():
        raise RuntimeError("结果已存在；不得覆盖真实运行记录")
    fixture = json.loads(args.fixture.read_text())
    api = Client(args.base, args.credentials, fixture["identities"]["owner"])
    owner, repo = fixture["owner"], fixture["repos"]["token-source"]["name"]
    runner_id = fixture["runner"]["id"]
    repo_api = f"/api/v1/repos/{owner}/{repo}"
    runner_api = f"/api/v1/orgs/{owner}/actions/runners/{runner_id}"
    state = {"说明": "S08-a 隔离 Runner 执行中" + ("停用" if args.operation == "disable" else "删除"), "groupID": fixture["ownerID"],
        "repoID": fixture["repos"]["token-source"]["id"], "runnerID": runner_id, "步骤": []}
    args.state.parent.mkdir(parents=True, exist_ok=True)

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def call(label, path, method="GET", body=None, expected=200):
        code, result = api.call(path, method, body)
        passed = code == expected
        state["步骤"].append({"检查": label, "实际": code, "期望": expected, "通过": passed})
        save()
        if not passed:
            raise AssertionError(f"{label}：HTTP {code}")
        return result

    reached = threading.Event()
    released = threading.Event()
    observed = threading.Event()

    class Barrier(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path.startswith("/observed?"):
                status = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query).get("status", [""])[0]
                state["执行中客户端回报"] = status
                save()
                observed.set()
                self.send_response(200)
                self.end_headers()
                return
            if self.path != "/wait":
                self.send_error(404)
                return
            reached.set()
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
    code = f'''import base64, os, urllib.error, urllib.request
base = {args.base.rstrip('/')!r}
owner = {owner!r}
package = {fixture['samePackage']['name']!r}
token = os.environ["GITEA_TOKEN"]
auth = "Basic " + base64.b64encode(("oauth2:" + token).encode()).decode()
def probe(label, expected):
    req = urllib.request.Request(base + "/api/packages/" + owner + "/generic/" + package + "/1.0.0/payload.txt", headers={{"Authorization": auth}})
    try:
        with urllib.request.build_opener(urllib.request.ProxyHandler({{}})).open(req, timeout=20) as response:
            status = response.status
    except urllib.error.HTTPError as error:
        status = error.code
        error.close()
    print("RUNNER_BOUNDARY %s=%s" % (label, status), flush=True)
    assert status in expected, (label, status)
probe("before", {{200}})
urllib.request.urlopen("http://127.0.0.1:{server.server_address[1]}/wait", timeout=125).read()
probe("after_{args.operation}", {{401, 403, 404}})
urllib.request.urlopen("http://127.0.0.1:{server.server_address[1]}/observed?status=denied", timeout=10).read()
'''
    workflow = "name: runner disable active\non:\n  workflow_dispatch:\njobs:\n  probe:\n    runs-on: " + fixture["runner"]["labelName"] + "\n    permissions:\n      packages: read\n    env:\n      GITEA_TOKEN: ${{ secrets.GITEA_TOKEN }}\n    steps:\n      - name: Check token across runner disable\n        run: |\n          set +x\n          python3 - <<'PY'\n" + "".join("          " + line + "\n" for line in code.splitlines()) + "          PY\n"
    workflow_path = repo_api + "/contents/.gitea/workflows/runner-disable-active.yml"
    dispatch_path = repo_api + "/actions/workflows/runner-disable-active.yml/dispatches?return_run_details=true"

    def run_details(run_id):
        return call("读取 Run", repo_api + "/actions/runs/" + str(run_id))

    try:
        runner = call("Runner 在线", runner_api)
        if runner["status"] != "online" or runner["disabled"]:
            raise AssertionError("专用 Runner 必须在线且启用")
        call("写入隔离工作流", workflow_path, "POST", {"content": base64.b64encode(workflow.encode()).decode(),
            "message": "加入执行中停用验收", "branch": fixture["repos"]["token-source"]["default_branch"]}, 201)
        first = call("调度执行中任务", dispatch_path, "POST", {"ref": "main"})["workflow_run_id"]
        state["activeRunID"] = first
        save()
        if not reached.wait(90):
            raise TimeoutError("真实任务未到达屏障")
        state["屏障"] = "真实 Job 已运行"
        save()
        second = call("调度后续任务", dispatch_path, "POST", {"ref": "main"})["workflow_run_id"]
        state["waitingRunID"] = second
        save()
        waiting = run_details(second)
        if waiting["status"] not in ("waiting", "queued", "pending"):
            raise AssertionError("后续任务不是待领取状态：" + waiting["status"])
        if args.operation == "disable":
            disabled = call("执行中停用 Runner", runner_api, "PATCH", {"disabled": True})
            if not disabled["disabled"]:
                raise AssertionError("Runner 停用后回读不是 disabled")
        else:
            call("执行中删除 Runner", runner_api, "DELETE", expected=204)
            code, _ = api.call(runner_api)
            if code != 404:
                raise AssertionError("删除后 Runner 仍可回读：" + str(code))
            state["删除后Runner回读"] = code
        state["配置变更时刻"] = time.time()
        save()
        time.sleep(8)
        waiting = run_details(second)
        if waiting["status"] not in ("waiting", "queued", "pending"):
            raise AssertionError("配置变更后后续任务被领取：" + waiting["status"])
        state["配置变更后后续Run"] = {"status": waiting["status"], "conclusion": waiting.get("conclusion")}
        released.set()
        if not observed.wait(30) or state.get("执行中客户端回报") != "denied":
            raise TimeoutError("真实客户端未回报令牌拒绝")
        if args.operation == "disable":
            for _ in range(50):
                current = run_details(first)
                if current["status"] == "completed":
                    break
                time.sleep(2)
            else:
                raise TimeoutError("停用后原任务未收尾")
            state["执行中Run"] = {"status": current["status"], "conclusion": current["conclusion"]}
            if current["conclusion"] != "success":
                raise AssertionError("原任务结论不是 success：" + str(current["conclusion"]))
            jobs = call("读取原 Job", repo_api + f"/actions/runs/{first}/jobs")["jobs"]
            if len(jobs) != 1 or jobs[0]["conclusion"] != "success":
                raise AssertionError("原 Job 未成功收尾")
            state["jobID"] = jobs[0]["id"]
            raw = call("读取原生日志", repo_api + f"/actions/jobs/{jobs[0]['id']}/logs")
            logs = raw.decode("utf-8", "replace") if isinstance(raw, bytes) else ""
            state["真实客户端状态"] = dict(re.findall(r"RUNNER_BOUNDARY ([a-z_]+)=(\d+)", logs))
            if state["真实客户端状态"].get("before") != "200" or state["真实客户端状态"].get("after_disable") not in ("401", "403", "404"):
                raise AssertionError("原生日志缺少停用前后令牌结果")
            state["结果"] = "通过"
        else:
            time.sleep(10)
            current = run_details(first)
            state["执行中Run删除后即时"] = {"status": current["status"], "conclusion": current.get("conclusion")}
            state["结果"] = "边界通过；自然收尾待观察"
    except Exception as error:
        state["结果"], state["首错"] = "失败", str(error)
        raise
    finally:
        released.set()
        server.shutdown()
        save()
    print("已记录：原 Run", state["activeRunID"], "待领取 Run", state["waitingRunID"], state["结果"])


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
