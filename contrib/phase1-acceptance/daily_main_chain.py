#!/usr/bin/env python3
"""在隔离实例准备父级授权、祖先工作流与同仓 PR 的最小主链。"""

import argparse
import base64
import hashlib
import json
import os
import pathlib
import re
import secrets
import subprocess
import sys
import time

from job_token_boundary import Client


UPLOAD = "https://github.com/ChristopherHX/gitea-upload-artifact@81f940d004763f986ba3582c007fd842dd5cb0d7"
DOWNLOAD = "https://github.com/ChristopherHX/gitea-download-artifact@75635f32b4c1c41c4b3d64e8f85210112ed4c9c7"
BASE = "http://127.0.0.1:13043"
PARENT_ID = 91
CHILD_ID = 92
OWNER = "p1share-user-283b08e68"
OUTSIDER = "p1share-out-283b08e68"
CHILD_TEAM_ID = 81


def workflow(label, variable_name, variable_value, secret_name, secret_hash, payload_hash):
    return f'''name: 一期父级主链\non:\n  pull_request:\njobs:\n  main-chain:\n    runs-on: {label}\n    env:\n      P1_PARENT_VAR: ${{{{ vars.{variable_name} }}}}\n      P1_PARENT_SECRET: ${{{{ secrets.{secret_name} }}}}\n    steps:\n      - name: 核对父级变量与凭据\n        run: |\n          set +x\n          python3 - <<'PY'\n          import hashlib, os\n          assert os.environ.get('P1_PARENT_VAR') == {variable_value!r}\n          assert hashlib.sha256(os.environ.get('P1_PARENT_SECRET', '').encode()).hexdigest() == {secret_hash!r}\n          print('父级变量与凭据校验通过')\n          PY\n      - name: 生成合成产物\n        run: |\n          mkdir -p phase1-chain\n          printf '一期父级主链产物\\n' > phase1-chain/payload.txt\n      - uses: {UPLOAD}\n        with:\n          name: phase1-chain-v4\n          path: phase1-chain/payload.txt\n          if-no-files-found: error\n      - uses: {DOWNLOAD}\n        with:\n          name: phase1-chain-v4\n          path: phase1-chain-downloaded\n      - name: 核对下载内容\n        run: |\n          python3 - <<'PY'\n          import hashlib, pathlib\n          actual = hashlib.sha256(pathlib.Path('phase1-chain-downloaded/payload.txt').read_bytes()).hexdigest()\n          assert actual == {payload_hash!r}\n          print('v4 下载内容摘要一致')\n          PY\n'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("stage", choices=("prepare", "inspect"))
    parser.add_argument("--credentials", required=True, type=pathlib.Path)
    parser.add_argument("--state", required=True, type=pathlib.Path)
    parser.add_argument("--runner-binary", type=pathlib.Path)
    parser.add_argument("--app-binary", type=pathlib.Path)
    parser.add_argument("--app-work-path", type=pathlib.Path)
    parser.add_argument("--app-config", type=pathlib.Path)
    args = parser.parse_args()
    if args.stage == "prepare" and args.state.exists():
        parser.error("样本状态已存在；保留首轮结果，不重复建立")
    if args.stage == "inspect" and not args.state.exists():
        parser.error("先执行 prepare")
    if args.stage == "prepare" and not args.runner_binary:
        parser.error("prepare 必须指定真实 Runner 二进制")
    if args.stage == "prepare" and not args.runner_binary.is_file():
        parser.error("Runner 二进制不存在")
    if args.stage == "prepare" and not all((args.app_binary, args.app_work_path, args.app_config)):
        parser.error("prepare 必须指定隔离应用的二进制、工作目录与配置")

    args.state.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    stamp = str(time.time_ns())
    state = json.loads(args.state.read_text()) if args.state.exists() else {
        "说明": "91/92 父级继承主链专用样本，最终候选触发前仅准备",
        "实例": BASE, "父组ID": PARENT_ID, "子组ID": CHILD_ID,
        "普通父Owner": OWNER, "Developer": "p1daily-developer-" + stamp[-10:],
        "无关用户": OUTSIDER,
        "步骤": [], "阶段": "准备中",
    }

    def save():
        args.state.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n")
        args.state.chmod(0o600)

    if args.stage == "prepare":
        save()
        command = [str(args.app_binary), "--work-path", str(args.app_work_path),
                   "--config", str(args.app_config), "admin", "user", "create",
                   "--username", state["Developer"],
                   "--email", state["Developer"] + "@example.invalid",
                   "--random-password", "--random-password-length", "32",
                   "--must-change-password=false"]
        result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                timeout=60)
        cli_log = args.state.with_suffix(".user-create-private.log")
        cli_log.write_bytes(result.stdout)
        cli_log.chmod(0o600)
        if result.returncode:
            raise RuntimeError("专用普通 Developer CLI 建立失败；私有首错已保留")
        matched = re.search(rb"generated random password is '([^']+)'", result.stdout)
        if not matched:
            raise RuntimeError("CLI 未返回可识别的随机密码；私有首错已保留")
        passwords = json.loads(args.credentials.read_text())
        passwords[state["Developer"]] = matched.group(1).decode()
        credentials_path = args.state.with_suffix(".credentials.json")
        credentials_path.write_text(json.dumps(passwords, ensure_ascii=False) + "\n")
        credentials_path.chmod(0o600)
        state["私有凭据文件"] = str(credentials_path)
        save()
    client = Client(BASE, state["私有凭据文件"], OWNER)

    def check(label, actual, expected):
        passed = actual == expected
        state["步骤"].append({"检查": label, "实际": actual, "期望": expected, "通过": passed})
        save()
        if not passed:
            raise AssertionError(label + "；首错已保留")

    def api(label, path, method="GET", body=None, expected=200, user=None):
        code, result = client.call("/api/v1" + path, method, body, user)
        check(label, code, expected)
        return result

    owner = api("父级 Owner 身份", "/user")
    developer = api("Developer 账号存在", "/user", user=state["Developer"])
    outsider = api("无关账号存在", "/users/" + OUTSIDER)
    check("父级 Owner 用户ID", owner["id"], 76)
    check("父级 Owner 非管理员", owner["is_admin"], False)
    check("专用Developer用户名", developer["login"], state["Developer"])
    check("Developer 非管理员", developer["is_admin"], False)
    check("无关用户ID", outsider["id"], 77)
    parent = api("读取父组", f"/governance/groups/{PARENT_ID}")
    child = api("读取子组", f"/governance/groups/{CHILD_ID}")
    check("父组路径", parent["full_path"], "p1daily-parent-0155a264")
    check("子组父ID", child["parent_id"], PARENT_ID)
    check("父级直接 Owner", [(g["source"], g["role"]) for g in parent["grants"]], [("direct", 50)])
    check("子级继承 Owner", [(g["source"], g["role"]) for g in child["grants"]], [("inherited", 50)])
    team = api("读取子组原生 Team", f"/teams/{CHILD_TEAM_ID}/members")
    check("父 Owner 无子组直接 Team", owner["id"] in {u["id"] for u in team}, False)
    check("Developer 无子组直接 Team", developer["id"] in {u["id"] for u in team}, False)
    code, _ = client.call(f"/api/v1/governance/groups/{CHILD_ID}", user=OUTSIDER)
    check("无关用户不可读子组", code, 404)

    if args.stage == "inspect":
        for name, group in (("来源", parent), ("消费", child)):
            repo = state["仓库"][name]
            current = api("回读" + name + "仓库", "/repos/" + group["compatibility_name"] + "/" + repo["name"])
            check(name + "仓库ID", current["id"], repo["id"])
        current = api("回读Developer子组授权", f"/governance/groups/{CHILD_ID}", user=state["Developer"])
        check("Developer 仅继承", [(g["source"], g["role"]) for g in current["grants"]], [("inherited", 30)])
        print("夹具身份与仓库仍有效；最终 Run 尚未触发")
        return

    label = "p1daily-chain-" + stamp[-8:] + ":host"
    state["Runner标签"] = label
    state["Runner二进制SHA256"] = hashlib.sha256(args.runner_binary.read_bytes()).hexdigest()
    state["工作流路径"] = ".gitea/scoped_workflows/main-chain.yml"
    state["内容摘要"] = hashlib.sha256("一期父级主链产物\n".encode()).hexdigest()
    save()

    repos = {}
    for kind, group in (("来源", parent), ("消费", child)):
        name = "phase1-chain-" + ("source-" if kind == "来源" else "consumer-") + stamp[-10:]
        repo = api("创建" + kind + "独立私有仓库", "/orgs/" + group["compatibility_name"] + "/repos",
                   "POST", {"name": name, "private": True, "auto_init": True,
                             "default_branch": "main", "description": "一期父级主链隔离验收"}, 201)
        repos[kind] = {k: repo[k] for k in ("id", "name", "html_url", "default_branch")}
        repos[kind]["full_path"] = group["full_path"] + "/" + name
        state["仓库"] = repos
        save()

    variable_name, secret_name = "P1_CHAIN_VAR_" + stamp[-8:], "P1_CHAIN_SECRET_" + stamp[-8:]
    variable_value = "P1_CHAIN_VALUE_" + stamp[-8:]
    secret_value = secrets.token_urlsafe(32)
    secret_hash = hashlib.sha256(secret_value.encode()).hexdigest()
    parent_name = parent["compatibility_name"]
    api("父组建立变量", f"/orgs/{parent_name}/actions/variables/{variable_name}",
        "POST", {"value": variable_value, "description": "一期主链合成变量"}, 201)
    api("父组建立合成Secret", f"/orgs/{parent_name}/actions/secrets/{secret_name}",
        "PUT", {"data": secret_value, "description": "一期主链合成凭据", "protected": False}, 201)
    state["父级配置"] = {"变量名": variable_name, "Secret名": secret_name, "SecretSHA256": secret_hash}
    save()

    yaml = workflow(label.split(":", 1)[0], variable_name, variable_value,
                    secret_name, secret_hash, state["内容摘要"])
    wf = args.state.with_suffix(".workflow.yml")
    wf.write_text(yaml)
    wf.chmod(0o600)
    source = parent_name + "/" + repos["来源"]["name"]
    created = api("真实默认分支保存祖先工作流", "/repos/" + source + "/contents/" + state["工作流路径"],
                  "POST", {"content": base64.b64encode(yaml.encode()).decode(),
                           "branch": "main", "message": "test(actions): 准备父级主链 scoped 来源"}, 201)
    state["来源提交SHA"] = created["commit"]["sha"]
    save()

    current = api("Developer 授权前读取父组修订", f"/governance/groups/{PARENT_ID}")
    members = api("读取父组现有直接成员", f"/governance/groups/{PARENT_ID}/members")
    check("Developer 原先不在父组", developer["id"] in {u["user_id"] for u in members}, False)
    api("只在父组授予 Developer", f"/governance/groups/{PARENT_ID}/members/{developer['id']}",
        "PUT", {"role": 30, "revision": current["revision"]}, 204)
    inherited = api("读取Developer子级来源", f"/governance/groups/{CHILD_ID}", user=state["Developer"])
    check("Developer 仅祖先继承", [(g["source"], g["role"]) for g in inherited["grants"]], [("inherited", 30)])
    code, _ = client.call("/api/v1/repos/" + child["compatibility_name"] + "/" + repos["消费"]["name"] + "/contents/.gitea/workflows")
    check("无消费者本级YAML", code, 404)

    consumer_id = repos["消费"]["id"]
    rule = api("为同仓PR建立独立一票审批", f"/governance/repositories/{consumer_id}/approval-rules",
               "POST", {"rule": {"name": "一期主链父级审批", "required": 1,
                                 "user_ids": [21], "branch_mode": "all", "enabled": True}}, 201)
    state["审批规则ID"] = rule["id"]
    save()

    runner_dir = args.state.parent / "runner"
    runner_dir.mkdir(mode=0o700, exist_ok=False)
    token = api("读取父组Runner注册令牌", f"/orgs/{parent_name}/actions/runners/registration-token", "POST")["token"]
    token_file = runner_dir / "registration-token.json"
    token_file.write_text(json.dumps({"token": token}) + "\n")
    token_file.chmod(0o600)
    config = runner_dir / "runner.yaml"
    config.write_text(f"log:\n  level: info\nrunner:\n  file: {runner_dir}/.runner\n  capacity: 1\n  fetch_interval: 2s\n  labels:\n    - {label}\ncache:\n  enabled: false\nhost:\n  workdir_parent: {runner_dir}/jobs\n")
    config.chmod(0o600)
    runner_name = "phase1-daily-chain-" + stamp[-10:]
    with (runner_dir / "registration.log").open("w") as log:
        registration = subprocess.run([str(args.runner_binary), "register", "--no-interactive",
                                       "--config", str(config), "--instance", BASE,
                                       "--token", token, "--name", runner_name, "--labels", label],
                                      stdout=log, stderr=subprocess.STDOUT, timeout=30)
    check("专用Runner注册退出码", registration.returncode, 0)
    with (runner_dir / "daemon.log").open("ab") as log:
        daemon = subprocess.Popen([str(args.runner_binary), "daemon", "--config", str(config)],
                                  stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
    (runner_dir / "daemon.pid").write_text(str(daemon.pid) + "\n")
    state["Runner"] = {"名称": runner_name, "标签": label, "PID": daemon.pid,
                       "配置": str(config)}
    save()
    for _ in range(30):
        listing = api("读取父组专用Runner", f"/orgs/{parent_name}/actions/runners")
        entries = listing.get("runners", listing.get("entries", []))
        online = [item for item in entries if item["name"] == runner_name and item["status"] == "online"]
        if online:
            state["Runner"]["ID"] = online[0]["id"]
            break
        time.sleep(1)
    else:
        raise TimeoutError("专用Runner未上线，保留注册状态与日志")
    state["来源设置页"] = BASE + "/org/" + parent["full_path"] + "/settings/actions/scoped-workflows"
    state["阶段"] = "准备完成；等待来源UI登记及最终候选后触发"
    save()
    print(json.dumps({"阶段": state["阶段"], "来源仓库ID": repos["来源"]["id"],
                      "消费仓库": repos["消费"]["html_url"], "来源设置页": state["来源设置页"],
                      "RunnerID": state["Runner"]["ID"]}, ensure_ascii=False))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print("已停止并保留首错：" + str(error), file=sys.stderr)
        raise
