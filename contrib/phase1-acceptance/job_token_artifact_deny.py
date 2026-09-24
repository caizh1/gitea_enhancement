#!/usr/bin/env python3
"""用独立同 Owner 真 v4 产物核对跨仓 Actions 列表与下载拒绝。"""

import argparse
import base64
import json
import pathlib
import re
import sys
import time

from job_token_boundary import Client


UPLOAD = "https://github.com/ChristopherHX/gitea-upload-artifact@81f940d004763f986ba3582c007fd842dd5cb0d7"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", type=pathlib.Path, required=True)
    parser.add_argument("--fixture", type=pathlib.Path, required=True)
    parser.add_argument("--state", type=pathlib.Path, required=True)
    args = parser.parse_args()
    if args.state.exists():
        raise RuntimeError("已有跨仓产物结果，不覆盖或重试同一夹具")
    args.state.parent.mkdir(parents=True, exist_ok=True)
    fixture = json.loads(args.fixture.read_text())
    owner = fixture["owner"]
    source = fixture["repos"]["token-source"]["name"]
    target = fixture["repos"]["token-allowed"]["name"]
    api = Client(args.base, args.credentials, fixture["identities"]["owner"])
    state = {"说明": "同 Owner allow-list 真产物的跨仓 Actions 拒绝", "ownerID": fixture["ownerID"],
        "sourceRepoID": fixture["repos"]["token-source"]["id"], "targetRepoID": fixture["repos"]["token-allowed"]["id"], "步骤": []}

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")

    def request(label, path, method="GET", body=None, expected=200):
        code, response = api.call(path, method, body)
        state["步骤"].append({"检查": label, "实际": code, "期望": expected, "通过": code == expected})
        save()
        if code != expected:
            raise AssertionError(label + "：HTTP " + str(code))
        return response

    def create_workflow(repo, filename, workflow):
        body = {"content": base64.b64encode(workflow.encode()).decode(), "message": "加入独立跨仓产物权限验收", "branch": "main"}
        return request("创建 " + filename, f"/api/v1/repos/{owner}/{repo}/contents/.gitea/workflows/{filename}", "POST", body, 201)

    def wait_run(repo, run_id):
        path = f"/api/v1/repos/{owner}/{repo}/actions/runs/{run_id}"
        for _ in range(90):
            run = request("轮询 Run", path)
            if run["status"] == "completed":
                if run["conclusion"] != "success":
                    raise AssertionError("Run 真实结论：" + str(run["conclusion"]))
                return run
            time.sleep(2)
        raise TimeoutError("Run 未完成")

    target_workflow = ("name: token target v4 artifact\non:\n  workflow_dispatch:\njobs:\n  upload:\n"
        + "    runs-on: " + fixture["runner"]["labelName"] + "\n    steps:\n"
        + "      - run: printf '独立真实 v4 产物\\n' > payload.txt\n"
        + "      - uses: " + UPLOAD + "\n        with:\n          name: token-target-v4\n          path: payload.txt\n          if-no-files-found: error\n")
    try:
        create_workflow(target, "token-target-v4.yml", target_workflow)
        dispatch = f"/api/v1/repos/{owner}/{target}/actions/workflows/token-target-v4.yml/dispatches?return_run_details=true"
        run_id = request("调度目标真产物", dispatch, "POST", {"ref": "main"})["workflow_run_id"]
        state["目标RunID"] = run_id
        save()
        wait_run(target, run_id)
        artifacts = request("Owner 列出真产物", f"/api/v1/repos/{owner}/{target}/actions/runs/{run_id}/artifacts")["artifacts"]
        matching = [item for item in artifacts if item["name"] == "token-target-v4"]
        if len(matching) != 1:
            raise AssertionError("真产物数量不是1")
        artifact_id = matching[0]["id"]
        state["ArtifactID"] = artifact_id
        save()
        request("Owner 下载同一真产物", f"/api/v1/repos/{owner}/{target}/actions/artifacts/{artifact_id}/zip")

        probe = r'''import os, urllib.error, urllib.request
token = os.environ["GITEA_TOKEN"]
base = CONFIG["base"]
owner = CONFIG["owner"]
target = CONFIG["target"]
artifact = CONFIG["artifact"]
client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
for label, path in (("list", f"/api/v1/repos/{owner}/{target}/actions/artifacts"),
                    ("zip", f"/api/v1/repos/{owner}/{target}/actions/artifacts/{artifact}/zip")):
    req = urllib.request.Request(base + path, headers={"Authorization": "token " + token})
    try:
        with client.open(req, timeout=20) as response:
            code = response.status
            response.read()
    except urllib.error.HTTPError as error:
        code = error.code
        error.close()
    print("CROSS_ACTIONS %s=%s" % (label, code), flush=True)
    assert code in (401, 403, 404), (label, code)
'''
        probe = probe.replace("CONFIG", repr({"base": args.base.rstrip("/"), "owner": owner, "target": target, "artifact": artifact_id}))
        workflow = ("name: token cross repo artifact denial\non:\n  workflow_dispatch:\njobs:\n  probe:\n"
            + "    runs-on: " + fixture["runner"]["labelName"] + "\n    permissions:\n      actions: read\n"
            + "    env:\n      GITEA_TOKEN: ${{ secrets.GITEA_TOKEN }}\n    steps:\n"
            + "      - run: |\n          set +x\n          python3 - <<'PY'\n"
            + "".join("          " + line + "\n" for line in probe.splitlines()) + "          PY\n")
        create_workflow(source, "token-cross-artifact.yml", workflow)
        dispatch = f"/api/v1/repos/{owner}/{source}/actions/workflows/token-cross-artifact.yml/dispatches?return_run_details=true"
        source_id = request("调度来源拒绝作业", dispatch, "POST", {"ref": "main"})["workflow_run_id"]
        state["来源RunID"] = source_id
        save()
        wait_run(source, source_id)
        jobs = request("读取来源 Job", f"/api/v1/repos/{owner}/{source}/actions/runs/{source_id}/jobs")["jobs"]
        if len(jobs) != 1 or jobs[0]["conclusion"] != "success":
            raise AssertionError("来源 Job 未成功")
        state["来源JobID"] = jobs[0]["id"]
        code, log = api.call(f"/api/v1/repos/{owner}/{source}/actions/jobs/{jobs[0]['id']}/logs")
        if code != 200:
            raise AssertionError("来源 Job 日志 HTTP " + str(code))
        log = log.decode("utf-8", "replace") if isinstance(log, bytes) else str(log)
        statuses = {name: int(status) for name, status in re.findall(r"CROSS_ACTIONS (list|zip)=([0-9]+)", log)}
        if set(statuses) != {"list", "zip"} or any(status not in (401, 403, 404) for status in statuses.values()):
            raise AssertionError("真产物跨仓拒绝断言缺失")
        state["真实Job状态"] = statuses
        state["结果"] = "通过"
    except Exception as error:
        state["结果"] = "失败"
        state["首错"] = str(error)
        raise
    finally:
        save()
    print("通过：目标Run", state["目标RunID"], "Artifact", state["ArtifactID"], "来源Run", state["来源RunID"])


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("停止：" + str(error), file=sys.stderr)
        sys.exit(1)
