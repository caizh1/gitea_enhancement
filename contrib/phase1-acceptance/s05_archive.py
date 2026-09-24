#!/usr/bin/env python3
"""分阶段验收独立父组归档时的任务与协议消费者。"""

import argparse
import base64
import hashlib
import json
import os
import pathlib
import subprocess
import sys
import time
import urllib.error
import urllib.request

from job_token_boundary import Client


UPLOAD = "https://github.com/ChristopherHX/gitea-upload-artifact@81f940d004763f986ba3582c007fd842dd5cb0d7"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("seed", "start", "verify-ui", "finish"))
    parser.add_argument("--base", default="http://127.0.0.1:13043")
    parser.add_argument("--credentials", required=True, type=pathlib.Path)
    parser.add_argument("--owner", default="phase1-v15-owner")
    parser.add_argument("--fixture", required=True, type=pathlib.Path)
    parser.add_argument("--state", required=True, type=pathlib.Path)
    args = parser.parse_args()
    if args.base.rstrip("/") != "http://127.0.0.1:13043":
        parser.error("只允许本机隔离实例 13043")
    fixture = json.loads(args.fixture.read_text())
    if args.mode == "seed" and args.state.exists():
        parser.error("首次状态已存在，不覆盖")
    if args.mode != "seed" and not args.state.exists():
        parser.error("先运行 seed")
    state = json.loads(args.state.read_text()) if args.state.exists() else {
        "说明": "S05 独立父组归档与协议消费者真实验收", "实例": args.base,
        "父组": fixture["父组"], "子组": fixture["子组"],
        "仓库": fixture["仓库"]["active"], "Runner": fixture["Runner"],
        "步骤": [], "结果": "准备中",
    }
    api = Client(args.base, args.credentials, args.owner)
    owner = fixture["子组"]["兼容名"]
    repo = "/api/v1/repos/" + owner + "/active"
    raw_repo = "/" + owner + "/active.git"
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    args.state.parent.mkdir(parents=True, exist_ok=True)

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def check(label, actual, expected):
        passed = actual in expected if isinstance(expected, (set, list)) else actual == expected
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

    def request(path, method="GET", data=None, content_type="application/octet-stream"):
        password = api.credentials.get(args.owner, api.credentials.get("password"))
        auth = base64.b64encode((args.owner + ":" + password).encode()).decode()
        headers = {"Authorization": "Basic " + auth}
        if data is not None:
            headers["Content-Type"] = content_type
        req = urllib.request.Request(args.base + path, method=method, data=data, headers=headers)
        try:
            with opener.open(req, timeout=30) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            result = error.code, error.read()
            error.close()
            return result

    def git(*argv):
        password = api.credentials.get(args.owner, api.credentials.get("password"))
        auth = base64.b64encode((args.owner + ":" + password).encode()).decode()
        env = os.environ.copy()
        env.update({"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "http.extraHeader",
                    "GIT_CONFIG_VALUE_0": "Authorization: Basic " + auth,
                    "GIT_TERMINAL_PROMPT": "0"})
        return subprocess.run(["git", *argv], env=env, capture_output=True, text=True, timeout=30)

    def put_workflow(name, content):
        created = call("写入" + name, repo + "/contents/.gitea/workflows/" + name,
                       "POST", {"content": base64.b64encode(content.encode()).decode(),
                                "branch": state["仓库"]["默认分支"],
                                "message": "test(actions): 一期归档隔离验收"}, 201)
        return created["commit"]["sha"]

    def run(run_id):
        return call("读取 Run " + str(run_id), repo + "/actions/runs/" + str(run_id))

    def wait_run(name, sha, predicate, seconds=240):
        end = time.monotonic() + seconds
        while time.monotonic() < end:
            listing = call("查询真实 Run", repo + "/actions/runs?limit=50")
            matches = [entry for entry in listing["workflow_runs"]
                       if entry["path"].split("@", 1)[0] == name and entry["head_sha"] == sha]
            if matches and predicate(matches[0]):
                return matches[0]
            time.sleep(2)
        raise TimeoutError(name + " 未达到预期运行状态")

    try:
        if args.mode == "seed":
            me = call("普通 Owner 身份", "/api/v1/user")
            check("非实例管理员", me["is_admin"], False)
            payload = ("一期归档协议样本 " + str(time.time_ns()) + "\n").encode()
            oid = hashlib.sha256(payload).hexdigest()
            state["协议"] = {"内容SHA256": oid, "内容Base64": base64.b64encode(payload).decode()}
            save()
            lfs_path = raw_repo + "/info/lfs/objects/" + oid + "/" + str(len(payload))
            code, _ = request(lfs_path, "PUT", payload)
            check("归档前真实 LFS 上传", code, 200)
            code, body = request(raw_repo + "/info/lfs/objects/" + oid)
            check("归档前真实 LFS 读取", code, 200)
            check("归档前 LFS 内容", hashlib.sha256(body).hexdigest(), oid)
            generic = "/api/packages/" + owner + "/generic/phase1-s05/1.0.0/payload.txt"
            code, _ = request(generic, "PUT", payload)
            check("归档前 Generic 发布", code, 201)
            code, body = request(generic)
            check("归档前 Generic 读取", code, 200)
            check("归档前 Generic 内容", hashlib.sha256(body).hexdigest(), oid)
            release = call("归档前正式 Release 发布", repo + "/releases", "POST",
                           {"tag_name": "phase1-s05-v1", "name": "一期归档保留样本",
                            "body": "一期归档只读保留样本"}, 201)
            state["协议"]["ReleaseID"] = release["id"]
            state["协议"]["Generic路径"] = generic
            state["协议"]["LFS路径"] = lfs_path
            save()
            code, _ = request(repo + "/releases/" + str(release["id"]))
            check("归档前 Release 读取", code, 200)
            artifact_sha = put_workflow("artifact.yml", "name: S05 真产物\non:\n  push:\n    branches: [main]\njobs:\n"
                "  upload:\n    runs-on: " + state["Runner"]["标签"] + "\n    steps:\n"
                "      - run: printf 'phase1-s05\\n' > payload.txt\n"
                "      - uses: " + UPLOAD + "\n        with:\n"
                "          name: phase1-s05\n          path: payload.txt\n")
            artifact_run = wait_run("artifact.yml", artifact_sha,
                                    lambda item: item["status"] == "completed", 300)
            check("真 Runner 上传产物成功", artifact_run["conclusion"], "success")
            artifacts = call("列出真实 artifact", repo + "/actions/runs/" +
                             str(artifact_run["id"]) + "/artifacts")["artifacts"]
            matches = [item for item in artifacts if item["name"] == "phase1-s05"]
            check("真实 artifact 数量", len(matches), 1)
            state["协议"]["ArtifactID"] = matches[0]["id"]
            state["协议"]["ArtifactRunID"] = artifact_run["id"]
            save()
            code, _ = request(repo + "/actions/artifacts/" + str(matches[0]["id"]) + "/zip")
            check("归档前 artifact 下载", code, 200)
            clone = args.state.parent / "git-clone"
            result = git("clone", args.base + raw_repo, str(clone))
            check("归档前 HTTP Git clone", result.returncode, 0)
            state["协议"]["Git目录"] = str(clone)
            state["结果"] = "协议基线就绪"
            print("S05 协议基线就绪，父组", state["父组"]["ID"], flush=True)
        elif args.mode == "start":
            check("协议基线已就绪", state["结果"], "协议基线就绪")
            barrier_state = args.state.parent / "barrier.json"
            barrier_log = args.state.parent / "barrier.log"
            with barrier_log.open("w") as log:
                process = subprocess.Popen([sys.executable,
                    str(pathlib.Path(__file__).with_name("pr_gate_barrier.py")),
                    "--isolated-instance", "--state", str(barrier_state)],
                    stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
            state["屏障PID"] = process.pid
            end = time.monotonic() + 10
            while not barrier_state.exists() and time.monotonic() < end:
                time.sleep(0.2)
            if not barrier_state.exists():
                raise TimeoutError("独立屏障未启动")
            barrier = json.loads(barrier_state.read_text())
            hold_sha = put_workflow("hold.yml", "name: S05 归档执行中任务\non:\n  workflow_dispatch:\njobs:\n"
                "  hold:\n    runs-on: " + state["Runner"]["标签"] + "\n    steps:\n"
                "      - run: curl --fail --silent http://127.0.0.1:" + str(barrier["端口"]) +
                "/ready/" + barrier["令牌"] + "\n")
            waiting_sha = put_workflow("waiting.yml", "name: S05 归档排队任务\non:\n  workflow_dispatch:\njobs:\n"
                "  wait:\n    runs-on: " + state["Runner"]["标签"] + "\n    steps:\n"
                "      - run: echo S05_UNTRUSTED_OLD_RUN\n")
            hold = call("正式派发执行中任务", repo +
                "/actions/workflows/hold.yml/dispatches?return_run_details=true", "POST",
                {"ref": state["仓库"]["默认分支"]})
            hold_id = hold["workflow_run_id"]
            end = time.monotonic() + 90
            while time.monotonic() < end:
                barrier = json.loads(barrier_state.read_text())
                if barrier["已到达"]:
                    break
                time.sleep(1)
            check("真 Runner 到达执行中屏障", barrier["已到达"], True)
            check("执行中 Run 状态", run(hold_id)["status"], "in_progress")
            waiting = call("正式派发排队任务", repo +
                "/actions/workflows/waiting.yml/dispatches?return_run_details=true", "POST",
                {"ref": state["仓库"]["默认分支"]})
            waiting_id = waiting["workflow_run_id"]
            check("排队 Run 状态", run(waiting_id)["status"], {"waiting", "queued", "pending"})
            state["任务"] = {"执行中RunID": hold_id, "排队RunID": waiting_id,
                             "执行中SHA": hold_sha, "排队SHA": waiting_sha,
                             "屏障端口": barrier["端口"]}
            state["结果"] = "等待普通Owner UI归档父组"
            print("S05 固定执行中与排队任务，父组", state["父组"]["ID"],
                  "Run", hold_id, waiting_id, flush=True)
        elif args.mode == "verify-ui":
            check("已固定在途任务", "任务" in state, True)
            group = call("读取普通Owner UI归档结果",
                "/api/v1/governance/groups/" + str(state["父组"]["ID"]))
            check("父组已归档", group["archived"], True)
            current = call("归档后仓库", repo)
            check("仓库只读归档", current["archived"], True)
            for kind, run_id in (("原执行中", state["任务"]["执行中RunID"]),
                                 ("原排队", state["任务"]["排队RunID"])):
                outcome = run(run_id)
                state["任务"][kind + "归档状态"] = {"status": outcome["status"],
                                                 "conclusion": outcome.get("conclusion")}
            waiting_jobs = call("读取归档后排队 Job", repo + "/actions/runs/" +
                                str(state["任务"]["排队RunID"]) + "/jobs")["jobs"]
            check("归档后旧排队 Job 尚未领取", all(job.get("runner_id") is None for job in waiting_jobs), True)
            oid = state["协议"]["内容SHA256"]
            payload = base64.b64decode(state["协议"]["内容Base64"])
            code, body = request(raw_repo + "/info/lfs/objects/" + oid)
            check("归档后 LFS 保留读取", code, 200)
            check("归档后 LFS 内容未变", hashlib.sha256(body).hexdigest(), oid)
            new_payload = payload + b"new"
            new_oid = hashlib.sha256(new_payload).hexdigest()
            code, _ = request(raw_repo + "/info/lfs/objects/" + new_oid + "/" +
                              str(len(new_payload)), "PUT", new_payload)
            check("归档后 LFS 新写拒绝", code, 423)
            code, body = request(state["协议"]["Generic路径"])
            check("归档后 Generic 保留读取", code, 200)
            check("归档后 Generic 内容未变", hashlib.sha256(body).hexdigest(), oid)
            code, _ = request("/api/packages/" + owner +
                "/generic/phase1-s05/2.0.0/payload.txt", "PUT", new_payload)
            check("归档后 Generic 新版发布拒绝", code, {401, 403, 423})
            code, _ = request("/api/packages/" + owner +
                "/generic/phase1-s05/2.0.0/payload.txt")
            check("拒绝发布后新 Generic 版本不存在", code, 404)
            code, _ = request(repo + "/releases/" + str(state["协议"]["ReleaseID"]))
            check("归档后 Release 保留读取", code, 200)
            code, _ = api.call(repo + "/releases", "POST",
                               {"tag_name": "phase1-s05-v2", "name": "归档拒绝发布"})
            check("归档后 Release 发布拒绝", code, 423)
            artifact_path = repo + "/actions/artifacts/" + str(state["协议"]["ArtifactID"])
            code, _ = request(artifact_path + "/zip")
            check("归档后 artifact 保留下载", code, 200)
            code, _ = request(artifact_path, "DELETE")
            check("归档后 artifact 删除拒绝", code, 423)
            result = git("ls-remote", args.base + raw_repo, "HEAD")
            check("归档后 HTTP Git 只读", result.returncode, 0)
            clone = pathlib.Path(state["协议"]["Git目录"])
            (clone / "s05-denied.txt").write_text("归档写入应拒绝\n")
            subprocess.run(["git", "-C", str(clone), "add", "s05-denied.txt"], check=True)
            subprocess.run(["git", "-C", str(clone), "-c", "user.name=Phase1 Probe",
                            "-c", "user.email=phase1@example.invalid", "commit", "-m",
                            "test: archived push denied"], check=True, capture_output=True)
            result = git("-C", str(clone), "push", "origin", state["仓库"]["默认分支"])
            check("归档后 HTTP Git push 拒绝", result.returncode != 0, True)
            state["结果"] = "UI归档与协议边界通过，待释放屏障"
            print("S05 UI归档与协议边界通过，等待 finish 释放屏障", flush=True)
        else:
            check("归档边界已验证", state["结果"], "UI归档与协议边界通过，待释放屏障")
            barrier = json.loads((args.state.parent / "barrier.json").read_text())
            release = urllib.request.Request("http://127.0.0.1:" + str(barrier["端口"]) +
                "/release/" + barrier["令牌"], method="POST", data=b"")
            with opener.open(release, timeout=10) as response:
                check("屏障放行响应", response.status, 204)
            end = time.monotonic() + 45
            while time.monotonic() < end:
                running = run(state["任务"]["执行中RunID"])
                if running["status"] == "completed":
                    break
                time.sleep(2)
            check("原执行中 Job 已收尾", running["status"], "completed")
            state["任务"]["执行中最终结果"] = running["conclusion"]
            for _ in range(8):
                waiting = run(state["任务"]["排队RunID"])
                waiting_jobs = call("检查旧排队 Job 未领取", repo + "/actions/runs/" +
                                    str(state["任务"]["排队RunID"]) + "/jobs")["jobs"]
                check("归档期间旧排队任务无 Runner 授权", all(job.get("runner_id") is None for job in waiting_jobs), True)
                check("归档期间旧排队任务未成功", waiting.get("conclusion") == "success", False)
                time.sleep(2)
            state["任务"]["排队最终观察"] = {"status": waiting["status"],
                                         "conclusion": waiting.get("conclusion")}
            state["结果"] = "S05 通过"
            print("S05 通过，父组", state["父组"]["ID"], flush=True)
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
