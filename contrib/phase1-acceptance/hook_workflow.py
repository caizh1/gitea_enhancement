#!/usr/bin/env python3
"""在隔离群组用专用 Runner 验证真实工作流 Hook 事件。"""

import argparse
import base64
import json
import os
from pathlib import Path
import subprocess
import time

from capacity_probe import API, write_json


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=("prepare", "trigger", "verify"))
    parser.add_argument("--base", required=True)
    parser.add_argument("--credentials", required=True, type=Path)
    parser.add_argument("--work", required=True, type=Path)
    parser.add_argument("--runner-binary", required=True, type=Path)
    parser.add_argument("--isolated-instance", action="store_true", required=True)
    args = parser.parse_args()
    state_path = args.work / "state.json"
    state = json.loads(state_path.read_text())
    if args.base.rstrip("/") != state["实例"].rstrip("/"):
        raise RuntimeError("隔离实例与固定夹具不一致")
    password = json.loads(args.credentials.read_text())["password"]
    api = API(args.base, state["根Owner"], password)
    repo = state["仓库"]["full_name"]
    group = state["根群组"]["compatibility_name"]
    runner_dir = args.work / "workflow-runner"

    def call(label, method, path, expected=200, body=None):
        status, value, _, _ = api.call(method, path, body)
        state["步骤"].append({"检查": label, "方法": method, "路径": path, "状态": status, "期望": expected})
        write_json(state_path, state)
        if status != expected:
            raise RuntimeError(f"{label} HTTP {status}，预期 {expected}；响应体未记录")
        return value

    if args.phase == "prepare":
        if "专用Runner" in state:
            raise RuntimeError("专用 Runner 已创建；不得重复注册")
        runner_dir.mkdir(mode=0o700, exist_ok=False)
        token = call("获取自建根群组注册令牌", "POST",
                     f"/orgs/{group}/actions/runners/registration-token")["token"]
        secret = runner_dir / "registration-token.json"
        write_json(secret, {"token": token})
        os.chmod(secret, 0o600)
        label = f"h02-hook-{state['根群组']['id']}:host"
        name = f"phase1-h02-hook-{state['根群组']['id']}"
        config = runner_dir / "runner.yaml"
        config.write_text(f"log:\n  level: info\nrunner:\n  file: {runner_dir}/.runner\n  capacity: 1\n"
                          f"  fetch_interval: 2s\n  labels:\n    - {label}\ncache:\n  enabled: false\n"
                          f"host:\n  workdir_parent: {runner_dir}/jobs\n")
        os.chmod(config, 0o600)
        with (runner_dir / "registration.log").open("w") as log:
            registration = subprocess.run([str(args.runner_binary), "register", "--no-interactive",
                                           "--config", str(config), "--instance", args.base,
                                           "--token", token, "--name", name, "--labels", label],
                                          stdout=log, stderr=subprocess.STDOUT, timeout=30)
        if registration.returncode:
            raise RuntimeError(f"专用 Runner 注册失败，退出码 {registration.returncode}")
        with (runner_dir / "daemon.log").open("ab") as log:
            daemon = subprocess.Popen([str(args.runner_binary), "daemon", "--config", str(config)],
                                      stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        (runner_dir / "daemon.pid").write_text(str(daemon.pid) + "\n")
        state["专用Runner"] = {"名称": name, "标签": label, "进程": daemon.pid}
        write_json(state_path, state)
        for _ in range(30):
            listing = call("回读自建根群组 Runner", "GET", f"/orgs/{group}/actions/runners")
            items = listing.get("runners", listing.get("entries", []))
            found = [item for item in items if item["name"] == name and item["status"] == "online"]
            if len(found) == 1:
                state["专用Runner"]["编号"] = found[0]["id"]
                break
            time.sleep(1)
        else:
            raise TimeoutError("专用 Runner 未上线")
        filename = "hook-events.yml"
        yaml = ("name: 一期隔离 Hook 工作流\non:\n  workflow_dispatch:\njobs:\n  verify:\n"
                f"    runs-on: {label.split(':', 1)[0]}\n    steps:\n"
                "      - name: 验证隔离执行\n        run: echo '一期隔离 Hook 工作流已执行'\n")
        call("写入隔离工作流", "PUT", f"/repos/{repo}/contents/.gitea/workflows/{filename}", 201,
             {"branch": state["仓库"]["default_branch"], "message": "建立一期隔离 Hook 工作流",
              "content": base64.b64encode(yaml.encode()).decode()})
        state["工作流文件"] = filename
        write_json(state_path, state)
        print("专用 Root Runner 上线，工作流文件已写入；尚未触发")

    elif args.phase == "trigger":
        if "专用Runner" not in state or "工作流文件" not in state:
            raise RuntimeError("必须先准备独立 Runner 与工作流")
        call("触发自建仓库工作流", "POST", f"/repos/{repo}/actions/workflows/{state['工作流文件']}/dispatches",
             204, {"ref": state["仓库"]["default_branch"]})
        state["工作流触发时间"] = time.time()
        write_json(state_path, state)
        print("工作流已触发，待接收端和 Run 状态核验")

    else:
        rows = [json.loads(line) for line in (args.work / "receipts.jsonl").read_text().splitlines()]
        found = {kind: [row for row in rows if row["事件"] == kind and row["仓库编号"] == state["仓库"]["id"]
                        and row["签名密钥标签"] == ["signing-key-root"] and row["响应状态"] == 200]
                 for kind in ("workflow_run", "workflow_job")}
        runs = call("读取隔离工作流 Run", "GET", f"/repos/{repo}/actions/runs")
        state["工作流验收"] = {"收件数": {kind: len(values) for kind, values in found.items()},
                             "Run数": runs.get("total_count", len(runs.get("workflow_runs", [])))}
        write_json(state_path, state)
        if not all(found.values()) or state["工作流验收"]["Run数"] < 1:
            raise RuntimeError("工作流真实 Run 或 Hook 接收尚未齐全")
        print("工作流 Run 与 workflow_run、workflow_job 真实签名收件通过")


if __name__ == "__main__":
    main()
