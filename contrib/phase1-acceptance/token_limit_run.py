#!/usr/bin/env python3
"""以真实 Runner 验证父、子群组和仓库的作业令牌上限交集。"""

import argparse
import base64
import hashlib
import json
import pathlib
import re
import sys
import time
import urllib.request

from job_token_boundary import Client


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--fixture", type=pathlib.Path, required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    parser.add_argument("--runner-label", default="phase1-v15")
    args = parser.parse_args()
    if args.state.exists():
        raise RuntimeError("结果已存在；不得覆盖真实 Run")
    args.state.parent.mkdir(parents=True, exist_ok=True)
    fixture = json.loads(args.fixture.read_text())
    api = Client(args.base, args.credentials, fixture["identities"]["owner"])
    owner = fixture["child"]["compatibility_name"]
    repo = fixture["repo"]["name"]
    repo_path = f"/api/v1/repos/{owner}/{repo}"
    package = "token-limit-" + str(fixture["child"]["id"])
    state = {"说明": "CI06-a 真实Runner逐级上限交集", "parentID": fixture["parent"]["id"],
        "childID": fixture["child"]["id"], "repoID": fixture["repo"]["id"], "步骤": []}

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def call(label, path, method="GET", body=None, expected=200):
        code, result = api.call(path, method, body)
        state["步骤"].append({"检查": label, "实际": code, "期望": expected, "通过": code == expected})
        save()
        if code != expected:
            raise AssertionError(f"{label}：HTTP {code}")
        return result

    code = f'''import base64, os, urllib.error, urllib.request
base = {args.base.rstrip('/')!r}
owner = {owner!r}
repo = {repo!r}
package = {package!r}
token = os.environ["GITEA_TOKEN"]
client = urllib.request.build_opener(urllib.request.ProxyHandler({{}}))
def check(label, path, method, headers, body, expected):
    req = urllib.request.Request(base + path, method=method, data=body, headers=headers)
    try:
        with client.open(req, timeout=25) as response:
            status = response.status
    except urllib.error.HTTPError as error:
        status = error.code
        error.close()
    print("TOKEN_LIMIT %s=%s" % (label, status), flush=True)
    assert status in expected, (label, status)
bearer = {{"Authorization": "token " + token}}
check("code_read", "/api/v1/repos/" + owner + "/" + repo + "/contents/probe.txt", "GET", bearer, None, {{200}})
body = '{{"content":"' + base64.b64encode("越权写入\\n".encode()).decode() + '","message":"越权探针","branch":"main"}}'
check("code_write", "/api/v1/repos/" + owner + "/" + repo + "/contents/token-limit-denied.txt", "POST", dict(bearer, **{{"Content-Type":"application/json"}}), body.encode(), {{403}})
basic = "Basic " + base64.b64encode(("oauth2:" + token).encode()).decode()
check("package_read", "/api/packages/" + owner + "/generic/" + package + "/1.0.0/payload.txt", "GET", {{"Authorization":basic}}, None, {{401,403,404}})
check("actions_read", "/api/v1/repos/" + owner + "/" + repo + "/actions/artifacts", "GET", bearer, None, {{200}})
'''
    workflow = "name: token limit closure\non:\n  workflow_dispatch:\njobs:\n  probe:\n    runs-on: " + args.runner_label + "\n    permissions:\n      contents: write\n      packages: read\n      actions: read\n    env:\n      GITEA_TOKEN: ${{ secrets.GITEA_TOKEN }}\n    steps:\n      - name: Check inherited token ceilings\n        run: |\n          set +x\n          python3 - <<'PY'\n" + "".join("          " + line + "\n" for line in code.splitlines()) + "          PY\n"

    try:
        call("Owner读私有消费仓", repo_path)
        payload = ("一期令牌上限包 " + package + "\n").encode()
        password = api.credentials.get(fixture["identities"]["owner"], api.credentials.get("password"))
        auth = base64.b64encode((fixture["identities"]["owner"] + ":" + password).encode()).decode()
        package_path = "/api/packages/" + owner + "/generic/" + package + "/1.0.0/payload.txt"
        req = urllib.request.Request(args.base.rstrip("/") + package_path, data=payload, method="PUT",
            headers={"Authorization": "Basic " + auth, "Content-Type": "application/octet-stream"})
        with api.opener.open(req, timeout=30) as response:
            package_status = response.status
        if package_status != 201:
            raise AssertionError("Owner 包发布HTTP " + str(package_status))
        state["包"] = {"Owner发布HTTP": package_status, "SHA256": hashlib.sha256(payload).hexdigest()}
        save()
        call("写入可读数据", repo_path + "/contents/probe.txt", "POST",
            {"content": base64.b64encode("一期上限读样本\n".encode()).decode(),
             "message": "加入上限读样本", "branch": fixture["repo"]["default_branch"]}, 201)
        call("写入固定工作流", repo_path + "/contents/.gitea/workflows/token-limit.yml", "POST",
            {"content": base64.b64encode(workflow.encode()).decode(), "message": "加入令牌上限验收",
             "branch": fixture["repo"]["default_branch"]}, 201)
        dispatch = repo_path + "/actions/workflows/token-limit.yml/dispatches?return_run_details=true"
        run_id = call("调度真实 Runner", dispatch, "POST", {"ref": fixture["repo"]["default_branch"]})["workflow_run_id"]
        state["RunID"] = run_id
        save()
        run_path = repo_path + "/actions/runs/" + str(run_id)
        for _ in range(90):
            run = call("轮询 Run", run_path)
            if run["status"] == "completed":
                break
            time.sleep(2)
        else:
            raise TimeoutError("真实 Runner 未完成")
        state["Run"] = {"status": run["status"], "conclusion": run["conclusion"]}
        if run["conclusion"] != "success":
            raise AssertionError("Run不是Success：" + str(run["conclusion"]))
        jobs = call("读取 Job", run_path + "/jobs")["jobs"]
        if len(jobs) != 1 or jobs[0]["conclusion"] != "success":
            raise AssertionError("Job未成功")
        state["JobID"] = jobs[0]["id"]
        raw = call("读取原生日志", repo_path + f"/actions/jobs/{jobs[0]['id']}/logs")
        lines = raw.decode("utf-8", "replace") if isinstance(raw, bytes) else ""
        state["真实客户端状态"] = dict(re.findall(r"TOKEN_LIMIT ([a-z_]+)=(\d+)", lines))
        statuses = state["真实客户端状态"]
        if (set(statuses) != {"code_read", "code_write", "actions_read", "package_read"}
                or statuses["code_read"] != "200" or statuses["code_write"] != "403"
                or statuses["actions_read"] != "200" or statuses["package_read"] not in {"401", "403", "404"}):
            raise AssertionError("真实客户端状态不符：" + str(state["真实客户端状态"]))
        code, _ = api.call(repo_path + "/contents/token-limit-denied.txt")
        if code != 404:
            raise AssertionError("越权写入文件意外存在")
        state["越权文件Owner回读"] = code
        state["结果"] = "通过"
    except Exception as error:
        state["结果"], state["首错"] = "失败", str(error)
        raise
    finally:
        save()
    print("通过：Run", state["RunID"], "Job", state["JobID"])


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
