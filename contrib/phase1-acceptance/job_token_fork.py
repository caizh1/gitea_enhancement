#!/usr/bin/env python3
"""用真实私有 fork PR 验证作业令牌的跨仓 Git 与 Generic 边界。"""

import argparse
import base64
import json
import pathlib
import re
import sys
import time

from job_token_boundary import Client



def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--fixture", type=pathlib.Path, required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    args = parser.parse_args()
    result_path = args.state
    if result_path.exists():
        raise RuntimeError("已有 fork 运行证据，不覆盖或复用夹具")
    result_path.parent.mkdir(parents=True, exist_ok=True)
    fixture = json.loads(args.fixture.read_text())
    api = Client(args.base, args.credentials, fixture["identities"]["owner"])
    owner = fixture["owner"]
    foreign = fixture["foreign"]
    source_repo = fixture["repos"]["token-source"]
    result = {"说明": "真实 fork PR 任务令牌边界", "来源OwnerID": fixture["ownerID"], "许可仓库ID": fixture["repos"]["token-allowed"]["id"], "步骤": []}

    def save():
        result_path.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")

    def call(label, path, method="GET", body=None, expected=200):
        status, payload = api.call(path, method, body)
        ok = status == expected
        result["步骤"].append({"检查": label, "实际": status, "期望": expected, "通过": ok})
        save()
        if not ok:
            raise AssertionError(label + "：HTTP " + str(status))
        return payload

    probe = r'''import base64, hashlib, os, pathlib, subprocess, tempfile, urllib.error, urllib.request
token = os.environ["GITEA_TOKEN"]
base = CONFIG["base"]
owner = CONFIG["owner"]
foreign = CONFIG["foreign"]
client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
def git(label, target, repo, expected):
    with tempfile.TemporaryDirectory() as directory:
        askpass = pathlib.Path(directory) / "askpass.sh"
        askpass.write_text('#!/bin/sh\ncase "$1" in *Username*) printf "oauth2\\n";; *) printf "%s\\n" "$GITEA_TOKEN";; esac\n')
        askpass.chmod(0o700)
        env = os.environ.copy()
        env.update({"GIT_ASKPASS": str(askpass), "GIT_TERMINAL_PROMPT": "0", "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null"})
        proc = subprocess.run(["git", "-c", "credential.helper=", "ls-remote", base + "/" + target + "/" + repo + ".git"], capture_output=True, env=env, timeout=25)
    ok = proc.returncode == 0 and bool(proc.stdout.strip())
    print("FORK %s_exit=%s" % (label, proc.returncode), flush=True)
    assert ok == expected, (label, proc.returncode)
def generic(label, target, name, version, expected, digest=None):
    auth = "Basic " + base64.b64encode(("oauth2:" + token).encode()).decode()
    req = urllib.request.Request(base + "/api/packages/" + target + "/generic/" + name + "/" + version + "/payload.txt", headers={"Authorization": auth})
    try:
        with client.open(req, timeout=20) as response:
            code, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        code, raw = error.code, error.read(1024)
        error.close()
    print("FORK %s_http=%s" % (label, code), flush=True)
    assert code in expected, (label, code)
    if digest:
        assert hashlib.sha256(raw).hexdigest() == digest
git("source", owner, CONFIG["sourceRepo"], True)
git("allowlisted_private", owner, CONFIG["allowedRepo"], False)
git("unlisted_private", owner, CONFIG["deniedRepo"], False)
git("foreign_private", foreign["owner"], foreign["repoName"], False)
generic("same_owner", owner, CONFIG["samePackage"], CONFIG["sameVersion"], {200}, CONFIG["sameSHA"])
generic("foreign_owner", foreign["owner"], foreign["packageName"], "1.0.0", {401,403,404})
'''
    config = {"base": args.base.rstrip("/"), "owner": owner,
        "foreign": {"owner": foreign["owner"], "repoName": foreign["repoName"], "packageName": foreign["packageName"]},
        "sourceRepo": source_repo["name"], "allowedRepo": fixture["repos"]["token-allowed"]["name"],
        "deniedRepo": fixture["repos"]["token-denied"]["name"],
        "samePackage": fixture["samePackage"]["name"], "sameVersion": fixture["samePackage"]["version"],
        "sameSHA": fixture["samePackage"]["sha256"]}
    probe = probe.replace("CONFIG", repr(config))
    workflow = "name: fork token boundary\non:\n  pull_request:\njobs:\n  probe:\n    runs-on: " + fixture["runner"]["labelName"] + "\n    permissions:\n      contents: read\n      packages: read\n      actions: read\n    env:\n      GITEA_TOKEN: ${{ secrets.GITEA_TOKEN }}\n    steps:\n      - name: Fork Git and Generic checks\n        run: |\n          set +x\n          python3 - <<'PY'\n" + "".join("          " + line + "\n" for line in probe.splitlines()) + "          PY\n"
    path = "/api/v1/repos/" + owner + "/" + source_repo["name"] + "/contents/.gitea/workflows/token-fork.yml"
    try:
        call("创建固定 fork PR 工作流", path, "POST", {"content": base64.b64encode(workflow.encode()).decode(), "message": "加入独立 fork PR 令牌验收", "branch": source_repo["default_branch"]}, 201)
        suffix = str(time.time_ns())
        fork_name = "phase1-token-fork-" + suffix
        fork = call("普通 Owner 建真实个人 fork", "/api/v1/repos/" + owner + "/" + source_repo["name"] + "/forks", "POST", {"name": fork_name}, 202)
        result["fork"] = {"id": fork["id"], "owner": fork["owner"]["login"], "name": fork["name"], "parentID": fork["parent"]["id"] if fork.get("parent") else None}
        save()
        branch = "token-fork-check"
        path = "/api/v1/repos/" + result["fork"]["owner"] + "/" + fork_name + "/contents/fork-change.txt"
        body = {"content": base64.b64encode("真实 fork PR 令牌边界\n".encode()).decode(), "message": "创建真实 fork PR 提交", "branch": source_repo["default_branch"], "new_branch": branch}
        call("在 fork 分支提交文件", path, "POST", body, 201)
        pull = call("向来源仓创建真实 fork PR", "/api/v1/repos/" + owner + "/" + source_repo["name"] + "/pulls", "POST", {"base": source_repo["default_branch"], "head": result["fork"]["owner"] + ":" + branch, "title": "一期 fork 作业令牌边界"}, 201)
        result["PR"] = {"number": pull["number"], "headRepoID": pull["head"]["repo"]["id"], "baseRepoID": pull["base"]["repo"]["id"]}
        save()
        run = None
        for _ in range(90):
            runs = call("查询实际 fork PR Run", "/api/v1/repos/" + owner + "/" + source_repo["name"] + "/actions/runs?limit=20")
            candidates = [x for x in runs.get("workflow_runs", []) if x.get("event") == "pull_request" and "token-fork.yml" in x.get("path", "")]
            if candidates:
                run = candidates[0]
                if run["status"] == "completed":
                    break
            time.sleep(2)
        if run is None or run["status"] != "completed":
            raise TimeoutError("真实 fork PR Run 未完成")
        result["Run"] = {"id": run["id"], "status": run["status"], "conclusion": run["conclusion"], "event": run.get("event"), "path": run.get("path")}
        assert run["conclusion"] == "success", result["Run"]
        jobs = call("读取 fork PR Jobs", "/api/v1/repos/" + owner + "/" + source_repo["name"] + "/actions/runs/" + str(run["id"]) + "/jobs")["jobs"]
        assert len(jobs) == 1 and jobs[0]["conclusion"] == "success", jobs
        result["Job"] = {"id": jobs[0]["id"], "status": jobs[0]["status"], "conclusion": jobs[0]["conclusion"]}
        status, log = api.call("/api/v1/repos/" + owner + "/" + source_repo["name"] + "/actions/jobs/" + str(jobs[0]["id"]) + "/logs")
        assert status == 200, status
        log = log.decode("utf-8", "replace") if isinstance(log, bytes) else str(log)
        result["真实客户端状态"] = {name: value for name, value in re.findall(r"FORK ([a-z_]+)_(?:exit|http)=([0-9]+)", log)}
        assert len(result["真实客户端状态"]) == 6, result["真实客户端状态"]
        result["结果"] = "通过"
    except Exception as error:
        result["结果"] = "失败"
        result["首错"] = str(error)
        raise
    finally:
        save()
    print("通过：真实 fork PR Run", result["Run"]["id"], "Job", result["Job"]["id"])


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
