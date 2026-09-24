#!/usr/bin/env python3
"""在已领取任务的屏障两侧验证父组归档后的新作业令牌写请求。"""

import argparse
import base64
import json
import pathlib
import subprocess
import sys
import time
import urllib.request

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("start", "finish"))
    parser.add_argument("--base", default="http://127.0.0.1:13043")
    parser.add_argument("--credentials", required=True, type=pathlib.Path)
    parser.add_argument("--owner", default="phase1-v15-owner")
    parser.add_argument("--fixture", required=True, type=pathlib.Path)
    parser.add_argument("--state", required=True, type=pathlib.Path)
    args = parser.parse_args()
    if args.base.rstrip("/") != "http://127.0.0.1:13043":
        parser.error("只允许本机隔离实例13043")
    fixture = json.loads(args.fixture.read_text())
    api = Client(args.base, args.credentials, args.owner)
    repo = "/api/v1/repos/" + fixture["子组"]["兼容名"] + "/active"
    if args.mode == "start" and args.state.exists():
        parser.error("首次结果已存在")
    if args.mode == "finish" and not args.state.exists():
        parser.error("先运行 start")
    state = json.loads(args.state.read_text()) if args.state.exists() else {
        "说明": "S05归档期间已领取Job token新请求", "实例": args.base,
        "父组ID": fixture["父组"]["ID"], "仓库ID": fixture["仓库"]["active"]["ID"],
        "RunnerID": fixture["Runner"]["ID"], "步骤": [], "结果": "准备中"}
    args.state.parent.mkdir(parents=True, exist_ok=True)

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def check(label, actual, expected):
        passed = actual in expected if isinstance(expected, set) else actual == expected
        state["步骤"].append({"时间": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                              "检查": label, "实际": actual,
                              "期望": sorted(expected) if isinstance(expected, set) else expected,
                              "通过": passed})
        save()
        if not passed:
            raise AssertionError(label + "：" + str(actual))

    def call(label, path, method="GET", body=None, expected=200):
        code, response = api.call(path, method, body)
        check(label, code, expected)
        return response

    try:
        if args.mode == "start":
            group = call("检查父组未归档", "/api/v1/governance/groups/" + str(state["父组ID"]))
            check("父组归档状态", group["archived"], False)
            barrier_file = args.state.parent / "token-barrier.json"
            with (args.state.parent / "token-barrier.log").open("w") as log:
                process = subprocess.Popen([sys.executable,
                    str(pathlib.Path(__file__).with_name("pr_gate_barrier.py")),
                    "--isolated-instance", "--state", str(barrier_file)],
                    stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            state["屏障PID"] = process.pid
            end = time.monotonic() + 10
            while not barrier_file.exists() and time.monotonic() < end:
                time.sleep(0.2)
            if not barrier_file.exists():
                raise TimeoutError("独立屏障未启动")
            barrier = json.loads(barrier_file.read_text())
            config = {"base": args.base, "repo": repo,
                      "barrier": "http://127.0.0.1:" + str(barrier["端口"]) + "/ready/" + barrier["令牌"]}
            probe = r'''import base64, json, os, urllib.error, urllib.request
config = CONFIG
token = os.environ["GITEA_TOKEN"]
auth = "Basic " + base64.b64encode(("oauth2:" + token).encode()).decode()
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
def write(name):
    body = json.dumps({"content": base64.b64encode(("归档任务令牌 " + name + "\n").encode()).decode(), "branch": "main", "message": "test: lifecycle token " + name}).encode()
    request = urllib.request.Request(config["base"] + config["repo"] + "/contents/" + name + ".txt", method="POST", data=body,
        headers={"Authorization": auth, "Content-Type": "application/json"})
    try:
        with opener.open(request, timeout=20) as response: return response.status
    except urllib.error.HTTPError as error:
        status = error.code
        error.close()
        return status
before = write("before-archive")
print("S05_TOKEN_BEFORE=" + str(before), flush=True)
assert before == 201, before
with opener.open(config["barrier"], timeout=900) as response: assert response.status == 200
after = write("after-archive")
print("S05_TOKEN_AFTER=" + str(after), flush=True)
assert after in {401, 403, 404, 423}, after
'''.replace("CONFIG", repr(config))
            workflow = ("name: S05 已领取任务令牌归档探针\non:\n  workflow_dispatch:\njobs:\n"
                "  probe:\n    runs-on: " + fixture["Runner"]["标签"] + "\n"
                "    permissions:\n      contents: write\n"
                "    env:\n      GITEA_TOKEN: ${{ secrets.GITEA_TOKEN }}\n"
                "    steps:\n      - name: 归档前后新请求\n        run: |\n          set +x\n          python3 - <<'PY'\n" +
                "".join("          " + line + "\n" for line in probe.splitlines()) + "          PY\n")
            created = call("写入独立 token 工作流", repo + "/contents/.gitea/workflows/token-archive.yml",
                           "POST", {"content": base64.b64encode(workflow.encode()).decode(),
                                    "branch": fixture["仓库"]["active"]["默认分支"],
                                    "message": "test(actions): 一期归档任务令牌探针"}, 201)
            state["工作流提交"] = created["commit"]["sha"]
            dispatched = call("正式派发 token 探针", repo +
                "/actions/workflows/token-archive.yml/dispatches?return_run_details=true",
                "POST", {"ref": fixture["仓库"]["active"]["默认分支"]})
            state["RunID"] = dispatched["workflow_run_id"]
            end = time.monotonic() + 90
            while time.monotonic() < end:
                if json.loads(barrier_file.read_text())["已到达"]:
                    break
                time.sleep(1)
            check("真Runner已到达屏障", json.loads(barrier_file.read_text())["已到达"], True)
            run = call("读取运行中token任务", repo + "/actions/runs/" + str(state["RunID"]))
            check("token探针运行中", run["status"], "in_progress")
            call("归档前 job token 新写已落盘", repo + "/contents/before-archive.txt")
            state["结果"] = "等待普通Owner UI归档"
            print("token 探针到达屏障；父组", state["父组ID"], "Run", state["RunID"], flush=True)
        else:
            group = call("读取普通Owner UI归档结果", "/api/v1/governance/groups/" + str(state["父组ID"]))
            check("父组已经归档", group["archived"], True)
            barrier = json.loads((args.state.parent / "token-barrier.json").read_text())
            release = urllib.request.Request("http://127.0.0.1:" + str(barrier["端口"]) +
                "/release/" + barrier["令牌"], method="POST", data=b"")
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(release, timeout=10) as response:
                check("放行已领取Job", response.status, 204)
            end = time.monotonic() + 90
            while time.monotonic() < end:
                run = call("读取token任务终态", repo + "/actions/runs/" + str(state["RunID"]))
                if run["status"] == "completed":
                    break
                time.sleep(2)
            check("token任务完成", run["status"], "completed")
            check("token任务预期拒写并成功收尾", run["conclusion"], "success")
            jobs = call("读取token任务Job", repo + "/actions/runs/" + str(state["RunID"]) + "/jobs")["jobs"]
            check("token任务仅一Job", len(jobs), 1)
            code, logs = api.call(repo + "/actions/jobs/" + str(jobs[0]["id"]) + "/logs")
            check("读取真Runner日志", code, 200)
            if not isinstance(logs, bytes):
                logs = json.dumps(logs).encode()
            visible = logs.decode("utf-8", "replace")
            check("归档前token写入成功日志", "S05_TOKEN_BEFORE=201" in visible, True)
            after = [code for code in (401, 403, 404, 423) if "S05_TOKEN_AFTER=" + str(code) in visible]
            check("归档后token新请求被拒日志", len(after), 1)
            state["归档后tokenHTTP"] = after[0]
            code, _ = api.call(repo + "/contents/after-archive.txt")
            check("拒写后目标文件不存在", code, 404)
            state["结果"] = "通过"
            print("S05 job token 新写被拒，HTTP", after[0], "Run", state["RunID"], flush=True)
    except Exception as error:
        state["首错"] = str(error)
        state["结果"] = "失败"
        raise
    finally:
        save()


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
